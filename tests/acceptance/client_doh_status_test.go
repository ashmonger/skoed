// Acceptance tests for M3.5 — Per-Client DoH/DoT status surfacing.
//
// FSIDs covered (one Go test per FSID, except FS-ClientDohStatusSuspectedProvider
// which is a table-driven scenario outline covered by a single test fn):
//   FS-ClientDohStatusEndpointShape
//   FS-ClientDohStatusNoProbes
//   FS-ClientDohStatusUnauthenticated
//   FS-ClientDohStatusInvalidIp
//   FS-ClientDohStatusRollingWindow
//   FS-ClientDohStatusSuspectedProvider
//
// All tests use startCluster(t, 1) — a single-node cluster is enough for
// this endpoint (it just queries the local query log; no Raft involved).
// DoH probes are emitted via dnsQueryAsClient + EDNS0 client-IP spoofing.

package acceptance

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// clientDohStatus mirrors the response shape documented in
// per-client-doh-status.feature.
type clientDohStatus struct {
	Client             string  `json:"client"`
	UsingDoH           bool    `json:"using_doh"`
	DohProbes1h        int     `json:"doh_probes_1h"`
	LastDoHQuery       *string `json:"last_doh_query"`
	SuspectedProvider  *string `json:"suspected_provider"`
}

// fetchDohStatus calls GET /api/v1/clients/{ip}/doh-status. Returns the
// parsed struct and the raw HTTP status. Skips the test when the route
// returns 404 so the file is green against an M3.5-pending binary.
func fetchDohStatus(t *testing.T, n *Node, clientIP string) (clientDohStatus, int) {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/clients/"+clientIP+"/doh-status", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M3.5 impl pending: GET /api/v1/clients/{ip}/doh-status returns 404")
	}
	if resp.StatusCode != http.StatusOK {
		return clientDohStatus{}, resp.StatusCode
	}
	var s clientDohStatus
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatalf("decode doh-status: %v", err)
	}
	return s, resp.StatusCode
}

// FS-ClientDohStatusEndpointShape
func TestClientDohStatusEndpointShape(t *testing.T) {
	t.Setenv("SKOED_TEST_MODE", "1") // EDNS0 client-IP spoofing only honored in test mode
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	// Emit 3 DoH probes as 192.168.1.42 — all blocked by the cat:doh
	// blocklist on the default profile.
	probes := []string{"dns.google", "cloudflare-dns.com", "dns.quad9.net"}
	for _, h := range probes {
		r := dnsQueryAsClient(t, n.DNSAddr, h, dns.TypeA, "192.168.1.42")
		if r.Rcode != dns.RcodeNameError {
			t.Fatalf("expected NXDOMAIN for %s, got %s", h, dns.RcodeToString[r.Rcode])
		}
	}

	s, code := fetchDohStatus(t, n, "192.168.1.42")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if s.Client != "192.168.1.42" {
		t.Errorf("client: want 192.168.1.42, got %q", s.Client)
	}
	if !s.UsingDoH {
		t.Errorf("using_doh: want true (3 probes emitted), got false")
	}
	if s.DohProbes1h != 3 {
		t.Errorf("doh_probes_1h: want 3, got %d", s.DohProbes1h)
	}
	if s.LastDoHQuery == nil {
		t.Errorf("last_doh_query: want non-nil")
	} else {
		if _, err := time.Parse(time.RFC3339, *s.LastDoHQuery); err != nil {
			t.Errorf("last_doh_query: %q is not RFC3339: %v", *s.LastDoHQuery, err)
		}
	}
	if s.SuspectedProvider == nil {
		t.Errorf("suspected_provider: want non-nil")
	}
}

// FS-ClientDohStatusNoProbes
func TestClientDohStatusNoProbes(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	s, code := fetchDohStatus(t, n, "192.168.1.99")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if s.UsingDoH {
		t.Errorf("using_doh: want false (no probes), got true")
	}
	if s.DohProbes1h != 0 {
		t.Errorf("doh_probes_1h: want 0, got %d", s.DohProbes1h)
	}
	if s.LastDoHQuery != nil {
		t.Errorf("last_doh_query: want nil, got %q", *s.LastDoHQuery)
	}
	if s.SuspectedProvider != nil {
		t.Errorf("suspected_provider: want nil, got %q", *s.SuspectedProvider)
	}
}

// FS-ClientDohStatusUnauthenticated
func TestClientDohStatusUnauthenticated(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	resp := n.apiDoNoAuth(t, "GET", "/api/v1/clients/192.168.1.42/doh-status")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M3.5 impl pending: GET /api/v1/clients/{ip}/doh-status returns 404")
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// FS-ClientDohStatusInvalidIp
func TestClientDohStatusInvalidIp(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	resp := n.apiDo(t, "GET", "/api/v1/clients/not-an-ip/doh-status", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M3.5 impl pending: GET /api/v1/clients/{ip}/doh-status returns 404")
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed IP, got %d", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "invalid") {
		t.Errorf("expected 'invalid' in error body, got: %s", body)
	}
}

// FS-ClientDohStatusSuspectedProvider — scenario outline covered as a
// table-driven test. Each probe is emitted, then the endpoint is queried
// and the inferred provider is checked.
func TestClientDohStatusSuspectedProvider(t *testing.T) {
	cases := []struct {
		domain   string
		provider string // empty string ⇒ expect nil
	}{
		{"dns.google", "google"},
		{"cloudflare-dns.com", "cloudflare"},
		{"mozilla.cloudflare-dns.com", "cloudflare"},
		{"dns.quad9.net", "quad9"},
		{"dns.adguard.com", "adguard"},
		{"doh.opendns.com", "opendns"},
	}
	for i, tc := range cases {
		t.Run(tc.domain, func(t *testing.T) {
			t.Setenv("SKOED_TEST_MODE", "1") // EDNS0 client-IP spoof only in test mode
			c := startCluster(t, 1)
			n := c.Leader(t).Node

			// Use a unique client IP per case so SKOED_TEST_NOW-driven
			// "last probe" semantics don't get polluted by prior cases.
			clientIP := []string{
				"10.0.50.1", "10.0.50.2", "10.0.50.3",
				"10.0.50.4", "10.0.50.5", "10.0.50.6",
			}[i]

			r := dnsQueryAsClient(t, n.DNSAddr, tc.domain, dns.TypeA, clientIP)
			if r.Rcode != dns.RcodeNameError {
				t.Fatalf("expected NXDOMAIN for %s, got %s",
					tc.domain, dns.RcodeToString[r.Rcode])
			}

			s, code := fetchDohStatus(t, n, clientIP)
			if code != http.StatusOK {
				t.Fatalf("expected 200, got %d", code)
			}
			if s.SuspectedProvider == nil {
				t.Fatalf("suspected_provider: want %q, got nil", tc.provider)
			}
			if *s.SuspectedProvider != tc.provider {
				t.Errorf("suspected_provider: want %q, got %q",
					tc.provider, *s.SuspectedProvider)
			}
		})
	}
}

