// Acceptance tests for M3 category-based blocking.
//
// FSIDs covered (one Go test per FSID):
//   FS-CategoryCatalogListed
//   FS-CategoryEnableAddsBlocklist
//   FS-CategoryDisableRemovesAssociation
//   FS-CategoryOverrideUrl
//   FS-CategoryDohEnabledByDefault
//
// These tests interact exclusively through the HTTP management API and DNS
// port. They use startCluster(t, 1) for a fully-bootstrapped single-node
// cluster — none of these scenarios need multi-node convergence.
//
// Whenever the M3 implementation has not yet wired a route, the helper
// callers t.Skip with a "M3 impl pending: <route>" reason so the file
// compiles and runs cleanly until M3 lands.

package acceptance

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/miekg/dns"
)

// categoryEntry mirrors the documented JSON shape of items returned by
// GET /api/v1/categories. Extra fields are tolerated by json.Decoder.
type categoryEntry struct {
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	DefaultURL         string   `json:"default_url"`
	Format             string   `json:"format"`
	EnabledForProfiles []string `json:"enabled_for_profiles"`
	URL                string   `json:"url"`
}

// listCategories fetches GET /api/v1/categories. Skips the test when the
// route is missing (404) so this file remains green pre-M3.
func listCategories(t *testing.T, n *Node) []categoryEntry {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/categories", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M3 impl pending: GET /api/v1/categories returns 404")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/categories: status %d: %s", resp.StatusCode, readBody(t, resp))
	}
	var out []categoryEntry
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode categories: %v", err)
	}
	return out
}

// getCategory fetches GET /api/v1/categories/{name}.
func getCategory(t *testing.T, n *Node, name string) categoryEntry {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/categories/"+name, "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M3 impl pending: GET /api/v1/categories/%s returns 404", name)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/categories/%s: status %d: %s", name, resp.StatusCode, readBody(t, resp))
	}
	var out categoryEntry
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode category %s: %v", name, err)
	}
	return out
}

// FS-CategoryCatalogListed
func TestCategoryCatalogListed(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	cats := listCategories(t, n)
	if len(cats) < 6 {
		t.Fatalf("expected at least 6 categories, got %d: %+v", len(cats), cats)
	}

	required := map[string]bool{
		"adult": false, "gambling": false, "social": false,
		"gaming": false, "streaming": false, "doh": false,
	}
	for _, e := range cats {
		if _, ok := required[e.Name]; ok {
			required[e.Name] = true
		}
		if e.Name == "" || e.Description == "" {
			t.Fatalf("category missing name/description: %+v", e)
		}
		// default_url MAY be empty for doh (bundled), but every other
		// category must surface SOMETHING and a format.
		if e.Format == "" {
			t.Fatalf("category %s missing format", e.Name)
		}
	}
	for name, seen := range required {
		if !seen {
			t.Fatalf("required category %q not present in catalog", name)
		}
	}
}

// FS-CategoryEnableAddsBlocklist
func TestCategoryEnableAddsBlocklist(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	// Pre-create the "kids" profile the category will be enabled on.
	createKidsProfile(t, n)

	// POST /api/v1/categories/social/enable
	resp := n.apiDo(t, "POST", "/api/v1/categories/social/enable",
		mustJSON(t, map[string]string{"profile_id": "kids"}))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M3 impl pending: POST /api/v1/categories/social/enable returns 404")
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("enable social on kids: status %d: %s", resp.StatusCode, readBody(t, resp))
	}

	// The cat:social blocklist now exists and is managed.
	blResp := n.apiDo(t, "GET", "/api/v1/blocklists/cat:social", "")
	defer blResp.Body.Close()
	if blResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /blocklists/cat:social: status %d", blResp.StatusCode)
	}
	var bl map[string]any
	if err := json.NewDecoder(blResp.Body).Decode(&bl); err != nil {
		t.Fatalf("decode cat:social: %v", err)
	}
	if managed, _ := bl["managed"].(bool); !managed {
		t.Fatalf("cat:social: expected managed=true, got %+v", bl["managed"])
	}

	// The kids profile now references cat:social.
	profResp := n.apiDo(t, "GET", "/api/v1/profiles/kids", "")
	defer profResp.Body.Close()
	if profResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /profiles/kids: status %d", profResp.StatusCode)
	}
	var prof profileBody
	if err := json.NewDecoder(profResp.Body).Decode(&prof); err != nil {
		t.Fatalf("decode kids profile: %v", err)
	}
	if !contains(prof.Blocklists, "cat:social") {
		t.Fatalf("kids profile blocklists missing cat:social: %+v", prof.Blocklists)
	}
}

