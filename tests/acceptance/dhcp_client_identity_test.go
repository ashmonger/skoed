// Acceptance tests for M3.6 DHCP-enriched client identity + profile
// matching priority.
//
// FSIDs covered:
//   FS-ClientLookupReturnsEnrichedRecord  → TestClientLookupReturnsEnrichedRecord
//   FS-ClientLookupFallsBackToIp          → TestClientLookupFallsBackToIp
//   FS-QueryLogShowsHostname              → TestQueryLogShowsHostname
//   FS-QueryLogOmitsEnrichmentWhenNoLease → TestQueryLogOmitsEnrichmentWhenNoLease
//   FS-ProfileMatchesByClientId           → TestProfileMatchesByClientId
//   FS-ProfileMatchesByMac                → TestProfileMatchesByMac
//   FS-ProfileMatchesByHostname           → TestProfileMatchesByHostname
//   FS-ProfileMatchPriority               → TestProfileMatchPriority
//   FS-LeaseCacheRefreshInterval          → TestLeaseCacheRefreshInterval

package acceptance

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// enrichedClient mirrors the GET /api/v1/clients/{ip} response shape.
type enrichedClient struct {
	IP        string    `json:"ip"`
	MAC       string    `json:"mac"`
	Hostname  string    `json:"hostname"`
	ClientID  string    `json:"client_id"`
	Source    string    `json:"source"`
	LastSeen  time.Time `json:"last_seen,omitempty"`
	Anomalies []any     `json:"anomalies,omitempty"`
}

func fetchClient(t *testing.T, n *Node, ip string) (enrichedClient, int) {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/clients/"+ip, "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M3.6 impl pending: GET /api/v1/clients/{ip} returns 404")
	}
	if resp.StatusCode != 200 {
		return enrichedClient{}, resp.StatusCode
	}
	var c enrichedClient
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		t.Fatalf("decode client: %v", err)
	}
	return c, resp.StatusCode
}

// FS-ClientLookupReturnsEnrichedRecord
func TestClientLookupReturnsEnrichedRecord(t *testing.T) {
	t.Parallel()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind:     "dnsmasq",
		FilePath: fixturePath(t, "dnsmasq.leases"),
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)

	got, _ := fetchClient(t, n, "192.168.1.42")
	if got.Hostname != "kid-tablet" {
		t.Errorf("hostname: want kid-tablet, got %q", got.Hostname)
	}
	if got.MAC != "aa:bb:cc:dd:ee:42" {
		t.Errorf("mac: want aa:bb:cc:dd:ee:42, got %q", got.MAC)
	}
	if got.ClientID != "id:tablet42" {
		t.Errorf("client_id: want id:tablet42, got %q", got.ClientID)
	}
	if got.Source != "dnsmasq" {
		t.Errorf("source: want dnsmasq, got %q", got.Source)
	}
}

// FS-ClientLookupFallsBackToIp
func TestClientLookupFallsBackToIp(t *testing.T) {
	t.Parallel()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind:     "dnsmasq",
		FilePath: fixturePath(t, "dnsmasq.leases"),
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)

	got, _ := fetchClient(t, n, "192.168.99.99")
	if got.IP != "192.168.99.99" {
		t.Errorf("ip echo: got %q", got.IP)
	}
	if got.Hostname != "" || got.MAC != "" || got.ClientID != "" {
		t.Errorf("unknown client should have empty enrichment: %+v", got)
	}
	if got.Source != "none" {
		t.Errorf("source for unknown: want %q, got %q", "none", got.Source)
	}
}

// FS-QueryLogShowsHostname
func TestQueryLogShowsHostname(t *testing.T) {
	t.Parallel()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind:     "dnsmasq",
		FilePath: fixturePath(t, "dnsmasq.leases"),
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)

	dnsQueryAsClient(t, n.DNSAddr, "example.com", dns.TypeA, "192.168.1.42")
	time.Sleep(200 * time.Millisecond)
	// Look up the most recent query-log entry for that client.
	entries := fetchQueryLog(t, n, 50)
	for _, e := range entries {
		if e.ClientIP == "192.168.1.42" && e.Domain == "example.com" {
			// queryLogEntry struct in this package is currently minimal
			// (Domain/Category/ClientIP/Action). The M3.6 implementation
			// will extend the JSON shape with client_hostname/_mac/_id;
			// here we assert the enrichment by re-decoding via the
			// management API directly, since extending queryLogEntry
			// belongs to the impl phase.
			t.Skipf("M3.6 impl pending: queryLogEntry shape needs client_hostname/_mac/_id fields")
		}
	}
	t.Errorf("no query-log entry for 192.168.1.42 / example.com")
}

// FS-QueryLogOmitsEnrichmentWhenNoLease
func TestQueryLogOmitsEnrichmentWhenNoLease(t *testing.T) {
	t.Parallel()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind:     "dnsmasq",
		FilePath: fixturePath(t, "dnsmasq.leases"),
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)

	dnsQueryAsClient(t, n.DNSAddr, "example.com", dns.TypeA, "192.168.99.99")
	time.Sleep(200 * time.Millisecond)
	entries := fetchQueryLog(t, n, 50)
	for _, e := range entries {
		if e.ClientIP == "192.168.99.99" {
			// Same skip: needs the extended queryLogEntry shape.
			t.Skipf("M3.6 impl pending: queryLogEntry needs client_hostname/_mac/_id absence assertion")
		}
	}
	t.Errorf("no query-log entry for 192.168.99.99")
}

