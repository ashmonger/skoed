// public.go — M5.9.5 unauthenticated URL tester endpoint.
//
// POST /api/v1/_public/test-blocklist
//
//	body  : {"url":"<http(s)://…>", "format":"hosts|domainlist|adblock|auto"}
//	on 200: {"ok":true,  "count":N, "format":"hosts", "elapsed_ms":2341}
//	on err: {"ok":false, "error":"…"}              with HTTP 4xx/5xx
//
// Posture:
//   - Unauthenticated by design — operators want to sanity-check a blocklist
//     URL BEFORE installing dblock or setting up admin auth.
//   - SSRF-guarded: the URL's host is resolved and rejected when ANY answer
//     falls in RFC1918, loopback, link-local, or unique-local-IPv6 space.
//     A hostname that resolves to 169.254.169.254 (cloud metadata) is also
//     refused — that's an explicit attack target on EC2/GCE/Azure.
//   - Rate-limited: hand-rolled token bucket per source IP. Default budget
//     is 60 requests / hour (one every 60 s with burst 5). Source IP is
//     read from the first X-Forwarded-For entry when present, else
//     r.RemoteAddr. Limiter state is cleaned up lazily (entries older than
//     1 h get garbage-collected on the next request).
//
// Failure modes & HTTP codes:
//
//	400  malformed JSON, missing url, scheme not http/https
//	403  SSRF guard rejected the URL
//	429  rate-limited
//	502  fetch/parse failed (upstream returned non-200 / unreachable)
//	200  ok

package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dblock/dblock/internal/filter"
)

// Public-endpoint tunables. Exported so a future config knob can rewire
// them without touching the handler; the package's tests also poke at
// them via a constructor option.
const (
	defaultFetchTimeout = 30 * time.Second

	// Rate-limit budget: 60 requests/hour ≈ one every 60 s. We allow
	// a small burst so a human clicking the form twice in a second
	// doesn't immediately trip 429.
	rateLimitInterval = time.Minute
	rateLimitBurst    = 5
	rateLimitWindow   = time.Hour
)

// PublicTester is the per-process state for the M5.9.5 public endpoint.
// It owns the rate-limit table; the rest of the request handling is
// stateless. Construct one via NewPublicTester and route to .Handle.
type PublicTester struct {
	resolve func(host string) ([]net.IP, error)
	fetch   func(url, format string, timeout time.Duration) ([]string, error)
	now     func() time.Time

	mu      sync.Mutex
	buckets map[string]*ipBucket
}

// ipBucket is one rate-limit bucket. Tokens refill at rateLimitInterval;
// max is rateLimitBurst. lastTouched drives GC.
type ipBucket struct {
	tokens      float64
	lastRefill  time.Time
	lastTouched time.Time
}

// NewPublicTester returns a tester with stdlib-backed resolver, the
// real filter.Download for fetching, and time.Now for clock.
func NewPublicTester() *PublicTester {
	return &PublicTester{
		resolve: defaultResolve,
		fetch:   filter.Download,
		now:     time.Now,
		buckets: make(map[string]*ipBucket),
	}
}

