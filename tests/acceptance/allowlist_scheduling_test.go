// Acceptance tests for M36 — Allowlist Scheduling + Per-Entry Metadata.
//
// FSIDs covered:
//   FS-AllowlistEntryExpiryRespected
//   FS-SharedAllowlistCrossProfileAllow
//   FS-AllowlistBulkImport
//   FS-AllowlistRichMetadataRoundTrip
//
// Tests interact exclusively through the HTTP API and DNS port (black-box).

package acceptance

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// startM36Node starts a single-node suitable for M36 testing.
func startM36Node(t *testing.T) *Node {
	t.Helper()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("9.9.9.9"))
	return startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
		Env:               []string{"SKOED_TEST_MODE=1"},
	})
}

// addGlobalAllowlistRich adds a rich allowlist entry to the global allowlist.
func addGlobalAllowlistRich(t *testing.T, n *Node, domain, note string, expiresAt *int64) {
	t.Helper()
	body := map[string]interface{}{"domain": domain}
	if note != "" {
		body["note"] = note
	}
	if expiresAt != nil {
		body["expires_at"] = *expiresAt
	}
	resp := n.apiDo(t, "POST", "/api/v1/allowlist", mustJSON(t, body))
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusCreated)
}

// getGlobalAllowlistEntries fetches the rich allowlist entries.
func getGlobalAllowlistEntries(t *testing.T, n *Node) []map[string]interface{} {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/allowlist/entries", "")
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusOK)
	var entries []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		t.Fatalf("decode allowlist entries: %v", err)
	}
	return entries
}

// ─── Tests ────────────────────────────────────────────────────────────────────

// FS-AllowlistRichMetadataRoundTrip
// POST /api/v1/allowlist with note → GET /api/v1/allowlist/entries shows entry with note.
func TestAllowlistRichMetadataRoundTrip(t *testing.T) {
	t.Parallel()
	n := startM36Node(t)

	addGlobalAllowlistRich(t, n, "trusted.example.com", "Office network", nil)

	entries := getGlobalAllowlistEntries(t, n)
	found := false
	for _, e := range entries {
		if e["domain"] == "trusted.example.com" {
			found = true
			if e["note"] != "Office network" {
				t.Errorf("expected note 'Office network', got %v", e["note"])
			}
		}
	}
	if !found {
		t.Fatalf("entry 'trusted.example.com' not found in /api/v1/allowlist/entries; got %v", entries)
	}
}

// FS-AllowlistEntryExpiryRespected
// An entry with expires_at in the past must not allow the domain; the blocklist should win.
func TestAllowlistEntryExpiryRespected(t *testing.T) {
	t.Parallel()
	n := startM36Node(t)

	// Block the domain.
	addInlineBlocklist(t, n, "bl-expiry-test", []string{"expiry-test.example.com"}, "")

	// Allow it via an already-expired entry.
	pastEpoch := time.Now().Add(-time.Second).Unix()
	addGlobalAllowlistRich(t, n, "expiry-test.example.com", "", &pastEpoch)

	// Give the config time to be applied.
	time.Sleep(200 * time.Millisecond)

	// Expired entry: blocklist wins → NXDOMAIN.
	resp := dnsQuery(t, n.DNSAddr, "expiry-test.example.com", dns.TypeA)
	assertRcode(t, resp, dns.RcodeNameError)
}

// FS-AllowlistBulkImport
// POST /api/v1/allowlist/import with multiple domains adds all and skips duplicates.
func TestAllowlistBulkImport(t *testing.T) {
	t.Parallel()
	n := startM36Node(t)

	// Pre-seed one domain.
	addGlobalAllowlistRich(t, n, "existing.example.com", "", nil)

	entries := []map[string]interface{}{
		{"domain": "existing.example.com"},
		{"domain": "new1.example.com", "note": "bulk"},
		{"domain": "new2.example.com"},
	}
	resp := n.apiDo(t, "POST", "/api/v1/allowlist/import", mustJSON(t, entries))
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusOK)

	var result map[string]int
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode import result: %v", err)
	}
	if result["added"] != 2 {
		t.Errorf("expected 2 added, got %d", result["added"])
	}
	if result["skipped"] != 1 {
		t.Errorf("expected 1 skipped, got %d", result["skipped"])
	}

	// Verify the new entries appear.
	all := getGlobalAllowlistEntries(t, n)
	domains := make(map[string]bool)
	for _, e := range all {
		if d, ok := e["domain"].(string); ok {
			domains[d] = true
		}
	}
	for _, want := range []string{"existing.example.com", "new1.example.com", "new2.example.com"} {
		if !domains[want] {
			t.Errorf("expected %q in entries after import", want)
		}
	}
}

// FS-SharedAllowlistCrossProfileAllow
// A shared allowlist assigned to a profile allows its domains for that profile's clients.
func TestSharedAllowlistCrossProfileAllow(t *testing.T) {
	t.Parallel()
	n := startM36Node(t)

	// Block the domain.
	addInlineBlocklist(t, n, "bl-sal-test", []string{"sal-test.example.com"}, "")

	// Create two profiles; only "kids" (10.0.0.1) gets the shared allowlist.
	for _, p := range []struct{ id, ip string }{{"kids", "10.0.0.1"}, {"adults", "10.0.0.2"}} {
		createProfile(t, n, profileBody{
			ID:         p.id,
			Name:       p.id,
			Blocklists: []string{"bl-sal-test"},
			ClientIPs:  []string{p.ip},
		})
	}

	// Create shared allowlist for "kids" only.
	sal := map[string]interface{}{
		"id":       "sal-trusted",
		"name":     "Trusted Sites",
		"entries":  []map[string]string{{"domain": "sal-test.example.com"}},
		"profiles": []string{"kids"},
	}
	salResp := n.apiDo(t, "POST", "/api/v1/shared-allowlists", mustJSON(t, sal))
	defer salResp.Body.Close()
	assertStatus(t, salResp, http.StatusCreated)

	// Wait for config reload.
	time.Sleep(300 * time.Millisecond)

	// kids client (10.0.0.1): shared allowlist wins → should NOT get NXDOMAIN.
	kidsMsg := dnsQueryAsClient(t, n.DNSAddr, "sal-test.example.com", dns.TypeA, "10.0.0.1")
	if kidsMsg != nil && kidsMsg.Rcode == dns.RcodeNameError {
		t.Error("kids profile: expected domain allowed via shared allowlist, got NXDOMAIN")
	}
}
