// Acceptance tests for the DNS engine.
//
// FSIDs covered:
//   FS-DnsQueryForwarding, FS-DnsQueryForwardingTcp,
//   FS-DnsQueryForwardingFallback, FS-DnsQueryForwardingAllUpstreamsUnreachable,
//   FS-DnsQueryForwardingAAAA, FS-DnsQueryForwardingMultipleRecordTypes,
//   FS-DualStackDnsIPv4Listener, FS-DualStackDnsNullBlockIPv4, FS-DualStackDnsNullBlockIPv6,
//   FS-DnssecTransparentProxy, FS-DnssecTransparentProxyWithoutDoBit,
//   FS-DnssecTransparentProxyBlockedDomain

package acceptance

import (
	"net"
	"sync/atomic"
	"testing"

	"github.com/miekg/dns"
)

// FS-DnsQueryForwarding
// A client query for an internet domain is forwarded to the upstream and the
// answer is returned to the client.
func TestDnsQueryForwarding(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	r := dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)

	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerA(t, r, "93.184.216.34")
}

// FS-DnsQueryForwardingTcp
// A client query sent over TCP is forwarded and answered correctly.
func TestDnsQueryForwardingTCP(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	c := &dns.Client{Net: "tcp", Timeout: dnsQueryTimeout}
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn("example.com"), dns.TypeA)
	r, _, err := c.Exchange(m, n.DNSAddr)
	if err != nil {
		t.Fatalf("TCP DNS query: %v", err)
	}

	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerA(t, r, "93.184.216.34")
}

// FS-DnsQueryForwardingFallback
// When the primary upstream is unreachable, skoed falls back to the next one.
func TestDnsQueryForwardingFallback(t *testing.T) {
	t.Parallel()
	// Port with no listener — will time out
	deadUpstream := "127.0.0.1:1"
	liveUpstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))

	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{deadUpstream, liveUpstream},
	})

	r := dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)

	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerA(t, r, "93.184.216.34")
}

// FS-DnsQueryForwardingAllUpstreamsUnreachable
// When all upstreams are unreachable, skoed returns SERVFAIL.
func TestDnsQueryForwardingAllUpstreamsUnreachable(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{"127.0.0.1:1"},
	})

	r := dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)

	assertRcode(t, r, dns.RcodeServerFailure)
}

// FS-DnsQueryForwardingAAAA
// AAAA queries are forwarded and the answer is returned.
func TestDnsQueryForwardingAAAA(t *testing.T) {
	t.Parallel()
	upstreamAddr := startFakeUpstream(t, func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		if len(r.Question) > 0 && r.Question[0].Qtype == dns.TypeAAAA {
			m.Answer = append(m.Answer, &dns.AAAA{
				Hdr:  dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 300},
				AAAA: net.ParseIP("2606:2800:220:1:248:1893:25c8:1946"),
			})
		}
		w.WriteMsg(m) //nolint:errcheck
	})

	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstreamAddr},
	})

	r := dnsQuery(t, n.DNSAddr, "example.com", dns.TypeAAAA)

	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerAAAA(t, r, "2606:2800:220:1:248:1893:25c8:1946")
}

// FS-DnsQueryForwardingMultipleRecordTypes
// Non-A/AAAA record types (e.g. MX) are forwarded and returned.
func TestDnsQueryForwardingMX(t *testing.T) {
	t.Parallel()
	upstreamAddr := startFakeUpstream(t, func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		if len(r.Question) > 0 && r.Question[0].Qtype == dns.TypeMX {
			m.Answer = append(m.Answer, &dns.MX{
				Hdr:        dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeMX, Class: dns.ClassINET, Ttl: 300},
				Preference: 10,
				Mx:         "mail.example.com.",
			})
		}
		w.WriteMsg(m) //nolint:errcheck
	})

	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstreamAddr},
	})

	r := dnsQuery(t, n.DNSAddr, "example.com", dns.TypeMX)

	assertRcode(t, r, dns.RcodeSuccess)
	if len(r.Answer) == 0 {
		t.Fatal("expected MX record in answer, got none")
	}
	mx, ok := r.Answer[0].(*dns.MX)
	if !ok {
		t.Fatalf("expected MX record type, got %T", r.Answer[0])
	}
	if mx.Mx != "mail.example.com." {
		t.Fatalf("expected MX target mail.example.com., got %s", mx.Mx)
	}
}

// FS-DualStackDnsIPv4Listener
// A query from an IPv4 client is accepted and answered.
func TestDualStackIPv4Listener(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	// dnsQuery uses 127.0.0.1 (IPv4) by default
	r := dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)

	assertRcode(t, r, dns.RcodeSuccess)
}

