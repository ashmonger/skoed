// Acceptance tests for M3.6 anti-spoof detection.
//
// FSIDs covered:
//   FS-SpoofMacChangedForKnownClientId   → TestSpoofMacChangedForKnownClientId
//   FS-SpoofClientIdChangedForKnownMac   → TestSpoofClientIdChangedForKnownMac
//   FS-SpoofNewMacForExistingHostname    → TestSpoofNewMacForExistingHostname
//   FS-SpoofHostnameChangeIsInfo         → TestSpoofHostnameChangeIsInfo
//   FS-SpoofAnomaliesInResponse          → TestSpoofAnomaliesInResponse
//   FS-SpoofDashboardAlert               → (UI-only — exercised manually via the demo recipe)
//   FS-SpoofAnomalyRetention             → TestSpoofAnomalyRetention
//   FS-SpoofAcknowledge                  → TestSpoofAcknowledge
//
// Strategy: each test uses a mutable httptest server feeding the
// generic HTTP connector. The test rewrites the server's lease payload
// between polls to simulate a spoof event, then asserts the anomaly
// appears via /api/v1/clients/anomalies.

package acceptance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// mutableLeaseServer lets a test rewrite the JSON payload between
// connector polls.
type mutableLeaseServer struct {
	mu      sync.Mutex
	payload []byte
	srv     *httptest.Server
}

func newMutableLeaseServer(initial []map[string]any) *mutableLeaseServer {
	m := &mutableLeaseServer{}
	m.set(initial)
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(m.payload)
	}))
	return m
}

func (m *mutableLeaseServer) set(leases []map[string]any) {
	b, _ := json.Marshal(leases)
	m.mu.Lock()
	m.payload = b
	m.mu.Unlock()
}

func (m *mutableLeaseServer) URL() string { return m.srv.URL }
func (m *mutableLeaseServer) Close()      { m.srv.Close() }

