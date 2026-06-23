// Acceptance tests for M27 per-profile allowlists (full feature surface).
//
// FSIDs covered:
//   FS-PerProfileAllowlistPutReplacesAll
//   FS-PerProfileAllowlistPutClearsOnEmpty
//   FS-PerProfileAllowlistDeletePurgesCache
//   FS-PerProfileAllowlistWildcardSubdomain
//   FS-PerProfileAllowlistWildcardApex
//
// Tests interact exclusively through the HTTP API and DNS port (black-box).
// SKOED_TEST_MODE=1 is set so the EDNS0 client-IP override is honoured by
// the DNS engine for profile matching.

package acceptance

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// startM27Node starts a single-node with SKOED_TEST_MODE=1 and a fake upstream
// that answers every A query with 1.2.3.4. SKOED_TEST_MODE is passed directly
// via NodeConfig.Env so this helper is safe to call from parallel tests.
func startM27Node(t *testing.T) (*Node, string) {
	t.Helper()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
		Env:               []string{"SKOED_TEST_MODE=1"},
	})
	return n, upstream
}

// setupM27Profile creates a blocklist that blocks `blockedDomain` and a
// profile assigned to `clientIP` that uses that blocklist.
// Returns the node, ready for per-profile allowlist manipulation.
func setupM27Profile(t *testing.T, n *Node, profileID, clientIP, blockedDomain string) {
	t.Helper()

	blID := "bl-" + profileID
	addInlineBlocklist(t, n, blID, []string{blockedDomain}, "")

	body := profileBody{
		ID:          profileID,
		Name:        profileID,
		Blocklists:  []string{blID},
		Allowlist:   []string{},
		ClientIPs:   []string{clientIP},
		ClientCIDRs: []string{},
	}
	createProfile(t, n, body)
}

// getAllowlistEntries fetches the per-profile allowlist and decodes it.
func getAllowlistEntries(t *testing.T, n *Node, profileID string) []string {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/profiles/"+profileID+"/allowlist", "")
	assertStatus(t, resp, http.StatusOK)
	defer resp.Body.Close()
	var list []string
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("decode allowlist: %v", err)
	}
	return list
}

// putAllowlist atomically replaces the profile allowlist via PUT.
func putAllowlist(t *testing.T, n *Node, profileID string, domains []string) *http.Response {
	t.Helper()
	return n.apiDo(t, "PUT", "/api/v1/profiles/"+profileID+"/allowlist", mustJSON(t, domains))
}

// addProfileDomain adds a single domain to a profile's allowlist via POST.
func addProfileDomain(t *testing.T, n *Node, profileID, domain string) {
	t.Helper()
	resp := n.apiDo(t, "POST", "/api/v1/profiles/"+profileID+"/allowlist",
		mustJSON(t, map[string]string{"domain": domain}))
	assertStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
}

// deleteProfileDomain removes a single domain from a profile's allowlist via DELETE.
func deleteProfileDomain(t *testing.T, n *Node, profileID, domain string) {
	t.Helper()
	resp := n.apiDo(t, "DELETE", "/api/v1/profiles/"+profileID+"/allowlist/"+domain, "")
	assertStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
}

// containsStr reports whether slice contains s.
func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// ─── Tests ────────────────────────────────────────────────────────────────────

// FS-PerProfileAllowlistPutReplacesAll
// PUT with a new list must replace the existing entries atomically.
// Subsequent GET must return exactly the new list; old entries must be gone.
func TestPerProfileAllowlistPutReplacesAll(t *testing.T) {
	t.Parallel()
	n, _ := startM27Node(t)

	setupM27Profile(t, n, "kids", "192.168.1.10", "blocked.example.com")

	// Seed initial entries via POST.
	addProfileDomain(t, n, "kids", "old1.com")
	addProfileDomain(t, n, "kids", "old2.com")

	// Verify seed.
	before := getAllowlistEntries(t, n, "kids")
	if !containsStr(before, "old1.com") || !containsStr(before, "old2.com") {
		t.Fatalf("seed failed: got %v", before)
	}

	// PUT replacement list.
	resp := putAllowlist(t, n, "kids", []string{"new1.com", "new2.com"})
	assertStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	// GET must return exactly the new list.
	after := getAllowlistEntries(t, n, "kids")
	if len(after) != 2 {
		t.Fatalf("expected 2 entries after PUT, got %d: %v", len(after), after)
	}
	if !containsStr(after, "new1.com") || !containsStr(after, "new2.com") {
		t.Fatalf("new entries missing: got %v", after)
	}
	if containsStr(after, "old1.com") || containsStr(after, "old2.com") {
		t.Fatalf("old entries still present: got %v", after)
	}
}

