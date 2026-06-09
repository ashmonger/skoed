// Acceptance tests for the filtering engine.
//
// FSIDs covered:
//   FS-DomainBlockingNxdomain, FS-DomainBlockingNull, FS-DomainBlockingNodata,
//   FS-DomainBlockingSubdomain, FS-DomainBlockingNotBlocked,
//   FS-DomainBlockingPerBlocklistPolicyOverridesGlobal,
//   FS-BlockPolicyConfigurationGlobalDefault, FS-BlockPolicyConfigurationPerBlocklist,
//   FS-BlockPolicyConfigurationChangeGlobal, FS-BlockPolicyConfigurationPerBlocklistReset,
//   FS-BlocklistAddManual, FS-BlocklistRemove, FS-BlocklistDisable, FS-BlocklistEnable,
//   FS-BlocklistRefresh (skipped: requires network), FS-BlocklistAddFromUrl (skipped: requires network),
//   FS-BlocklistParseHostsFormat, FS-BlocklistParseAskoedFormat, FS-BlocklistStats,
//   FS-BlocklistWildcardEntry,
//   FS-AllowlistAddDomain, FS-AllowlistOverridesBlocklist, FS-AllowlistRemoveDomain,
//   FS-AllowlistWildcardEntry, FS-AllowlistDoesNotAffectUnblockedDomains

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/miekg/dns"
)

// ── Domain blocking ───────────────────────────────────────────────────────

// FS-DomainBlockingNxdomain
func TestDomainBlockingNXDOMAIN(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"ads.example.com"}, "")

	r := dnsQuery(t, n.DNSAddr, "ads.example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeNameError)
}

// FS-DomainBlockingNull
func TestDomainBlockingNULL(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"ads.example.com"}, "null")

	r := dnsQuery(t, n.DNSAddr, "ads.example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerA(t, r, "0.0.0.0")
}

// FS-DomainBlockingNodata
func TestDomainBlockingNODATA(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"ads.example.com"}, "nodata")

	r := dnsQuery(t, n.DNSAddr, "ads.example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess)
	assertNoAnswer(t, r)
}

// FS-DomainBlockingSubdomain
// A subdomain query for a domain on the blocklist is blocked.
func TestDomainBlockingSubdomain(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"tracker.example.com"}, "")

	r := dnsQuery(t, n.DNSAddr, "deep.sub.tracker.example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeNameError)
}

// FS-DomainBlockingNotBlocked
// A domain not on any blocklist is resolved normally.
func TestDomainNotBlocked(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"ads.example.com"}, "")

	r := dnsQuery(t, n.DNSAddr, "safe.example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerA(t, r, "93.184.216.34")
}

// FS-DomainBlockingPerBlocklistPolicyOverridesGlobal
// A per-blocklist policy (NULL) overrides the global default (NXDOMAIN).
func TestPerBlocklistPolicyOverridesGlobal(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"ads.example.com"}, "null")

	r := dnsQuery(t, n.DNSAddr, "ads.example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerA(t, r, "0.0.0.0")
}

// ── Block policy configuration ────────────────────────────────────────────

// FS-BlockPolicyConfigurationGlobalDefault
func TestBlockPolicyGlobalDefault(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"ads.example.com"}, "")

	r := dnsQuery(t, n.DNSAddr, "ads.example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeNameError)
}

// FS-BlockPolicyConfigurationPerBlocklist
// Setting a per-blocklist policy does not affect other blocklists.
func TestBlockPolicyPerBlocklistIsolation(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads-null", []string{"ads.example.com"}, "null")
	addInlineBlocklist(t, n, "malware-nxdomain", []string{"malware.example.com"}, "")

	// ads list uses NULL
	rAds := dnsQuery(t, n.DNSAddr, "ads.example.com", dns.TypeA)
	assertRcode(t, rAds, dns.RcodeSuccess)
	assertAnswerA(t, rAds, "0.0.0.0")

	// malware list inherits global NXDOMAIN
	rMalware := dnsQuery(t, n.DNSAddr, "malware.example.com", dns.TypeA)
	assertRcode(t, rMalware, dns.RcodeNameError)
}

// FS-BlockPolicyConfigurationChangeGlobal
// Changing the global block policy via PATCH /api/v1/settings takes effect.
func TestBlockPolicyChangeGlobal(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"ads.example.com"}, "")

	// Verify NXDOMAIN initially
	r1 := dnsQuery(t, n.DNSAddr, "ads.example.com", dns.TypeA)
	assertRcode(t, r1, dns.RcodeNameError)

	// Change to NODATA
	resp := n.apiDo(t, "PATCH", "/api/v1/settings", mustJSON(t, map[string]any{
		"filtering": map[string]string{"block_policy": "nodata"},
	}))
	assertStatus(t, resp, http.StatusOK)

	// Now expect NODATA
	r2 := dnsQuery(t, n.DNSAddr, "ads.example.com", dns.TypeA)
	assertRcode(t, r2, dns.RcodeSuccess)
	assertNoAnswer(t, r2)
}

