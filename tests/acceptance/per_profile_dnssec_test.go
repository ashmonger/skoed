// Acceptance tests for M38 — Per-Profile DNSSEC Policy.
//
// FSIDs covered:
//   FS-PerProfileDnssecInherit
//   FS-PerProfileDnssecValidate
//   FS-PerProfileDnssecTransparent
//
// Tests interact exclusively through the HTTP API and DNS port (black-box).
// Client-IP spoofing uses the EDNS0-65500 affordance from M3 (dnsQueryAsClient).

package acceptance

import (
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// startM38Node starts a forwarding node suitable for per-profile DNSSEC testing.
// The upstream returns the given handler's responses.
func startM38Node(t *testing.T, handler func(dns.ResponseWriter, *dns.Msg)) *Node {
	t.Helper()
	upstream := startFakeUpstream(t, handler)
	return startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})
}

// patchProfileDnssecMode PATCHes a profile's dnssec_mode via the API.
func patchProfileDnssecMode(t *testing.T, n *Node, profileID, mode string) {
	t.Helper()
	resp := n.apiDo(t, "PATCH", "/api/v1/profiles/"+profileID, mustJSON(t, map[string]any{
		"dnssec_mode": mode,
	}))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		t.Skipf("M38 per-profile DNSSEC not yet implemented (PATCH /api/v1/profiles/{id} returned %d)", resp.StatusCode)
	}
	assertStatus(t, resp, http.StatusOK)
}

// upstreamBogus returns a fake upstream that responds with a signed record
// but AD=0 (bogus — signature present but not verified).
func upstreamBogus(domain string) func(dns.ResponseWriter, *dns.Msg) {
	return func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.AuthenticatedData = false
		if len(r.Question) > 0 {
			m.Answer = append(m.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
				A:   net.ParseIP("1.2.3.4").To4(),
			})
			m.Answer = append(m.Answer, &dns.RRSIG{
				Hdr:         dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeRRSIG, Class: dns.ClassINET, Ttl: 300},
				TypeCovered: dns.TypeA,
				Algorithm:   dns.ECDSAP256SHA256,
				Labels:      2,
			})
		}
		w.WriteMsg(m) //nolint:errcheck
	}
}

// ─── Tests ────────────────────────────────────────────────────────────────────

// FS-PerProfileDnssecInherit
// A profile with dnssec_mode "inherit" (or "") falls back to the cluster-wide
// setting. When the global setting is "transparent" and a bogus response comes
// back, the query should succeed (NOERROR) — no validation is done.
func TestPerProfileDnssecInherit(t *testing.T) {
	t.Parallel()
	n := startM38Node(t, upstreamBogus("inherit-test.example.com"))

	// Ensure cluster global is transparent.
	patchDnssecMode(t, n, "transparent")

	// Create a profile that uses the default (inherit).
	createProfile(t, n, profileBody{
		ID:        "inheriting",
		Name:      "Inherit DNSSEC",
		ClientIPs: []string{"10.10.10.1"},
	})
	patchProfileDnssecMode(t, n, "inheriting", "inherit")
	time.Sleep(200 * time.Millisecond)

	// In transparent mode, bogus AD=0+RRSIG passes through → NOERROR.
	r := dnsQueryAsClient(t, n.DNSAddr, "inherit-test.example.com", dns.TypeA, "10.10.10.1")
	if r == nil {
		t.Fatal("got nil DNS response")
	}
	if r.Rcode == dns.RcodeServerFailure {
		t.Errorf("FS-PerProfileDnssecInherit: inherit profile should use transparent global setting; got SERVFAIL")
	}
}

// FS-PerProfileDnssecValidate
// A profile with dnssec_mode "validate" should reject bogus DNSSEC responses
// (RRSIG present but AD=0) with SERVFAIL, even when the global setting is
// "transparent".
func TestPerProfileDnssecValidate(t *testing.T) {
	t.Parallel()
	n := startM38Node(t, upstreamBogus("validate-test.example.com"))

	// Global is transparent; profile overrides to validate.
	patchDnssecMode(t, n, "transparent")

	createProfile(t, n, profileBody{
		ID:        "corporate",
		Name:      "Corporate",
		ClientIPs: []string{"10.20.0.5"},
	})
	patchProfileDnssecMode(t, n, "corporate", "validate")
	time.Sleep(200 * time.Millisecond)

	// Profile is in validate mode → bogus response → SERVFAIL.
	r := dnsQueryAsClient(t, n.DNSAddr, "validate-test.example.com", dns.TypeA, "10.20.0.5")
	if r == nil {
		t.Fatal("got nil DNS response")
	}
	if r.Rcode != dns.RcodeServerFailure {
		t.Errorf("FS-PerProfileDnssecValidate: profile validate mode should return SERVFAIL for bogus response; got %s",
			dns.RcodeToString[r.Rcode])
	}
}

// FS-PerProfileDnssecTransparent
// A profile with dnssec_mode "transparent" passes bogus responses even when
// the global setting is "validate".
func TestPerProfileDnssecTransparent(t *testing.T) {
	t.Parallel()
	n := startM38Node(t, upstreamBogus("transparent-test.example.com"))

	// Global validates; profile overrides to transparent.
	patchDnssecMode(t, n, "validate")

	createProfile(t, n, profileBody{
		ID:        "iot-devices",
		Name:      "IoT",
		ClientIPs: []string{"10.30.0.1"},
	})
	patchProfileDnssecMode(t, n, "iot-devices", "transparent")
	time.Sleep(200 * time.Millisecond)

	// Profile is transparent → bogus passes through → NOERROR.
	r := dnsQueryAsClient(t, n.DNSAddr, "transparent-test.example.com", dns.TypeA, "10.30.0.1")
	if r == nil {
		t.Fatal("got nil DNS response")
	}
	if r.Rcode == dns.RcodeServerFailure {
		t.Errorf("FS-PerProfileDnssecTransparent: transparent profile should pass bogus response; got SERVFAIL")
	}
}
