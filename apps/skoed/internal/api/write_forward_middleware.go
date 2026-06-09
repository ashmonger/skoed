package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// WriteForwardMiddleware returns an HTTP middleware that:
//   - Sets X-Served-By and X-Raft-Commit-Index on every response.
//   - For mutating methods (POST, PUT, PATCH, DELETE) on a non-leader node:
//     reads the request body, calls cluster.ForwardWrite, writes the result,
//     and returns without calling next.
//   - For GET/HEAD or when the node is the leader: passes the request to next.
func WriteForwardMiddleware(cluster WriteForwarder) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Always set observability headers so callers can diagnose which
			// node served the response and how far behind it is.
			w.Header().Set("X-Served-By", cluster.NodeID())
			w.Header().Set("X-Raft-Commit-Index", strconv.FormatUint(cluster.CommitIndex(), 10))

			// Read-only methods and leader nodes serve locally.
			if !isMutating(r.Method) || cluster.IsLeader() {
				next.ServeHTTP(w, r)
				return
			}

			// Follower receiving a mutating request: read body once, then forward.
			var body []byte
			if r.Body != nil {
				var err error
				body, err = io.ReadAll(r.Body)
				if err != nil {
					writeForwardError(w, fmt.Sprintf("failed to read request body: %s", err.Error()))
					return
				}
				_ = r.Body.Close()
			}

			// Include path and raw query so parameters are preserved.
			urlPath := r.URL.RequestURI()

			statusCode, respBody, respHeaders, err := cluster.ForwardWrite(r.Context(), r.Method, urlPath, body, r.Header)
			if err != nil {
				writeForwardError(w, fmt.Sprintf("forward to leader failed: %s", err.Error()))
				return
			}

			// Mirror the leader's headers onto the response, skipping any that
			// WriteForwardMiddleware already wrote (X-Served-By, X-Raft-Commit-Index).
			for k, vs := range respHeaders {
				if k == "X-Served-By" || k == "X-Raft-Commit-Index" {
					continue
				}
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(statusCode)
			if len(respBody) > 0 {
				_, _ = w.Write(respBody)
			}
		})
	}
}

// isMutating reports whether the HTTP method modifies server state.
func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// writeForwardError emits a 503 with a JSON error body when the forward
// attempt itself fails (network error, body read failure, etc.).
func writeForwardError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	body, _ := json.Marshal(map[string]string{"error": msg})
	_, _ = w.Write(body)
}
