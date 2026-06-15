package api

import (
	"net/http"
	"strings"
)

// Auth returns a middleware that accepts a Bearer token — either a session
// token issued by POST /api/v1/auth/login (node-local, 8 h TTL) or an M7
// API token issued by POST /api/v1/tokens.
//
// Forwarded writes from follower nodes carry X-Cluster-Secret instead of an
// Authorization header. A valid cluster secret is accepted and grants full
// write scope so that session-token users on followers can transparently
// trigger writes via the leader.
//
// On success the authenticated Principal is stored on the request context
// (see PrincipalFrom). Scope enforcement is performed by RequireScope and
// by the individual route handlers for cluster:admin operations.
//
// Routing rules:
//   - Public endpoints (health, auth/setup, auth/login, auth/session) are
//     registered outside the protected group and never reach this middleware.
//   - When auth is NOT configured, all protected endpoints return 401.
//   - An unknown / expired Bearer token → 401.
func (a *App) Auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.authStore.IsConfigured() {
			http.Error(w, `{"error":"authentication not configured — call POST /api/v1/auth/setup first"}`, http.StatusUnauthorized)
			return
		}

		// ── Inter-node forwarded write (cluster secret) ───────────────────
		// Follower nodes strip their session token and add X-Cluster-Secret
		// before forwarding mutating requests to the leader. Accept and give
		// full write scope without requiring a user session/API token.
		if cs := r.Header.Get("X-Cluster-Secret"); cs != "" {
			if a.cluster.ValidateClusterSecret(cs) {
				p := &Principal{
					Kind:   "node",
					ID:     r.Header.Get("X-Served-By"),
					Scopes: []string{"read", "write", "cluster:admin"},
				}
				next.ServeHTTP(w, withPrincipal(r, p))
				return
			}
			// Invalid secret — don't fall through to Bearer check; reject.
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		hdr := r.Header.Get("Authorization")
		if !strings.HasPrefix(hdr, "Bearer ") {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		raw := strings.TrimPrefix(hdr, "Bearer ")

		// ── Session token (login-issued, node-local, 8 h TTL) ────────────
		if username, ok := a.sessions.lookup(raw); ok {
			p := &Principal{
				Kind:   "user",
				ID:     username,
				Scopes: []string{"read", "write", "cluster:admin"},
			}
			next.ServeHTTP(w, withPrincipal(r, p))
			return
		}

		// ── M7 API token (Raft-replicated, scoped) ────────────────────────
		tok, ok := a.lookupAPIToken(raw)
		if !ok || tok.IsExpired() {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		p := &Principal{
			Kind:   "token",
			ID:     tok.ID,
			Scopes: tok.Scopes,
			Token:  tok,
		}
		next.ServeHTTP(w, withPrincipal(r, p))
	})
}

// RequireScope returns a middleware that rejects the request with 403 when
// the authenticated principal does not carry the requested scope. Must be
// applied after Auth.
func (a *App) RequireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := PrincipalFrom(r)
			if p == nil || !p.HasScope(scope) {
				http.Error(w, `{"error":"forbidden — insufficient scope"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requireWrite rejects mutating requests (POST, PUT, PATCH, DELETE) when
// the authenticated principal only has read scope. Applied globally so
// all existing write routes are covered without per-route annotation.
func (a *App) requireWrite(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			p := PrincipalFrom(r)
			if p == nil || !p.HasScope("write") {
				http.Error(w, `{"error":"forbidden — write scope required"}`, http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
