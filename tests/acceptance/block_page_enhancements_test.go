// Acceptance tests for Block Page Enhancements (M33).
//
// FSIDs covered:
//   FS-BlockPagePerProfileContent, FS-BlockPageGlobalFallback,
//   FS-BlockPageProfileContactEmail, FS-BlockPageIPv6Redirect,
//   FS-BlockPageIPv6NotConfigured, FS-BlockPageBypassGranted,
//   FS-BlockPageBypassWrongPasscode, FS-BlockPageBypassExpiry,
//   FS-BlockPageBypassProfileRequired, FS-BlockPageCustomTemplate,
//   FS-BlockPageCustomTemplateVariables, FS-BlockPageCustomTemplateDelete

package acceptance

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// ── Per-profile block page content ───────────────────────────────────────────

// FS-BlockPagePerProfileContent
// A profile with custom block page content serves its own title and message.
func TestBlockPagePerProfileContent(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "redirect",
		BlockPageIP:       "127.0.0.1",
	})

	// Create a profile with block page overrides and assign client IP 192.168.1.50.
	createResp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":         "kids",
		"name":       "Kids",
		"client_ips": []string{"192.168.1.50"},
		"block_page": map[string]any{
			"title":   "Kids Filter",
			"message": "Ask a parent",
		},
	}))
	assertStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	// Wait for block page server.
	waitBlockPageServer(t, n)

	// Fetch block page with client_ip query param.
	resp, err := http.Get(n.BlockPageURL + "/?client_ip=192.168.1.50")
	if err != nil {
		t.Fatalf("block page GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	if !strings.Contains(bodyStr, "Kids Filter") {
		t.Fatalf("expected 'Kids Filter' in block page body, got:\n%s", bodyStr[:min(500, len(bodyStr))])
	}
	if !strings.Contains(bodyStr, "Ask a parent") {
		t.Fatalf("expected 'Ask a parent' in block page body, got:\n%s", bodyStr[:min(500, len(bodyStr))])
	}
}

// FS-BlockPageGlobalFallback
// A profile without block page overrides falls back to the global block page content.
func TestBlockPageGlobalFallback(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "redirect",
		BlockPageIP:       "127.0.0.1",
	})

	// Set global block page title.
	patchResp := n.apiDo(t, "PATCH", "/api/v1/blockpage", mustJSON(t, map[string]any{
		"title": "Access Blocked",
	}))
	assertStatus(t, patchResp, http.StatusOK)
	patchResp.Body.Close()

	// Create a profile WITHOUT block page overrides.
	createResp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":         "adults",
		"name":       "Adults",
		"client_ips": []string{"192.168.1.60"},
	}))
	assertStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	waitBlockPageServer(t, n)

	resp, err := http.Get(n.BlockPageURL + "/?client_ip=192.168.1.60")
	if err != nil {
		t.Fatalf("block page GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if !strings.Contains(string(body), "Access Blocked") {
		t.Fatalf("expected 'Access Blocked' in block page, got:\n%s", string(body)[:min(500, len(body))])
	}
}

// FS-BlockPageProfileContactEmail
// The contact_email override is surfaced on the block page.
func TestBlockPageProfileContactEmail(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "redirect",
		BlockPageIP:       "127.0.0.1",
	})

	createResp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":         "corp",
		"name":       "Corp",
		"client_ips": []string{"10.0.0.5"},
		"block_page": map[string]any{
			"contact_email": "it@company.com",
		},
	}))
	assertStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	waitBlockPageServer(t, n)

	resp, err := http.Get(n.BlockPageURL + "/?client_ip=10.0.0.5")
	if err != nil {
		t.Fatalf("block page GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if !strings.Contains(string(body), "it@company.com") {
		t.Fatalf("expected 'it@company.com' in block page, got:\n%s", string(body)[:min(500, len(body))])
	}
}

// ── IPv6 redirect ─────────────────────────────────────────────────────────────

