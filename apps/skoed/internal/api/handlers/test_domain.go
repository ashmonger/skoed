// test_domain.go — M5.9.7 "would this domain be blocked?" tester.
//
// Two surfaces, one evaluator:
//
//   POST /api/v1/_public/test-domain   (no auth, default profile only)
//   POST /api/v1/test-domain           (auth, full chain)
//
// Both delegate to the same filter.Engine.EvaluateForClientID call the
// DNS handler uses, so test verdicts can never drift from real-query
// behaviour.
//
// Posture mirrors the M5.9.5 URL tester:
//   - Guest endpoint gated by node.api.public_landing.enabled.
//   - Guest endpoint reuses the M5.9.5 per-IP token bucket so the
//     combined budget across all public test endpoints is 60/h.
//   - No SSRF surface: the domain is a string key into the in-memory
//     filter engine, nothing is fetched.
//   - Audit-exempt: the test routes are read-only, no state change.
//   - Metric cardinality: 4 series total (2 surfaces × 2 verdicts).
//     Domain is NEVER a label.

package handlers

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/skoed/skoed/internal/config"
	"github.com/skoed/skoed/internal/filter"
)

// testDomainReq is the wire body for both surfaces.
type testDomainReq struct {
	Domain    string `json:"domain"`
	ClientIP  string `json:"client_ip,omitempty"`
	ProfileID string `json:"profile_id,omitempty"`
}

// testDomainResp is the full authenticated response. The guest
// endpoint marshals a stripped-down version with only WouldBlock +
// Reason.
type testDomainResp struct {
	Domain             string `json:"domain,omitempty"`
	ClientIP           string `json:"client_ip,omitempty"`
	WouldBlock         bool   `json:"would_block"`
	Reason             string `json:"reason"`
	MatchedProfileID   string `json:"matched_profile_id,omitempty"`
	MatchedBlocklistID string `json:"matched_blocklist_id,omitempty"`
	BlockPolicy        string `json:"block_policy,omitempty"`
	LocalDNSAnswer     string `json:"local_dns_answer,omitempty"`
	SafeSearchRewrite  string `json:"safesearch_rewrite,omitempty"`
	EvaluatedAt        string `json:"evaluated_at,omitempty"`
	Error              string `json:"error,omitempty"`
}

// reserved suffixes the endpoints refuse — these never resolve to a
// real public service; offering verdicts for them just confuses
// operators and the endpoint becomes a DNS-server discovery probe.
var reservedSuffixes = map[string]struct{}{
	"invalid":   {},
	"local":     {},
	"localhost": {},
	"example":   {},
	"onion":     {},
	"test":      {},
	"alt":       {},
}

// validateDomain normalises and validates an operator-supplied domain.
func validateDomain(raw string) (string, error) {
	d := strings.TrimSpace(strings.ToLower(strings.TrimSuffix(raw, ".")))
	if d == "" {
		return "", errStr("domain is required")
	}
	if len(d) > 253 {
		return "", errStr("domain too long (max 253 chars)")
	}
	if net.ParseIP(d) != nil {
		return "", errStr("refusing literal IP — give a domain name like example.com")
	}
	if !strings.Contains(d, ".") {
		return "", errStr("domain must contain at least one dot")
	}
	if dot := strings.LastIndex(d, "."); dot >= 0 {
		tld := d[dot+1:]
		if _, bad := reservedSuffixes[tld]; bad {
			return "", errStr("refusing reserved TLD ." + tld)
		}
	}
	return d, nil
}

type strErr string

func (s strErr) Error() string { return string(s) }
func errStr(s string) error    { return strErr(s) }

// evaluateTestDomain runs the verdict chain in the same order the DNS
// handler does: local-DNS → allowlist → engine (profile-aware
// blocklists) → SafeSearch → forwarded. Caller decides how much of
// the result to expose.
func evaluateTestDomain(app AppState, domain, clientIPStr string) testDomainResp {
	out := testDomainResp{
		Domain:      domain,
		ClientIP:    clientIPStr,
		EvaluatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	cfg := app.GetCfg()
	if cfg == nil {
		out.Reason = "forwarded"
		return out
	}

	// 1. Local DNS — same priority as the DNS handler.
	if val := lookupLocalDNS(cfg.LocalDNS.Entries, domain); val != "" {
		out.Reason = "local-dns"
		out.LocalDNSAnswer = val
		return out
	}

	// 2. Allowlist — surfaced as a distinct reason. The engine returns
	// Allow for both "no rule matched" and "allowlist hit" without
	// distinguishing, so we check separately.
	if matchesAllowlist(cfg.Filtering.Allowlist, domain) {
		out.Reason = "allowlist"
		return out
	}

	// 3. Profile-aware filter engine — exactly the same call ServeDNS
	// makes. This is the single source of truth.
	fe := app.GetFilterEng()
	if fe == nil {
		out.Reason = "forwarded"
		return out
	}
	var clientIP net.IP
	if clientIPStr != "" {
		clientIP = net.ParseIP(clientIPStr)
	}
	var ident filter.ClientIdentity
	result := fe.EvaluateForClientID(domain, clientIP, ident, filter.Now())

	if result.Disposition == filter.Block {
		out.WouldBlock = true
		out.Reason = "blocklist"
		out.MatchedBlocklistID = result.BlocklistID
		out.BlockPolicy = policyLabel(fe.EffectivePolicy(result))
		// Attribute the verdict to the profile that owns the matched
		// blocklist — NOT just profiles[0]. When multiple profiles
		// match (e.g. the implicit "default" + a specific kids one),
		// the engine's walk picks the one whose blocklists slice
		// contains result.BlocklistID. Mirror that pick here so the
		// reported profile is the operator's profile, not "default".
		out.MatchedProfileID = findOwningProfile(cfg, result.BlocklistID, clientIPStr)
		return out
	}

	// Non-block path: best-effort attribution of "which profile would
	// have evaluated this" — first non-default match, else default.
	if profiles := fe.ProfilesMatching(clientIP, ident); len(profiles) > 0 {
		out.MatchedProfileID = profiles[0]
		for _, pid := range profiles {
			if pid != "default" {
				out.MatchedProfileID = pid
				break
			}
		}
	} else {
		out.MatchedProfileID = "default"
	}

	// 4. SafeSearch — would CNAME-rewrite (informational; not a block).
	if target, ok := filter.SafeSearchRewrite(domain,
		fe.SafeSearchProvidersForClientID(clientIP, ident)); ok {
		out.Reason = "safesearch"
		out.SafeSearchRewrite = target
		return out
	}

	// 5. Forwarded.
	out.Reason = "forwarded"
	return out
}

func lookupLocalDNS(entries []config.LocalDNSEntry, domain string) string {
	for _, e := range entries {
		if strings.EqualFold(strings.TrimSuffix(e.Hostname, "."), domain) {
			return e.Value
		}
	}
	return ""
}

// matchesAllowlist returns true if domain equals or is a subdomain of
// any allowlist entry. Entries support an optional "*." prefix.
func matchesAllowlist(allowed []string, domain string) bool {
	for _, raw := range allowed {
		e := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(raw, "*."), "."))
		if e == "" {
			continue
		}
		if domain == e || strings.HasSuffix(domain, "."+e) {
			return true
		}
	}
	return false
}

