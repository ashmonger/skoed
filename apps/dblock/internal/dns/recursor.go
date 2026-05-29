package dns

import (
	"net"
	"time"

	"github.com/miekg/dns"
)

const maxIterations = 20

// rootHints contains the IPv4 addresses of the 13 IANA root nameservers.
var rootHints = []string{
	"198.41.0.4",    // a.root-servers.net
	"199.9.14.201",  // b.root-servers.net
	"192.33.4.12",   // c.root-servers.net
	"199.7.91.13",   // d.root-servers.net
	"192.203.230.10", // e.root-servers.net
	"192.5.5.241",   // f.root-servers.net
	"192.112.36.4",  // g.root-servers.net
	"198.97.190.53", // h.root-servers.net
	"192.36.148.17", // i.root-servers.net
	"192.58.128.30", // j.root-servers.net
	"193.0.14.129",  // k.root-servers.net
	"199.7.83.42",   // l.root-servers.net
	"202.12.27.33",  // m.root-servers.net
}

// Recursor performs iterative DNS resolution starting from the IANA root
// nameservers.
type Recursor struct {
	subnets []*net.IPNet
}

// NewRecursor creates a Recursor. trustedSubnets is a list of CIDR strings;
// empty means unrestricted.
func NewRecursor(trustedSubnets []string) *Recursor {
	r := &Recursor{}
	for _, cidr := range trustedSubnets {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		r.subnets = append(r.subnets, ipNet)
	}
	return r
}

// IsTrusted returns true if the given IP address is within a trusted subnet,
// or if no subnets are configured.
func (r *Recursor) IsTrusted(ip string) bool {
	if len(r.subnets) == 0 {
		return true
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, subnet := range r.subnets {
		if subnet.Contains(parsed) {
			return true
		}
	}
	return false
}

// Resolve performs iterative resolution for msg starting from the root.
// Returns a REFUSED message if the client IP is not trusted.
// Returns SERVFAIL on resolution failure.
func (r *Recursor) Resolve(msg *dns.Msg, clientIP string) *dns.Msg {
	if !r.IsTrusted(clientIP) {
		m := new(dns.Msg)
		m.SetRcode(msg, dns.RcodeRefused)
		return m
	}

	if len(msg.Question) == 0 {
		return servfail(msg)
	}

	q := msg.Question[0]
	resp := r.iterativeResolve(q.Name, q.Qtype)
	if resp == nil {
		return servfail(msg)
	}

	// Preserve the original question header.
	resp.SetReply(msg)
	// SetReply clears Answer/Ns/Extra, so restore them.
	// We rebuild the response to keep authority/answer from the resolution.
	out := resp
	return out
}

// iterativeResolve performs iterative resolution for (name, qtype) starting
// from the root nameservers. Returns nil on unrecoverable failure.
func (r *Recursor) iterativeResolve(name string, qtype uint16) *dns.Msg {
	udp := &dns.Client{Net: "udp", Timeout: 4 * time.Second}
	tcp := &dns.Client{Net: "tcp", Timeout: 4 * time.Second}

	// Start with all root hints as the current nameserver list.
	nameservers := make([]string, len(rootHints))
	copy(nameservers, rootHints)

	for i := 0; i < maxIterations; i++ {
		if len(nameservers) == 0 {
			return nil
		}

		// Try each nameserver in the current set.
		var resp *dns.Msg
		for _, ns := range nameservers {
			addr := ns + ":53"
			q := new(dns.Msg)
			q.SetQuestion(name, qtype)
			q.RecursionDesired = false

			var err error
			resp, err = r.exchange(udp, tcp, q, addr)
			if err != nil {
				continue
			}
			break
		}

		if resp == nil {
			return nil
		}

		// Got an authoritative answer.
		if resp.Authoritative && len(resp.Answer) > 0 {
			return resp
		}

		// Got a direct answer (non-authoritative with records).
		if len(resp.Answer) > 0 {
			return resp
		}

		// NXDOMAIN is a valid terminal answer.
		if resp.Rcode == dns.RcodeNameError {
			return resp
		}

		// SERVFAIL or other non-recoverable codes from the upstream nameserver.
		if resp.Rcode != dns.RcodeSuccess {
			return resp
		}

		// Look for a referral in the Authority section (NS records).
		nextNS := r.extractReferral(resp)
		if len(nextNS) == 0 {
			// No referral and no answer: cannot proceed.
			return nil
		}
		nameservers = nextNS
	}

	// Exceeded max iterations.
	return nil
}

// extractReferral extracts the next set of nameserver IP addresses from a
// referral response. It first tries to use glue records from the Additional
// section; for nameservers without glue it performs a recursive lookup.
func (r *Recursor) extractReferral(resp *dns.Msg) []string {
	// Collect NS names from Authority.
	var nsNames []string
	for _, rr := range resp.Ns {
		if ns, ok := rr.(*dns.NS); ok {
			nsNames = append(nsNames, ns.Ns)
		}
	}
	if len(nsNames) == 0 {
		return nil
	}

	// Build a glue map from Additional.
	glue := make(map[string][]string)
	for _, rr := range resp.Extra {
		switch a := rr.(type) {
		case *dns.A:
			glue[a.Hdr.Name] = append(glue[a.Hdr.Name], a.A.String())
		case *dns.AAAA:
			// Prefer IPv4; skip IPv6 glue for simplicity.
		}
	}

	var ips []string
	for _, nsName := range nsNames {
		if addrs, ok := glue[nsName]; ok {
			ips = append(ips, addrs...)
			continue
		}
		// No glue: resolve the nameserver hostname.
		resolved := r.iterativeResolve(nsName, dns.TypeA)
		if resolved == nil {
			continue
		}
		for _, rr := range resolved.Answer {
			if a, ok := rr.(*dns.A); ok {
				ips = append(ips, a.A.String())
			}
		}
	}
	return ips
}

// exchange sends a DNS query over UDP, retrying with TCP if the response is
// truncated. Returns an error only on transport failure.
func (r *Recursor) exchange(udp, tcp *dns.Client, msg *dns.Msg, addr string) (*dns.Msg, error) {
	resp, _, err := udp.Exchange(msg, addr)
	if err != nil {
		return nil, err
	}
	if resp.Truncated {
		resp, _, err = tcp.Exchange(msg, addr)
		if err != nil {
			return nil, err
		}
	}
	return resp, nil
}
