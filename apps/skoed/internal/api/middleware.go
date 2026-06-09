package api

import (
	"net/http"
	"strings"
)

// Auth returns a middleware that accepts either HTTP Basic Auth (deprecated
// transition path) or a Bearer token issued by POST /api/v1/tokens.
//
// On success the authenticated Principal is stored on the request context
// (see PrincipalFrom). Scope enforcement is performed by RequireScope and
// by the individual route handlers for cluster:admin operations.
//
// Routing rules:
//   - POST /api/v1/auth/setup and GET /api/v1/health are always exempt
//     (registered outside the protected group in Router).
//   - When auth is NOT configured, all other endpoints return 401 so the
//     operator is forced to call the setup endpoint first.
//   - An unknown / expired / revoked Bearer token → 401.
//   - A valid Bearer token for a read-only scope calling a mutating method
//     is NOT rejected here; RequireScope handles that at the route level.
func (a *App) Auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Safety nets — these routes are registered outside the group.
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/setup" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/v1/health" {
			next.ServeHTTP(w, r)
			return
		}

		if !a.authStore.IsConfigured() {
			w.Header().Set("WWW-Authenticate", `Basic realm="skoed"`)
			http.Error(w, `{"error":"authentication not configured — call POST /api/v1/auth/setup first"}`, http.StatusUnauthorized)
			return
		}

		hdr := r.Header.Get("Authorization")

		// ── Bearer token path ─────────────────────────────────────────────
		if strings.HasPrefix(hdr, "Bearer ") {
			raw := strings.TrimPrefix(hdr, "Bearer ")
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
			return
		}

		// ── Basic Auth path (deprecated transition) ───────────────────────
		username, password, ok := r.BasicAuth()
		if !ok || !a.authStore.Verify(username, password) {
			w.Header().Set("WWW-Authenticate", `Basic realm="skoed"`)
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		p := &Principal{
			Kind:  "user",
			ID:    username,
			Scopes: []string{"read", "write", "cluster:admin"},
		}
		next.ServeHTTP(w, withPrincipal(r, p))
	})
}

// BasicAuth is kept as an alias for the old middleware name so that any
// external test or embed that still references it continues to compile.
// New code should use Auth.
func (a *App) BasicAuth(next http.Handler) http.Handler {
	return a.Auth(next)
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