func policyLabel(p filter.BlockPolicy) string {
	switch p {
	case filter.PolicyNULL:
		return "null"
	case filter.PolicyNODATA:
		return "nodata"
	}
	// PolicyNXDOMAIN AND PolicyInherit both surface as "nxdomain" — the
	// DNS handler's buildBlockResponse uses the same default (see
	// internal/dns/handler.go: PolicyInherit falls through to NXDOMAIN).
	// Same-source-of-truth principle applies to the policy label too.
	return "nxdomain"
}

// findOwningProfile returns the profile id whose blocklists slice
// contains blocklistID AND whose client identifiers match the request.
// When several profiles match, prefer the first non-"default" one so
// the operator sees the specific profile they configured. Falls back
// to "default" only when nothing better fits.
func findOwningProfile(cfg *config.Config, blocklistID, clientIPStr string) string {
	if cfg == nil || blocklistID == "" {
		return "default"
	}
	clientIP := net.ParseIP(clientIPStr)
	// First pass: profiles that explicitly contain the blocklist AND
	// match the client.
	for _, p := range cfg.Profiles {
		if !profileHasBlocklist(p, blocklistID) {
			continue
		}
		if p.ID == "default" {
			continue
		}
		if profileMatchesClient(p, clientIP) {
			return p.ID
		}
	}
	// Second pass: default profile if it owns the blocklist.
	for _, p := range cfg.Profiles {
		if p.ID == "default" && profileHasBlocklist(p, blocklistID) {
			return "default"
		}
	}
	// Orphan blocklist (no profile attached) — attribute to default.
	return "default"
}

func profileHasBlocklist(p config.Profile, blID string) bool {
	for _, x := range p.Blocklists {
		if x == blID {
			return true
		}
	}
	return false
}

func profileMatchesClient(p config.Profile, ip net.IP) bool {
	if ip == nil {
		// Without a client IP, only the default profile matches (mirrors
		// the engine's matchesProfileIP behaviour).
		return p.ID == "default"
	}
	for _, x := range p.ClientIPs {
		if x == ip.String() {
			return true
		}
	}
	for _, c := range p.ClientCIDRs {
		_, n, err := net.ParseCIDR(c)
		if err == nil && n.Contains(ip) {
			return true
		}
	}
	return false
}

// TestDomainAuth handles POST /api/v1/test-domain.
func (h *Handler) TestDomainAuth(w http.ResponseWriter, r *http.Request) {
	var body testDomainReq
	if !decodeJSON(w, r, &body) {
		return
	}
	d, err := validateDomain(body.Domain)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	out := evaluateTestDomain(h.app, d, body.ClientIP)
	bumpTestDomainMetric(h.app, "auth", out.WouldBlock)
	writeJSON(w, http.StatusOK, out)
}

// HandleTestDomain is the guest entry point. Lives on PublicTester so
// it shares the per-IP rate-limit token bucket with the M5.9.5
// blocklist URL tester — combined budget across all public test
// endpoints stays at 60/h. Auth-aware shim in app.go calls this.
func (p *PublicTester) HandleTestDomain(app AppState, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"error": "method not allowed",
		})
		return
	}
	if !p.allow(sourceIP(r)) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error": "rate limit exceeded — try again in a minute",
		})
		return
	}
	var body testDomainReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	d, err := validateDomain(body.Domain)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Guest endpoint ignores client_ip / profile_id — verdict is for
	// the default profile only. Stripped response (verdict + reason).
	full := evaluateTestDomain(app, d, "")
	bumpTestDomainMetric(app, "guest", full.WouldBlock)
	writeJSON(w, http.StatusOK, map[string]any{
		"would_block": full.WouldBlock,
		"reason":      full.Reason,
	})
}

func bumpTestDomainMetric(app AppState, surface string, blocked bool) {
	verdict := "allow"
	if blocked {
		verdict = "block"
	}
	app.ObserveTestDomain(surface, verdict)
}