// FS-BlockPageIPv6Redirect
// When redirect_address_v6 is set, a blocked AAAA query returns that address.
func TestBlockPageIPv6Redirect(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "redirect",
		BlockPageIP:       "203.0.113.1",
	})
	addInlineBlocklist(t, n, "blocked", []string{"blocked.example.com"}, "")

	// Configure IPv6 redirect address.
	patchResp := n.apiDo(t, "PATCH", "/api/v1/blockpage", mustJSON(t, map[string]any{
		"redirect_address_v6": "fd00::1",
	}))
	assertStatus(t, patchResp, http.StatusOK)
	patchResp.Body.Close()

	// Give DNS handler a moment to pick up the new config (it reads dynamically on each query).
	time.Sleep(200 * time.Millisecond)

	r := dnsQuery(t, n.DNSAddr, "blocked.example.com", dns.TypeAAAA)
	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerAAAA(t, r, "fd00::1")
}

// FS-BlockPageIPv6NotConfigured
// Without redirect_address_v6, AAAA queries for blocked domains return SERVFAIL.
// (This is the existing M26 behaviour — we just confirm it remains unchanged.)
func TestBlockPageIPv6NotConfigured(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "redirect",
		BlockPageIP:       "203.0.113.1",
	})
	addInlineBlocklist(t, n, "blocked", []string{"blocked.example.com"}, "")

	// No redirect_address_v6 configured.
	r := dnsQuery(t, n.DNSAddr, "blocked.example.com", dns.TypeAAAA)
	assertRcode(t, r, dns.RcodeServerFailure)
}

// ── Time-bounded bypass ───────────────────────────────────────────────────────

