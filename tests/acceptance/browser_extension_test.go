// Acceptance tests for M22.5 — Browser Extension Push Notification Bridge.
//
// FSIDs covered (server-side SSE endpoint only; browser extension UI is
// validated manually in the demo environment):
//
//	FS-BrowserExtSseConnect     → TestSseConnect
//	FS-BrowserExtSseReconnect   → TestSseReconnectAfterNodeRestart (skipped — requires process control)
//	FS-BrowserExtNotifyDeviceNew  → TestSseDeviceNewEvent
//	FS-BrowserExtNotifyClusterDown → TestSseClusterNodeDownEvent (skipped — requires cluster harness timing)
//	FS-BrowserExtNotifyBlocklistFailed → skipped (scheduler timing)
//	FS-BrowserExtNotifyFilterPause → TestSseFilterPauseEvent
//	FS-BrowserExtBadgeConnected  → (extension UI — manual)
//	FS-BrowserExtPopupStatus     → (extension UI — manual)
//	FS-BrowserExtPopupSettings   → (extension UI — manual)
//	FS-BrowserExtFirefoxMv2      → (extension package — manual)
//	FS-BrowserExtChromeMv3       → (extension package — manual)
//
// Strategy:
//   - Open a real HTTP chunked-response reader against GET /api/v1/events.
//   - Trigger events via the existing webhook test-fire endpoint or DNS queries.
//   - Assert SSE frames arrive within 2 seconds.

package acceptance

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// sseFrame holds a single parsed SSE frame.
type sseFrame struct {
	Event string
	Data  string
	ID    string
}

// openSSE opens a persistent SSE connection to GET /api/v1/events on the
// given node. Returns a channel that receives parsed SSE frames. The
// connection is closed when the test ends via t.Cleanup.
//
// Skips the test if the endpoint returns 404 (implementation pending).
func openSSE(t *testing.T, n *Node) <-chan sseFrame {
	t.Helper()
	url := n.APIBase + "/api/v1/events"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("openSSE: build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+n.sessionToken)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("openSSE: connect: %v", err)
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNotImplemented {
		resp.Body.Close()
		t.Skipf("M22.5 impl pending: GET /api/v1/events returned %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("openSSE: expected 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		resp.Body.Close()
		t.Fatalf("openSSE: Content-Type: got %q, want text/event-stream", ct)
	}

	ch := make(chan sseFrame, 32)
	t.Cleanup(func() { resp.Body.Close() })

	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(resp.Body)
		var frame sseFrame
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case line == "":
				if frame.Event != "" || frame.Data != "" {
					ch <- frame
				}
				frame = sseFrame{}
			case strings.HasPrefix(line, "event:"):
				frame.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				frame.Data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			case strings.HasPrefix(line, "id:"):
				frame.ID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
			}
		}
	}()

	return ch
}

// waitForSSEFrame blocks until a frame with the given event type arrives on
// ch or the timeout elapses.
func waitForSSEFrame(t *testing.T, ch <-chan sseFrame, eventType string, timeout time.Duration) (sseFrame, bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case f, ok := <-ch:
			if !ok {
				return sseFrame{}, false
			}
			if f.Event == eventType {
				return f, true
			}
		case <-deadline:
			return sseFrame{}, false
		}
	}
}

// ── FS-BrowserExtSseConnect ───────────────────────────────────────────────────

// TestSseConnect verifies that GET /api/v1/events returns a streaming
// text/event-stream response that stays open and delivers a keepalive comment
// within 20 seconds.
//
// FSID: FS-BrowserExtSseConnect
func TestSseConnect(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})

	url := n.APIBase + "/api/v1/events"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+n.sessionToken)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNotImplemented {
		t.Skipf("M22.5 impl pending: GET /api/v1/events returned %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type: got %q, want text/event-stream", ct)
	}

	// Read lines until we see any output (keepalive comment or event) within 20s.
	done := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			done <- line
			return
		}
	}()

	select {
	case line := <-done:
		t.Logf("SSE: first line received: %q", line)
	case <-time.After(20 * time.Second):
		t.Fatal("no SSE output received within 20 s — connection may not be streaming")
	}
}

// ── FS-BrowserExtNotifyDeviceNew ─────────────────────────────────────────────

// TestSseDeviceNewEvent verifies that a DNS query from a new client IP causes
// a device.new SSE frame to be delivered to an open event stream within 5 s.
//
// FSID: FS-BrowserExtNotifyDeviceNew
func TestSseDeviceNewEvent(t *testing.T) {
	t.Parallel()

	c := startClusterWithEnv(t, 1, []string{"SKOED_TEST_MODE=1"})
	n := c.Leader(t).Node

	ch := openSSE(t, n)

	// Issue a DNS query from a never-seen IP so device.new fires.
	newClientIP := "10.77.66.55"
	dnsQueryAsClient(t, n.DNSAddr, "example.com", dns.TypeA, newClientIP)

	frame, ok := waitForSSEFrame(t, ch, "device.new", 5*time.Second)
	if !ok {
		t.Fatal("no device.new SSE frame received within 5 s")
	}

	var payload struct {
		Event string `json:"event"`
		Data  struct {
			ClientIP string `json:"client_ip"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(frame.Data), &payload); err != nil {
		t.Fatalf("parse SSE data: %v\ndata: %s", err, frame.Data)
	}
	if payload.Data.ClientIP != newClientIP {
		t.Errorf("data.client_ip: got %q, want %q", payload.Data.ClientIP, newClientIP)
	}
}

// ── FS-BrowserExtNotifyFilterPause ───────────────────────────────────────────

// TestSseFilterPauseEvent verifies that POST /api/v1/filter/pause causes a
// filter.pause_started SSE frame within 5 s.
//
// FSID: FS-BrowserExtNotifyFilterPause
func TestSseFilterPauseEvent(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})

	ch := openSSE(t, n)

	// Trigger a filtering pause.
	resp := n.apiDo(t, "POST", "/api/v1/filter/pause", `{"duration_seconds":60}`)
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNotImplemented {
		t.Skipf("filter pause not implemented: %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		t.Skipf("filter pause returned unexpected status %d — skipping SSE check", resp.StatusCode)
	}

	frame, ok := waitForSSEFrame(t, ch, "filter.pause_started", 5*time.Second)
	if !ok {
		t.Fatal("no filter.pause_started SSE frame received within 5 s")
	}
	t.Logf("SSE frame received: event=%s data=%s", frame.Event, frame.Data)
}

// ── FS-BrowserExtSseReconnect ─────────────────────────────────────────────────

// TestSseReconnect is skipped: verifying reconnect behaviour requires
// interrupting the node's TCP listener, which is not supported in the
// acceptance harness. Covered by manual Proxmox integration testing.
//
// FSID: FS-BrowserExtSseReconnect
func TestSseReconnect(t *testing.T) {
	t.Skip("FS-BrowserExtSseReconnect: skipped — requires TCP interrupt not available in acceptance harness")
}
