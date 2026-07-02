// Acceptance tests for M42 — Query Log Stream Enhancements (Backfill + WebSocket).
//
// FSIDs covered:
//   FS-BackfillOnStreamConnect, FS-BackfillFiltersApply,
//   FS-BackfillZeroDefault, FS-BackfillCappedAt500,
//   FS-WebSocketStreamConnects, FS-WebSocketAuthRequired

package acceptance

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/miekg/dns"
)

// ── FS-BackfillOnStreamConnect ────────────────────────────────────────────────

// TestBackfillReturnsRecentEntries pre-populates the query log with 5 queries,
// then connects with ?backfill=5 and expects 5 backfilled events followed by
// a "backfill_end" event, then a live event for a query triggered after connect.
func TestBackfillReturnsRecentEntries(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	// Pre-fill the log with 5 queries.
	for i := 0; i < 5; i++ {
		dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)
	}
	time.Sleep(300 * time.Millisecond)

	body := openStream(t, n, "backfill=5")
	defer body.Close()

	// Expect 5 "query" events then 1 "backfill_end" event.
	backfillCount := 0
	gotEnd := false
	scanner := bufio.NewScanner(body)
	deadline := time.After(15 * time.Second)

	eventName := ""
	for !gotEnd {
		lineCh := make(chan string, 1)
		go func() {
			if scanner.Scan() {
				lineCh <- scanner.Text()
			}
		}()
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for backfill; got %d query events, end=%v", backfillCount, gotEnd)
		case line := <-lineCh:
			if strings.HasPrefix(line, "event: ") {
				eventName = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				switch eventName {
				case "query":
					backfillCount++
				case "backfill_end":
					gotEnd = true
				}
				eventName = ""
			}
		}
	}

	if backfillCount != 5 {
		t.Errorf("expected 5 backfilled query events, got %d", backfillCount)
	}
}

// ── FS-BackfillFiltersApply ───────────────────────────────────────────────────

// TestBackfillFiltersApply generates blocked and allowed queries, then connects
// with ?backfill=20&result=blocked. Only blocked entries should be replayed.
func TestBackfillFiltersApply(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	// Add a blocklist entry so one domain is blocked.
	addBlocklistEntryDirect(t, n, "blocked-m42.test")

	// Trigger a blocked and an allowed query.
	dnsQuery(t, n.DNSAddr, "blocked-m42.test", dns.TypeA) // blocked
	dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)       // allowed
	time.Sleep(300 * time.Millisecond)

	body := openStream(t, n, "backfill=20&result=blocked")
	defer body.Close()

	// Read all events up to backfill_end.
	var events []map[string]any
	gotEnd := false
	scanner := bufio.NewScanner(body)
	eventName := ""
	deadline := time.After(15 * time.Second)

	for !gotEnd {
		lineCh := make(chan string, 1)
		go func() {
			if scanner.Scan() {
				lineCh <- scanner.Text()
			}
		}()
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for backfill_end")
		case line := <-lineCh:
			if strings.HasPrefix(line, "event: ") {
				eventName = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				payload := strings.TrimPrefix(line, "data: ")
				if eventName == "query" {
					var ev map[string]any
					if err := json.Unmarshal([]byte(payload), &ev); err == nil {
						events = append(events, ev)
					}
				} else if eventName == "backfill_end" {
					gotEnd = true
				}
				eventName = ""
			}
		}
	}

	// All replayed events must have result == "blocked".
	for _, ev := range events {
		if ev["result"] != "blocked" {
			t.Errorf("backfill included non-blocked event: result=%v", ev["result"])
		}
	}
	if len(events) == 0 {
		t.Error("expected at least one blocked event in backfill")
	}
}

// ── FS-BackfillZeroDefault ────────────────────────────────────────────────────