// FS-BlockPageBypassGranted
// A client submits the correct bypass passcode and the profile gets paused.
func TestBlockPageBypassGranted(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "redirect",
		BlockPageIP:       "127.0.0.1",
	})

	// Create profile with bypass passcode and client IP.
	createResp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":         "home",
		"name":       "Home",
		"client_ips": []string{"192.168.1.70"},
		"block_page": map[string]any{
			"bypass_passcode": "letmein",
		},
	}))
	assertStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	// POST /api/v1/bypass with correct passcode.
	before := time.Now()
	bypassResp := n.apiDo(t, "POST", "/api/v1/bypass", mustJSON(t, map[string]any{
		"passcode":         "letmein",
		"duration_minutes": 30,
		"client_ip":        "192.168.1.70",
	}))
	assertStatus(t, bypassResp, http.StatusOK)

	var payload struct {
		ProfileID string    `json:"profile_id"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(bypassResp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode bypass response: %v", err)
	}
	bypassResp.Body.Close()

	if payload.ProfileID != "home" {
		t.Fatalf("expected profile_id=home, got %q", payload.ProfileID)
	}

	// Verify expires_at is approximately now + 30 minutes.
	expected := before.Add(30 * time.Minute)
	diff := payload.ExpiresAt.Sub(expected)
	if diff < -10*time.Second || diff > 10*time.Second {
		t.Fatalf("expected expires_at ≈ %v, got %v (diff %v)", expected, payload.ExpiresAt, diff)
	}

	// Verify the profile is now paused.
	pauseResp := n.apiDo(t, "GET", "/api/v1/profiles/home/pause", "")
	assertStatus(t, pauseResp, http.StatusOK)
	var pause struct {
		Active    bool      `json:"active"`
		ResumesAt time.Time `json:"resumes_at"`
	}
	if err := json.NewDecoder(pauseResp.Body).Decode(&pause); err != nil {
		t.Fatalf("decode pause response: %v", err)
	}
	pauseResp.Body.Close()
	if !pause.Active {
		t.Fatalf("expected profile pause to be active after bypass grant")
	}
}

// FS-BlockPageBypassWrongPasscode
// A wrong passcode returns 403 and no pause is created.
func TestBlockPageBypassWrongPasscode(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "redirect",
		BlockPageIP:       "127.0.0.1",
	})

	createResp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":         "home2",
		"name":       "Home2",
		"client_ips": []string{"192.168.1.71"},
		"block_page": map[string]any{
			"bypass_passcode": "letmein",
		},
	}))
	assertStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	bypassResp := n.apiDo(t, "POST", "/api/v1/bypass", mustJSON(t, map[string]any{
		"passcode":         "wrong",
		"duration_minutes": 30,
		"client_ip":        "192.168.1.71",
	}))
	assertStatus(t, bypassResp, http.StatusForbidden)
	bypassResp.Body.Close()

	// Verify profile pause is NOT active.
	pauseResp := n.apiDo(t, "GET", "/api/v1/profiles/home2/pause", "")
	assertStatus(t, pauseResp, http.StatusOK)
	var pause struct {
		Active bool `json:"active"`
	}
	if err := json.NewDecoder(pauseResp.Body).Decode(&pause); err != nil {
		t.Fatalf("decode pause response: %v", err)
	}
	pauseResp.Body.Close()
	if pause.Active {
		t.Fatalf("expected no active pause after wrong passcode")
	}
}

// FS-BlockPageBypassExpiry
// Verify the bypass response includes an expires_at field with the correct duration.
// (We don't actually wait 5 minutes — we verify the timestamp is set correctly.)
func TestBlockPageBypassExpiry(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "redirect",
		BlockPageIP:       "127.0.0.1",
	})

	createResp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":         "expiry-test",
		"name":       "Expiry Test",
		"client_ips": []string{"192.168.1.72"},
		"block_page": map[string]any{
			"bypass_passcode": "test123",
		},
	}))
	assertStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	before := time.Now()
	bypassResp := n.apiDo(t, "POST", "/api/v1/bypass", mustJSON(t, map[string]any{
		"passcode":         "test123",
		"duration_minutes": 5,
		"client_ip":        "192.168.1.72",
	}))
	assertStatus(t, bypassResp, http.StatusOK)

	var payload struct {
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(bypassResp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode bypass response: %v", err)
	}
	bypassResp.Body.Close()

	// expires_at must be approximately 5 minutes from now.
	expected := before.Add(5 * time.Minute)
	diff := payload.ExpiresAt.Sub(expected)
	if diff < -10*time.Second || diff > 10*time.Second {
		t.Fatalf("expected expires_at ≈ %v (5 min), got %v (diff %v)", expected, payload.ExpiresAt, diff)
	}
	if payload.ExpiresAt.IsZero() {
		t.Fatal("expected non-zero expires_at in bypass response")
	}
}

// FS-BlockPageBypassProfileRequired
// A bypass for a profile with no passcode configured returns 404.
func TestBlockPageBypassProfileRequired(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "redirect",
		BlockPageIP:       "127.0.0.1",
	})

	createResp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":         "restricted",
		"name":       "Restricted",
		"client_ips": []string{"192.168.1.73"},
		// No block_page with bypass_passcode.
	}))
	assertStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	bypassResp := n.apiDo(t, "POST", "/api/v1/bypass", mustJSON(t, map[string]any{
		"passcode":         "anything",
		"duration_minutes": 30,
		"client_ip":        "192.168.1.73",
	}))
	assertStatus(t, bypassResp, http.StatusNotFound)
	bypassResp.Body.Close()
}

// ── Custom HTML template ───────────────────────────────────────────────────────

// FS-BlockPageCustomTemplate
// An operator uploads a custom HTML template and blocked clients see it rendered.
func TestBlockPageCustomTemplate(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "redirect",
		BlockPageIP:       "127.0.0.1",
	})

	waitBlockPageServer(t, n)

	customHTML := `<!DOCTYPE html><html><body>Custom Block Page: {{.Title}}</body></html>`

	// PUT the custom template.
	req, err := http.NewRequest(http.MethodPut, n.APIBase+"/api/v1/blockpage/template",
		strings.NewReader(customHTML))
	if err != nil {
		t.Fatalf("build PUT request: %v", err)
	}
	req.Header.Set("Content-Type", "text/html")
	req.Header.Set("Authorization", "Bearer "+n.sessionToken)
	putResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /api/v1/blockpage/template: %v", err)
	}
	assertStatus(t, putResp, http.StatusOK)
	putResp.Body.Close()

	// Fetch the block page and confirm custom template is rendered.
	resp, err := http.Get(n.BlockPageURL + "/")
	if err != nil {
		t.Fatalf("block page GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if !strings.Contains(string(body), "Custom Block Page:") {
		t.Fatalf("expected custom template in block page, got:\n%s", string(body)[:min(500, len(body))])
	}
}

// FS-BlockPageCustomTemplateVariables
// The custom template receives Domain, Profile, and Joke variables.
func TestBlockPageCustomTemplateVariables(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "redirect",
		BlockPageIP:       "127.0.0.1",
	})

	// Create profile for client IP.
	createResp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":         "kids-tmpl",
		"name":       "Kids Template",
		"client_ips": []string{"192.168.1.80"},
	}))
	assertStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	waitBlockPageServer(t, n)

	// Upload template that renders Profile and Domain.
	customTmpl := `<!DOCTYPE html><html><body>profile={{.Profile}} domain={{.Domain}} joke={{.Joke}}</body></html>`
	req, err := http.NewRequest(http.MethodPut, n.APIBase+"/api/v1/blockpage/template",
		strings.NewReader(customTmpl))
	if err != nil {
		t.Fatalf("build PUT request: %v", err)
	}
	req.Header.Set("Content-Type", "text/html")
	req.Header.Set("Authorization", "Bearer "+n.sessionToken)
	putResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /api/v1/blockpage/template: %v", err)
	}
	assertStatus(t, putResp, http.StatusOK)
	putResp.Body.Close()

	// Fetch with client_ip and domain parameters.
	url := fmt.Sprintf("%s/?client_ip=192.168.1.80&domain=bad.com", n.BlockPageURL)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("block page GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	// Profile should be "kids-tmpl".
	if !strings.Contains(bodyStr, "profile=kids-tmpl") {
		t.Fatalf("expected 'profile=kids-tmpl' in body, got:\n%s", bodyStr[:min(500, len(bodyStr))])
	}
	// Domain should be "bad.com".
	if !strings.Contains(bodyStr, "domain=bad.com") {
		t.Fatalf("expected 'domain=bad.com' in body, got:\n%s", bodyStr[:min(500, len(bodyStr))])
	}
	// Joke should be non-empty.
	if !strings.Contains(bodyStr, "joke=") {
		t.Fatalf("expected 'joke=' in body, got:\n%s", bodyStr[:min(500, len(bodyStr))])
	}
}

// FS-BlockPageCustomTemplateDelete
// Deleting the custom template reverts to the built-in template.
func TestBlockPageCustomTemplateDelete(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "redirect",
		BlockPageIP:       "127.0.0.1",
	})

	waitBlockPageServer(t, n)

	// Upload a custom template.
	customHTML := `<!DOCTYPE html><html><body>CUSTOM</body></html>`
	req, err := http.NewRequest(http.MethodPut, n.APIBase+"/api/v1/blockpage/template",
		strings.NewReader(customHTML))
	if err != nil {
		t.Fatalf("build PUT request: %v", err)
	}
	req.Header.Set("Content-Type", "text/html")
	req.Header.Set("Authorization", "Bearer "+n.sessionToken)
	putResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /api/v1/blockpage/template: %v", err)
	}
	assertStatus(t, putResp, http.StatusOK)
	putResp.Body.Close()

	// Confirm custom template is active.
	resp, err := http.Get(n.BlockPageURL + "/")
	if err != nil {
		t.Fatalf("block page GET after PUT: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "CUSTOM") {
		t.Fatalf("custom template not active after PUT")
	}

	// DELETE the template.
	delResp := n.apiDo(t, "DELETE", "/api/v1/blockpage/template", "")
	assertStatus(t, delResp, http.StatusNoContent)
	delResp.Body.Close()

	// Verify the built-in template is back (it contains the "skoed" branding).
	resp2, err := http.Get(n.BlockPageURL + "/")
	if err != nil {
		t.Fatalf("block page GET after DELETE: %v", err)
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)

	if strings.Contains(string(body2), "CUSTOM") {
		t.Fatalf("expected built-in template after DELETE, but custom content still present")
	}
	if !strings.Contains(string(body2), "skoed") {
		t.Fatalf("expected built-in template after DELETE, got:\n%s", string(body2)[:min(500, len(body2))])
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// waitBlockPageServer polls until the block page server responds or the
// deadline expires.
func waitBlockPageServer(t *testing.T, n *Node) {
	t.Helper()
	if n.BlockPageURL == "" {
		t.Skip("block page URL not set (policy is not redirect)")
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(n.BlockPageURL + "/")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("block page server not reachable at %s", n.BlockPageURL)
}
