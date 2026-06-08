package dns

import (
	"time"

	"github.com/skoed/skoed/internal/config"
	"github.com/miekg/dns"
)

// Forwarder sends DNS queries to a list of upstream resolvers with fallback.
type Forwarder struct {
	upstreams []string
	timeout   time.Duration
}

// NewForwarder creates a Forwarder from the DNS config.
func NewForwarder(cfg config.DNSConfig) *Forwarder {
	timeout := time.Duration(cfg.UpstreamTimeout) * time.Second
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &Forwarder{
		upstreams: cfg.UpstreamResolvers,
		timeout:   timeout,
	}
}

// Forward sends msg to the upstream list in order and returns the first
// successful response. A DNS-level error (NXDOMAIN, SERVFAIL, etc.) from an
// upstream is considered successful and is returned directly; fallback only
// happens on network/connection errors or timeouts. Returns a SERVFAIL
// message if all upstreams fail.
func (f *Forwarder) Forward(msg *dns.Msg) *dns.Msg {
	udpClient := &dns.Client{
		Net:     "udp",
		Timeout: f.timeout,
	}
	tcpClient := &dns.Client{
		Net:     "tcp",
		Timeout: f.timeout,
	}

	for _, upstream := range f.upstreams {
		resp, err := f.query(udpClient, tcpClient, msg, upstream)
		if err != nil {
			// Network/timeout error: try next upstream.
			continue
		}
		return resp
	}

	// All upstreams failed: return SERVFAIL.
	return servfail(msg)
}

// query sends msg to a single upstream, retrying over TCP if the UDP response
// is truncated. Returns an error only on network/transport failures.
func (f *Forwarder) query(udp, tcp *dns.Client, msg *dns.Msg, upstream string) (*dns.Msg, error) {
	resp, _, err := udp.Exchange(msg, upstream)
	if err != nil {
		return nil, err
	}
	if resp.Truncated {
		// Retry over TCP.
		resp, _, err = tcp.Exchange(msg, upstream)
		if err != nil {
			return nil, err
		}
	}
	return resp, nil
}

// servfail builds a SERVFAIL response for the given query.
func servfail(req *dns.Msg) *dns.Msg {
	m := new(dns.Msg)
	m.SetRcode(req, dns.RcodeServerFailure)
	return m
}
