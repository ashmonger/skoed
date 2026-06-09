// Acceptance tests for M3.6 DHCP source connectors.
//
// FSIDs covered:
//   FS-DhcpKeaReadsLeases             → TestDhcpKeaReadsLeases
//   FS-DhcpKeaHandlesAuth             → TestDhcpKeaHandlesAuth
//   FS-DhcpDnsmasqParsesLeaseFile     → TestDhcpDnsmasqParsesLeaseFile
//   FS-DhcpDnsmasqSkipsExpired        → TestDhcpDnsmasqSkipsExpired
//   FS-DhcpGenericJsonRoundtrip       → TestDhcpGenericJsonRoundtrip
//   FS-DhcpGenericRetry               → TestDhcpGenericRetry
//   FS-DhcpConnectorRefreshInterval   → TestDhcpConnectorRefreshInterval
//   FS-DhcpConnectorMalformedSkips    → TestDhcpConnectorMalformedSkips
//
// Strategy per decisions/20260604-M36DhcpTestDesign.md:
//   - dnsmasq + ISC connector tests use static fixture files under
//     tests/fixtures/dhcp/ — no live daemon
//   - Kea + Generic-HTTP connector tests spin a httptest.NewServer in-
//     process and have skoed poll it
//   - Each test self-skips when the harness doesn't yet support DHCP
//
// All tests rely on a forthcoming harness helper `startClusterWithDhcp`
// (see cluster_harness_test.go) and a node field `DhcpConnector` /
// `LeaseSnapshot` for inspection. Until those exist, every test below
// skips with a clear "M3.6 impl pending" message.

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const dhcpTestTimeout = 5 * time.Second

func requireDhcpHarness(t *testing.T, n *Node) {
	t.Helper()
	// LeaseSnapshotURL is set by startClusterWithDhcp; remaining empty
	// would mean the caller didn't use that helper.
	if n.LeaseSnapshotURL == "" {
		t.Skipf("test did not use startClusterWithDhcp")
	}
}

// fixturePath resolves a fixture filename to its absolute path so the
// node subprocess can read it from inside its temp data dir.
func fixturePath(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "fixtures", "dhcp", name))
	if err != nil {
		t.Fatalf("resolve fixture path: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("fixture %s not found: %v", name, err)
	}
	return abs
}

