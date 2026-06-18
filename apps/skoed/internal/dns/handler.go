package dns

import (
	"net"
	"os"
	"strings"
	"time"

	"github.com/skoed/skoed/internal/config"
	"github.com/skoed/skoed/internal/filter"
	"github.com/skoed/skoed/internal/filter/categories"
	dlog "github.com/skoed/skoed/internal/log"
	"github.com/miekg/dns"
)

// HandlerConfig bundles all dependencies for the DNS query pipeline.
type HandlerConfig struct {
	DNSCfg        config.DNSConfig
	FilterEngine  func() *filter.Engine // called on each query to get the current engine
	LocalResolver *LocalResolver
	Forwarder     *Forwarder // nil when mode=recursive
	Recursor      *Recursor  // nil when mode=forwarding
	Cache         *Cache
	QueryLog      *dlog.QueryLog
	// M3.6: optional DHCP lookup. When DhcpLookup is non-nil, the handler
	// resolves hostname / MAC / Client-ID for each query's client IP and
	// stamps them onto the query-log entry. Signature mirrors
	// (*dhcp.Manager).LookupByIP without dragging in the dhcp package.
	DhcpLookup func(ip string) (hostname, mac, clientID string, ok bool)
	// M5.1: optional Prometheus observation hook. When non-nil, called
	// once per query with the final outcome label (may carry the
	// "-doh"/"-dot" transport suffix) and wall-clock elapsed time. nil
	// disables metrics observation cleanly — useful for unit tests.
	ObserveQuery func(outcome string, elapsed time.Duration)
	// M22: optional device-new notification hook. When non-nil, called for
	// every query whose client IP is not recognised by the DHCP lookup.
	// The dispatcher deduplicates within a 10-minute window.
	OnDeviceNew func(clientIP string)
}

// Handler implements dns.Handler and processes all incoming DNS queries.
type Handler struct {
	cfg         config.DNSConfig
	fe          func() *filter.Engine
	lr          *LocalResolver
	fwd         *Forwarder
	rec         *Recursor
	ch          *Cache
	ql          *dlog.QueryLog
	dhcpFn      func(ip string) (hostname, mac, clientID string, ok bool)
	observe     func(outcome string, elapsed time.Duration)
	onDeviceNew func(clientIP string)
}

// NewHandler constructs a Handler from the provided configuration.
func NewHandler(cfg HandlerConfig) *Handler {
	return &Handler{
		cfg:         cfg.DNSCfg,
		fe:          cfg.FilterEngine,
		lr:          cfg.LocalResolver,
		fwd:         cfg.Forwarder,
		rec:         cfg.Recursor,
		ch:          cfg.Cache,
		ql:          cfg.QueryLog,
		dhcpFn:      cfg.DhcpLookup,
		observe:     cfg.ObserveQuery,
		onDeviceNew: cfg.OnDeviceNew,
	}
}

