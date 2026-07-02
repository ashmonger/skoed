package dns

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/miekg/dns"
	"github.com/skoed/skoed/internal/config"
)

// upstreamFn sends one DNS message to a specific upstream and returns the response.
// Returns a non-nil error only on transport/connection failures; DNS-level errors
// (NXDOMAIN, SERVFAIL from the upstream) are returned as valid responses.
type upstreamFn func(*dns.Msg) (*dns.Msg, error)

// Forwarder sends DNS queries to a list of upstream resolvers with fallback.
type Forwarder struct {
	fns      []upstreamFn
	urls     []string // resolved URL/addr for each fn — used by the optional observer
	timeout  time.Duration
	observer func(upstream string, dur time.Duration) // M39: per-upstream latency hook
}

// NewForwarder creates a Forwarder from the DNS config.
// Each entry in cfg.UpstreamResolvers is parsed and dispatched to the
// appropriate transport: plain UDP+TCP, DNS-over-TLS, or DNS-over-HTTPS.
func NewForwarder(cfg config.DNSConfig) *Forwarder {
	timeout := time.Duration(cfg.UpstreamTimeout) * time.Second
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return newForwarderFromResolvers(cfg.UpstreamResolvers, timeout)
}

func newForwarderFromResolvers(resolvers []string, timeout time.Duration) *Forwarder {
	fns := make([]upstreamFn, 0, len(resolvers))
	urls := make([]string, 0, len(resolvers))
	for _, u := range resolvers {
		urls = append(urls, upstreamLabel(u))
		switch {
		case strings.HasPrefix(u, "tls://"):
			fns = append(fns, makeDotFn(u, timeout))
		case strings.HasPrefix(u, "https://"):
			fns = append(fns, makeDohFn(u, timeout))
		default:
			fns = append(fns, makePlainFn(u, timeout))
		}
	}
	return &Forwarder{fns: fns, urls: urls, timeout: timeout}
}

// upstreamLabel returns scheme+host from a resolver URL, stripping credentials,
// paths, and query params so Prometheus labels stay low-cardinality.
func upstreamLabel(raw string) string {
	// Plain host:port — return as-is.
	if !strings.Contains(raw, "://") {
		h, _, err := net.SplitHostPort(raw)
		if err != nil {
			return raw
		}
		return h
	}
	// tls:// or https:// — keep scheme+host only.
	rest := raw
	scheme := ""
	if i := strings.Index(rest, "://"); i >= 0 {
		scheme = rest[:i+3]
		rest = rest[i+3:]
	}
	// strip path/query
	if i := strings.IndexAny(rest, "/?@"); i >= 0 {
		// if @ present it's credentials — take the part after
		if at := strings.IndexByte(rest, '@'); at >= 0 && at < i {
			rest = rest[at+1:]
		} else {
			rest = rest[:i]
		}
	}
	return scheme + rest
}

// WithObserver returns a shallow copy of f with the given per-upstream latency
// observer set. The observer is called after every upstream attempt (success
// or failure) with the upstream label and the round-trip duration.
func (f *Forwarder) WithObserver(obs func(upstream string, dur time.Duration)) *Forwarder {
	c := *f
	c.observer = obs
	return &c
}

// Forward sends msg to the upstream list in order and returns the first
// successful response. Falls back on transport/connection errors. Returns
// SERVFAIL when all upstreams fail.
func (f *Forwarder) Forward(msg *dns.Msg) *dns.Msg {
	for i, fn := range f.fns {
		start := time.Now()
		resp, err := fn(msg)
		if f.observer != nil {
			label := ""
			if i < len(f.urls) {
				label = f.urls[i]
			}
			f.observer(label, time.Since(start))
		}
		if err != nil {
			continue
		}
		return resp
	}
	return servfail(msg)
}

// ForwardWithRoutes checks routes top-down before the global upstream list.
// The first route whose match covers the query domain is used; if none match,
// the global upstream list is used.
func (f *Forwarder) ForwardWithRoutes(msg *dns.Msg, routes []config.UpstreamRoute) *dns.Msg {
	if len(routes) == 0 || len(msg.Question) == 0 {
		return f.Forward(msg)
	}
	domain := strings.TrimSuffix(strings.ToLower(msg.Question[0].Name), ".")
	for _, r := range routes {
		if matchRoute(domain, r.Match) {
			rf := newForwarderFromResolvers(r.Resolvers, f.timeout)
			rf.observer = f.observer
			return rf.Forward(msg)
		}
	}
	return f.Forward(msg)
}

// matchRoute reports whether domain is covered by the match pattern.
// Patterns:
//   - "*.suffix"  → domain ends with ".suffix" (any depth)
//   - "exact"     → domain == exact (no subdomains)
func matchRoute(domain, pattern string) bool {
	pattern = strings.ToLower(strings.TrimSuffix(pattern, "."))
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".suffix"
		return strings.HasSuffix(domain, suffix)
	}
	return domain == pattern
}

