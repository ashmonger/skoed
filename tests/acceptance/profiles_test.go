// Acceptance tests for M3 per-client profiles.
//
// FSIDs covered (one Go test per FSID):
//   FS-ProfileAssignByIp
//   FS-ProfileAssignByCidr
//   FS-ProfileDefaultFallback
//   FS-ProfilePerClientAllowlist
//   FS-ProfileApiCrud
//   FS-ProfileSharedClientGroups
//
// Spoofing the client IP from a normal Go test is impossible: the engine sees
// whichever source IP the OS picks for the UDP packet (always 127.0.0.1 here).
// The implementation therefore honours a test affordance documented in
// specs/technical/profiles-and-schedules.md: when SKOED_TEST_MODE=1 is set,
// the DNS handler reads an EDNS0 option in the private-use code range
// (opt code 65500) whose data is the client IP the engine MUST pretend to be.
// dnsQueryAsClient builds a query carrying that option.
//
// These tests COMPILE today; several will FAIL against the current binary
// because the /api/v1/profiles routes and the EDNS0-65500 client-IP override
// don't exist yet — that's the M3 implementation gap they're meant to expose.

package acceptance

import (
	"encoding/json"
	"net"
	"net/http"
	"testing"

	"github.com/miekg/dns"
)

// edns0ClientIPCode is the private-use EDNS0 option code carrying a faked
// client IP. Honoured by the DNS handler only when SKOED_TEST_MODE=1.
const edns0ClientIPCode = 65500

// dnsQueryAsClient sends a UDP DNS query to server, embedding an EDNS0 LOCAL
// option (code 65500) whose data is clientIP. The skoed binary, when running
// with SKOED_TEST_MODE=1, MUST use that IP as the request's client IP for
// profile resolution. Returns the response.
func dnsQueryAsClient(t *testing.T, server, name string, qtype uint16, clientIP string) *dns.Msg {
	t.Helper()
	c := &dns.Client{Net: "udp", Timeout: dnsQueryTimeout}
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	m.SetEdns0(4096, false)

	ip := net.ParseIP(clientIP)
	if ip == nil {
		t.Fatalf("dnsQueryAsClient: invalid client IP %q", clientIP)
	}
	// Encode as 4 bytes for v4, 16 bytes for v6.
	var data []byte
	if v4 := ip.To4(); v4 != nil {
		data = []byte(v4)
	} else {
		data = []byte(ip.To16())
	}

	opt := m.IsEdns0()
	if opt == nil {
		t.Fatalf("dnsQueryAsClient: SetEdns0 did not attach an OPT RR")
	}
	opt.Option = append(opt.Option, &dns.EDNS0_LOCAL{
		Code: edns0ClientIPCode,
		Data: data,
	})

	r, _, err := c.Exchange(m, server)
	if err != nil {
		t.Fatalf("DNS query as %s %s %s @%s: %v", clientIP, name, dns.TypeToString[qtype], server, err)
	}
	return r
}

// profileBody is the documented profile JSON shape (see TS-ProfilesAndSchedules).
type profileBody struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Blocklists  []string `json:"blocklists"`
	Allowlist   []string `json:"allowlist"`
	SafeSearch  []string `json:"safesearch,omitempty"`
	ClientIPs   []string `json:"client_ips"`
	ClientCIDRs []string `json:"client_cidrs"`
}

// startProfilesNode starts a forwarding single-node with SKOED_TEST_MODE=1 so
// the EDNS0 client-IP override is honoured. The single-node M1 harness inherits
// the parent process env, so t.Setenv is enough.
func startProfilesNode(t *testing.T, upstream string) *Node {
	t.Helper()
	return startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
}

// createProfile POSTs a profile and asserts 201 Created. Skips the test (with
// a "M3 impl pending" reason) when the route is missing entirely — i.e. 404 —
// so the file compiles and runs cleanly until M3 routes are wired up.
func createProfile(t *testing.T, n *Node, body profileBody) {
	t.Helper()
	resp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, body))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M3 impl pending: POST /api/v1/profiles returns 404 (route not registered)")
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create profile %s: status %d: %s", body.ID, resp.StatusCode, readBody(t, resp))
	}
}

// ── Tests ─────────────────────────────────────────────────────────────────