// ServeDNS implements dns.Handler.
func (h *Handler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	start := time.Now()
	clientIPStr, clientIP := h.resolveClient(w, r)

	// M22: fire on every query; dispatcher deduplicates within 10 minutes.
	if h.onDeviceNew != nil {
		go h.onDeviceNew(clientIPStr)
	}

	// M4: when a query comes in over DoH/DoT, suffix every query-log
	// outcome with -doh / -dot so analytics can split by transport.
	transport := transportFromWriter(w)

	// M5.1: every exit path tags itself for the metrics observer via
	// observed. The query-log "Append" hook is the only place outcome
	// is already known at every exit, so tee through it.
	var observed dlog.Outcome
	defer func() {
		if h.observe != nil && observed != "" {
			h.observe(string(observed), time.Since(start))
		}
	}()

	applyT := func(o dlog.Outcome) dlog.Outcome {
		if transport == "" {
			observed = o
			return o
		}
		out := dlog.Outcome(string(o) + "-" + transport)
		observed = out
		return out
	}

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

	// M3 interceptions BEFORE any filtering. These three special cases
	// short-circuit the regular pipeline. Firefox canary is non-overridable
	// by design — the entire point is to be the operator's signal that
	// network-wide DNS filtering is in effect.
	if name == categories.FirefoxCanary {
		_ = w.WriteMsg(nxdomain(r))
		h.logCategorised(clientIPStr, name, qtypeStr, applyT(dlog.OutcomeBlocked), "", "doh-canary", "", false)
		return
	}
	if name == categories.DDRProbeDomain {
		_ = w.WriteMsg(noData(r))
		h.logCategorised(clientIPStr, name, qtypeStr, applyT(dlog.OutcomeBlocked), "", "ddr-probe", "", false)
		return
	}

	// Local DNS entries take priority over both the filter and upstream resolution.
	if h.lr != nil {
		if msg, ok := h.lr.Resolve(q.Name, qtype); ok {
			msg.SetReply(r)
			_ = w.WriteMsg(msg)
			h.logEntry(clientIPStr, name, qtypeStr, applyT(dlog.OutcomeLocal), "")
			return
		}
	}

	// SafeSearch — profile-aware. If the active profile(s) enable
	// SafeSearch for a matching provider, rewrite via CNAME before any
	// blocklist evaluation.
	fe := h.fe()
	// M3.6: derive optional Client-ID / MAC / hostname from the DHCP
	// lease cache so profile matching can use stable identity instead
	// of fragile IPs.
	var ident filter.ClientIdentity
	if h.dhcpFn != nil {
		if host, mac, cid, ok := h.dhcpFn(clientIPStr); ok {
			ident.ClientID = cid
			ident.MAC = mac
			ident.Hostname = host
		}
	}
	if target, ok := filter.SafeSearchRewrite(name, fe.SafeSearchProvidersForClientID(clientIP, ident)); ok && (qtype == dns.TypeA || qtype == dns.TypeAAAA) {
		resp := h.buildSafeSearchResponse(r, q, target)
		_ = w.WriteMsg(resp)
		h.logEntry(clientIPStr, name, qtypeStr, applyT(dlog.OutcomeLocal), "")
		return
	}

	// Block if the domain matches any active blocklist for this client's
	// matched profile(s). Per-client evaluation falls back to the global
	// blocklists when no Profile object exists yet.
	result := fe.EvaluateForClientID(name, clientIP, ident, filter.Now())
	if result.Disposition == filter.Block {
		policy := fe.EffectivePolicy(result)
		resp := h.buildBlockResponse(r, q, policy)
		_ = w.WriteMsg(resp)
		// Tag DoH-probe entries when the matched blocklist is the bundled
		// DoH category.
		cat := ""
		if result.BlocklistID == categories.BlocklistID("doh") {
			cat = "doh-probe"
		}
		h.logCategorised(clientIPStr, name, qtypeStr, applyT(dlog.OutcomeBlocked), result.BlocklistID, cat, "", false)
		return
	}

	// Serve from cache before hitting upstream.
	key := cacheKey{Name: q.Name, Qtype: qtype}
	if h.ch != nil && h.cfg.Cache.Enabled {
		if cached, ok := h.ch.get(key); ok {
			cached.SetReply(r)
			_ = w.WriteMsg(cached)
			h.logEntry(clientIPStr, name, qtypeStr, applyT(dlog.OutcomeCached), "")
			return
		}
	}

	// M21: in validate mode, set the DO bit so upstream returns DNSSEC records.
	req := r
	if h.cfg.DNSSECMode == "validate" {
		req = r.Copy()
		req.SetEdns0(4096, true)
	}

	// Resolve via forwarder or recursor.
	var resolved *dns.Msg
	if h.cfg.Mode == "recursive" && h.rec != nil {
		resolved = h.rec.Resolve(req, clientIPStr)
	} else if h.fwd != nil {
		resolved = h.fwd.Forward(req)
	} else {
		resolved = servfail(r)
	}

	if resolved == nil {
		resolved = servfail(r)
	}

	// M21: classify DNSSEC status and replace BOGUS responses with SERVFAIL.
	dnssecStatus := ""
	if h.cfg.DNSSECMode == "validate" {
		dnssecStatus, resolved = classifyDNSSEC(r, resolved)
	}

	if h.ch != nil && h.cfg.Cache.Enabled {
		h.ch.set(key, resolved)
	}

	// Preserve the upstream Rcode — SetReply resets it to NOERROR.
	rcode := resolved.Rcode
	resolved.SetReply(r)
	resolved.Rcode = rcode
	_ = w.WriteMsg(resolved)
	h.logForwarded(clientIPStr, name, qtypeStr, applyT(dlog.OutcomeForwarded), result.PauseActive, dnssecStatus)
}

// classifyDNSSEC inspects the upstream response and returns a DNSSEC status
// string and potentially a replacement response (SERVFAIL for bogus).
//
// AD=1 → "ok" (upstream validated the chain).
// AD=0 + RRSIG in answer → "bogus" (signed but not authenticated) → SERVFAIL.
// AD=0, no RRSIG → "insecure" (unsigned domain) → pass through.
// Upstream SERVFAIL → "bogus" (treat as validation failure) → pass through.
func classifyDNSSEC(req, resp *dns.Msg) (status string, out *dns.Msg) {
	if resp.Rcode == dns.RcodeServerFailure {
		return "bogus", resp
	}
	if resp.AuthenticatedData {
		return "ok", resp
	}
	for _, rr := range resp.Answer {
		if rr.Header().Rrtype == dns.TypeRRSIG {
			sf := new(dns.Msg)
			sf.SetRcode(req, dns.RcodeServerFailure)
			return "bogus", sf
		}
	}
	return "insecure", resp
}

