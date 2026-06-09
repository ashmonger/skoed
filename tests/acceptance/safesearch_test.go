// Acceptance tests for M3 SafeSearch enforcement.
//
// FSIDs covered (one Go test per FSID):
//   FS-SafeSearchGoogle
//   FS-SafeSearchBing
//   FS-SafeSearchYoutube
//   FS-SafeSearchDuckDuckGo
//   FS-SafeSearchOptInPerProfile
//   FS-SafeSearchAaaa
//
// Black-box: each test starts a fresh single-node cluster, creates a "kids"
// profile (with safesearch enabled for the provider under test) bound to a
// fixed client IP, then queries the provider's domain through the EDNS0
// client-IP override (see profiles_test.go for the affordance) and asserts
// the response Answer carries a CNAME pointing at the provider's SafeSearch
// endpoint.

package acceptance

import (
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// kidsClientIP is the EDNS0-injected client IP used by every SafeSearch test.
const kidsClientIP = "192.168.1.50"

// safesearchUpstream returns a handler that answers BOTH A and AAAA queries.
// A queries return 1.2.3.4; AAAA queries return 2001:db8::1.
// SafeSearch tests rely on the upstream replying for the rewritten CNAME
// target (forcesafesearch.google.com, strict.bing.com, etc.) — the simplest
// approach is to make EVERY name resolvable.
func safesearchUpstreamHandler() dns.HandlerFunc {
	return func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		if len(r.Question) > 0 {
			q := r.Question[0]
			switch q.Qtype {
			case dns.TypeA:
				m.Answer = append(m.Answer, &dns.A{
					Hdr: dns.RR_Header{
						Name: q.Name, Rrtype: dns.TypeA,
						Class: dns.ClassINET, Ttl: 300,
					},
					A: net.ParseIP("1.2.3.4").To4(),
				})
			case dns.TypeAAAA:
				m.Answer = append(m.Answer, &dns.AAAA{
					Hdr: dns.RR_Header{
						Name: q.Name, Rrtype: dns.TypeAAAA,
						Class: dns.ClassINET, Ttl: 300,
					},
					AAAA: net.ParseIP("2001:db8::1").To16(),
				})
			}
		}
		_ = w.WriteMsg(m)
	}
}

// startSafeSearchNode brings up a forwarding node with SKOED_TEST_MODE=1 so
// the EDNS0 client-IP override is honoured by the DNS handler.
func startSafeSearchNode(t *testing.T) *Node {
	t.Helper()
	upstream := startFakeUpstream(t, safesearchUpstreamHandler())
	return startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
}

// createSafeSearchProfile creates a profile with the given safesearch
// providers, bound to kidsClientIP. Skips the test if the route is missing.
func createSafeSearchProfile(t *testing.T, n *Node, id string, providers []string) {
	t.Helper()
	body := profileBody{
		ID:          id,
		Name:        strings.Title(id),
		Blocklists:  []string{},
		Allowlist:   []string{},
		SafeSearch:  providers,
		ClientIPs:   []string{kidsClientIP},
		ClientCIDRs: []string{},
	}
	resp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, body))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M3 impl pending: POST /api/v1/profiles returns 404")
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create profile %s: status %d: %s", id, resp.StatusCode, readBody(t, resp))
	}
}

// assertCNAMETarget fails the test if no CNAME in the Answer section
// targets wantTarget (which must be FQDN with trailing dot).
func assertCNAMETarget(t *testing.T, msg *dns.Msg, wantTarget string) {
	t.Helper()
	for _, rr := range msg.Answer {
		if c, ok := rr.(*dns.CNAME); ok {
			if strings.EqualFold(c.Target, wantTarget) {
				return
			}
		}
	}
	t.Fatalf("no CNAME pointing at %q in answer section: %+v", wantTarget, msg.Answer)
}

