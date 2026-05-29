package dns

import (
	"net"
	"strings"
	"time"

	"github.com/dblock/dblock/internal/config"
	"github.com/dblock/dblock/internal/filter"
	dlog "github.com/dblock/dblock/internal/log"
	"github.com/miekg/dns"
)

// HandlerConfig bundles all dependencies for the DNS query pipeline.
type HandlerConfig struct {
	DNSCfg       config.DNSConfig
	FilterEngine func() *filter.Engine // called on each query to get the current engine
	LocalResolver *LocalResolver
	Forwarder     *Forwarder // nil when mode=recursive
	Recursor      *Recursor  // nil when mode=forwarding
	Cache         *Cache
	QueryLog      *dlog.QueryLog
}

// Handler implements dns.Handler and processes all incoming DNS queries.
type Handler struct {
	cfg config.DNSConfig
	fe  func() *filter.Engine
	lr  *LocalResolver
	fwd *Forwarder
	rec *Recursor
	ch  *Cache
	ql  *dlog.QueryLog
}

// NewHandler constructs a Handler from the provided configuration.
func NewHandler(cfg HandlerConfig) *Handler {
	return &Handler{
		cfg: cfg.DNSCfg,
		fe:  cfg.FilterEngine,
		lr:  cfg.LocalResolver,
		fwd: cfg.Forwarder,
		rec: cfg.Recursor,
		ch:  cfg.Cache,
		ql:  cfg.QueryLog,
	}
}

// ServeDNS implements dns.Handler.
func (h *Handler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	clientIP := extractIP(w.RemoteAddr())

	if len(r.Question) == 0 {
		m := new(dns.Msg)
		m.SetRcode(r, dns.RcodeFormatError)
		_ = w.WriteMsg(m)
		return
	}

	// Process the first question (standard DNS behaviour).
	q := r.Question[0]
	name := strings.ToLower(strings.TrimSuffix(q.Name, "."))
	qtype := q.Qtype
	qtypeStr := dns.TypeToString[qtype]
	if qtypeStr == "" {
		qtypeStr = "UNKNOWN"
	}

	// Step (b): check local entries.
	if h.lr != nil {
		if msg, ok := h.lr.Resolve(q.Name, qtype); ok {
			msg.SetReply(r)
			_ = w.WriteMsg(msg)
			h.logEntry(clientIP, name, qtypeStr, dlog.OutcomeLocal, "")
			return
		}
	}

	// Steps (c)/(d): filter engine evaluation.
	fe := h.fe()
	result := fe.Evaluate(name)
	if result.Disposition == filter.Block {
		policy := fe.EffectivePolicy(result)
		resp := h.buildBlockResponse(r, q, policy)
		_ = w.WriteMsg(resp)
		h.logEntry(clientIP, name, qtypeStr, dlog.OutcomeBlocked, result.BlocklistID)
		return
	}
	// result.Disposition == filter.Allow: proceed.

	// Step (e): cache lookup.
	key := cacheKey{Name: q.Name, Qtype: qtype}
	if h.ch != nil && h.cfg.Cache.Enabled {
		if cached, ok := h.ch.get(key); ok {
			cached.SetReply(r)
			_ = w.WriteMsg(cached)
			h.logEntry(clientIP, name, qtypeStr, dlog.OutcomeCached, "")
			return
		}
	}

	// Step (f): resolve.
	var resolved *dns.Msg
	if h.cfg.Mode == "recursive" && h.rec != nil {
		resolved = h.rec.Resolve(r, clientIP)
	} else if h.fwd != nil {
		resolved = h.fwd.Forward(r)
	} else {
		resolved = servfail(r)
	}

	if resolved == nil {
		resolved = servfail(r)
	}

	// Step (g): cache the response.
	if h.ch != nil && h.cfg.Cache.Enabled {
		h.ch.set(key, resolved)
	}

	// Step (h)/(i): log and return.
	// Preserve the upstream Rcode — SetReply resets it to NOERROR.
	rcode := resolved.Rcode
	resolved.SetReply(r)
	resolved.Rcode = rcode
	_ = w.WriteMsg(resolved)
	h.logEntry(clientIP, name, qtypeStr, dlog.OutcomeForwarded, "")
}

// buildBlockResponse constructs the appropriate DNS response for a blocked query.
func (h *Handler) buildBlockResponse(r *dns.Msg, q dns.Question, policy filter.BlockPolicy) *dns.Msg {
	m := new(dns.Msg)
	m.SetReply(r)
	m.RecursionAvailable = true

	switch policy {
	case filter.PolicyNULL:
		switch q.Qtype {
		case dns.TypeA:
			m.Rcode = dns.RcodeSuccess
			m.Answer = []dns.RR{
				&dns.A{
					Hdr: dns.RR_Header{
						Name:   q.Name,
						Rrtype: dns.TypeA,
						Class:  dns.ClassINET,
						Ttl:    0,
					},
					A: net.IPv4zero,
				},
			}
		case dns.TypeAAAA:
			m.Rcode = dns.RcodeSuccess
			m.Answer = []dns.RR{
				&dns.AAAA{
					Hdr: dns.RR_Header{
						Name:   q.Name,
						Rrtype: dns.TypeAAAA,
						Class:  dns.ClassINET,
						Ttl:    0,
					},
					AAAA: net.IPv6unspecified,
				},
			}
		default:
			m.Rcode = dns.RcodeNameError
		}

	case filter.PolicyNODATA:
		m.Rcode = dns.RcodeSuccess
		// Empty answer section.

	default:
		// PolicyNXDOMAIN and PolicyInherit (shouldn't reach here with Inherit,
		// but default to NXDOMAIN to be safe).
		m.Rcode = dns.RcodeNameError
	}

	return m
}

// logEntry appends an entry to the query log if one is configured.
func (h *Handler) logEntry(clientIP, domain, qtype string, outcome dlog.Outcome, blocklistID string) {
	if h.ql == nil {
		return
	}
	h.ql.Append(dlog.Entry{
		Client:      clientIP,
		Domain:      domain,
		QueryType:   qtype,
		Timestamp:   time.Now(),
		Outcome:     outcome,
		BlocklistID: blocklistID,
	})
}

// extractIP returns the host part of a net.Addr string (strips the port).
func extractIP(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	s := addr.String()
	host, _, err := net.SplitHostPort(s)
	if err != nil {
		return s
	}
	return host
}