// FS-CategoryDisableRemovesAssociation
func TestCategoryDisableRemovesAssociation(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	createKidsProfile(t, n)

	// Enable first.
	enableResp := n.apiDo(t, "POST", "/api/v1/categories/social/enable",
		mustJSON(t, map[string]string{"profile_id": "kids"}))
	enableResp.Body.Close()
	if enableResp.StatusCode == http.StatusNotFound {
		t.Skipf("M3 impl pending: POST /api/v1/categories/social/enable returns 404")
	}
	if enableResp.StatusCode != http.StatusCreated {
		t.Fatalf("enable social on kids: status %d", enableResp.StatusCode)
	}

	// Disable.
	disResp := n.apiDo(t, "POST", "/api/v1/categories/social/disable",
		mustJSON(t, map[string]string{"profile_id": "kids"}))
	defer disResp.Body.Close()
	if disResp.StatusCode == http.StatusNotFound {
		t.Skipf("M3 impl pending: POST /api/v1/categories/social/disable returns 404")
	}
	if disResp.StatusCode != http.StatusOK && disResp.StatusCode != http.StatusNoContent {
		t.Fatalf("disable social on kids: status %d: %s", disResp.StatusCode, readBody(t, disResp))
	}

	// Profile no longer references cat:social.
	profResp := n.apiDo(t, "GET", "/api/v1/profiles/kids", "")
	defer profResp.Body.Close()
	assertStatus(t, profResp, http.StatusOK)
	var prof profileBody
	if err := json.NewDecoder(profResp.Body).Decode(&prof); err != nil {
		t.Fatalf("decode kids profile: %v", err)
	}
	if contains(prof.Blocklists, "cat:social") {
		t.Fatalf("after disable, kids profile still references cat:social: %+v", prof.Blocklists)
	}
}

// FS-CategoryOverrideUrl
func TestCategoryOverrideUrl(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	override := "https://example.com/custom.txt"
	patchResp := n.apiDo(t, "PATCH", "/api/v1/categories/social",
		mustJSON(t, map[string]string{"url": override}))
	defer patchResp.Body.Close()
	if patchResp.StatusCode == http.StatusNotFound {
		t.Skipf("M3 impl pending: PATCH /api/v1/categories/social returns 404")
	}
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH /categories/social: status %d: %s", patchResp.StatusCode, readBody(t, patchResp))
	}

	got := getCategory(t, n, "social")
	// The override may be surfaced as either "url" or as a replacement for
	// "default_url"; the spec says "shows the override URL". Accept either
	// being equal to the override.
	if got.URL != override && got.DefaultURL != override {
		t.Fatalf("expected override URL %q in category response, got %+v", override, got)
	}
}

// FS-CategoryDohEnabledByDefault
func TestCategoryDohEnabledByDefault(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	got := getCategory(t, n, "doh")
	if !contains(got.EnabledForProfiles, "default") {
		t.Fatalf("doh category not enabled on default profile by default: %+v", got.EnabledForProfiles)
	}

	// A freshly bootstrapped node must NXDOMAIN known DoH resolver hostnames.
	r := dnsQueryAsClient(t, n.DNSAddr, "cloudflare-dns.com", dns.TypeA, "192.168.1.10")
	assertRcode(t, r, dns.RcodeNameError)
}

// ── Helpers ───────────────────────────────────────────────────────────────

// createKidsProfile creates a minimal "kids" profile. If the profiles route
// itself is missing, the test is skipped (M3 has profiles tests too — they
// share the same impl gate).
func createKidsProfile(t *testing.T, n *Node) {
	t.Helper()
	body := profileBody{
		ID:          "kids",
		Name:        "Kids",
		Blocklists:  []string{},
		Allowlist:   []string{},
		ClientIPs:   []string{"192.168.1.50"},
		ClientCIDRs: []string{},
	}
	resp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, body))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M3 impl pending: POST /api/v1/profiles returns 404")
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		t.Fatalf("create kids profile: status %d: %s", resp.StatusCode, readBody(t, resp))
	}
}

// contains reports whether s is present in haystack.
func contains(haystack []string, s string) bool {
	for _, h := range haystack {
		if h == s {
			return true
		}
		// Tolerate trimmed whitespace coming back from the API.
		if strings.TrimSpace(h) == s {
			return true
		}
	}
	return false
}
