package dns

import (
	"net"
	"strings"

	"github.com/skoed/skoed/internal/config"
	"github.com/miekg/dns"
)

// LocalResolver serves locally configured DNS entries.
type LocalResolver struct {
	// entries maps lowercase FQDN to a list of local DNS entries.
	entries map[string][]config.LocalDNSEntry
}

// NewLocalResolver builds a LocalResolver from a slice of LocalDNSEntry.
func NewLocalResolver(entries []config.LocalDNSEntry) *LocalResolver {
	r := &LocalResolver{
		entries: make(map[string][]config.LocalDNSEntry, len(entries)),
	}
	for _, e := range entries {
		fqdn := toFQDN(strings.ToLower(e.Hostname))
		r.entries[fqdn] = append(r.entries[fqdn], e)
	}
	return r
}

// Resolve returns a DNS message for the query (name, qtype) if a local entry
// matches, or (nil, false) if no local entry covers this query.
// name must be a fully-qualified domain name (with trailing dot).
func (r *LocalResolver) Resolve(name string, qtype uint16) (*dns.Msg, bool) {
	key := strings.ToLower(name)
	if !strings.HasSuffix(key, ".") {
		key += "."
	}

	candidates, ok := r.entries[key]
	if !ok {
		return nil, false
	}

	m := new(dns.Msg)
	m.SetReply(&dns.Msg{})
	m.RecursionAvailable = true
	m.Authoritative = true

	switch qtype {
	case dns.TypeA:
		for _, e := range candidates {
			if e.Type == "A" {
				rr := &dns.A{
					Hdr: dns.RR_Header{
						Name:   name,
						Rrtype: dns.TypeA,
						Class:  dns.ClassINET,
						Ttl:    uint32(e.TTL),
					},
					A: net.ParseIP(e.Value).To4(),
				}
				if rr.A == nil {
					continue
				}
				m.Answer = append(m.Answer, rr)
			}
		}
		if len(m.Answer) == 0 {
			return r.resolveCNAME(name, qtype, candidates)
		}
		return m, true

	case dns.TypeAAAA:
		for _, e := range candidates {
			if e.Type == "AAAA" {
				rr := &dns.AAAA{
					Hdr: dns.RR_Header{
						Name:   name,
						Rrtype: dns.TypeAAAA,
						Class:  dns.ClassINET,
						Ttl:    uint32(e.TTL),
					},
					AAAA: net.ParseIP(e.Value).To16(),
				}
				if rr.AAAA == nil {
					continue
				}
				m.Answer = append(m.Answer, rr)
			}
		}
		if len(m.Answer) == 0 {
			return r.resolveCNAME(name, qtype, candidates)
		}
		return m, true

	case dns.TypeCNAME:
		for _, e := range candidates {
			if e.Type == "CNAME" {
				target := toFQDN(e.Value)
				rr := &dns.CNAME{
					Hdr: dns.RR_Header{
						Name:   name,
						Rrtype: dns.TypeCNAME,
						Class:  dns.ClassINET,
						Ttl:    uint32(e.TTL),
					},
					Target: target,
				}
				m.Answer = append(m.Answer, rr)
				// Include the A/AAAA target if it has a local entry.
				r.appendCNAMETarget(m, target, dns.TypeA)
				r.appendCNAMETarget(m, target, dns.TypeAAAA)
			}
		}
		if len(m.Answer) == 0 {
			return nil, false
		}
		return m, true

	default:
		return nil, false
	}
}

// resolveCNAME tries to answer qtype via a CNAME chain from candidates.
func (r *LocalResolver) resolveCNAME(name string, qtype uint16, candidates []config.LocalDNSEntry) (*dns.Msg, bool) {
	for _, e := range candidates {
		if e.Type != "CNAME" {
			continue
		}
		target := toFQDN(e.Value)
		m := new(dns.Msg)
		m.SetReply(&dns.Msg{})
		m.RecursionAvailable = true
		m.Authoritative = true

		// Add the CNAME record first.
		cname := &dns.CNAME{
			Hdr: dns.RR_Header{
				Name:   name,
				Rrtype: dns.TypeCNAME,
				Class:  dns.ClassINET,
				Ttl:    uint32(e.TTL),
			},
			Target: target,
		}
		m.Answer = append(m.Answer, cname)

		// Attempt to resolve the target for the requested type.
		if resolved, ok := r.Resolve(target, qtype); ok {
			m.Answer = append(m.Answer, resolved.Answer...)
		}
		return m, true
	}
	return nil, false
}

// appendCNAMETarget adds local A/AAAA records for a CNAME target into m.Answer.
func (r *LocalResolver) appendCNAMETarget(m *dns.Msg, target string, qtype uint16) {
	key := strings.ToLower(target)
	if !strings.HasSuffix(key, ".") {
		key += "."
	}
	candidates, ok := r.entries[key]
	if !ok {
		return
	}
	switch qtype {
	case dns.TypeA:
		for _, e := range candidates {
			if e.Type == "A" {
				ip := net.ParseIP(e.Value).To4()
				if ip == nil {
					continue
				}
				m.Answer = append(m.Answer, &dns.A{
					Hdr: dns.RR_Header{
						Name:   target,
						Rrtype: dns.TypeA,
						Class:  dns.ClassINET,
						Ttl:    uint32(e.TTL),
					},
					A: ip,
				})
			}
		}
	case dns.TypeAAAA:
		for _, e := range candidates {
			if e.Type == "AAAA" {
				ip := net.ParseIP(e.Value).To16()
				if ip == nil {
					continue
				}
				m.Answer = append(m.Answer, &dns.AAAA{
					Hdr: dns.RR_Header{
						Name:   target,
						Rrtype: dns.TypeAAAA,
						Class:  dns.ClassINET,
						Ttl:    uint32(e.TTL),
					},
					AAAA: ip,
				})
			}
		}
	}
}

// toFQDN appends a trailing dot if absent.
func toFQDN(s string) string {
	if strings.HasSuffix(s, ".") {
		return s
	}
	return s + "."
}