// FS-BlockPolicyConfigurationPerBlocklistReset
// Removing a per-blocklist policy causes it to inherit the global default.
func TestBlockPolicyPerBlocklistReset(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"ads.example.com"}, "null")

	// Verify NULL initially
	r1 := dnsQuery(t, n.DNSAddr, "ads.example.com", dns.TypeA)
	assertAnswerA(t, r1, "0.0.0.0")

	// Reset per-blocklist policy to inherit global
	resp := n.apiDo(t, "PATCH", "/api/v1/blocklists/ads", mustJSON(t, map[string]any{
		"block_policy": "",
	}))
	assertStatus(t, resp, http.StatusOK)

	// Now inherits NXDOMAIN
	r2 := dnsQuery(t, n.DNSAddr, "ads.example.com", dns.TypeA)
	assertRcode(t, r2, dns.RcodeNameError)
}

// ── Blocklist management ──────────────────────────────────────────────────

// FS-BlocklistAddManual
func TestBlocklistAddManual(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})

	resp := n.apiDo(t, "POST", "/api/v1/blocklists", mustJSON(t, map[string]any{
		"id":     "custom",
		"name":   "Custom",
		"source": map[string]string{"type": "inline"},
		"domains": []string{"malware.example.com"},
	}))
	assertStatus(t, resp, http.StatusCreated)

	r := dnsQuery(t, n.DNSAddr, "malware.example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeNameError)
}

// FS-BlocklistRemove
func TestBlocklistRemove(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"ads.example.com"}, "")

	// Confirm blocked
	r1 := dnsQuery(t, n.DNSAddr, "ads.example.com", dns.TypeA)
	assertRcode(t, r1, dns.RcodeNameError)

	// Delete the blocklist
	resp := n.apiDo(t, "DELETE", "/api/v1/blocklists/ads", "")
	assertStatus(t, resp, http.StatusNoContent)

	// Now resolved normally
	r2 := dnsQuery(t, n.DNSAddr, "ads.example.com", dns.TypeA)
	assertRcode(t, r2, dns.RcodeSuccess)
	assertAnswerA(t, r2, "93.184.216.34")
}

// FS-BlocklistDisable
func TestBlocklistDisable(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"ads.example.com"}, "")

	resp := n.apiDo(t, "PATCH", "/api/v1/blocklists/ads", mustJSON(t, map[string]any{
		"enabled": false,
	}))
	assertStatus(t, resp, http.StatusOK)

	r := dnsQuery(t, n.DNSAddr, "ads.example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerA(t, r, "93.184.216.34")
}

// FS-BlocklistEnable
func TestBlocklistEnable(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"ads.example.com"}, "")

	// Disable first
	n.apiDo(t, "PATCH", "/api/v1/blocklists/ads", mustJSON(t, map[string]any{"enabled": false}))

	// Re-enable
	resp := n.apiDo(t, "PATCH", "/api/v1/blocklists/ads", mustJSON(t, map[string]any{"enabled": true}))
	assertStatus(t, resp, http.StatusOK)

	r := dnsQuery(t, n.DNSAddr, "ads.example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeNameError)
}

// FS-BlocklistParseHostsFormat
// A blocklist in hosts file format is parsed correctly (IPs are ignored, domains extracted).
func TestBlocklistParseHostsFormat(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})

	// Hosts file format: "0.0.0.0 domain" — the IPs should not be treated as blocked domains
	resp := n.apiDo(t, "POST", "/api/v1/blocklists", mustJSON(t, map[string]any{
		"id":   "hosts-format",
		"name": "Hosts",
		"source": map[string]string{
			"type":   "inline",
			"format": "hosts",
		},
		"domains": []string{
			"0.0.0.0 ads.example.com",
			"127.0.0.1 tracker.example.org",
		},
	}))
	assertStatus(t, resp, http.StatusCreated)

	// Domains are blocked
	assertRcode(t, dnsQuery(t, n.DNSAddr, "ads.example.com", dns.TypeA), dns.RcodeNameError)
	assertRcode(t, dnsQuery(t, n.DNSAddr, "tracker.example.org", dns.TypeA), dns.RcodeNameError)

	// The IPs themselves are not blocked
	r := dnsQuery(t, n.DNSAddr, "0.0.0.0", dns.TypeA)
	if r.Rcode == dns.RcodeNameError {
		t.Fatal("the IP address 0.0.0.0 should not be treated as a blocked domain")
	}
}

// FS-BlocklistParseAskoedFormat
// A blocklist in AdBlock/ABP format is parsed correctly (comment lines ignored).
func TestBlocklistParseAskoedFormat(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})

	resp := n.apiDo(t, "POST", "/api/v1/blocklists", mustJSON(t, map[string]any{
		"id":   "askoed-format",
		"name": "AdBlock",
		"source": map[string]string{
			"type":   "inline",
			"format": "askoed",
		},
		"domains": []string{
			"||ads.example.com^",
			"! this is a comment",
			"||tracker.example.org^",
		},
	}))
	assertStatus(t, resp, http.StatusCreated)

	assertRcode(t, dnsQuery(t, n.DNSAddr, "ads.example.com", dns.TypeA), dns.RcodeNameError)
	assertRcode(t, dnsQuery(t, n.DNSAddr, "tracker.example.org", dns.TypeA), dns.RcodeNameError)
}