// FS-PerProfileAllowlistPutClearsOnEmpty
// PUT with an empty array must clear the allowlist entirely.
func TestPerProfileAllowlistPutClearsOnEmpty(t *testing.T) {
	t.Parallel()
	n, _ := startM27Node(t)

	setupM27Profile(t, n, "adults", "192.168.1.20", "blocked.example.com")

	addProfileDomain(t, n, "adults", "domain.com")

	resp := putAllowlist(t, n, "adults", []string{})
	assertStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	after := getAllowlistEntries(t, n, "adults")
	if len(after) != 0 {
		t.Fatalf("expected empty allowlist after PUT [], got %v", after)
	}
}

// FS-PerProfileAllowlistDeletePurgesCache
// After deleting a domain from the profile allowlist, a DNS query for that
// domain from a client in that profile must return NXDOMAIN — not a cached
// NOERROR from before the deletion.
func TestPerProfileAllowlistDeletePurgesCache(t *testing.T) {
	t.Parallel()
	n, _ := startM27Node(t)

	const clientIP = "192.168.1.30"
	const domain = "allowed.example.com"

	// Create a blocklist that blocks the domain.
	addInlineBlocklist(t, n, "bl-cache-test", []string{domain}, "")

	// Create a profile for clientIP using that blocklist, with the domain allowed.
	body := profileBody{
		ID:          "cache-test",
		Name:        "cache-test",
		Blocklists:  []string{"bl-cache-test"},
		Allowlist:   []string{domain},
		ClientIPs:   []string{clientIP},
		ClientCIDRs: []string{},
	}
	createProfile(t, n, body)

	// Confirm the domain is currently allowed (NOERROR with answer).
	r := dnsQueryAsClient(t, n.DNSAddr, domain, dns.TypeA, clientIP)
	if r.Rcode != dns.RcodeSuccess {
		t.Fatalf("expected NOERROR before delete, got %s", dns.RcodeToString[r.Rcode])
	}

	// Warm the DNS cache: query again so the response is definitely cached.
	_ = dnsQueryAsClient(t, n.DNSAddr, domain, dns.TypeA, clientIP)

	// Delete the domain from the profile allowlist.
	deleteProfileDomain(t, n, "cache-test", domain)

	// Give the server a moment to apply the change and purge the cache.
	time.Sleep(50 * time.Millisecond)

	// The domain must now be blocked — cache must have been purged.
	r2 := dnsQueryAsClient(t, n.DNSAddr, domain, dns.TypeA, clientIP)
	if r2.Rcode != dns.RcodeNameError {
		t.Fatalf("expected NXDOMAIN after delete+cache purge, got %s (stale cache?)", dns.RcodeToString[r2.Rcode])
	}
}

// FS-PerProfileAllowlistWildcardSubdomain
// A wildcard entry "*.example.com" must allow sub.example.com for profile clients.
func TestPerProfileAllowlistWildcardSubdomain(t *testing.T) {
	t.Parallel()
	n, _ := startM27Node(t)

	const clientIP = "192.168.1.40"

	// Block all subdomains of example.com via the blocklist.
	addInlineBlocklist(t, n, "bl-wildcard", []string{"example.com"}, "")

	body := profileBody{
		ID:          "wildcard-test",
		Name:        "wildcard-test",
		Blocklists:  []string{"bl-wildcard"},
		Allowlist:   []string{"*.example.com"},
		ClientIPs:   []string{clientIP},
		ClientCIDRs: []string{},
	}
	createProfile(t, n, body)

	// sub.example.com should be allowed (NOERROR with upstream answer).
	r := dnsQueryAsClient(t, n.DNSAddr, "sub.example.com", dns.TypeA, clientIP)
	if r.Rcode != dns.RcodeSuccess {
		t.Fatalf("expected NOERROR for sub.example.com with wildcard allowlist, got %s", dns.RcodeToString[r.Rcode])
	}
}

// FS-PerProfileAllowlistWildcardApex
// A wildcard entry "*.example.com" must also allow the apex "example.com".
func TestPerProfileAllowlistWildcardApex(t *testing.T) {
	t.Parallel()
	n, _ := startM27Node(t)

	const clientIP = "192.168.1.50"

	addInlineBlocklist(t, n, "bl-wildcard-apex", []string{"example.com"}, "")

	body := profileBody{
		ID:          "wildcard-apex-test",
		Name:        "wildcard-apex-test",
		Blocklists:  []string{"bl-wildcard-apex"},
		Allowlist:   []string{"*.example.com"},
		ClientIPs:   []string{clientIP},
		ClientCIDRs: []string{},
	}
	createProfile(t, n, body)

	// The apex example.com should also be allowed.
	r := dnsQueryAsClient(t, n.DNSAddr, "example.com", dns.TypeA, clientIP)
	if r.Rcode != dns.RcodeSuccess {
		t.Fatalf("expected NOERROR for apex example.com with wildcard allowlist, got %s", dns.RcodeToString[r.Rcode])
	}
}