// keaStub serves /lease4-get-all-style payloads from a fixture. Returns
// 401 if basicAuth is set and the request omits the matching header.
func keaStub(t *testing.T, fixture string, basicAuthUser, basicAuthPass string) *httptest.Server {
	t.Helper()
	body, err := os.ReadFile(fixturePath(t, fixture))
	if err != nil {
		t.Fatalf("read kea fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if basicAuthUser != "" {
			u, p, ok := r.BasicAuth()
			if !ok || u != basicAuthUser || p != basicAuthPass {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	return srv
}

// genericStub serves /generic-leases.json for the generic HTTP connector.
// The flake counter on the first call lets us drive retry behavior.
func genericStub(t *testing.T, fixture string, fail503Once bool) *httptest.Server {
	t.Helper()
	body, err := os.ReadFile(fixturePath(t, fixture))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if fail503Once && calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	return srv
}

// FS-DhcpKeaReadsLeases
func TestDhcpKeaReadsLeases(t *testing.T) {
	t.Parallel()
	stub := keaStub(t, "kea-lease4-get-all.json", "", "")
	defer stub.Close()

	c := startClusterWithDhcp(t, DhcpOpts{Kind: "kea", URL: stub.URL})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)

	leases := fetchLeaseSnapshot(t, n)
	if len(leases) < 3 {
		t.Fatalf("want >=3 Kea leases, got %d: %+v", len(leases), leases)
	}
	want := map[string]string{
		"192.168.1.42": "kid-tablet",
		"192.168.1.10": "home-laptop",
		"192.168.1.51": "desktop",
	}
	for ip, host := range want {
		if got := findLeaseByIP(leases, ip).Hostname; got != host {
			t.Errorf("lease for %s: hostname %q, want %q", ip, got, host)
		}
	}
}

// FS-DhcpKeaHandlesAuth
func TestDhcpKeaHandlesAuth(t *testing.T) {
	t.Parallel()
	stub := keaStub(t, "kea-lease4-get-all.json", "admin", "kea-secret")
	defer stub.Close()

	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "kea", URL: stub.URL,
		Username: "admin", Password: "kea-secret",
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	leases := fetchLeaseSnapshot(t, n)
	if len(leases) < 3 {
		t.Fatalf("Kea+auth: want >=3 leases, got %d", len(leases))
	}
}

// FS-DhcpDnsmasqParsesLeaseFile
func TestDhcpDnsmasqParsesLeaseFile(t *testing.T) {
	t.Parallel()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind:     "dnsmasq",
		FilePath: fixturePath(t, "dnsmasq.leases"),
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)

	leases := fetchLeaseSnapshot(t, n)
	// Fixture: 4 active + 1 expired + 1 malformed → expect 4 active.
	if len(leases) != 4 {
		t.Errorf("dnsmasq: want 4 active leases, got %d: %+v", len(leases), leases)
	}
	// "*" hostname marker yields empty Hostname.
	l := findLeaseByIP(leases, "192.168.1.99")
	if l.Hostname != "" {
		t.Errorf("lease 192.168.1.99: hostname should be empty (dnsmasq '*'), got %q", l.Hostname)
	}
	// Client-ID preserved verbatim.
	tablet := findLeaseByIP(leases, "192.168.1.42")
	if tablet.ClientID != "id:tablet42" {
		t.Errorf("lease 192.168.1.42: client_id %q, want id:tablet42", tablet.ClientID)
	}
}

// FS-DhcpDnsmasqSkipsExpired
func TestDhcpDnsmasqSkipsExpired(t *testing.T) {
	t.Parallel()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind:     "dnsmasq",
		FilePath: fixturePath(t, "dnsmasq.leases"),
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	leases := fetchLeaseSnapshot(t, n)
	for _, l := range leases {
		if l.IP == "192.168.1.50" {
			t.Errorf("expired lease 192.168.1.50 should have been skipped: %+v", l)
		}
	}
}

// FS-DhcpConnectorMalformedSkips
func TestDhcpConnectorMalformedSkips(t *testing.T) {
	t.Parallel()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind:     "dnsmasq",
		FilePath: fixturePath(t, "dnsmasq.leases"),
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	// The fixture contains a malformed line; the test just asserts that
	// other lines were still parsed. If the connector aborted on the
	// malformed line, leases would be < 4.
	leases := fetchLeaseSnapshot(t, n)
	if len(leases) < 4 {
		t.Errorf("malformed line should not abort parsing; got %d leases", len(leases))
	}
}

// FS-DhcpGenericJsonRoundtrip
func TestDhcpGenericJsonRoundtrip(t *testing.T) {
	t.Parallel()
	stub := genericStub(t, "generic-leases.json", false)
	defer stub.Close()
	c := startClusterWithDhcp(t, DhcpOpts{Kind: "http_json", URL: stub.URL})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	leases := fetchLeaseSnapshot(t, n)
	if len(leases) != 2 {
		t.Fatalf("generic: want 2 leases, got %d", len(leases))
	}
	for _, l := range leases {
		if !strings.HasPrefix(l.MAC, "aa:bb:cc:dd:ee:") {
			t.Errorf("MAC should be lowercased: %q", l.MAC)
		}
	}
}

// FS-DhcpGenericRetry
func TestDhcpGenericRetry(t *testing.T) {
	t.Parallel()
	stub := genericStub(t, "generic-leases.json", true) // 503 once, then 200
	defer stub.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: stub.URL,
		RefreshSeconds: 1,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	deadline := time.Now().Add(dhcpTestTimeout)
	for time.Now().Before(deadline) {
		leases := fetchLeaseSnapshot(t, n)
		if len(leases) == 2 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("connector did not recover from transient 503 within %s", dhcpTestTimeout)
}

// FS-DhcpConnectorRefreshInterval
func TestDhcpConnectorRefreshInterval(t *testing.T) {
	t.Parallel()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, "[]")
	}))
	defer srv.Close()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind: "http_json", URL: srv.URL,
		RefreshSeconds: 2,
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)
	time.Sleep(5 * time.Second)
	if calls < 2 || calls > 4 {
		t.Errorf("with refresh=2s after 5s expected 2-4 polls, got %d", calls)
	}
}

// ─── helpers exposed by the harness (placeholder shapes) ────────────

func fetchLeaseSnapshot(t *testing.T, n *Node) []Lease {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/clients/_leases", "")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("lease snapshot status %d", resp.StatusCode)
	}
	var out []Lease
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("lease snapshot decode: %v", err)
	}
	return out
}

func findLeaseByIP(ls []Lease, ip string) Lease {
	for _, l := range ls {
		if l.IP == ip {
			return l
		}
	}
	return Lease{}
}
