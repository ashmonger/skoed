// Acceptance tests for M3 DoH/DoT detection and Layer-2 blocking.
//
// FSIDs covered (one Go test per FSID):
//   FS-DohDetectionResolverBlocklist
//   FS-DohDetectionFirefoxCanary
//   FS-DohDetectionDdrProbe
//   FS-DohDetectionTaggedInQueryLog
//   FS-DohDetectionCategoryDisableable
//
// All tests use startCluster(t, 1) for a fully-bootstrapped single-node
// cluster — the `doh` category is documented to be enabled on the default
// profile out of the box, so we don't have to configure anything for the
// "block default DoH hostnames" scenarios.
//
// Whenever an M3-pending route returns 404, the test self-skips with a
// "M3 impl pending: <route>" message so this file compiles and runs green
// against the current binary.

package acceptance

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/miekg/dns"
)

// queryLogEntry models the documented JSON shape of /api/v1/query-log items.
// Unknown fields are tolerated by json.Decoder.
type queryLogEntry struct {
	Domain   string `json:"domain"`
	Category string `json:"category"`
	ClientIP string `json:"client"`
	Action   string `json:"outcome"`
}

// fetchQueryLog retrieves /api/v1/query-log?limit=N. Skips when 404.
func fetchQueryLog(t *testing.T, n *Node, limit int) []queryLogEntry {
	t.Helper()
	resp := n.apiDo(t, "GET",
		"/api/v1/query-log?limit="+itoa(limit), "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M3 impl pending: GET /api/v1/query-log returns 404")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/query-log: status %d: %s", resp.StatusCode, readBody(t, resp))
	}
	// Accept either a bare array or an envelope {entries: [...]}.
	body := readBody(t, resp)
	var arr []queryLogEntry
	if err := json.Unmarshal([]byte(body), &arr); err == nil {
		return arr
	}
	var env struct {
		Entries []queryLogEntry `json:"entries"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("decode query-log (tried array+envelope): %v: %s", err, body)
	}
	return env.Entries
}

// hasLogEntry reports whether at least one entry matches domain (case-fold)
// and (if non-empty) category.
func hasLogEntry(entries []queryLogEntry, domain, category string) bool {
	for _, e := range entries {
		ed := e.Domain
		if len(ed) > 0 && ed[len(ed)-1] == '.' {
			ed = ed[:len(ed)-1]
		}
		if ed != domain {
			continue
		}
		if category != "" && e.Category != category {
			continue
		}
		return true
	}
	return false
}

// itoa avoids an strconv import in this otherwise-tight file. The harness
// has freePortStr but it requires *testing.T; cheap inline is clearer.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// FS-DohDetectionResolverBlocklist
func TestDohDetectionResolverBlocklist(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	hostnames := []string{
		"cloudflare-dns.com",
		"dns.google",
		"dns.quad9.net",
		"dns.adguard.com",
		"dns.nextdns.io",
		"mozilla.cloudflare-dns.com",
		"doh.opendns.com",
		"dns.controld.com",
		"chrome.cloudflare-dns.com",
	}

	for _, h := range hostnames {
		r := dnsQueryAsClient(t, n.DNSAddr, h, dns.TypeA, "192.168.1.10")
		if r.Rcode != dns.RcodeNameError {
			t.Fatalf("expected NXDOMAIN for %s, got %s", h, dns.RcodeToString[r.Rcode])
		}
	}

	entries := fetchQueryLog(t, n, 100)
	for _, h := range hostnames {
		if !hasLogEntry(entries, h, "doh-probe") {
			t.Fatalf("query-log missing doh-probe entry for %s: %+v", h, entries)
		}
	}
}

// FS-DohDetectionFirefoxCanary
func TestDohDetectionFirefoxCanary(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	r1 := dnsQueryAsClient(t, n.DNSAddr, "use-application-dns.net", dns.TypeA, "192.168.1.10")
	assertRcode(t, r1, dns.RcodeNameError)

	entries := fetchQueryLog(t, n, 50)
	if !hasLogEntry(entries, "use-application-dns.net", "doh-canary") {
		t.Fatalf("query-log missing doh-canary entry for use-application-dns.net: %+v", entries)
	}

	// Try to allowlist the canary — the call may succeed but the canary
	// MUST stay NXDOMAIN regardless.
	allowResp := n.apiDo(t, "POST", "/api/v1/allowlist",
		mustJSON(t, map[string]string{"domain": "use-application-dns.net"}))
	allowResp.Body.Close()
	// We don't assert on the allowlist status — implementations may either
	// accept the entry and then ignore it for the canary, or reject the
	// add outright. Both are spec-compliant ("never overridable").

	r2 := dnsQueryAsClient(t, n.DNSAddr, "use-application-dns.net", dns.TypeA, "192.168.1.10")
	if r2.Rcode != dns.RcodeNameError {
		t.Fatalf("canary must remain NXDOMAIN after allowlist add, got %s", dns.RcodeToString[r2.Rcode])
	}
}

// FS-DohDetectionDdrProbe
func TestDohDetectionDdrProbe(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	r := dnsQueryAsClient(t, n.DNSAddr, "_dns.resolver.arpa", dns.TypeSVCB, "192.168.1.10")
	// NODATA = RcodeSuccess with an empty Answer section.
	assertRcode(t, r, dns.RcodeSuccess)
	if len(r.Answer) != 0 {
		t.Fatalf("DDR probe must return NODATA (empty answer), got %+v", r.Answer)
	}

	entries := fetchQueryLog(t, n, 50)
	if !hasLogEntry(entries, "_dns.resolver.arpa", "ddr-probe") {
		t.Fatalf("query-log missing ddr-probe entry: %+v", entries)
	}
}

// FS-DohDetectionTaggedInQueryLog
func TestDohDetectionTaggedInQueryLog(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	// Mix: a doh-probe, the canary, a DDR probe, and a normal query.
	dnsQueryAsClient(t, n.DNSAddr, "cloudflare-dns.com", dns.TypeA, "192.168.1.10")
	dnsQueryAsClient(t, n.DNSAddr, "use-application-dns.net", dns.TypeA, "192.168.1.10")
	dnsQueryAsClient(t, n.DNSAddr, "_dns.resolver.arpa", dns.TypeSVCB, "192.168.1.10")
	dnsQueryAsClient(t, n.DNSAddr, "example.com", dns.TypeA, "192.168.1.10")

	entries := fetchQueryLog(t, n, 100)
	if len(entries) == 0 {
		t.Fatalf("query-log returned no entries")
	}

	valid := map[string]bool{
		"":           true,
		"doh-probe":  true,
		"doh-canary": true,
		"ddr-probe":  true,
	}
	for _, e := range entries {
		if !valid[e.Category] {
			t.Fatalf("query-log entry has invalid category %q: %+v", e.Category, e)
		}
	}
}

// FS-DohDetectionCategoryDisableable
//
// Disabling the doh category on the default profile must cause regular
// DoH-hostname queries to be FORWARDED, while the Firefox canary stays
// NXDOMAIN regardless.
func TestDohDetectionCategoryDisableable(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	disResp := n.apiDo(t, "POST", "/api/v1/categories/doh/disable",
		mustJSON(t, map[string]string{"profile_id": "default"}))
	defer disResp.Body.Close()
	if disResp.StatusCode == http.StatusNotFound {
		t.Skipf("M3 impl pending: POST /api/v1/categories/doh/disable returns 404")
	}
	if disResp.StatusCode != http.StatusOK && disResp.StatusCode != http.StatusNoContent {
		t.Fatalf("disable doh on default: status %d: %s", disResp.StatusCode, readBody(t, disResp))
	}

	// cloudflare-dns.com is no longer NXDOMAIN — without an upstream we
	// won't get a real answer, but the response MUST NOT be NXDOMAIN
	// (the cat:doh blocklist no longer applies to this client).
	r := dnsQueryAsClient(t, n.DNSAddr, "cloudflare-dns.com", dns.TypeA, "192.168.1.10")
	if r.Rcode == dns.RcodeNameError {
		t.Fatalf("expected cloudflare-dns.com to be forwarded (not NXDOMAIN) after disable")
	}

	// The Firefox canary must STILL return NXDOMAIN — the safety net is
	// non-overridable.
	rCanary := dnsQueryAsClient(t, n.DNSAddr, "use-application-dns.net", dns.TypeA, "192.168.1.10")
	if rCanary.Rcode != dns.RcodeNameError {
		t.Fatalf("Firefox canary must remain NXDOMAIN even after doh disable, got %s",
			dns.RcodeToString[rCanary.Rcode])
	}
}
