// Package middleware holds HTTP middleware specific to the dblock management API.
package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// Cluster is the subset of *cluster.Cluster the forwarder needs. Defined as
// an interface so the middleware doesn't import the concrete cluster package
// (avoids cycles if api/handlers/cluster.go later moves to this package).
type Cluster interface {
	IsLeader() bool
	LeaderAPIAddress() string // e.g. "http://192.168.1.10:8080"; "" if no leader known
	LeaderID() string
}

// leaderRedirect is the JSON body returned when a follower cannot forward a
// mutating request because no leader is currently elected (or the forward
// attempt failed). It mirrors the LeaderRedirect schema in the management API
// OpenAPI spec (x-cluster-write-semantics).
type leaderRedirect struct {
	Error         string `json:"error"`
	LeaderID      string `json:"leader_id"`
	LeaderAddress string `json:"leader_address"`
}

// forwardTimeout caps each follower→leader round-trip. Chosen to be longer
// than a typical Raft apply (single-digit ms) but short enough that a wedged
// leader fails fast rather than tying up follower goroutines.
const forwardTimeout = 10 * time.Second

// LeaderForward wraps next so that mutating requests on followers are
// reverse-proxied to the current leader. GET/HEAD always pass through and are
// served locally from bbolt so reads remain available during leader
// transitions.
func LeaderForward(c Cluster, next http.Handler) http.Handler {
	client := &http.Client{Timeout: forwardTimeout}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reads are always served locally — no cluster round-trip required.
		if isReadMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}

		// Leader: serve locally.
		if c.IsLeader() {
			next.ServeHTTP(w, r)
			return
		}

		// Follower: forward to the current leader.
		leaderBase := strings.TrimRight(c.LeaderAPIAddress(), "/")
		if leaderBase == "" {
			writeLeaderRedirect(w, http.StatusServiceUnavailable, leaderRedirect{
				Error: "no leader",
			})
			return
		}

		// Read the body once so we can safely build a new outbound request.
		// r.Body is nil for some bodyless mutations (e.g. POST refresh).
		var body io.Reader
		if r.Body != nil {
			buf, err := io.ReadAll(r.Body)
			if err != nil {
				writeLeaderRedirect(w, http.StatusServiceUnavailable, leaderRedirect{
					Error:         "failed to read request body: " + err.Error(),
					LeaderID:      c.LeaderID(),
					LeaderAddress: leaderBase,
				})
				return
			}
			_ = r.Body.Close()
			if len(buf) > 0 {
				body = bytes.NewReader(buf)
			}
		}

		// Preserve the full request URI (path + raw query) when targeting the leader.
		target := leaderBase + r.URL.RequestURI()

		outReq, err := http.NewRequestWithContext(r.Context(), r.Method, target, body)
		if err != nil {
			writeLeaderRedirect(w, http.StatusServiceUnavailable, leaderRedirect{
				Error:         "failed to build forward request: " + err.Error(),
				LeaderID:      c.LeaderID(),
				LeaderAddress: leaderBase,
			})
			return
		}

		// Copy headers verbatim — Authorization, Content-Type, X-Request-ID,
		// and anything else the admin sent. Hop-by-hop headers are dropped so
		// we don't accidentally pin the forwarded connection's lifecycle.
		copyHeaders(outReq.Header, r.Header)

		resp, err := client.Do(outReq)
		if err != nil {
			// Leader just died, connection refused, timeout, etc. Surface as 503
			// so the client can retry against the new leader once one is elected.
			writeLeaderRedirect(w, http.StatusServiceUnavailable, leaderRedirect{
				Error:         "forward to leader failed: " + err.Error(),
				LeaderID:      c.LeaderID(),
				LeaderAddress: leaderBase,
			})
			return
		}
		defer resp.Body.Close()

		// Mirror the leader's response back to the original caller verbatim.
		// We intentionally do not rewrite status codes — if the leader returns
		// 409 LeaderRedirect (leadership changed mid-flight), the client sees
		// it as-is and can react.
		copyHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})
}

// isReadMethod reports whether the HTTP method is a safe, read-only method
// that the spec mandates we serve locally from bbolt even on followers.
func isReadMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

// hopByHopHeaders are connection-scoped headers defined by RFC 7230 §6.1.
// They must not be forwarded across a proxy hop.
var hopByHopHeaders = map[string]struct{}{
	"Connection":          {},
	"Proxy-Connection":    {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

// copyHeaders copies src into dst, skipping hop-by-hop headers.
func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		if _, skip := hopByHopHeaders[http.CanonicalHeaderKey(k)]; skip {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// writeLeaderRedirect writes a JSON LeaderRedirect body with the given status.
// Used both when no leader is known and when the forward round-trip fails.
func writeLeaderRedirect(w http.ResponseWriter, status int, body leaderRedirect) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
