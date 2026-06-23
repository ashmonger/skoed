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
	fns     []upstreamFn
	timeout time.Duration
}

// NewForwarder creates a Forwarder from the DNS config.
// Each entry in cfg.UpstreamResolvers is parsed and dispatched to the
// appropriate transport: plain UDP+TCP, DNS-over-TLS, or DNS-over-HTTPS.
func NewForwarder(cfg config.DNSConfig) *Forwarder {
	timeout := time.Duration(cfg.UpstreamTimeout) * time.Second
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	fns := make([]upstreamFn, 0, len(cfg.UpstreamResolvers))
	for _, u := range cfg.UpstreamResolvers {
		switch {
		case strings.HasPrefix(u, "tls://"):
			fns = append(fns, makeDotFn(u, timeout))
		case strings.HasPrefix(u, "https://"):
			fns = append(fns, makeDohFn(u, timeout))
		default:
			fns = append(fns, makePlainFn(u, timeout))
		}
	}
	return &Forwarder{fns: fns, timeout: timeout}
}

// Forward sends msg to the upstream list in order and returns the first
// successful response. Falls back on transport/connection errors. Returns
// SERVFAIL when all upstreams fail.
func (f *Forwarder) Forward(msg *dns.Msg) *dns.Msg {
	for _, fn := range f.fns {
		resp, err := fn(msg)
		if err != nil {
			continue
		}
		return resp
	}
	return servfail(msg)
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