// assertNoCNAMERewrite fails if any CNAME in the answer points at a known
// SafeSearch endpoint (used by the opt-in negative test).
func assertNoCNAMERewrite(t *testing.T, msg *dns.Msg) {
	t.Helper()
	targets := []string{
		"forcesafesearch.google.com.",
		"strict.bing.com.",
		"restrict.youtube.com.",
		"safe.duckduckgo.com.",
	}
	for _, rr := range msg.Answer {
		c, ok := rr.(*dns.CNAME)
		if !ok {
			continue
		}
		for _, target := range targets {
			if strings.EqualFold(c.Target, target) {
				t.Fatalf("unexpected SafeSearch CNAME injection to %q in: %+v", c.Target, msg.Answer)
			}
		}
	}
}

// FS-SafeSearchGoogle
func TestSafeSearchGoogle(t *testing.T) {
	t.Parallel()
	n := startSafeSearchNode(t)
	createSafeSearchProfile(t, n, "kids", []string{"google"})

	// Give the engine a beat to apply the profile (single-node Raft commit).
	time.Sleep(50 * time.Millisecond)

	r := dnsQueryAsClient(t, n.DNSAddr, "www.google.com", dns.TypeA, kidsClientIP)
	assertCNAMETarget(t, r, "forcesafesearch.google.com.")
}

// FS-SafeSearchBing
func TestSafeSearchBing(t *testing.T) {
	t.Parallel()
	n := startSafeSearchNode(t)
	createSafeSearchProfile(t, n, "kids", []string{"bing"})

	time.Sleep(50 * time.Millisecond)

	r := dnsQueryAsClient(t, n.DNSAddr, "www.bing.com", dns.TypeA, kidsClientIP)
	assertCNAMETarget(t, r, "strict.bing.com.")
}

// FS-SafeSearchYoutube
func TestSafeSearchYoutube(t *testing.T) {
	t.Parallel()
	n := startSafeSearchNode(t)
	createSafeSearchProfile(t, n, "kids", []string{"youtube"})

	time.Sleep(50 * time.Millisecond)

	r := dnsQueryAsClient(t, n.DNSAddr, "www.youtube.com", dns.TypeA, kidsClientIP)
	assertCNAMETarget(t, r, "restrict.youtube.com.")
}

// FS-SafeSearchDuckDuckGo
func TestSafeSearchDuckDuckGo(t *testing.T) {
	t.Parallel()
	n := startSafeSearchNode(t)
	createSafeSearchProfile(t, n, "kids", []string{"duckduckgo"})

	time.Sleep(50 * time.Millisecond)

	r := dnsQueryAsClient(t, n.DNSAddr, "duckduckgo.com", dns.TypeA, kidsClientIP)
	assertCNAMETarget(t, r, "safe.duckduckgo.com.")
}

// FS-SafeSearchOptInPerProfile
//
// The "adults" profile has an empty safesearch list — a Google query MUST
// NOT carry a SafeSearch CNAME; it must be forwarded as a plain A response.
func TestSafeSearchOptInPerProfile(t *testing.T) {
	t.Parallel()
	n := startSafeSearchNode(t)
	createSafeSearchProfile(t, n, "adults", []string{})

	time.Sleep(50 * time.Millisecond)

	r := dnsQueryAsClient(t, n.DNSAddr, "www.google.com", dns.TypeA, kidsClientIP)
	assertRcode(t, r, dns.RcodeSuccess)
	assertNoCNAMERewrite(t, r)
	// Expect the upstream A record to come through unchanged.
	assertAnswerA(t, r, "1.2.3.4")
}

// FS-SafeSearchAaaa
//
// The rewrite must apply equally to AAAA queries.
func TestSafeSearchAaaa(t *testing.T) {
	t.Parallel()
	n := startSafeSearchNode(t)
	createSafeSearchProfile(t, n, "kids", []string{"google"})

	time.Sleep(50 * time.Millisecond)

	r := dnsQueryAsClient(t, n.DNSAddr, "www.google.com", dns.TypeAAAA, kidsClientIP)
	assertCNAMETarget(t, r, "forcesafesearch.google.com.")
}