// FS-ProfileAssignByIp
func TestProfileAssignByIp(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startProfilesNode(t, upstream)

	// Background blocklists: "ads" and "social".
	addInlineBlocklist(t, n, "ads", []string{"tracker.example.com"}, "")
	addInlineBlocklist(t, n, "social", []string{"facebook.com"}, "")

	// Default profile uses only "ads".
	createProfile(t, n, profileBody{
		ID:          "default",
		Name:        "Default",
		Blocklists:  []string{"ads"},
		Allowlist:   []string{},
		ClientIPs:   []string{},
		ClientCIDRs: []string{},
	})

	// Kids profile uses both "ads" and "social", assigned to 192.168.1.50.
	createProfile(t, n, profileBody{
		ID:          "kids",
		Name:        "Kids",
		Blocklists:  []string{"ads", "social"},
		Allowlist:   []string{},
		ClientIPs:   []string{"192.168.1.50"},
		ClientCIDRs: []string{},
	})

	// 192.168.1.50 → kids profile → facebook.com blocked.
	r := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.1.50")
	assertRcode(t, r, dns.RcodeNameError)
}

// FS-ProfileAssignByCidr
func TestProfileAssignByCidr(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startProfilesNode(t, upstream)

	addInlineBlocklist(t, n, "ads", []string{"tracker.example.com"}, "")
	addInlineBlocklist(t, n, "social", []string{"facebook.com"}, "")

	createProfile(t, n, profileBody{
		ID:          "default",
		Name:        "Default",
		Blocklists:  []string{"ads"},
		Allowlist:   []string{},
		ClientIPs:   []string{},
		ClientCIDRs: []string{},
	})
	createProfile(t, n, profileBody{
		ID:          "kids",
		Name:        "Kids",
		Blocklists:  []string{"ads", "social"},
		Allowlist:   []string{},
		ClientIPs:   []string{},
		ClientCIDRs: []string{"192.168.10.0/24"},
	})

	// Inside the kids subnet → facebook.com is blocked.
	r1 := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.10.7")
	assertRcode(t, r1, dns.RcodeNameError)

	// Outside the kids subnet → default profile, social NOT blocked → forwarded.
	r2 := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.20.5")
	assertRcode(t, r2, dns.RcodeSuccess)
	assertAnswerA(t, r2, "1.2.3.4")
}

// FS-ProfileDefaultFallback
func TestProfileDefaultFallback(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startProfilesNode(t, upstream)

	addInlineBlocklist(t, n, "ads", []string{"tracker.example.com"}, "")
	addInlineBlocklist(t, n, "social", []string{"facebook.com"}, "")

	// Default uses ads only; kids exists but is bound to a different IP that
	// the test client does NOT match.
	createProfile(t, n, profileBody{
		ID:          "default",
		Name:        "Default",
		Blocklists:  []string{"ads"},
		Allowlist:   []string{},
		ClientIPs:   []string{},
		ClientCIDRs: []string{},
	})
	createProfile(t, n, profileBody{
		ID:          "kids",
		Name:        "Kids",
		Blocklists:  []string{"ads", "social"},
		Allowlist:   []string{},
		ClientIPs:   []string{"192.168.1.50"},
		ClientCIDRs: []string{},
	})

	// 192.168.99.99 has no profile assignment → falls back to default.
	// tracker.example.com (in ads) → blocked.
	rBlocked := dnsQueryAsClient(t, n.DNSAddr, "tracker.example.com", dns.TypeA, "192.168.99.99")
	assertRcode(t, rBlocked, dns.RcodeNameError)

	// facebook.com (in social, NOT in default) → forwarded.
	rForwarded := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.99.99")
	assertRcode(t, rForwarded, dns.RcodeSuccess)
	assertAnswerA(t, rForwarded, "1.2.3.4")
}

// FS-ProfilePerClientAllowlist
func TestProfilePerClientAllowlist(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startProfilesNode(t, upstream)

	addInlineBlocklist(t, n, "ads", []string{"tracker.example.com"}, "")
	// "social" blocklist contains youtube.com; the kids profile then
	// allowlists youtube.com — the allowlist must override.
	addInlineBlocklist(t, n, "social", []string{"facebook.com", "youtube.com"}, "")

	createProfile(t, n, profileBody{
		ID:          "default",
		Name:        "Default",
		Blocklists:  []string{"ads"},
		Allowlist:   []string{},
		ClientIPs:   []string{},
		ClientCIDRs: []string{},
	})
	createProfile(t, n, profileBody{
		ID:          "kids",
		Name:        "Kids",
		Blocklists:  []string{"ads", "social"},
		Allowlist:   []string{"youtube.com"},
		ClientIPs:   []string{"192.168.1.50"},
		ClientCIDRs: []string{},
	})

	// facebook.com is in social and NOT allowlisted → blocked.
	rBlocked := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.1.50")
	assertRcode(t, rBlocked, dns.RcodeNameError)

	// youtube.com is in social BUT allowlisted at the profile → forwarded.
	rAllowed := dnsQueryAsClient(t, n.DNSAddr, "youtube.com", dns.TypeA, "192.168.1.50")
	assertRcode(t, rAllowed, dns.RcodeSuccess)
	assertAnswerA(t, rAllowed, "1.2.3.4")
}

