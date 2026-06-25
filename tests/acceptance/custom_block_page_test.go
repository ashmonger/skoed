// Acceptance tests for the Custom Block Page feature (M26).
//
// FSIDs covered:
//   FS-BlockPageRedirectReturnsIP, FS-BlockPageRedirectServfailAAAA,
//   FS-BlockPageNonRedirectUnaffected, FS-BlockPageHttpServerResponds,
//   FS-BlockPageConfigGet, FS-BlockPageConfigPatch,
//   FS-BlockPageTitleInResponse

package acceptance

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// ── DNS behaviour ─────────────────────────────────────────────────────────────

// FS-BlockPageRedirectReturnsIP
// When block_policy="redirect", a blocked A query returns the configured IP.
func TestBlockPageRedirectReturnsIP(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "redirect",
		BlockPageIP:       "203.0.113.1",
	})
	addInlineBlocklist(t, n, "blocked", []string{"blocked.example.com"}, "")

	r := dnsQuery(t, n.DNSAddr, "blocked.example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerA(t, r, "203.0.113.1")
}

// FS-BlockPageRedirectServfailAAAA
// When block_policy="redirect" and no redirect_address_v6 is set, a blocked
// AAAA query returns NXDOMAIN (M33 updated the pre-M26 SERVFAIL behaviour).
func TestBlockPageRedirectServfailAAAA(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "redirect",
		BlockPageIP:       "203.0.113.1",
	})
	addInlineBlocklist(t, n, "blocked", []string{"blocked.example.com"}, "")

	r := dnsQuery(t, n.DNSAddr, "blocked.example.com", dns.TypeAAAA)
	assertRcode(t, r, dns.RcodeNameError)
}

// FS-BlockPageNonRedirectUnaffected
// Non-redirect policies are unaffected by block_page configuration.
func TestBlockPageNonRedirectUnaffected(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "blocked", []string{"blocked.example.com"}, "")

	r := dnsQuery(t, n.DNSAddr, "blocked.example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeNameError)
}

// ── Block page HTTP server ────────────────────────────────────────────────────

// FS-BlockPageHttpServerResponds
// The block page HTTP server responds with 200 and HTML on GET /.
func TestBlockPageHttpServerResponds(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "redirect",
		BlockPageIP:       "127.0.0.1",
	})

	// Wait for the block page server to be ready (it starts shortly after DNS).
	var resp *http.Response
	var err error
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err = http.Get(n.BlockPageURL + "/")
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("block page server not reachable at %s: %v", n.BlockPageURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("expected text/html content-type, got %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "<!DOCTYPE html>") {
		t.Fatalf("response body does not look like HTML: %s", body[:min(200, len(body))])
	}
}

// ── API endpoints ─────────────────────────────────────────────────────────────

// FS-BlockPageConfigGet
// GET /api/v1/blockpage returns the current block page configuration.
func TestBlockPageConfigGet(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "redirect",
		BlockPageIP:       "203.0.113.1",
	})

	resp := n.apiDo(t, "GET", "/api/v1/blockpage", "")
	assertStatus(t, resp, http.StatusOK)

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	resp.Body.Close()

	// The IP from config must be present.
	if ip, ok := payload["ip"]; !ok || ip != "203.0.113.1" {
		t.Fatalf("expected ip=203.0.113.1 in response, got: %v", payload)
	}
	// Port must be present (default 8053 or the one we set).
	if _, ok := payload["port"]; !ok {
		t.Fatalf("expected port field in response, got: %v", payload)
	}
}

// FS-BlockPageConfigPatch
// PATCH /api/v1/blockpage updates config; subsequent GET reflects new values.
func TestBlockPageConfigPersisted(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "redirect",
		BlockPageIP:       "203.0.113.1",
	})

	body := mustJSON(t, map[string]any{
		"title":   "Access Denied",
		"message": "Contact your network admin.",
	})
	patch := n.apiDo(t, "PATCH", "/api/v1/blockpage", body)
	assertStatus(t, patch, http.StatusOK)
	patch.Body.Close()

	get := n.apiDo(t, "GET", "/api/v1/blockpage", "")
	assertStatus(t, get, http.StatusOK)
	var payload map[string]any
	if err := json.NewDecoder(get.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	get.Body.Close()

	if title, _ := payload["title"].(string); title != "Access Denied" {
		t.Fatalf("expected title=Access Denied, got %q", title)
	}
	if msg, _ := payload["message"].(string); msg != "Contact your network admin." {
		t.Fatalf("expected updated message, got %q", msg)
	}
}

// FS-BlockPageTitleInResponse
// After PATCH sets a custom title, the block page HTML contains that title.
func TestBlockPageTitleInResponse(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "redirect",
		BlockPageIP:       "127.0.0.1",
	})

	// Wait for block page server.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(n.BlockPageURL + "/"); err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Update title via PATCH.
	customTitle := "My Custom Network Title"
	patch := n.apiDo(t, "PATCH", "/api/v1/blockpage", mustJSON(t, map[string]string{
		"title": customTitle,
	}))
	assertStatus(t, patch, http.StatusOK)
	patch.Body.Close()

	// Give the server a moment to propagate the new config.
	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get(n.BlockPageURL + "/")
	if err != nil {
		t.Fatalf("block page GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), customTitle) {
		t.Fatalf("block page body does not contain %q:\n%s", customTitle, body[:min(500, len(body))])
	}
}

// min returns the smaller of a and b (Go 1.21+ has built-in min, but keep explicit for compat).
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
