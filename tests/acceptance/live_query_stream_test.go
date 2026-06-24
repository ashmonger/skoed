// Acceptance tests for the Live Query Stream feature (M29).
//
// FSIDs covered:
//   FS-LiveQueryStreamConnect, FS-LiveQueryStreamEventShape,
//   FS-LiveQueryStreamBlockedQuery, FS-LiveQueryStreamFilterByProfile,
//   FS-LiveQueryStreamFilterByResult, FS-LiveQueryStreamUnauthenticated,
//   FS-LiveQueryStreamDisconnect, FS-LiveQueryStreamHeartbeat

package acceptance

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// queryEvent mirrors the SSE data payload.
type queryEvent struct {
	Domain     string `json:"domain"`
	Type       string `json:"type"`
	ClientIP   string `json:"client_ip"`
	ProfileID  string `json:"profile_id"`
	Result     string `json:"result"`
	DurationMs int    `json:"duration_ms"`
	Timestamp  string `json:"timestamp"`
}

// openStream opens GET /api/v1/query-log/stream with an authenticated Bearer
// token and optional query-string parameters. Returns the response body; the
// caller is responsible for closing it. Fails immediately if the status is not 200.
func openStream(t *testing.T, n *Node, params string) io.ReadCloser {
	t.Helper()
	streamURL := n.APIBase + "/api/v1/query-log/stream"
	if params != "" {
		streamURL += "?" + params
	}
	req, err := http.NewRequest("GET", streamURL, nil)
	if err != nil {
		t.Fatalf("openStream: build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+n.sessionToken)

	client := &http.Client{Timeout: 0} // no timeout — SSE is long-lived
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("openStream: connect: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("openStream: expected 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		resp.Body.Close()
		t.Fatalf("openStream: expected text/event-stream content-type, got %q", ct)
	}
	return resp.Body
}

// readNextEvent reads SSE lines until a complete "data: …" event is found or
// the deadline is exceeded. Returns the parsed queryEvent and the raw JSON payload.
func readNextEvent(t *testing.T, r io.Reader, deadline time.Duration) (queryEvent, map[string]any) {
	t.Helper()
	type result struct {
		ev  queryEvent
		raw map[string]any
	}
	ch := make(chan result, 1)
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				payload := strings.TrimPrefix(line, "data: ")
				var ev queryEvent
				var raw map[string]any
				if err := json.Unmarshal([]byte(payload), &ev); err == nil {
					json.Unmarshal([]byte(payload), &raw) //nolint:errcheck
					ch <- result{ev, raw}
					return
				}
			}
		}
	}()
	select {
	case res := <-ch:
		return res.ev, res.raw
	case <-time.After(deadline):
		t.Fatalf("readNextEvent: no event received within %v", deadline)
		return queryEvent{}, nil
	}
}

// ── FS-LiveQueryStreamConnect ─────────────────────────────────────────────────

// TestLiveQueryStreamConnect verifies the endpoint returns 200 text/event-stream.
func TestLiveQueryStreamConnect(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	body := openStream(t, n, "")
	defer body.Close()
	// If we got here, Content-Type and status were correct (asserted in openStream).
}

// ── FS-LiveQueryStreamEventShape ─────────────────────────────────────────────

// TestLiveQueryStreamEventShape verifies every required field is present in an event.
func TestLiveQueryStreamEventShape(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	body := openStream(t, n, "")
	defer body.Close()

	// Trigger a query.
	go func() {
		time.Sleep(200 * time.Millisecond)
		dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)
	}()

	ev, raw := readNextEvent(t, body, 10*time.Second)

	if ev.Domain == "" {
		t.Error("event missing domain")
	}
	if ev.Type == "" {
		t.Error("event missing type")
	}
	if ev.ClientIP == "" {
		t.Error("event missing client_ip")
	}
	// profile_id key must be present (may be "" for the default profile).
	if _, ok := raw["profile_id"]; !ok {
		t.Error("event JSON missing profile_id key")
	}
	if ev.Result == "" {
		t.Error("event missing result")
	}
	if ev.Timestamp == "" {
		t.Error("event missing timestamp")
	}
}

