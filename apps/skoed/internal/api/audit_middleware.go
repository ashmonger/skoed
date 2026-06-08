package api

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/skoed/skoed/internal/cluster"
	"github.com/go-chi/chi/v5"
)

// auditMiddleware wraps the authenticated mutating route group. For
// every non-read request it captures method + path + body, lets the
// inner handler run, then posts an audit.append Raft command summarising
// what happened.
//
// Failures during the audit write are LOGGED but never propagated —
// a successful blocklist create that we couldn't audit is still better
// than a failed create. Audit is best-effort observability, not a
// transactional barrier.
func (a *App) auditMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auditExempt(r) {
			next.ServeHTTP(w, r)
			return
		}

		// Capture body up to 8 KB so we can write a diff summary.
		var bodyBytes []byte
		if r.Body != nil && r.ContentLength != 0 {
			body, _ := io.ReadAll(io.LimitReader(r.Body, 8*1024))
			bodyBytes = body
			r.Body = io.NopCloser(bytes.NewReader(body))
		}

		rw := &auditResponseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)

		// Only emit audit on the leader (writes get here via LeaderForward
		// already, but defensive). If we're not the leader, the forward
		// path already audited on the actual leader's side.
		c := a.cluster
		if c == nil || !c.IsLeader() {
			return
		}

		actor := "user:" + currentActor(a, r)
		action := actionForRoute(r)
		target := targetFromPath(r, bodyBytes)
		result := "ok"
		errStr := ""
		if rw.status < 200 || rw.status >= 300 {
			result = "error"
			errStr = strings.TrimSpace(rw.bodyBuf.String())
			if len(errStr) > 256 {
				errStr = errStr[:256] + "…"
			}
		}

		payload := cluster.AuditAppendPayload{
			ID:        newAuditID(),
			TimeUnix:  time.Now().Unix(),
			Actor:     actor,
			Action:    action,
			Target:    target,
			Result:    result,
			Error:     errStr,
			Diff:      summariseBody(bodyBytes),
			RequestID: r.Header.Get("X-Request-ID"),
		}
		if err := c.AppendAuditEntry(payload); err != nil {
			log.Printf("audit: append failed: %v", err)
			return
		}
		if a.metrics != nil {
			a.metrics.ObserveAudit(action)
		}
	})
}

// auditExempt returns true for routes that should NOT be audited:
// read verbs, auth setup (recorded by the SetupAuth handler itself), and
// the always-public /api/v1/health probe.
func auditExempt(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if r.URL.Path == "/api/v1/health" {
		return true
	}
	// Internal peer-to-peer forwarding endpoints — already gated by the
	// cluster secret, not user-triggered.
	if strings.HasPrefix(r.URL.Path, "/api/v1/cluster/_internal/") {
		return true
	}
	// M5.9.7 — domain tester is read-only (no state change); skip the
	// audit append. Keeps bbolt + the audit page free of "user
	// scanned 200 random domains via the curl loop" noise.
	if r.URL.Path == "/api/v1/test-domain" {
		return true
	}
	return false
}

// auditResponseWriter captures status + a small slice of the response
// body so the middleware can stash an error snippet.
type auditResponseWriter struct {
	http.ResponseWriter
	status  int
	bodyBuf bytes.Buffer
}

func (w *auditResponseWriter) WriteHeader(s int) {
	w.status = s
	w.ResponseWriter.WriteHeader(s)
}

func (w *auditResponseWriter) Write(b []byte) (int, error) {
	if w.bodyBuf.Len() < 1024 {
		w.bodyBuf.Write(b[:min(len(b), 1024-w.bodyBuf.Len())])
	}
	return w.ResponseWriter.Write(b)
}

func currentActor(a *App, r *http.Request) string {
	u, _, ok := r.BasicAuth()
	if !ok || u == "" {
		// Pre-setup, the request still gets here if it's POST /auth/setup
		// but that's auditExempt. Any other auth-less mutation is a bug
		// caught by the BasicAuth middleware before this runs.
		return "anonymous"
	}
	return u
}

// actionForRoute maps the request to a "<resource>.<verb>" action. The
// chi route pattern (r.URL.Path with params replaced) would be cleaner
// but raw path is available everywhere; the table here covers every
// mutating route currently registered.
func actionForRoute(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	pattern := r.URL.Path
	if rctx != nil && rctx.RoutePattern() != "" {
		pattern = rctx.RoutePattern()
	}
	// pattern is something like /api/v1/blocklists/{id}/refresh
	verb := strings.ToLower(r.Method)
	switch r.Method {
	case http.MethodPost:
		verb = "create"
	case http.MethodPut:
		verb = "update"
	case http.MethodPatch:
		verb = "update"
	case http.MethodDelete:
		verb = "delete"
	}

	// Lookup against the explicit catalogue first.
	if v, ok := auditActionCatalogue[r.Method+" "+pattern]; ok {
		return v
	}
	// Fallback: <segment>.<verb> from /api/v1/<segment>/...
	seg := strings.TrimPrefix(pattern, "/api/v1/")
	if i := strings.Index(seg, "/"); i >= 0 {
		seg = seg[:i]
	}
	if seg == "" {
		seg = "request"
	}
	return seg + "." + verb
}

