package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)


// forwardClient is reused across ForwardWrite calls to benefit from
// connection pooling. The 10 s timeout covers the full round-trip to the leader.
var forwardClient = &http.Client{Timeout: 10 * time.Second}

// hopByHopHeaders lists headers that must not be forwarded to the leader.
// Includes the standard hop-by-hop set plus Host (leader address differs).
var hopByHopHeaders = map[string]bool{
	"Host":                true,
	"Content-Length":      true,
	"Transfer-Encoding":   true,
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Upgrade":             true,
}

// ForwardWrite proxies a write request to the current Raft leader's HTTP API.
// It is used by follower nodes that receive a mutating request: rather than
// returning an error, the follower transparently forwards the request so the
// caller gets the same result it would get talking directly to the leader.
//
// method is the HTTP verb (e.g. "POST", "PUT", "DELETE").
// path is the request-URI path (e.g. "/api/v1/blocklists").
// body is the raw request body; may be nil for DELETE / bodiless requests.
// inHeaders are the original request headers; hop-by-hop and routing headers
// are stripped before forwarding.
//
// Returns the response status code, body bytes, response headers, and any
// transport-level error. On transport failure or when no leader is known a
// 503 status is returned together with a JSON error body.
func (c *Cluster) ForwardWrite(
	ctx context.Context,
	method string,
	path string,
	body []byte,
	inHeaders http.Header,
) (int, []byte, http.Header, error) {
	leaderURL := c.LeaderAPIAddress()
	if leaderURL == "" {
		errBody, _ := json.Marshal(map[string]string{"error": "no leader currently elected"})
		return http.StatusServiceUnavailable, errBody, nil, fmt.Errorf("no leader currently elected")
	}

	target := leaderURL + path

	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, bodyReader)
	if err != nil {
		errBody, _ := json.Marshal(map[string]string{"error": fmt.Sprintf("build forward request: %s", err)})
		return http.StatusServiceUnavailable, errBody, nil, fmt.Errorf("build forward request: %w", err)
	}

	for k, vv := range inHeaders {
		if hopByHopHeaders[k] {
			continue
		}
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}

	resp, err := forwardClient.Do(req)
	if err != nil {
		errBody, _ := json.Marshal(map[string]string{"error": fmt.Sprintf("forward to leader: %s", err)})
		return http.StatusServiceUnavailable, errBody, nil, fmt.Errorf("forward to leader: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		errBody, _ := json.Marshal(map[string]string{"error": fmt.Sprintf("read leader response: %s", err)})
		return http.StatusServiceUnavailable, errBody, nil, fmt.Errorf("read leader response: %w", err)
	}

	return resp.StatusCode, respBody, resp.Header, nil
}
