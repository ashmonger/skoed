// Acceptance tests for M21: DNSSEC Validation Mode.
//
// FSIDs covered:
//   FS-DnssecModeConfigurable
//   FS-DnssecValidateBogusServfail
//   FS-DnssecValidateOkPassthrough
//   FS-DnssecValidateInsecurePassthrough

package acceptance

import (
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// waitForLogDnssecStatus polls GET /api/v1/query-log until an entry matching
// domain and dnssec_status appears, or until maxWait elapses. Returns the entry
// or fails the test.
func waitForLogDnssecStatus(t *testing.T, n *Node, domain, dnssecStatus string, maxWait time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		entries := getLog(t, n, "")
		for _, e := range entries {
			d, _ := e["domain"].(string)
			s, _ := e["dnssec_status"].(string)
			if d == domain && s == dnssecStatus {
				return e
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("query log entry for domain=%q dnssec_status=%q not found within %s", domain, dnssecStatus, maxWait)
	return nil
}

// patchDnssecMode PATCHes dns.dnssec_mode on the given node and asserts 200.
func patchDnssecMode(t *testing.T, n *Node, mode string) {
	t.Helper()
	resp := n.apiDo(t, "PATCH", "/api/v1/settings", mustJSON(t, map[string]any{
		"dns": map[string]any{
			"dnssec_mode": mode,
		},
	}))
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusOK)
}

// getDnssecMode fetches dns.dnssec_mode from GET /api/v1/settings.
func getDnssecMode(t *testing.T, n *Node) string {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/settings", "")
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusOK)
	var settings map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&settings); err != nil {
		t.Fatalf("decode GET /api/v1/settings: %v", err)
	}
	dnsSection, ok := settings["dns"].(map[string]any)
	if !ok {
		t.Fatalf("settings response missing dns object: %v", settings)
	}
	mode, _ := dnsSection["dnssec_mode"].(string)
	return mode
}

// FS-DnssecModeConfigurable
// The dns.dnssec_mode setting can be changed via PATCH /api/v1/settings and
// the new value is reflected by GET /api/v1/settings.
func TestDnssecModeConfigurable(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	patchDnssecMode(t, n, "validate")
	if got := getDnssecMode(t, n); got != "validate" {
		t.Fatalf("expected dnssec_mode=validate after PATCH, got %q", got)
	}

	patchDnssecMode(t, n, "transparent")
	if got := getDnssecMode(t, n); got != "transparent" {
		t.Fatalf("expected dnssec_mode=transparent after PATCH, got %q", got)
	}
}

// FS-DnssecValidateBogusServfail
// In validate mode, a response that carries RRSIG records but has AD=0 (bogus
// signature) is rejected and SERVFAIL is returned to the client.
func TestDnssecValidateBogusReturnsServfail(t *testing.T) {
	t.Parallel()
	// Fake upstream returns a response that looks signed (has RRSIG) but sets
	// AD=0, signalling the signature did not verify.
	upstreamAddr := startFakeUpstream(t, func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.AuthenticatedData = false // AD=0 — bogus
		if len(r.Question) > 0 {
			m.Answer = append(m.Answer, &dns.A{
				Hdr: dns.RR_Header{
					Name:   r.Question[0].Name,
					Rrtype: dns.TypeA,
					Class:  dns.ClassINET,
					Ttl:    300,
				},
				A: net.ParseIP("93.184.216.34").To4(),
			})
			// RRSIG present — domain claims to be signed but AD=0 makes it bogus.
			m.Answer = append(m.Answer, &dns.RRSIG{
				Hdr: dns.RR_Header{
					Name:   r.Question[0].Name,
					Rrtype: dns.TypeRRSIG,
					Class:  dns.ClassINET,
					Ttl:    300,
				},
				TypeCovered: dns.TypeA,
				Algorithm:   dns.ECDSAP256SHA256,
				Labels:      2,
			})
		}
		w.WriteMsg(m) //nolint:errcheck
	})

	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstreamAddr},
	})
	patchDnssecMode(t, n, "validate")

	r := dnsQuery(t, n.DNSAddr, "bogus.example.com", dns.TypeA)

	assertRcode(t, r, dns.RcodeServerFailure)
}

// FS-DnssecValidateOkPassthrough
// In validate mode, a response with AD=1 (signature verified by upstream
// resolver) passes through with NOERROR and the query log records
// dnssec_status="ok".
func TestDnssecValidateOkPassthrough(t *testing.T) {
	t.Parallel()
	upstreamAddr := startFakeUpstream(t, func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.AuthenticatedData = true // AD=1 — upstream validated the signatures
		if len(r.Question) > 0 {
			m.Answer = append(m.Answer, &dns.A{
				Hdr: dns.RR_Header{
					Name:   r.Question[0].Name,
					Rrtype: dns.TypeA,
					Class:  dns.ClassINET,
					Ttl:    300,
				},
				A: net.ParseIP("93.184.216.34").To4(),
			})
		}
		w.WriteMsg(m) //nolint:errcheck
	})

	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstreamAddr},
	})
	patchDnssecMode(t, n, "validate")

	r := dnsQuery(t, n.DNSAddr, "signed.example.com", dns.TypeA)

	assertRcode(t, r, dns.RcodeSuccess)
	waitForLogDnssecStatus(t, n, "signed.example.com", "ok", 3*time.Second)
}

// FS-DnssecValidateInsecurePassthrough
// In validate mode, a response with AD=0 and no RRSIG records (unsigned
// domain) passes through with NOERROR and the query log records
// dnssec_status="insecure".
func TestDnssecValidateInsecurePassthrough(t *testing.T) {
	t.Parallel()
	upstreamAddr := startFakeUpstream(t, func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.AuthenticatedData = false // AD=0 — no DNSSEC
		if len(r.Question) > 0 {
			m.Answer = append(m.Answer, &dns.A{
				Hdr: dns.RR_Header{
					Name:   r.Question[0].Name,
					Rrtype: dns.TypeA,
					Class:  dns.ClassINET,
					Ttl:    300,
				},
				A: net.ParseIP("93.184.216.34").To4(),
			})
			// No RRSIG records — domain is simply unsigned (insecure).
		}
		w.WriteMsg(m) //nolint:errcheck
	})

	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstreamAddr},
	})
	patchDnssecMode(t, n, "validate")

	r := dnsQuery(t, n.DNSAddr, "unsigned.example.com", dns.TypeA)

	assertRcode(t, r, dns.RcodeSuccess)
	waitForLogDnssecStatus(t, n, "unsigned.example.com", "insecure", 3*time.Second)
}