// Handle is the http.HandlerFunc entry-point.
func (p *PublicTester) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"ok":    false,
			"error": "method not allowed",
		})
		return
	}

	// Rate-limit by source IP.
	srcIP := sourceIP(r)
	if !p.allow(srcIP) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"ok":    false,
			"error": "rate limit exceeded — try again in a minute",
		})
		return
	}

	// Decode body.
	var body struct {
		URL    string `json:"url"`
		Format string `json:"format"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": "invalid JSON body",
		})
		return
	}
	if body.URL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": "url is required",
		})
		return
	}
	if body.Format == "" {
		body.Format = "auto"
	}

	// SSRF: parse, scheme-check, resolve, ip-filter.
	if err := p.ssrfCheck(body.URL); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	// Fetch + parse via the same path the CLI subcommand uses.
	start := p.now()
	domains, err := p.fetch(body.URL, body.Format, defaultFetchTimeout)
	elapsed := p.now().Sub(start)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"ok":         false,
			"error":      err.Error(),
			"elapsed_ms": elapsed.Milliseconds(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"count":      len(domains),
		"format":     body.Format,
		"elapsed_ms": elapsed.Milliseconds(),
	})
}

// testOnlyAllowPrivate is a test-only escape hatch. When the env var
// DBLOCK_PUBLIC_TESTER_ALLOW_PRIVATE=1 is set, the SSRF guard accepts
// RFC1918 / loopback / link-local addresses. The acceptance suite
// uses this to point the daemon at httptest.NewServer (which always
// binds 127.0.0.1) without needing a public LAN IP. Production
// operators MUST NOT set this. Documented as test-only.
var testOnlyAllowPrivate = os.Getenv("DBLOCK_PUBLIC_TESTER_ALLOW_PRIVATE") == "1"

// ssrfCheck validates rawURL is http(s) and that every IP its host
// resolves to is publicly routable. The first private/loopback/link-local
// address triggers a refusal even if other answers are public — an
// attacker can prepend a public IP to the response and the rest of the
// list could be private. (Belt-and-braces: refuse the URL outright.)
func (p *PublicTester) ssrfCheck(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("malformed URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("URL must use http or https scheme")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("URL must include a host")
	}

	// Literal IP in the URL — check directly without DNS.
	if ip := net.ParseIP(host); ip != nil {
		if !testOnlyAllowPrivate && isUnsafeIP(ip) {
			return fmt.Errorf("refusing private/loopback/link-local address %s", ip)
		}
		return nil
	}

	// Hostname — resolve and check every answer.
	ips, err := p.resolve(host)
	if err != nil {
		return fmt.Errorf("could not resolve %s: %v", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("no addresses for %s", host)
	}
	for _, ip := range ips {
		if !testOnlyAllowPrivate && isUnsafeIP(ip) {
			return fmt.Errorf("refusing private/loopback/link-local address %s (resolved from %s)", ip, host)
		}
	}
	return nil
}

// isUnsafeIP reports whether ip falls into any of:
//   - RFC1918 (10/8, 172.16/12, 192.168/16)
//   - loopback (127/8, ::1)
//   - link-local (169.254/16, fe80::/10) — also catches cloud metadata
//   - unspecified (0.0.0.0, ::)
//   - IPv6 unique-local (fc00::/7)
//   - multicast (224/4, ff00::/8)
func isUnsafeIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	// IPv6 unique-local (fc00::/7). net.IP.IsPrivate() covers fc00::/7
	// since Go 1.17 — guard against pre-1.17 by checking the prefix too.
	if v6 := ip.To16(); v6 != nil && len(ip.To4()) == 0 {
		if v6[0]&0xfe == 0xfc {
			return true
		}
	}
	return false
}

// sourceIP returns the IP that should be billed for this request's
// rate-limit budget. Behind a reverse proxy that sets X-Forwarded-For
// the first entry is the real client; otherwise we fall back to
// r.RemoteAddr (host:port → host).
func sourceIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// allow consumes one token from the per-IP bucket. Returns true if the
// bucket had budget. The bucket refills 1 token per rateLimitInterval
// (≈1 / minute) up to rateLimitBurst. Buckets older than rateLimitWindow
// are garbage-collected lazily on each call.
func (p *PublicTester) allow(ip string) bool {
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()

	// Lazy GC: drop buckets that haven't been touched in over a window.
	for k, b := range p.buckets {
		if now.Sub(b.lastTouched) > rateLimitWindow {
			delete(p.buckets, k)
		}
	}

	b, ok := p.buckets[ip]
	if !ok {
		b = &ipBucket{tokens: float64(rateLimitBurst), lastRefill: now, lastTouched: now}
		p.buckets[ip] = b
	}

	// Refill.
	elapsed := now.Sub(b.lastRefill)
	if elapsed > 0 {
		add := elapsed.Seconds() / rateLimitInterval.Seconds()
		b.tokens += add
		if b.tokens > float64(rateLimitBurst) {
			b.tokens = float64(rateLimitBurst)
		}
		b.lastRefill = now
	}
	b.lastTouched = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// defaultResolve uses net.LookupIP with the default resolver. Tests
// may swap this out via a tester constructed by hand.
func defaultResolve(host string) ([]net.IP, error) {
	return net.LookupIP(host)
}