// resolveClient returns the client's string IP and parsed net.IP. When
// SKOED_TEST_MODE=1 and the query carries an EDNS0 LOCAL option with
// code 65500, that option's data overrides the source IP — used by
// acceptance tests that can't actually spoof UDP source addresses.
func (h *Handler) resolveClient(w dns.ResponseWriter, r *dns.Msg) (string, net.IP) {
	clientIPStr := extractIP(w.RemoteAddr())
	clientIP := net.ParseIP(clientIPStr)
	if os.Getenv("SKOED_TEST_MODE") == "1" {
		if opt := r.IsEdns0(); opt != nil {
			for _, o := range opt.Option {
				if loc, ok := o.(*dns.EDNS0_LOCAL); ok && loc.Code == 65500 {
					if ip := net.IP(loc.Data); ip != nil {
						clientIP = ip
						clientIPStr = ip.String()
					}
				}
			}
		}
	}
	return clientIPStr, clientIP
}

// buildSafeSearchResponse emits a CNAME pointing the queried name at the
// provider's SafeSearch endpoint. The handler doesn't follow the CNAME
// itself — clients will issue a separate query for the target, which we
// forward upstream normally.
func (h *Handler) buildSafeSearchResponse(r *dns.Msg, q dns.Question, target string) *dns.Msg {
	m := new(dns.Msg)
	m.SetReply(r)
	m.RecursionAvailable = true
	m.Answer = append(m.Answer, &dns.CNAME{
		Hdr:    dns.RR_Header{Name: q.Name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 300},
		Target: dns.Fqdn(target),
	})
	return m
}

// nxdomain returns an NXDOMAIN reply for r. Used by the Firefox canary
// short-circuit and by the noop block-policy.
func nxdomain(r *dns.Msg) *dns.Msg {
	m := new(dns.Msg)
	m.SetReply(r)
	m.Rcode = dns.RcodeNameError
	m.RecursionAvailable = true
	return m
}

// noData returns NOERROR with an empty answer section — the canonical
// shape for "this name exists but has no records of the requested type".
// Used for RFC 9462 DDR probes.
func noData(r *dns.Msg) *dns.Msg {
	m := new(dns.Msg)
	m.SetReply(r)
	m.Rcode = dns.RcodeSuccess
	m.RecursionAvailable = true
	return m
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
	h.logCategorised(clientIP, domain, qtype, outcome, blocklistID, "", "", false)
}

// logCategorised is the M3 form that also carries a category tag (e.g.
// "doh-probe") and a profile id (best-effort). M3.6 adds optional
// hostname / MAC / Client-ID enrichment from the DHCP lease cache.
// M13 adds pauseActive to mark queries forwarded during a filtering pause.
func (h *Handler) logCategorised(clientIP, domain, qtype string, outcome dlog.Outcome, blocklistID, category, profileID string, pauseActive bool) {
	if h.ql == nil {
		return
	}
	e := dlog.Entry{
		Client:      clientIP,
		Domain:      domain,
		QueryType:   qtype,
		Timestamp:   time.Now(),
		Outcome:     outcome,
		BlocklistID: blocklistID,
		Category:    category,
		ProfileID:   profileID,
		PauseActive: pauseActive,
	}
	if h.dhcpFn != nil {
		if host, mac, cid, ok := h.dhcpFn(clientIP); ok {
			e.ClientHostname = host
			e.ClientMAC = mac
			e.ClientID = cid
		}
	}
	h.ql.Append(e)
}

// logForwarded is the M21 forwarded-query logger that also stamps DNSSEC status.
func (h *Handler) logForwarded(clientIP, domain, qtype string, outcome dlog.Outcome, pauseActive bool, dnssecStatus string) {
	if h.ql == nil {
		return
	}
	e := dlog.Entry{
		Client:       clientIP,
		Domain:       domain,
		QueryType:    qtype,
		Timestamp:    time.Now(),
		Outcome:      outcome,
		PauseActive:  pauseActive,
		DnssecStatus: dnssecStatus,
	}
	if h.dhcpFn != nil {
		if host, mac, cid, ok := h.dhcpFn(clientIP); ok {
			e.ClientHostname = host
			e.ClientMAC = mac
			e.ClientID = cid
		}
	}
	h.ql.Append(e)
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