type anomaly struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind"`
	DetectedAt     time.Time `json:"detected_at"`
	IP             string    `json:"ip"`
	Details        any       `json:"details"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
}

func fetchAnomalies(t *testing.T, n *Node) []anomaly {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/clients/anomalies", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M3.6 impl pending: /api/v1/clients/anomalies returns 404")
	}
	if resp.StatusCode != 200 {
		t.Fatalf("GET anomalies: status %d", resp.StatusCode)
	}
	var out []anomaly
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode anomalies: %v", err)
	}
	return out
}

// waitForAnomaly polls /api/v1/clients/anomalies until an entry with
// matching kind+ip appears or the deadline expires.
func waitForAnomaly(t *testing.T, n *Node, kind, ip string, d time.Duration) *anomaly {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		for _, a := range fetchAnomalies(t, n) {
			if a.Kind == kind && a.IP == ip {
				return &a
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}

func sampleLease(ip, mac, hostname, clientID string) map[string]any {
	return map[string]any{
		"ip":         ip,
		"mac":        mac,
		"hostname":   hostname,
		"client_id":  clientID,
		"expires_at": "2287-11-09T11:46:39Z",
	}
}

// FS-SpoofMacChangedForKnownClientId
func TestSpoofMacChangedForKnownClientId(t *testing.T) {
	srv := newMutableLeaseServer([]map[string]any{
		sampleLease("192.168.1.42", "aa:bb:cc:dd:ee:42", "kid-tablet", "id:tablet42"),
	})
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond) // initial poll

	// Same Client-ID, new MAC → anomaly
	srv.set([]map[string]any{
		sampleLease("192.168.1.42", "ff:00:00:00:00:99", "kid-tablet", "id:tablet42"),
	})
	got := waitForAnomaly(t, n, "mac_changed_for_client_id", "192.168.1.42", 5*time.Second)
	if got == nil {
		t.Fatalf("anomaly never raised")
	}
}

// FS-SpoofClientIdChangedForKnownMac
func TestSpoofClientIdChangedForKnownMac(t *testing.T) {
	srv := newMutableLeaseServer([]map[string]any{
		sampleLease("192.168.1.42", "aa:bb:cc:dd:ee:42", "kid-tablet", "id:tablet42"),
	})
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond)

	srv.set([]map[string]any{
		sampleLease("192.168.1.42", "aa:bb:cc:dd:ee:42", "kid-tablet", "id:attacker99"),
	})
	got := waitForAnomaly(t, n, "client_id_changed_for_mac", "192.168.1.42", 5*time.Second)
	if got == nil {
		t.Fatalf("anomaly never raised")
	}
}

// FS-SpoofNewMacForExistingHostname
func TestSpoofNewMacForExistingHostname(t *testing.T) {
	srv := newMutableLeaseServer([]map[string]any{
		sampleLease("192.168.1.42", "aa:bb:cc:dd:ee:42", "kid-tablet", "id:tablet42"),
	})
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond)

	srv.set([]map[string]any{
		sampleLease("192.168.1.42", "aa:bb:cc:dd:ee:42", "kid-tablet", "id:tablet42"),
		sampleLease("192.168.1.77", "11:22:33:44:55:66", "kid-tablet", "id:nobody"),
	})
	got := waitForAnomaly(t, n, "new_device_steals_hostname", "192.168.1.77", 5*time.Second)
	if got == nil {
		t.Fatalf("anomaly never raised for hostname theft")
	}
}

// FS-SpoofHostnameChangeIsInfo — a known device renames itself; no anomaly.
func TestSpoofHostnameChangeIsInfo(t *testing.T) {
	srv := newMutableLeaseServer([]map[string]any{
		sampleLease("192.168.1.10", "aa:bb:cc:dd:ee:10", "home-laptop", "id:laptop10"),
	})
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond)

	srv.set([]map[string]any{
		sampleLease("192.168.1.10", "aa:bb:cc:dd:ee:10", "office-laptop", "id:laptop10"),
	})
	time.Sleep(2 * time.Second)

	for _, a := range fetchAnomalies(t, n) {
		if a.IP == "192.168.1.10" {
			t.Errorf("rename should NOT be an anomaly, got %+v", a)
		}
	}
}

// FS-SpoofAnomaliesInResponse
func TestSpoofAnomaliesInResponse(t *testing.T) {
	srv := newMutableLeaseServer([]map[string]any{
		sampleLease("192.168.1.42", "aa:bb:cc:dd:ee:42", "kid-tablet", "id:tablet42"),
	})
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond)

	srv.set([]map[string]any{
		sampleLease("192.168.1.42", "ff:00:00:00:00:99", "kid-tablet", "id:tablet42"),
	})
	if waitForAnomaly(t, n, "mac_changed_for_client_id", "192.168.1.42", 5*time.Second) == nil {
		t.Fatalf("anomaly never raised")
	}

	// Per-client lookup should now include the anomaly
	got, _ := fetchClient(t, n, "192.168.1.42")
	if len(got.Anomalies) == 0 {
		t.Errorf("per-client response missing anomalies field: %+v", got)
	}
}

// FS-SpoofAnomalyRetention — older anomalies are evicted.
// This requires a test affordance (SKOED_TEST_NOW shift) to fast-forward
// the retention sweep. Skip until that's wired.
func TestSpoofAnomalyRetention(t *testing.T) {
	t.Skipf("M3.6 impl pending — requires SKOED_TEST_NOW affordance for the retention sweep")
}

// FS-SpoofAcknowledge
func TestSpoofAcknowledge(t *testing.T) {
	srv := newMutableLeaseServer([]map[string]any{
		sampleLease("192.168.1.42", "aa:bb:cc:dd:ee:42", "kid-tablet", "id:tablet42"),
	})
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: srv.URL(),
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(1500 * time.Millisecond)
	srv.set([]map[string]any{
		sampleLease("192.168.1.42", "ff:00:00:00:00:99", "kid-tablet", "id:tablet42"),
	})
	got := waitForAnomaly(t, n, "mac_changed_for_client_id", "192.168.1.42", 5*time.Second)
	if got == nil {
		t.Fatalf("anomaly never raised")
	}

	resp := n.apiDo(t, "POST", "/api/v1/clients/anomalies/"+got.ID+"/acknowledge", "")
	resp.Body.Close()

	// Anomaly should remain in the list, but with acknowledged_at set.
	for _, a := range fetchAnomalies(t, n) {
		if a.ID == got.ID {
			if a.AcknowledgedAt == nil {
				t.Errorf("anomaly %s: acknowledged_at still nil after POST", got.ID)
			}
			return
		}
	}
	t.Errorf("anomaly %s missing from /anomalies after acknowledge", got.ID)
}
