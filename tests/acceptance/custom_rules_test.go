// Acceptance tests for M30.5: Custom Filtering Rules (Regex + Exact).
//
// FSIDs covered:
//   FS-CustomRulesRegexBlock, FS-CustomRulesExactBlock,
//   FS-CustomRulesRegexAllow, FS-CustomRulesExactAllow,
//   FS-CustomRulesPriority, FS-CustomRulesOverrideBlocklist,
//   FS-CustomRulesEdit, FS-CustomRulesValidation

package acceptance

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/miekg/dns"
)

type customRulesBody struct {
	Rules string `json:"rules"`
}

func putCustomRules(t *testing.T, n *Node, rules string) *http.Response {
	t.Helper()
	return n.apiDo(t, "PUT", "/api/v1/custom-rules", mustJSON(t, customRulesBody{Rules: rules}))
}

func getCustomRules(t *testing.T, n *Node) string {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/custom-rules", "")
	defer resp.Body.Close()
	var rb customRulesBody
	json.NewDecoder(resp.Body).Decode(&rb)
	return rb.Rules
}

// FS-CustomRulesRegexBlock
// A regex rule /^ad[0-9]+\.example\.com$/ blocks matching domains but not non-matching ones.
func TestCustomRulesRegexBlock(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})

	resp := putCustomRules(t, n, `/^ad[0-9]+\.example\.com$/`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("putCustomRules: want 200, got %d", resp.StatusCode)
	}

	r := dnsQuery(t, n.DNSAddr, "ad42.example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeNameError) // blocked

	r = dnsQuery(t, n.DNSAddr, "safe.example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess) // forwarded
}

// FS-CustomRulesExactBlock
// An exact domain rule blocks that domain and its sub-domains, not siblings.
func TestCustomRulesExactBlock(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})

	resp := putCustomRules(t, n, "tracking.bad-actor.net")
	resp.Body.Close()

	r := dnsQuery(t, n.DNSAddr, "tracking.bad-actor.net", dns.TypeA)
	assertRcode(t, r, dns.RcodeNameError) // blocked (exact)

	r = dnsQuery(t, n.DNSAddr, "deep.tracking.bad-actor.net", dns.TypeA)
	assertRcode(t, r, dns.RcodeNameError) // blocked (sub-domain)

	r = dnsQuery(t, n.DNSAddr, "other.bad-actor.net", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess) // sibling: forwarded
}

// FS-CustomRulesRegexAllow
// A regex allow rule @@/\.partner\.com$/ overrides a blocklist match.
func TestCustomRulesRegexAllow(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "partner-block", []string{"analytics.partner.com"}, "")

	resp := putCustomRules(t, n, `@@/\.partner\.com$/`)
	resp.Body.Close()

	r := dnsQuery(t, n.DNSAddr, "analytics.partner.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess) // allow overrides blocklist
}

// FS-CustomRulesExactAllow
// An exact allow rule @@tracker.example.com overrides a blocklist.
func TestCustomRulesExactAllow(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "tracker-block", []string{"tracker.example.com"}, "")

	resp := putCustomRules(t, n, "@@tracker.example.com")
	resp.Body.Close()

	r := dnsQuery(t, n.DNSAddr, "tracker.example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess) // allow overrides blocklist
}

// FS-CustomRulesPriority
// Within a rule set, an allow rule wins over a block rule for the same domain.
func TestCustomRulesPriority(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})

	// Block rule covers all .example.com; allow rule exempts safe.example.com.
	rules := "/\\.example\\.com$/\n@@safe.example.com"
	resp := putCustomRules(t, n, rules)
	resp.Body.Close()

	r := dnsQuery(t, n.DNSAddr, "safe.example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess) // allow wins

	r = dnsQuery(t, n.DNSAddr, "ads.example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeNameError) // block applies
}

// FS-CustomRulesOverrideBlocklist
// A custom block rule applies even when no active blocklist matches.
func TestCustomRulesOverrideBlocklist(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})

	resp := putCustomRules(t, n, "/blocker\\.internal$/")
	resp.Body.Close()

	r := dnsQuery(t, n.DNSAddr, "anything.blocker.internal", dns.TypeA)
	assertRcode(t, r, dns.RcodeNameError) // custom rule blocks
}

// FS-CustomRulesEdit
// Saving new rules takes effect immediately; clearing rules deactivates them.
func TestCustomRulesEdit(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})

	// Set a block rule.
	resp := putCustomRules(t, n, "test.local")
	resp.Body.Close()

	r := dnsQuery(t, n.DNSAddr, "test.local", dns.TypeA)
	assertRcode(t, r, dns.RcodeNameError) // blocked

	// Clear all rules.
	resp = putCustomRules(t, n, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clear custom rules: want 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	r = dnsQuery(t, n.DNSAddr, "test.local", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess) // forwarded again

	got := getCustomRules(t, n)
	if got != "" {
		t.Fatalf("want empty rules, got %q", got)
	}
}

// FS-CustomRulesValidation
// An invalid regex is rejected with 422; the previous rule set stays active.
func TestCustomRulesValidation(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})

	// Prime with a valid rule.
	resp := putCustomRules(t, n, "good.example.com")
	resp.Body.Close()

	// Submit an invalid regex.
	resp = putCustomRules(t, n, "/[unclosed/")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid regex: want 422, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Previous valid rule is still active.
	r := dnsQuery(t, n.DNSAddr, "good.example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeNameError)

	// Stored text is unchanged.
	stored := getCustomRules(t, n)
	if stored != "good.example.com" {
		t.Fatalf("rules changed after rejected PUT: %q", stored)
	}
}