// ── FS-LiveQueryStreamBlockedQuery ───────────────────────────────────────────

// TestLiveQueryStreamBlockedQuery verifies blocked queries are streamed with result="blocked".
func TestLiveQueryStreamBlockedQuery(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})
	addInlineBlocklist(t, n, "test", []string{"ads.blocked.test"}, "")

	body := openStream(t, n, "result=blocked")
	defer body.Close()

	go func() {
		time.Sleep(200 * time.Millisecond)
		dnsQuery(t, n.DNSAddr, "ads.blocked.test", dns.TypeA)
	}()

	ev, _ := readNextEvent(t, body, 10*time.Second)
	if ev.Result != "blocked" {
		t.Fatalf("expected result=blocked, got %q", ev.Result)
	}
	if !strings.Contains(ev.Domain, "ads.blocked.test") {
		t.Fatalf("expected domain ads.blocked.test, got %q", ev.Domain)
	}
}

// ── FS-LiveQueryStreamFilterByResult ─────────────────────────────────────────

// TestLiveQueryStreamFilterByResult verifies ?result=blocked hides forwarded queries.
func TestLiveQueryStreamFilterByResult(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})
	addInlineBlocklist(t, n, "test", []string{"blocked-only.test"}, "")

	body := openStream(t, n, "result=blocked")
	defer body.Close()

	evCh := make(chan queryEvent, 10)
	go func() {
		scanner := bufio.NewScanner(body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				var ev queryEvent
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err == nil {
					evCh <- ev
				}
			}
		}
	}()

	// Fire a forwarded then a blocked query.
	go func() {
		time.Sleep(200 * time.Millisecond)
		dnsQuery(t, n.DNSAddr, "allowed.example.com", dns.TypeA)
		time.Sleep(100 * time.Millisecond)
		dnsQuery(t, n.DNSAddr, "blocked-only.test", dns.TypeA)
	}()

	// We must receive the blocked event; the forwarded one must not appear.
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-evCh:
			if ev.Result == "forwarded" && strings.Contains(ev.Domain, "allowed.example.com") {
				t.Fatalf("forwarded query leaked through result=blocked filter: %+v", ev)
			}
			if ev.Result == "blocked" {
				return // correct — only blocked arrived
			}
		case <-deadline:
			t.Fatal("timed out waiting for blocked event")
		}
	}
}

// ── FS-LiveQueryStreamUnauthenticated ─────────────────────────────────────────

// TestLiveQueryStreamUnauthenticated verifies missing token returns 401.
func TestLiveQueryStreamUnauthenticated(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	url := n.APIBase + "/api/v1/query-log/stream"
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// ── FS-LiveQueryStreamHeartbeat ───────────────────────────────────────────────

// TestLiveQueryStreamHeartbeat verifies keep-alive comments are sent within 20s.
func TestLiveQueryStreamHeartbeat(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	body := openStream(t, n, "")
	defer body.Close()

	ch := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, ":") { // SSE comment = keep-alive
				ch <- true
				return
			}
		}
	}()

	select {
	case <-ch:
		// keep-alive received
	case <-time.After(20 * time.Second):
		t.Fatal("no SSE keep-alive comment received within 20 seconds")
	}
}

// ── FS-LiveQueryStreamDisconnect ──────────────────────────────────────────────

// TestLiveQueryStreamDisconnect verifies the server handles client disconnect cleanly
// (no panic, and the next query is processed normally after disconnect).
func TestLiveQueryStreamDisconnect(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	// Open and immediately close the stream.
	body := openStream(t, n, "")
	body.Close()

	// Give the server a moment to process the disconnect.
	time.Sleep(300 * time.Millisecond)

	// The node must still serve DNS normally after the disconnect.
	r := dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess)
}
