package api

import (
	"net/http"
)

// BasicAuth returns a middleware that enforces HTTP Basic Auth.
//
// Routing rules:
//   - POST /api/v1/auth/setup is always exempt (handled as a public endpoint).
//   - GET /api/v1/health is always exempt (no auth required).
//   - When auth is NOT configured, all other endpoints return 401 to force setup.
//   - When auth IS configured, requests without valid credentials return 401.
func (a *App) BasicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The setup and health endpoints are registered outside the authenticated
		// group in Router(), so this middleware is never invoked for them.
		// The guard below is a safety net in case the middleware is ever applied
		// more broadly.
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/setup" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/v1/health" {
			next.ServeHTTP(w, r)
			return
		}

		if !a.authStore.IsConfigured() {
			// No credentials configured yet: reject all non-setup traffic so that
			// the client is forced to call the setup endpoint first.
			w.Header().Set("WWW-Authenticate", `Basic realm="skoed"`)
			http.Error(w, `{"error":"authentication not configured — call POST /api/v1/auth/setup first"}`, http.StatusUnauthorized)
			return
		}

		username, password, ok := r.BasicAuth()
		if !ok || !a.authStore.Verify(username, password) {
			w.Header().Set("WWW-Authenticate", `Basic realm="skoed"`)
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}