// FS-DualStackDnsNullBlockIPv4
// When a domain is blocked with NULL policy, an A query returns 0.0.0.0.
func TestDualStackNullBlockIPv4(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "null",
	})

	// Add a blocklist with null policy via API
	n.apiDo(t, "POST", "/api/v1/blocklists", mustJSON(t, map[string]any{
		"id":   "test",
		"name": "Test",
		"source": map[string]string{"type": "inline"},
		"domains": []string{"blocked.example.com"},
	}))

	r := dnsQuery(t, n.DNSAddr, "blocked.example.com", dns.TypeA)

	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerA(t, r, "0.0.0.0")
}

// FS-DualStackDnsNullBlockIPv6
// When a domain is blocked with NULL policy, an AAAA query returns ::.
func TestDualStackNullBlockIPv6(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "null",
	})

	n.apiDo(t, "POST", "/api/v1/blocklists", mustJSON(t, map[string]any{
		"id":   "test",
		"name": "Test",
		"source": map[string]string{"type": "inline"},
		"domains": []string{"blocked.example.com"},
	}))

	r := dnsQuery(t, n.DNSAddr, "blocked.example.com", dns.TypeAAAA)

	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerAAAA(t, r, "::")
}

// FS-DnssecTransparentProxy
// When the client sets the DO bit, DNSSEC records from upstream are forwarded.
func TestDnssecTransparentProxyDOBitForwarded(t *testing.T) {
	t.Parallel()
	var receivedDO atomic.Bool
	upstreamAddr := startFakeUpstream(t, func(w dns.ResponseWriter, r *dns.Msg) {
		// Record whether the DO bit arrived at the upstream
		if opt := r.IsEdns0(); opt != nil {
			receivedDO.Store(opt.Do())
		}
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = append(m.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
			A:   net.ParseIP("93.184.216.34").To4(),
		})
		// Include a fake RRSIG to simulate DNSSEC-capable upstream
		m.Answer = append(m.Answer, &dns.RRSIG{
			Hdr:        dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeRRSIG, Class: dns.ClassINET, Ttl: 300},
			TypeCovered: dns.TypeA,
			Algorithm:  dns.ECDSAP256SHA256,
			Labels:     2,
		})
		w.WriteMsg(m) //nolint:errcheck
	})

	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstreamAddr},
	})

	r := dnsQueryWithDO(t, n.DNSAddr, "example.com", dns.TypeA)

	if !receivedDO.Load() {
		t.Fatal("skoed did not forward the DO bit to the upstream resolver")
	}

	// RRSIG must be present in the response forwarded to the client
	hasRRSIG := false
	for _, rr := range r.Answer {
		if _, ok := rr.(*dns.RRSIG); ok {
			hasRRSIG = true
			break
		}
	}
	if !hasRRSIG {
		t.Fatal("expected RRSIG in response when DO bit was set, got none")
	}
}

// FS-DnssecTransparentProxyWithoutDoBit
// Without the DO bit, the upstream is not asked for DNSSEC records.
func TestDnssecTransparentProxyNoDOBit(t *testing.T) {
	t.Parallel()
	var receivedDO atomic.Bool
	upstreamAddr := startFakeUpstream(t, func(w dns.ResponseWriter, r *dns.Msg) {
		if opt := r.IsEdns0(); opt != nil {
			receivedDO.Store(opt.Do())
		}
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = append(m.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
			A:   net.ParseIP("93.184.216.34").To4(),
		})
		w.WriteMsg(m) //nolint:errcheck
	})

	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstreamAddr},
	})

	// Plain query — no DO bit
	dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)

	if receivedDO.Load() {
		t.Fatal("skoed set the DO bit on the upstream query but the client did not request it")
	}
}

// FS-DnssecTransparentProxyBlockedDomain
// DNSSEC records are not returned for blocked domains; upstream is not contacted.
func TestDnssecTransparentProxyBlockedDomain(t *testing.T) {
	t.Parallel()
	var contacted atomic.Bool
	upstreamAddr := startFakeUpstream(t, func(w dns.ResponseWriter, r *dns.Msg) {
		contacted.Store(true)
		m := new(dns.Msg)
		m.SetReply(r)
		w.WriteMsg(m) //nolint:errcheck
	})

	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstreamAddr},
		BlockPolicy:       "nxdomain",
	})

	n.apiDo(t, "POST", "/api/v1/blocklists", mustJSON(t, map[string]any{
		"id":   "test",
		"name": "Test",
		"source": map[string]string{"type": "inline"},
		"domains": []string{"blocked.example.com"},
	}))

	r := dnsQueryWithDO(t, n.DNSAddr, "blocked.example.com", dns.TypeA)

	assertRcode(t, r, dns.RcodeNameError) // NXDOMAIN
	if contacted.Load() {
		t.Fatal("skoed contacted the upstream resolver for a blocked domain")
	}
}