// FS-BlocklistStats
// The blocklist list endpoint returns domain count and metadata.
func TestBlocklistStats(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})
	addInlineBlocklist(t, n, "ads", []string{"a.example.com", "b.example.com", "c.example.com"}, "")

	resp := n.apiDo(t, "GET", "/api/v1/blocklists", "")
	assertStatus(t, resp, http.StatusOK)

	var lists []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&lists); err != nil {
		t.Fatalf("decode blocklists: %v", err)
	}
	resp.Body.Close()

	var found map[string]any
	for _, l := range lists {
		if l["id"] == "ads" {
			found = l
			break
		}
	}
	if found == nil {
		t.Fatal("blocklist 'ads' not found in list response")
	}
	count, ok := found["domain_count"].(float64)
	if !ok || int(count) != 3 {
		t.Fatalf("expected domain_count=3, got %v", found["domain_count"])
	}
}

// FS-BlocklistWildcardEntry
// A wildcard entry *.example.com blocks the apex and all subdomains.
func TestBlocklistWildcardEntry(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "custom", []string{"*.ads.example.com"}, "")

	for _, domain := range []string{
		"ads.example.com",
		"sub.ads.example.com",
		"deep.sub.ads.example.com",
	} {
		t.Run(domain, func(t *testing.T) {
			r := dnsQuery(t, n.DNSAddr, domain, dns.TypeA)
			assertRcode(t, r, dns.RcodeNameError)
		})
	}

	// A sibling domain must not be blocked
	r := dnsQuery(t, n.DNSAddr, "other.example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess)
}

// ── Allowlist management ──────────────────────────────────────────────────

// FS-AllowlistAddDomain / FS-AllowlistOverridesBlocklist
func TestAllowlistOverridesBlocklist(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"allowed-but-blocked.example.com"}, "")

	// Verify it's initially blocked
	assertRcode(t, dnsQuery(t, n.DNSAddr, "allowed-but-blocked.example.com", dns.TypeA), dns.RcodeNameError)

	// Add to allowlist
	resp := n.apiDo(t, "POST", "/api/v1/allowlist", mustJSON(t, map[string]string{
		"domain": "allowed-but-blocked.example.com",
	}))
	assertStatus(t, resp, http.StatusCreated)

	// Now resolves normally
	r := dnsQuery(t, n.DNSAddr, "allowed-but-blocked.example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerA(t, r, "93.184.216.34")
}

// FS-AllowlistRemoveDomain
func TestAllowlistRemove(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"allowed-but-blocked.example.com"}, "")

	n.apiDo(t, "POST", "/api/v1/allowlist", mustJSON(t, map[string]string{
		"domain": "allowed-but-blocked.example.com",
	}))

	// Remove from allowlist
	resp := n.apiDo(t, "DELETE", fmt.Sprintf("/api/v1/allowlist/%s", "allowed-but-blocked.example.com"), "")
	assertStatus(t, resp, http.StatusNoContent)

	// Domain is blocked again
	r := dnsQuery(t, n.DNSAddr, "allowed-but-blocked.example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeNameError)
}

// FS-AllowlistWildcardEntry
// A wildcard allowlist entry *.example.com overrides blocklist for apex and all subdomains.
func TestAllowlistWildcardEntry(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"*.safe.example.com"}, "")

	// Add wildcard to allowlist
	resp := n.apiDo(t, "POST", "/api/v1/allowlist", mustJSON(t, map[string]string{
		"domain": "*.safe.example.com",
	}))
	assertStatus(t, resp, http.StatusCreated)

	for _, domain := range []string{
		"safe.example.com",
		"sub.safe.example.com",
		"deep.sub.safe.example.com",
	} {
		t.Run(domain, func(t *testing.T) {
			r := dnsQuery(t, n.DNSAddr, domain, dns.TypeA)
			assertRcode(t, r, dns.RcodeSuccess)
			assertAnswerA(t, r, "93.184.216.34")
		})
	}
}

// FS-AllowlistDoesNotAffectUnblockedDomains
func TestAllowlistDoesNotAffectUnblockedDomains(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	n.apiDo(t, "POST", "/api/v1/allowlist", mustJSON(t, map[string]string{
		"domain": "safe.example.com",
	}))

	r := dnsQuery(t, n.DNSAddr, "safe.example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerA(t, r, "93.184.216.34")
}

// ── Helpers ───────────────────────────────────────────────────────────────

// addInlineBlocklist adds an inline blocklist via the API and fails the test on error.
// Pass blockPolicy="" to inherit the global default.
func addInlineBlocklist(t *testing.T, n *Node, id string, domains []string, blockPolicy string) {
	t.Helper()
	body := map[string]any{
		"id":     id,
		"name":   id,
		"source": map[string]string{"type": "inline"},
		"domains": domains,
	}
	if blockPolicy != "" {
		body["block_policy"] = blockPolicy
	}
	resp := n.apiDo(t, "POST", "/api/v1/blocklists", mustJSON(t, body))
	assertStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
}