// auditActionCatalogue captures the explicit "<method> <pattern>" →
// action mapping used by the spec. Patterns are chi route patterns.
var auditActionCatalogue = map[string]string{
	"POST /api/v1/blocklists":                                    "blocklist.create",
	"PATCH /api/v1/blocklists/{id}":                              "blocklist.update",
	"DELETE /api/v1/blocklists/{id}":                             "blocklist.delete",
	"POST /api/v1/blocklists/{id}/refresh":                       "blocklist.refresh",
	"POST /api/v1/allowlist":                                     "allowlist.create",
	"DELETE /api/v1/allowlist/{domain}":                          "allowlist.delete",
	"POST /api/v1/local-dns":                                     "local_dns.create",
	"PUT /api/v1/local-dns/{id}":                                 "local_dns.update",
	"DELETE /api/v1/local-dns/{id}":                              "local_dns.delete",
	"PATCH /api/v1/settings":                                     "settings.update",
	"PUT /api/v1/auth/password":                                  "auth.password",
	"POST /api/v1/profiles":                                      "profile.create",
	"PATCH /api/v1/profiles/{id}":                                "profile.update",
	"DELETE /api/v1/profiles/{id}":                               "profile.delete",
	"POST /api/v1/schedules":                                     "schedule.create",
	"PATCH /api/v1/schedules/{id}":                               "schedule.update",
	"DELETE /api/v1/schedules/{id}":                              "schedule.delete",
	"POST /api/v1/schedules/{id}/bindings":                       "schedule.bind",
	"DELETE /api/v1/schedules/{id}/bindings/{profile}/{blocklist}": "schedule.unbind",
	"PATCH /api/v1/categories/{name}":                            "category.update",
	"POST /api/v1/categories/{name}/enable":                      "category.enable",
	"POST /api/v1/categories/{name}/disable":                     "category.disable",
	"POST /api/v1/clients/anomalies/{id}/acknowledge":            "anomaly.acknowledge",
	"POST /api/v1/cluster/tokens":                                "cluster.token",
	"POST /api/v1/cluster/leadership/transfer":                   "cluster.leadership",
	"DELETE /api/v1/cluster/nodes/{node_id}":                     "cluster.remove_node",
	"POST /api/v1/config/import":                                 "config.import",
	"POST /api/v1/dns/cache/purge":                               "dns_cache.purge",
	"POST /api/v1/upgrade/start":                                 "upgrade.start",
}

// targetFromPath returns "<resource>:<id>" when an id is available
// from either the URL pattern (e.g. PATCH /blocklists/{id}) or the
// request body (POST /blocklists with {"id":"…"}). Falls back to the
// bare resource name when neither carries an id.
func targetFromPath(r *http.Request, body []byte) string {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return ""
	}
	resource := resourceFromPattern(rctx.RoutePattern())
	// URL params first — for {id}, {name}, {domain}, {node_id} routes.
	for _, k := range []string{"id", "name", "domain", "node_id"} {
		if v := rctx.URLParam(k); v != "" {
			return resource + ":" + v
		}
	}
	// Body fallback — POSTs typically carry the new id in the JSON body.
	if id := idFromBody(body); id != "" {
		return resource + ":" + id
	}
	return resource
}

// idFromBody extracts the most likely identity field from a JSON body.
func idFromBody(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	for _, k := range []string{"id", "name", "domain", "hostname", "username", "target_node_id"} {
		if v, ok := obj[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func resourceFromPattern(pattern string) string {
	seg := strings.TrimPrefix(pattern, "/api/v1/")
	if i := strings.Index(seg, "/"); i >= 0 {
		seg = seg[:i]
	}
	return seg
}

// summariseBody turns a JSON request body into a one-line operator
// summary. For JSON objects we cherry-pick a couple of well-known
// fields; otherwise we fall back to a length indicator.
func summariseBody(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	var parts []string
	for _, k := range []string{"id", "name", "domain", "hostname", "username"} {
		if v, ok := obj[k]; ok {
			parts = append(parts, k+"="+toString(v))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	out := strings.Join(parts, ", ")
	if len(out) > 256 {
		out = out[:256] + "…"
	}
	return out
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// newAuditID returns a 26-char ULID-ish id. Crockford-base32 of a
// timestamp + 80 bits of entropy would be the textbook form; this is
// the practical 16-bytes-hex form already used by query-log entries.
// Adequate for cross-node correlation; not cryptographically meaningful.
func newAuditID() string {
	b := make([]byte, 13)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
