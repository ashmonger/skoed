// Acceptance tests for M5.9.7 — "Would this domain be blocked?" tester.
//
// FSIDs covered:
//   FS-TestDomainGuestVerdictBlocked          → TestTestDomainGuestBlocked
//   FS-TestDomainGuestVerdictAllowed          → TestTestDomainGuestAllowed
//   FS-TestDomainGuestRefusesInvalidInput     → TestTestDomainGuestRefusesIP
//   FS-TestDomainGuestRateLimited             → TestTestDomainGuestRateLimited
//   FS-TestDomainGuestDisabledWithPublicLanding → TestTestDomainGuestDisabled
//   FS-TestDomainAuthRequiresAuth             → TestTestDomainAuthRequiresAuth
//   FS-TestDomainAuthReturnsFullChain         → TestTestDomainAuthFullChain
//   FS-TestDomainAuthLocalDnsTakesPriority    → TestTestDomainAuthLocalDnsPriority
//   FS-TestDomainAuthAllowlistOverridesBlocklist → TestTestDomainAuthAllowlistOverrides
//   FS-TestDomainAuthFiresOnSameEvaluatorAsRealQueries → TestTestDomainMatchesRealQuery
//   FS-TestDomainCliVerb                      → TestTestDomainCli
//   FS-TestDomainMetricsCounter               → TestTestDomainMetricsCounter

package acceptance

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/miekg/dns"
)

type testDomainResp struct {
	Domain             string `json:"domain"`
	ClientIP           string `json:"client_ip"`
	WouldBlock         bool   `json:"would_block"`
	Reason             string `json:"reason"`
	MatchedProfileID   string `json:"matched_profile_id"`
	MatchedBlocklistID string `json:"matched_blocklist_id"`
	BlockPolicy        string `json:"block_policy"`
	LocalDNSAnswer     string `json:"local_dns_answer"`
	SafeSearchRewrite  string `json:"safesearch_rewrite"`
	Error              string `json:"error"`
}