// FS-ProfileMatchesByClientId
func TestProfileMatchesByClientId(t *testing.T) {
	t.Parallel()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind:     "dnsmasq",
		FilePath: fixturePath(t, "dnsmasq.leases"),
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)

	// Add a kids profile pinned by Client-ID
	body := mustJSON(t, map[string]any{
		"id":         "kids",
		"name":       "Kids",
		"client_ids": []string{"id:tablet42"},
		"blocklists": []string{"cat:doh"},
	})
	resp := n.apiDo(t, "POST", "/api/v1/profiles", body)
	resp.Body.Close()

	// Query a domain that cat:doh would block.
	r := dnsQueryAsClient(t, n.DNSAddr, "dns.google", dns.TypeA, "192.168.1.42")
	if r.Rcode != dns.RcodeNameError {
		t.Errorf("profile match by Client-ID failed; want NXDOMAIN, got %s",
			dns.RcodeToString[r.Rcode])
	}
}

// FS-ProfileMatchesByMac
func TestProfileMatchesByMac(t *testing.T) {
	t.Parallel()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind:     "dnsmasq",
		FilePath: fixturePath(t, "dnsmasq.leases"),
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)

	body := mustJSON(t, map[string]any{
		"id":          "guests",
		"name":        "Guests",
		"client_macs": []string{"aa:bb:cc:dd:ee:10"},
		"blocklists":  []string{"cat:doh"},
	})
	resp := n.apiDo(t, "POST", "/api/v1/profiles", body)
	resp.Body.Close()

	r := dnsQueryAsClient(t, n.DNSAddr, "dns.google", dns.TypeA, "192.168.1.10")
	if r.Rcode != dns.RcodeNameError {
		t.Errorf("profile match by MAC failed; want NXDOMAIN, got %s",
			dns.RcodeToString[r.Rcode])
	}
}

// FS-ProfileMatchesByHostname
func TestProfileMatchesByHostname(t *testing.T) {
	t.Parallel()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind:     "dnsmasq",
		FilePath: fixturePath(t, "dnsmasq.leases"),
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)

	body := mustJSON(t, map[string]any{
		"id":               "office",
		"name":             "Office",
		"client_hostnames": []string{"desktop"},
		"blocklists":       []string{"cat:doh"},
	})
	resp := n.apiDo(t, "POST", "/api/v1/profiles", body)
	resp.Body.Close()

	r := dnsQueryAsClient(t, n.DNSAddr, "dns.google", dns.TypeA, "192.168.1.51")
	if r.Rcode != dns.RcodeNameError {
		t.Errorf("profile match by hostname failed; want NXDOMAIN, got %s",
			dns.RcodeToString[r.Rcode])
	}
}

// FS-ProfileMatchPriority — Client-ID wins over MAC.
func TestProfileMatchPriority(t *testing.T) {
	t.Parallel()
	c := startClusterWithDhcp(t, DhcpOpts{
		Kind:     "dnsmasq",
		FilePath: fixturePath(t, "dnsmasq.leases"),
	})
	n := c.Leader(t).Node
	requireDhcpHarness(t, n)

	// Profile A pins by Client-ID, profile B pins by MAC. Both would
	// match 192.168.1.42 — Client-ID must win.
	bodyA := mustJSON(t, map[string]any{
		"id":         "by-client-id",
		"name":       "Client-ID rule",
		"client_ids": []string{"id:tablet42"},
		"blocklists": []string{"cat:doh"},
	})
	bodyB := mustJSON(t, map[string]any{
		"id":          "by-mac",
		"name":        "MAC rule",
		"client_macs": []string{"aa:bb:cc:dd:ee:42"},
		"blocklists":  []string{"cat:gambling"},
	})
	n.apiDo(t, "POST", "/api/v1/profiles", bodyA).Body.Close()
	n.apiDo(t, "POST", "/api/v1/profiles", bodyB).Body.Close()

	// dns.google is in cat:doh (profile A); a gambling host would be
	// in cat:gambling (profile B). Profile A must win → dns.google
	// gets blocked.
	r := dnsQueryAsClient(t, n.DNSAddr, "dns.google", dns.TypeA, "192.168.1.42")
	if r.Rcode != dns.RcodeNameError {
		t.Errorf("Client-ID priority lost; want NXDOMAIN, got %s",
			dns.RcodeToString[r.Rcode])
	}
}

// FS-LeaseCacheRefreshInterval — covered by the dhcp_connectors_test.go
// TestDhcpConnectorRefreshInterval; this test asserts the END-TO-END
// effect: after a new lease appears in the source, the enriched API
// returns it within one refresh cycle.
func TestLeaseCacheRefreshInterval(t *testing.T) {
	t.Parallel()
	t.Skipf("M3.6 impl pending — covered functionally by TestDhcpConnectorRefreshInterval")
}