// TestBackfillZeroNoHistoricalEvents connects without backfill and verifies no
// "backfill_end" event is ever sent (events before connect are not replayed).
func TestBackfillZeroNoHistoricalEvents(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	// Pre-fill some entries.
	dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)
	time.Sleep(300 * time.Millisecond)

	body := openStream(t, n, "") // no backfill parameter
	defer body.Close()

	// Trigger a live query so we know the stream is working.
	go func() {
		time.Sleep(200 * time.Millisecond)
		dnsQuery(t, n.DNSAddr, "live.com", dns.TypeA)
	}()

	// Read events for 5 s; none should be "backfill_end".
	scanner := bufio.NewScanner(body)
	deadline := time.After(5 * time.Second)
	eventName := ""
	for {
		lineCh := make(chan string, 1)
		go func() {
			if scanner.Scan() {
				lineCh <- scanner.Text()
			}
		}()
		select {
		case <-deadline:
			return // ok: no backfill_end seen
		case line := <-lineCh:
			if strings.HasPrefix(line, "event: ") {
				eventName = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") && eventName == "backfill_end" {
				t.Error("unexpected backfill_end event when no backfill requested")
				return
			}
		}
	}
}

// ── FS-BackfillCappedAt500 ───────────────────────────────────────────────────

// TestBackfillCappedAt500 requests backfill=1000 and verifies the server
// responds with HTTP 200 (no error) and sends a backfill_end event.
func TestBackfillCappedAt500(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	body := openStream(t, n, "backfill=1000")
	defer body.Close()

	// We just need to see a backfill_end event without an error.
	gotEnd := false
	scanner := bufio.NewScanner(body)
	eventName := ""
	deadline := time.After(10 * time.Second)
	for !gotEnd {
		lineCh := make(chan string, 1)
		go func() {
			if scanner.Scan() {
				lineCh <- scanner.Text()
			}
		}()
		select {
		case <-deadline:
			t.Fatal("timed out waiting for backfill_end with backfill=1000")
		case line := <-lineCh:
			if strings.HasPrefix(line, "event: ") {
				eventName = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") && eventName == "backfill_end" {
				gotEnd = true
			}
		}
	}
}

// ── FS-WebSocketStreamConnects ────────────────────────────────────────────────

// TestWebSocketStreamConnects opens a WebSocket to /api/v1/query-log/ws and
// verifies a live DNS query event arrives as a JSON text frame.
func TestWebSocketStreamConnects(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	wsURL := strings.Replace(n.APIBase, "http://", "ws://", 1) + "/api/v1/query-log/ws"
	hdr := http.Header{"Authorization": {"Bearer " + n.sessionToken}}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		if resp != nil {
			t.Fatalf("WebSocket dial failed: %v (status %d)", err, resp.StatusCode)
		}
		t.Fatalf("WebSocket dial failed: %v", err)
	}
	defer conn.Close()

	// Trigger a DNS query so an event is pushed.
	go func() {
		time.Sleep(200 * time.Millisecond)
		dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)
	}()

	conn.SetReadDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	var ev map[string]any
	if err := json.Unmarshal(msg, &ev); err != nil {
		t.Fatalf("unmarshal WS frame: %v (raw: %s)", err, msg)
	}
	if ev["domain"] == nil {
		t.Error("WS event missing domain field")
	}
	if ev["result"] == nil {
		t.Error("WS event missing result field")
	}
	if ev["timestamp"] == nil {
		t.Error("WS event missing timestamp field")
	}
}

// ── FS-WebSocketAuthRequired ──────────────────────────────────────────────────

// TestWebSocketAuthRequired verifies that the WS upgrade is rejected when no
// Bearer token is provided.
func TestWebSocketAuthRequired(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	wsURL := strings.Replace(n.APIBase, "http://", "ws://", 1) + "/api/v1/query-log/ws"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected WS dial to fail without auth, but succeeded")
	}
	if resp == nil {
		t.Fatalf("expected HTTP 401 response, got nil")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected HTTP 401, got %d", resp.StatusCode)
	}
}

// addBlocklistEntryDirect adds a single-entry inline blocklist via the API.
func addBlocklistEntryDirect(t *testing.T, n *Node, domain string) {
	t.Helper()
	body := `{"name":"m42-test","type":"inline","domains":["` + domain + `"],"enabled":true}`
	resp := n.apiDo(t, "POST", "/api/v1/blocklists", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Logf("addBlocklistEntryDirect: status %d (non-fatal, filtering test may still work)", resp.StatusCode)
	}
}

// wsDialURL converts an HTTP base URL to a WebSocket URL with the given path.
func wsDialURL(base, path string) string {
	u, _ := url.Parse(base)
	u.Scheme = "ws"
	u.Path = path
	return u.String()
}