func postTestDomain(t *testing.T, n *Node, path, body string, auth bool) (int, testDomainResp) {
	t.Helper()
	var resp *http.Response
	if auth {
		resp = n.apiDo(t, "POST", path, body)
	} else {
		req, _ := http.NewRequest("POST", n.APIBase+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		var err error
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
	}
	defer resp.Body.Close()
	var out testDomainResp
	buf, _ := io.ReadAll(resp.Body)
	if len(buf) > 0 {
		_ = json.Unmarshal(buf, &out)
	}
	return resp.StatusCode, out
}

// seedBlockedDomain creates a manual blocklist that blocks doubleclick.net
// and assigns it to the default profile via the bundled categories
// path.
func seedBlockedDomain(t *testing.T, n *Node) {
	t.Helper()
	body := mustJSON(t, map[string]any{
		"id":      "td-block",
		"name":    "td-block",
		"enabled": true,
		"source":  map[string]string{"type": "manual"},
		"domains": []string{"doubleclick.net"},
	})
	r := n.apiDo(t, "POST", "/api/v1/blocklists", body)
	r.Body.Close()
	if r.StatusCode != http.StatusCreated && r.StatusCode != http.StatusOK {
		t.Fatalf("seed blocklist: status %d", r.StatusCode)
	}
}

// FS-TestDomainGuestVerdictBlocked
func TestTestDomainGuestBlocked(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	seedBlockedDomain(t, n)
	// The default profile inherits all blocklists by default in M3, so
	// the manual blocklist participates in the default profile's filter.
	status, out := postTestDomain(t, n, "/api/v1/_public/test-domain",
		`{"domain":"doubleclick.net"}`, false)
	if status == http.StatusNotFound {
		t.Skip("M5.9.7 impl pending: /api/v1/_public/test-domain 404")
	}
	if status != 200 {
		t.Fatalf("status %d: %+v", status, out)
	}
	if !out.WouldBlock {
		t.Errorf("want would_block=true, got false; out=%+v", out)
	}
	if out.Reason != "blocklist" {
		t.Errorf("want reason=blocklist, got %q", out.Reason)
	}
}

// FS-TestDomainGuestVerdictAllowed
func TestTestDomainGuestAllowed(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	status, out := postTestDomain(t, n, "/api/v1/_public/test-domain",
		`{"domain":"github.com"}`, false)
	if status == http.StatusNotFound {
		t.Skip("M5.9.7 impl pending")
	}
	if status != 200 {
		t.Fatalf("status %d", status)
	}
	if out.WouldBlock {
		t.Errorf("want would_block=false, got true; out=%+v", out)
	}
}

// FS-TestDomainGuestRefusesInvalidInput
func TestTestDomainGuestRefusesIP(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	for _, bad := range []string{
		`{"domain":"10.0.0.5"}`,
		`{"domain":"127.0.0.1"}`,
		`{"domain":"localhost"}`,
		`{"domain":""}`,
		`{"domain":"example.local"}`,
	} {
		status, out := postTestDomain(t, n, "/api/v1/_public/test-domain", bad, false)
		if status == http.StatusNotFound {
			t.Skip("M5.9.7 impl pending")
		}
		if status != http.StatusBadRequest {
			t.Errorf("input %s: want 400, got %d (%+v)", bad, status, out)
		}
	}
}

// FS-TestDomainGuestRateLimited
func TestTestDomainGuestRateLimited(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	saw429 := false
	for i := 0; i < 30; i++ {
		status, _ := postTestDomain(t, n, "/api/v1/_public/test-domain",
			`{"domain":"example.com"}`, false)
		if status == http.StatusNotFound {
			t.Skip("M5.9.7 impl pending")
		}
		if status == http.StatusTooManyRequests {
			saw429 = true
			break
		}
	}
	if !saw429 {
		t.Errorf("expected at least one 429 after 30 tight POSTs")
	}
}

// FS-TestDomainAuthRequiresAuth
func TestTestDomainAuthRequiresAuth(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	req, _ := http.NewRequest("POST", n.APIBase+"/api/v1/test-domain",
		strings.NewReader(`{"domain":"example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("M5.9.7 impl pending")
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no-auth: want 401, got %d", resp.StatusCode)
	}
}

// FS-TestDomainAuthReturnsFullChain
func TestTestDomainAuthFullChain(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	seedBlockedDomain(t, n)
	// Profile keyed by client IP.
	prof := mustJSON(t, map[string]any{
		"id": "kids", "name": "Kids",
		"blocklists": []string{"td-block"},
		"client_ips": []string{"10.42.10.50"},
	})
	r := n.apiDo(t, "POST", "/api/v1/profiles", prof)
	r.Body.Close()

	status, out := postTestDomain(t, n, "/api/v1/test-domain",
		`{"domain":"doubleclick.net","client_ip":"10.42.10.50"}`, true)
	if status == http.StatusNotFound {
		t.Skip("M5.9.7 impl pending")
	}
	if status != 200 {
		t.Fatalf("status %d: %+v", status, out)
	}
	if !out.WouldBlock {
		t.Errorf("want would_block=true")
	}
	if out.Reason != "blocklist" {
		t.Errorf("want reason=blocklist, got %q", out.Reason)
	}
	if out.MatchedProfileID != "kids" {
		t.Errorf("want matched_profile_id=kids, got %q", out.MatchedProfileID)
	}
	if out.MatchedBlocklistID == "" {
		t.Errorf("matched_blocklist_id should be non-empty")
	}
	if out.BlockPolicy == "" {
		t.Errorf("block_policy should be non-empty")
	}
}

// FS-TestDomainAuthLocalDnsTakesPriority
func TestTestDomainAuthLocalDnsPriority(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	entry := mustJSON(t, map[string]any{
		"hostname": "nas.lab", "type": "A", "value": "10.42.10.20", "ttl": 300,
	})
	r := n.apiDo(t, "POST", "/api/v1/local-dns", entry)
	r.Body.Close()

	status, out := postTestDomain(t, n, "/api/v1/test-domain",
		`{"domain":"nas.lab"}`, true)
	if status == http.StatusNotFound {
		t.Skip("M5.9.7 impl pending")
	}
	if status != 200 {
		t.Fatalf("status %d", status)
	}
	if out.WouldBlock {
		t.Errorf("local-DNS should not block; got blocked")
	}
	if out.Reason != "local-dns" {
		t.Errorf("want reason=local-dns, got %q", out.Reason)
	}
	if out.LocalDNSAnswer != "10.42.10.20" {
		t.Errorf("want local_dns_answer=10.42.10.20, got %q", out.LocalDNSAnswer)
	}
}

// FS-TestDomainAuthAllowlistOverridesBlocklist
func TestTestDomainAuthAllowlistOverrides(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	seedBlockedDomain(t, n)
	allow := mustJSON(t, map[string]string{"domain": "doubleclick.net"})
	r := n.apiDo(t, "POST", "/api/v1/allowlist", allow)
	r.Body.Close()

	status, out := postTestDomain(t, n, "/api/v1/test-domain",
		`{"domain":"doubleclick.net"}`, true)
	if status == http.StatusNotFound {
		t.Skip("M5.9.7 impl pending")
	}
	if status != 200 {
		t.Fatalf("status %d", status)
	}
	if out.WouldBlock {
		t.Errorf("allowlist must override blocklist; got blocked")
	}
	if out.Reason != "allowlist" {
		t.Errorf("want reason=allowlist, got %q", out.Reason)
	}
}

// FS-TestDomainAuthFiresOnSameEvaluatorAsRealQueries
func TestTestDomainMatchesRealQuery(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	seedBlockedDomain(t, n)

	// Fire a real DNS query for the blocked domain and observe the rcode.
	r := dnsQuery(t, n.DNSAddr, "doubleclick.net", dns.TypeA)
	realBlocked := r.Rcode == dns.RcodeNameError ||
		// NULL policy returns 0.0.0.0
		(len(r.Answer) > 0 && func() bool {
			if a, ok := r.Answer[0].(*dns.A); ok {
				return a.A.String() == "0.0.0.0"
			}
			return false
		}())

	status, out := postTestDomain(t, n, "/api/v1/test-domain",
		`{"domain":"doubleclick.net"}`, true)
	if status == http.StatusNotFound {
		t.Skip("M5.9.7 impl pending")
	}
	if status != 200 {
		t.Fatalf("status %d", status)
	}
	if out.WouldBlock != realBlocked {
		t.Errorf("verdict drift: real=%v, test-endpoint=%v", realBlocked, out.WouldBlock)
	}
}

// FS-TestDomainCliVerb
func TestTestDomainCli(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	seedBlockedDomain(t, n)
	auth := fmt.Sprintf("SKOED_AUTH=%s:%s", defaultUsername, defaultPassword)
	stdout, stderr, exit := runCli(t, []string{auth},
		"domain", "test", "doubleclick.net", "--api", n.APIBase)
	if strings.Contains(stderr, "unknown command") {
		t.Skip("M5.9.7 impl pending: domain subcommand missing")
	}
	if exit != 0 {
		t.Errorf("domain test: exit=%d, stderr=%q", exit, stderr)
	}
	low := strings.ToLower(stdout)
	if !strings.Contains(low, "block") {
		t.Errorf("CLI output should mention 'block'; got %q", stdout)
	}
}

// FS-TestDomainMetricsCounter
func TestTestDomainMetricsCounter(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	seedBlockedDomain(t, n)

	// Fire one block + one allow via guest path.
	postTestDomain(t, n, "/api/v1/_public/test-domain", `{"domain":"doubleclick.net"}`, false)
	postTestDomain(t, n, "/api/v1/_public/test-domain", `{"domain":"github.com"}`, false)

	resp, _ := http.Get(n.APIBase + "/metrics")
	if resp == nil || resp.StatusCode != 200 {
		t.Skip("M5.1 /metrics unavailable")
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	body := string(b)
	if !strings.Contains(body, "skoed_test_domain_requests_total") {
		t.Skipf("M5.9.7 impl pending: counter absent")
	}
	for _, want := range []string{
		`skoed_test_domain_requests_total{surface="guest",verdict="block"}`,
		`skoed_test_domain_requests_total{surface="guest",verdict="allow"}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing counter series %q", want)
		}
	}
}