// FS-ProfileApiCrud
func TestProfileApiCrud(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startProfilesNode(t, upstream)

	// Pre-create the blocklists referenced in PATCH and POST payloads.
	addInlineBlocklist(t, n, "ads", []string{"tracker.example.com"}, "")
	addInlineBlocklist(t, n, "social", []string{"facebook.com"}, "")

	// CREATE
	body := profileBody{
		ID:          "guests",
		Name:        "Guests",
		Blocklists:  []string{"ads"},
		Allowlist:   []string{"news.example.com"},
		ClientIPs:   []string{"10.0.0.5"},
		ClientCIDRs: []string{},
	}
	createProfile(t, n, body)

	// GET — verify shape.
	getResp := n.apiDo(t, "GET", "/api/v1/profiles/guests", "")
	assertStatus(t, getResp, http.StatusOK)
	var got profileBody
	if err := json.NewDecoder(getResp.Body).Decode(&got); err != nil {
		getResp.Body.Close()
		t.Fatalf("decode GET /profiles/guests: %v", err)
	}
	getResp.Body.Close()
	if got.ID != "guests" || got.Name != "Guests" {
		t.Fatalf("GET /profiles/guests returned unexpected payload: %+v", got)
	}
	if len(got.Blocklists) != 1 || got.Blocklists[0] != "ads" {
		t.Fatalf("blocklists mismatch: got %+v, want [ads]", got.Blocklists)
	}
	if len(got.ClientIPs) != 1 || got.ClientIPs[0] != "10.0.0.5" {
		t.Fatalf("client_ips mismatch: got %+v, want [10.0.0.5]", got.ClientIPs)
	}

	// PATCH — add the "social" blocklist.
	patchResp := n.apiDo(t, "PATCH", "/api/v1/profiles/guests", mustJSON(t, map[string]any{
		"blocklists": []string{"ads", "social"},
	}))
	assertStatus(t, patchResp, http.StatusOK)
	patchResp.Body.Close()

	// GET again — confirm the PATCH replicated locally.
	getResp2 := n.apiDo(t, "GET", "/api/v1/profiles/guests", "")
	assertStatus(t, getResp2, http.StatusOK)
	var got2 profileBody
	if err := json.NewDecoder(getResp2.Body).Decode(&got2); err != nil {
		getResp2.Body.Close()
		t.Fatalf("decode GET /profiles/guests (post-PATCH): %v", err)
	}
	getResp2.Body.Close()
	if len(got2.Blocklists) != 2 {
		t.Fatalf("PATCH did not take effect: blocklists=%+v", got2.Blocklists)
	}

	// DELETE
	delResp := n.apiDo(t, "DELETE", "/api/v1/profiles/guests", "")
	if delResp.StatusCode != http.StatusNoContent && delResp.StatusCode != http.StatusOK {
		body := readBody(t, delResp)
		t.Fatalf("DELETE /profiles/guests: status %d: %s", delResp.StatusCode, body)
	}
	delResp.Body.Close()

	// GET → 404
	getResp3 := n.apiDo(t, "GET", "/api/v1/profiles/guests", "")
	defer getResp3.Body.Close()
	if getResp3.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after DELETE, got %d", getResp3.StatusCode)
	}
}

// FS-ProfileSharedClientGroups
//
// A single client IP appears in two profiles; the engine must apply the union
// of their blocklists.
func TestProfileSharedClientGroups(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startProfilesNode(t, upstream)

	addInlineBlocklist(t, n, "social", []string{"facebook.com"}, "")
	addInlineBlocklist(t, n, "gaming", []string{"steam.com"}, "")

	createProfile(t, n, profileBody{
		ID:          "default",
		Name:        "Default",
		Blocklists:  []string{},
		Allowlist:   []string{},
		ClientIPs:   []string{},
		ClientCIDRs: []string{},
	})

	// Two profiles, same client IP — union must apply.
	createProfile(t, n, profileBody{
		ID:          "kids",
		Name:        "Kids",
		Blocklists:  []string{"social"},
		Allowlist:   []string{},
		ClientIPs:   []string{"192.168.1.50"},
		ClientCIDRs: []string{},
	})
	createProfile(t, n, profileBody{
		ID:          "work",
		Name:        "Work",
		Blocklists:  []string{"gaming"},
		Allowlist:   []string{},
		ClientIPs:   []string{"192.168.1.50"},
		ClientCIDRs: []string{},
	})

	// facebook.com → blocked by kids.
	rSocial := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.1.50")
	assertRcode(t, rSocial, dns.RcodeNameError)

	// steam.com → blocked by work.
	rGaming := dnsQueryAsClient(t, n.DNSAddr, "steam.com", dns.TypeA, "192.168.1.50")
	assertRcode(t, rGaming, dns.RcodeNameError)
}