// makePlainFn creates an upstreamFn for a plain UDP upstream (TCP fallback on truncation).
func makePlainFn(addr string, timeout time.Duration) upstreamFn {
	udp := &dns.Client{Net: "udp", Timeout: timeout}
	tcp := &dns.Client{Net: "tcp", Timeout: timeout}
	return func(msg *dns.Msg) (*dns.Msg, error) {
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
}

// makeDotFn creates an upstreamFn for a DNS-over-TLS upstream.
// rawURL has the form "tls://host:port[?skip_verify=true]".
func makeDotFn(rawURL string, timeout time.Duration) upstreamFn {
	rest := rawURL[len("tls://"):]
	hostPart := rest
	queryPart := ""
	if q := strings.IndexByte(rest, '?'); q >= 0 {
		hostPart = rest[:q]
		queryPart = rest[q+1:]
	}
	skipVerify := queryParamTrue(queryPart, "skip_verify")
	host, _, _ := net.SplitHostPort(hostPart)

	client := &dns.Client{
		Net:     "tcp-tls",
		Timeout: timeout,
		TLSConfig: &tls.Config{ //nolint:gosec — skip_verify is operator-controlled
			ServerName:         host,
			InsecureSkipVerify: skipVerify,
		},
	}
	return func(msg *dns.Msg) (*dns.Msg, error) {
		resp, _, err := client.Exchange(msg, hostPart)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
}

// makeDohFn creates an upstreamFn for a DNS-over-HTTPS upstream (RFC 8484).
// rawURL has the form "https://host/path[?skip_verify=true][&method=get][&other=...]".
// skip_verify and method are consumed; remaining query params are forwarded.
func makeDohFn(rawURL string, timeout time.Duration) upstreamFn {
	base := rawURL
	query := ""
	if i := strings.Index(rawURL, "?"); i >= 0 {
		base = rawURL[:i]
		query = rawURL[i+1:]
	}

	skipVerify := queryParamTrue(query, "skip_verify")
	useGet := queryParamValue(query, "method") == "get"

	// Rebuild the base URL without skip_verify / method params.
	remaining := rebuildQuery(query, "skip_verify", "method")
	dohURL := base
	if remaining != "" {
		dohURL = base + "?" + remaining
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: skipVerify}, //nolint:gosec
	}
	client := &http.Client{Transport: transport, Timeout: timeout}

	return func(msg *dns.Msg) (*dns.Msg, error) {
		wire, err := msg.Pack()
		if err != nil {
			return nil, err
		}

		var req *http.Request
		if useGet {
			encoded := base64.RawURLEncoding.EncodeToString(wire)
			sep := "?"
			if strings.Contains(dohURL, "?") {
				sep = "&"
			}
			req, err = http.NewRequest(http.MethodGet, dohURL+sep+"dns="+encoded, nil)
			if err != nil {
				return nil, err
			}
			req.Header.Set("Accept", "application/dns-message")
		} else {
			req, err = http.NewRequest(http.MethodPost, dohURL, bytes.NewReader(wire))
			if err != nil {
				return nil, err
			}
			req.Header.Set("Content-Type", "application/dns-message")
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("DoH upstream returned HTTP %d", resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		reply := new(dns.Msg)
		if err := reply.Unpack(body); err != nil {
			return nil, fmt.Errorf("DoH upstream returned invalid DNS wire: %w", err)
		}
		return reply, nil
	}
}

// queryParamTrue reports whether key=true appears in a URL query string.
// Only handles simple key=value pairs; does not use net/url to avoid an import.
func queryParamTrue(query, key string) bool {
	return queryParamValue(query, key) == "true"
}

// queryParamValue returns the first value for key in query, or "".
func queryParamValue(query, key string) string {
	for _, part := range strings.Split(query, "&") {
		k, v, _ := strings.Cut(part, "=")
		if k == key {
			return v
		}
	}
	return ""
}

// rebuildQuery rebuilds a query string omitting the specified keys.
func rebuildQuery(query string, omit ...string) string {
	omitSet := make(map[string]bool, len(omit))
	for _, k := range omit {
		omitSet[k] = true
	}
	var parts []string
	for _, part := range strings.Split(query, "&") {
		if part == "" {
			continue
		}
		k, _, _ := strings.Cut(part, "=")
		if !omitSet[k] {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "&")
}

// servfail builds a SERVFAIL response for the given query.
func servfail(req *dns.Msg) *dns.Msg {
	m := new(dns.Msg)
	m.SetRcode(req, dns.RcodeServerFailure)
	return m
}
