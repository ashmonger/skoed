// Acceptance tests for the filtering-pause feature.
//
// FSIDs covered (one Go test per FSID):
//   FS-FilterPauseGlobalSuspendsAllProfiles          → TestFilterPauseGlobalSuspendsAllProfiles
//   FS-FilterPauseGlobalExpiresAutomatically         → TestFilterPauseGlobalExpiresAutomatically
//   FS-FilterPauseGlobalCancelledEarly               → TestFilterPauseGlobalCancelledEarly
//   FS-FilterPauseGlobalSurvivesRestart              → TestFilterPauseGlobalSurvivesRestart
//   FS-FilterPauseGlobalEnforcedCeiling              → TestFilterPauseGlobalEnforcedCeiling
//   FS-FilterPauseGlobalIdempotentWhileActive        → TestFilterPauseGlobalIdempotentWhileActive
//   FS-FilterPauseQueryLogMarkedDuringGlobalPause    → TestFilterPauseQueryLogMarkedDuringGlobalPause
//   FS-FilterPauseProfileSuspendsOneProfile          → TestFilterPauseProfileSuspendsOneProfile
//   FS-FilterPauseProfileDoesNotAffectOtherProfiles  → TestFilterPauseProfileDoesNotAffectOtherProfiles
//   FS-FilterPauseProfileExpiresAutomatically        → TestFilterPauseProfileExpiresAutomatically
//   FS-FilterPauseProfileCancelledEarly              → TestFilterPauseProfileCancelledEarly
//   FS-FilterPauseProfileMultipleSimultaneous        → TestFilterPauseProfileMultipleSimultaneous
//   FS-FilterPauseGlobalOverridesProfile             → TestFilterPauseGlobalOverridesProfile
//   FS-FilterPauseCeilingEnforced                    → TestFilterPauseCeilingEnforced
//   FS-FilterPauseFeatureDisabledWhenCeilingZero     → TestFilterPauseFeatureDisabledWhenCeilingZero
//   FS-FilterPauseQueryLogMarkedDuringProfilePause   → TestFilterPauseQueryLogMarkedDuringProfilePause
//
// Strategy:
//   - Each test starts its own single-node cluster via startCluster(t, 1) so
//     tests are fully isolated from each other.
//   - Global pause: POST /api/v1/filtering/pause  {"duration_seconds": N}
//                   DELETE /api/v1/filtering/pause
//                   GET    /api/v1/filtering/pause
//   - Profile pause: POST /api/v1/profiles/{id}/pause  {"duration_seconds": N}
//                    DELETE /api/v1/profiles/{id}/pause
//                    GET    /api/v1/profiles/{id}/pause
//   - Settings ceiling: PATCH /api/v1/settings {"filtering":{"pause_max_seconds":N}}
//   - DNS verification uses dnsQueryAsClient with EDNS0 option 65500 (SKOED_TEST_MODE)
//     to simulate client IPs for per-profile tests.
//   - Skip-on-404/501 guards compile the file cleanly before the M10 impl lands.

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// ── API helpers ───────────────────────────────────────────────────────────────

// postGlobalPause calls POST /api/v1/filtering/pause with the given seconds.
// Returns (status, body). Skips the test if the route is not yet implemented.
func postGlobalPause(t *testing.T, n *Node, seconds int) (int, string) {
	t.Helper()
	body := mustJSON(t, map[string]any{"duration_seconds": seconds})
	resp := n.apiDo(t, "POST", "/api/v1/filtering/pause", body)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNotImplemented {
		t.Skipf("M10 impl pending: POST /api/v1/filtering/pause returns %d", resp.StatusCode)
	}
	return resp.StatusCode, readBody(t, resp)
}

// deleteGlobalPause calls DELETE /api/v1/filtering/pause.
func deleteGlobalPause(t *testing.T, n *Node) int {
	t.Helper()
	resp := n.apiDo(t, "DELETE", "/api/v1/filtering/pause", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNotImplemented {
		t.Skipf("M10 impl pending: DELETE /api/v1/filtering/pause returns %d", resp.StatusCode)
	}
	return resp.StatusCode
}

// getGlobalPause calls GET /api/v1/filtering/pause and returns the decoded body
// as a map. Returns nil when the server responds with 404 (not yet implemented).
func getGlobalPause(t *testing.T, n *Node) map[string]any {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/filtering/pause", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNotImplemented {
		t.Skipf("M10 impl pending: GET /api/v1/filtering/pause returns %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/filtering/pause: status %d", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode GET /api/v1/filtering/pause: %v", err)
	}
	return out
}

// postProfilePause calls POST /api/v1/profiles/{id}/pause with the given seconds.
func postProfilePause(t *testing.T, n *Node, profileID string, seconds int) (int, string) {
	t.Helper()
	body := mustJSON(t, map[string]any{"duration_seconds": seconds})
	resp := n.apiDo(t, "POST", fmt.Sprintf("/api/v1/profiles/%s/pause", profileID), body)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNotImplemented {
		t.Skipf("M10 impl pending: POST /api/v1/profiles/%s/pause returns %d", profileID, resp.StatusCode)
	}
	return resp.StatusCode, readBody(t, resp)
}

// deleteProfilePause calls DELETE /api/v1/profiles/{id}/pause.
func deleteProfilePause(t *testing.T, n *Node, profileID string) int {
	t.Helper()
	resp := n.apiDo(t, "DELETE", fmt.Sprintf("/api/v1/profiles/%s/pause", profileID), "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNotImplemented {
		t.Skipf("M10 impl pending: DELETE /api/v1/profiles/%s/pause returns %d", profileID, resp.StatusCode)
	}
	return resp.StatusCode
}

// getProfilePause calls GET /api/v1/profiles/{id}/pause.
func getProfilePause(t *testing.T, n *Node, profileID string) map[string]any {
	t.Helper()
	resp := n.apiDo(t, "GET", fmt.Sprintf("/api/v1/profiles/%s/pause", profileID), "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNotImplemented {
		t.Skipf("M10 impl pending: GET /api/v1/profiles/%s/pause returns %d", profileID, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/profiles/%s/pause: status %d", profileID, resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode GET /api/v1/profiles/%s/pause: %v", profileID, err)
	}
	return out
}

// setPauseMaxSeconds patches filtering.pause_max_seconds in settings.
// Skips the test when the route is not yet registered.
func setPauseMaxSeconds(t *testing.T, n *Node, maxSeconds int) {
	t.Helper()
	resp := n.apiDo(t, "PATCH", "/api/v1/settings", mustJSON(t, map[string]any{
		"filtering": map[string]any{"pause_max_seconds": maxSeconds},
	}))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M10 impl pending: PATCH /api/v1/settings returns 404")
	}
	if resp.StatusCode != http.StatusOK {
		b := readBody(t, resp)
		t.Fatalf("PATCH /api/v1/settings pause_max_seconds=%d: status %d: %s", maxSeconds, resp.StatusCode, b)
	}
}

// pauseIsActive reads GET /api/v1/filtering/pause and returns whether
// active=true is present in the response body. Tolerates the field being
// absent (treated as false) but does NOT skip: callers that reach this
// point have already called postGlobalPause which would have skipped first.
func pauseIsActive(t *testing.T, n *Node) bool {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/filtering/pause", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false
	}
	v, _ := out["active"].(bool)
	return v
}

// profilePauseIsActive reads GET /api/v1/profiles/{id}/pause and returns
// whether active=true is present.
func profilePauseIsActive(t *testing.T, n *Node, profileID string) bool {
	t.Helper()
	resp := n.apiDo(t, "GET", fmt.Sprintf("/api/v1/profiles/%s/pause", profileID), "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false
	}
	v, _ := out["active"].(bool)
	return v
}

// ── shared setup helpers ──────────────────────────────────────────────────────

// startPauseNode starts a forwarding single-node cluster with an upstream
// that resolves every A query to 1.2.3.4. Registers a "ads" inline blocklist
// that blocks "doubleclick.net". Returns the node and the upstream address.
func startPauseNode(t *testing.T) *Node {
	t.Helper()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"doubleclick.net"}, "")
	return n
}

// startPauseClusterNode starts a 1-node cluster and seeds a fake upstream +
// blocklist via the API. Returns the cluster (needed for KillNode/RestartNode).
func startPauseClusterNode(t *testing.T) (*Cluster, *ClusterNode) {
	t.Helper()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	c := startCluster(t, 1)
	leader := c.Leader(t)
	// Wire the fake upstream via settings PATCH (cluster-level, Raft-replicated).
	resp := leader.apiDo(t, "PATCH", "/api/v1/settings", mustJSON(t, map[string]any{
		"dns": map[string]any{
			"mode":              "forwarding",
			"upstream_resolvers": []string{upstream},
		},
	}))
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	// Seed the blocklist on the cluster.
	addInlineBlocklist(t, leader.Node, "ads", []string{"doubleclick.net"}, "")
	return c, leader
}

// assertBlocked asserts that doubleclick.net is blocked (NXDOMAIN) for the
// given node using the loopback address as client.
func assertBlocked(t *testing.T, n *Node) {
	t.Helper()
	r := dnsQuery(t, n.DNSAddr, "doubleclick.net", dns.TypeA)
	if r.Rcode != dns.RcodeNameError {
		t.Errorf("expected NXDOMAIN (blocked), got %s", dns.RcodeToString[r.Rcode])
	}
}

// assertNotBlocked asserts that doubleclick.net is NOT blocked (resolves or
// at least does not return NXDOMAIN) for the given node.
func assertNotBlocked(t *testing.T, n *Node) {
	t.Helper()
	r := dnsQuery(t, n.DNSAddr, "doubleclick.net", dns.TypeA)
	if r.Rcode == dns.RcodeNameError {
		t.Errorf("expected domain to resolve (pause active), got NXDOMAIN")
	}
}

// assertBlockedAsClient asserts NXDOMAIN for doubleclick.net from the given client IP.
func assertBlockedAsClient(t *testing.T, n *Node, clientIP string) {
	t.Helper()
	r := dnsQueryAsClient(t, n.DNSAddr, "doubleclick.net", dns.TypeA, clientIP)
	if r.Rcode != dns.RcodeNameError {
		t.Errorf("client %s: expected NXDOMAIN (blocked), got %s", clientIP, dns.RcodeToString[r.Rcode])
	}
}

// assertNotBlockedAsClient asserts that doubleclick.net is NOT NXDOMAIN from
// the given client IP (pause is active for that client).
func assertNotBlockedAsClient(t *testing.T, n *Node, clientIP string) {
	t.Helper()
	r := dnsQueryAsClient(t, n.DNSAddr, "doubleclick.net", dns.TypeA, clientIP)
	if r.Rcode == dns.RcodeNameError {
		t.Errorf("client %s: expected domain to resolve (pause active), got NXDOMAIN", clientIP)
	}
}

// pollUntilNotBlocked polls DNS until doubleclick.net resolves (not NXDOMAIN)
// or maxWait expires.
func pollUntilNotBlocked(t *testing.T, n *Node, maxWait time.Duration) {
	t.Helper()
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		r := dnsQuery(t, n.DNSAddr, "doubleclick.net", dns.TypeA)
		if r.Rcode != dns.RcodeNameError {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("doubleclick.net still blocked after %s (pause did not take effect)", maxWait)
}

// pollUntilBlocked polls DNS until doubleclick.net is NXDOMAIN again (pause
// expired or was cancelled) or maxWait expires.
func pollUntilBlocked(t *testing.T, n *Node, maxWait time.Duration) {
	t.Helper()
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		r := dnsQuery(t, n.DNSAddr, "doubleclick.net", dns.TypeA)
		if r.Rcode == dns.RcodeNameError {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("doubleclick.net still unblocked after %s (pause should have expired)", maxWait)
}

// ── Global pause tests ────────────────────────────────────────────────────────

// FSID: FS-FilterPauseGlobalSuspendsAllProfiles
func TestFilterPauseGlobalSuspendsAllProfiles(t *testing.T) {
	t.Parallel()
	n := startPauseNode(t)

	// Confirm the domain is blocked before the pause.
	assertBlocked(t, n)

	// Activate a 600s global pause.
	status, body := postGlobalPause(t, n, 600)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST /api/v1/filtering/pause: want 201/200, got %d: %s", status, body)
	}

	// Domain must now resolve (or at least not return NXDOMAIN).
	pollUntilNotBlocked(t, n, 5*time.Second)

	// Safe domain (not blocked) still resolves normally — pause does not break DNS.
	r := dnsQuery(t, n.DNSAddr, "safe.example.com", dns.TypeA)
	if r.Rcode == dns.RcodeNameError {
		t.Errorf("safe domain returned NXDOMAIN during pause — allowlist or unblocked domain broken")
	}
}

// FSID: FS-FilterPauseGlobalExpiresAutomatically
func TestFilterPauseGlobalExpiresAutomatically(t *testing.T) {
	t.Parallel()
	n := startPauseNode(t)

	// Activate a 2s pause.
	status, body := postGlobalPause(t, n, 2)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST /api/v1/filtering/pause (2s): want 201/200, got %d: %s", status, body)
	}

	// Wait for the pause to take effect.
	pollUntilNotBlocked(t, n, 3*time.Second)

	// Sleep past the 2s pause window (3s total margin).
	time.Sleep(3 * time.Second)

	// Blocking must have resumed.
	pollUntilBlocked(t, n, 3*time.Second)
}

// FSID: FS-FilterPauseGlobalCancelledEarly
func TestFilterPauseGlobalCancelledEarly(t *testing.T) {
	t.Parallel()
	n := startPauseNode(t)

	// Activate a 600s pause.
	status, body := postGlobalPause(t, n, 600)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST /api/v1/filtering/pause: want 201/200, got %d: %s", status, body)
	}
	pollUntilNotBlocked(t, n, 5*time.Second)

	// Cancel the pause early.
	deleteStatus := deleteGlobalPause(t, n)
	if deleteStatus != http.StatusNoContent && deleteStatus != http.StatusOK {
		t.Fatalf("DELETE /api/v1/filtering/pause: want 204/200, got %d", deleteStatus)
	}

	// Blocking must resume promptly.
	pollUntilBlocked(t, n, 5*time.Second)
}

// FSID: FS-FilterPauseGlobalSurvivesRestart
func TestFilterPauseGlobalSurvivesRestart(t *testing.T) {
	t.Parallel()

	// Use the cluster harness so KillNode/RestartNode is available.
	c, leader := startPauseClusterNode(t)

	// Activate a 600s pause.
	status, body := postGlobalPause(t, leader.Node, 600)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST /api/v1/filtering/pause: want 201/200, got %d: %s", status, body)
	}
	pollUntilNotBlocked(t, leader.Node, 5*time.Second)

	// Kill and restart the node — the pause state is persisted.
	c.KillNode(t, 0)
	c.RestartNode(t, 0)

	node := c.Node(0).Node

	// GET must still report active=true.
	if !pauseIsActive(t, node) {
		t.Fatalf("after restart: GET /api/v1/filtering/pause should still report active=true")
	}

	// DNS: domain should still not be blocked.
	assertNotBlocked(t, node)
}

// FSID: FS-FilterPauseGlobalEnforcedCeiling
func TestFilterPauseGlobalEnforcedCeiling(t *testing.T) {
	t.Parallel()
	n := startPauseNode(t)

	// Set ceiling to 5 seconds.
	setPauseMaxSeconds(t, n, 5)

	// Attempt to POST a 10s pause — must be rejected.
	status, body := postGlobalPause(t, n, 10)
	if status != http.StatusBadRequest {
		t.Fatalf("POST /api/v1/filtering/pause (10s, ceiling=5): want 400, got %d: %s", status, body)
	}
	low := strings.ToLower(body)
	if !strings.Contains(low, "max") && !strings.Contains(low, "ceiling") && !strings.Contains(low, "exceed") {
		t.Errorf("400 body should mention the ceiling constraint; got %q", body)
	}

	// Confirm the domain is still blocked (pause was not activated).
	assertBlocked(t, n)
}

// FSID: FS-FilterPauseGlobalIdempotentWhileActive
func TestFilterPauseGlobalIdempotentWhileActive(t *testing.T) {
	t.Parallel()
	n := startPauseNode(t)

	// POST a 600s pause.
	status, body := postGlobalPause(t, n, 600)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST /api/v1/filtering/pause (600s): want 201/200, got %d: %s", status, body)
	}
	pollUntilNotBlocked(t, n, 5*time.Second)

	// POST a 2s pause — replaces (or shortens) the active window.
	status2, body2 := postGlobalPause(t, n, 2)
	if status2 != http.StatusCreated && status2 != http.StatusOK {
		t.Fatalf("POST /api/v1/filtering/pause (2s): want 201/200, got %d: %s", status2, body2)
	}

	// GET must still report active.
	state := getGlobalPause(t, n)
	active, _ := state["active"].(bool)
	if !active {
		t.Fatalf("after second POST: GET /api/v1/filtering/pause should report active=true; got %v", state)
	}

	// Sleep past 2s — the 2s window must have expired.
	time.Sleep(3 * time.Second)

	// Block must have resumed (the shorter 2s window now governs).
	pollUntilBlocked(t, n, 3*time.Second)
}

// FSID: FS-FilterPauseQueryLogMarkedDuringGlobalPause
func TestFilterPauseQueryLogMarkedDuringGlobalPause(t *testing.T) {
	t.Parallel()
	n := startPauseNode(t)

	// Activate a 2s pause.
	status, body := postGlobalPause(t, n, 2)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST /api/v1/filtering/pause (2s): want 201/200, got %d: %s", status, body)
	}
	pollUntilNotBlocked(t, n, 3*time.Second)

	// Issue a query while paused.
	dnsQuery(t, n.DNSAddr, "doubleclick.net", dns.TypeA)

	// Wait for the log entry (outcome=forwarded while paused).
	entry := waitForLog(t, n, "doubleclick.net", "forwarded", 3*time.Second)

	// The log entry must carry pause_active=true.
	v, ok := entry["pause_active"]
	if !ok {
		t.Errorf("query log entry during global pause missing pause_active field: %v", entry)
	} else if active, _ := v.(bool); !active {
		t.Errorf("query log entry during global pause: pause_active should be true, got %v", v)
	}

	// Wait for the 2s pause to expire.
	time.Sleep(3 * time.Second)
	pollUntilBlocked(t, n, 3*time.Second)

	// Issue a query while unpaused (domain is blocked → outcome=blocked).
	dnsQuery(t, n.DNSAddr, "doubleclick.net", dns.TypeA)

	// Now find an entry where pause_active is false or absent.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		entries := getLog(t, n, "")
		for _, e := range entries {
			d, _ := e["domain"].(string)
			if d != "doubleclick.net" {
				continue
			}
			if pa, _ := e["pause_active"].(bool); !pa {
				// Found an entry with pause_active=false (or absent).
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("no query log entry for doubleclick.net with pause_active=false after pause expired")
}

// ── Per-profile pause tests ───────────────────────────────────────────────────

// FSID: FS-FilterPauseProfileSuspendsOneProfile
func TestFilterPauseProfileSuspendsOneProfile(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"doubleclick.net"}, "")

	// Create "kids" profile bound to 192.168.10.0/24.
	resp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":           "kids",
		"name":         "Kids",
		"blocklists":   []string{"ads"},
		"allowlist":    []string{},
		"client_cidrs": []string{"192.168.10.0/24"},
		"client_ips":   []string{},
	}))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M10 impl pending: POST /api/v1/profiles returns 404")
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create kids profile: status %d: %s", resp.StatusCode, readBody(t, resp))
	}

	// Create "work" profile bound to 192.168.20.0/24 — should remain blocked.
	resp2 := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":           "work",
		"name":         "Work",
		"blocklists":   []string{"ads"},
		"allowlist":    []string{},
		"client_cidrs": []string{"192.168.20.0/24"},
		"client_ips":   []string{},
	}))
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("create work profile: status %d: %s", resp2.StatusCode, readBody(t, resp2))
	}

	// Both clients should be blocked before the pause.
	assertBlockedAsClient(t, n, "192.168.10.5")
	assertBlockedAsClient(t, n, "192.168.20.5")

	// Activate pause on the kids profile.
	status, body := postProfilePause(t, n, "kids", 600)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST /api/v1/profiles/kids/pause: want 201/200, got %d: %s", status, body)
	}

	// Kids client (192.168.10.x) should now resolve.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r := dnsQueryAsClient(t, n.DNSAddr, "doubleclick.net", dns.TypeA, "192.168.10.5")
		if r.Rcode != dns.RcodeNameError {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	assertNotBlockedAsClient(t, n, "192.168.10.5")

	// Work client (192.168.20.x) must remain blocked.
	assertBlockedAsClient(t, n, "192.168.20.5")
}

// FSID: FS-FilterPauseProfileDoesNotAffectOtherProfiles
func TestFilterPauseProfileDoesNotAffectOtherProfiles(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"doubleclick.net"}, "")

	// Create "alpha" profile.
	resp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":           "alpha",
		"name":         "Alpha",
		"blocklists":   []string{"ads"},
		"allowlist":    []string{},
		"client_cidrs": []string{"10.1.0.0/24"},
		"client_ips":   []string{},
	}))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M10 impl pending: POST /api/v1/profiles returns 404")
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create alpha profile: status %d: %s", resp.StatusCode, readBody(t, resp))
	}

	// Create "beta" profile.
	resp2 := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":           "beta",
		"name":         "Beta",
		"blocklists":   []string{"ads"},
		"allowlist":    []string{},
		"client_cidrs": []string{"10.2.0.0/24"},
		"client_ips":   []string{},
	}))
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("create beta profile: status %d: %s", resp2.StatusCode, readBody(t, resp2))
	}

	// Pause only the alpha profile.
	status, body := postProfilePause(t, n, "alpha", 600)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST /api/v1/profiles/alpha/pause: want 201/200, got %d: %s", status, body)
	}

	// Alpha client resolves.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r := dnsQueryAsClient(t, n.DNSAddr, "doubleclick.net", dns.TypeA, "10.1.0.5")
		if r.Rcode != dns.RcodeNameError {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	assertNotBlockedAsClient(t, n, "10.1.0.5")

	// Beta client must remain blocked — its profile is not paused.
	assertBlockedAsClient(t, n, "10.2.0.5")
}

// FSID: FS-FilterPauseProfileExpiresAutomatically
func TestFilterPauseProfileExpiresAutomatically(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"doubleclick.net"}, "")

	// Create a profile.
	resp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":           "home",
		"name":         "Home",
		"blocklists":   []string{"ads"},
		"allowlist":    []string{},
		"client_cidrs": []string{"192.168.30.0/24"},
		"client_ips":   []string{},
	}))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M10 impl pending: POST /api/v1/profiles returns 404")
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create home profile: status %d: %s", resp.StatusCode, readBody(t, resp))
	}

	assertBlockedAsClient(t, n, "192.168.30.5")

	// Activate a 2s pause.
	status, body := postProfilePause(t, n, "home", 2)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST /api/v1/profiles/home/pause (2s): want 201/200, got %d: %s", status, body)
	}

	// Wait for pause to take effect.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r := dnsQueryAsClient(t, n.DNSAddr, "doubleclick.net", dns.TypeA, "192.168.30.5")
		if r.Rcode != dns.RcodeNameError {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Sleep past 2s pause.
	time.Sleep(3 * time.Second)

	// Block must have resumed.
	deadline2 := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline2) {
		r := dnsQueryAsClient(t, n.DNSAddr, "doubleclick.net", dns.TypeA, "192.168.30.5")
		if r.Rcode == dns.RcodeNameError {
			return // success
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("profile pause did not expire: doubleclick.net still resolves for 192.168.30.5")
}

// FSID: FS-FilterPauseProfileCancelledEarly
func TestFilterPauseProfileCancelledEarly(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"doubleclick.net"}, "")

	// Create a profile.
	resp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":           "guest",
		"name":         "Guest",
		"blocklists":   []string{"ads"},
		"allowlist":    []string{},
		"client_cidrs": []string{"192.168.40.0/24"},
		"client_ips":   []string{},
	}))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M10 impl pending: POST /api/v1/profiles returns 404")
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create guest profile: status %d: %s", resp.StatusCode, readBody(t, resp))
	}

	assertBlockedAsClient(t, n, "192.168.40.5")

	// Activate a 600s pause.
	status, body := postProfilePause(t, n, "guest", 600)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST /api/v1/profiles/guest/pause: want 201/200, got %d: %s", status, body)
	}

	// Wait for pause to take effect.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r := dnsQueryAsClient(t, n.DNSAddr, "doubleclick.net", dns.TypeA, "192.168.40.5")
		if r.Rcode != dns.RcodeNameError {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	assertNotBlockedAsClient(t, n, "192.168.40.5")

	// Cancel the pause.
	deleteStatus := deleteProfilePause(t, n, "guest")
	if deleteStatus != http.StatusNoContent && deleteStatus != http.StatusOK {
		t.Fatalf("DELETE /api/v1/profiles/guest/pause: want 204/200, got %d", deleteStatus)
	}

	// Block must resume.
	deadline2 := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline2) {
		r := dnsQueryAsClient(t, n.DNSAddr, "doubleclick.net", dns.TypeA, "192.168.40.5")
		if r.Rcode == dns.RcodeNameError {
			return // success
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("profile pause was cancelled but doubleclick.net still resolves for 192.168.40.5")
}

// FSID: FS-FilterPauseProfileMultipleSimultaneous
func TestFilterPauseProfileMultipleSimultaneous(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"doubleclick.net"}, "")

	// Create two profiles.
	for _, p := range []struct {
		id   string
		name string
		cidr string
	}{
		{"early", "Early", "10.10.1.0/24"},
		{"late", "Late", "10.10.2.0/24"},
	} {
		r := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
			"id":           p.id,
			"name":         p.name,
			"blocklists":   []string{"ads"},
			"allowlist":    []string{},
			"client_cidrs": []string{p.cidr},
			"client_ips":   []string{},
		}))
		r.Body.Close()
		if r.StatusCode == http.StatusNotFound {
			t.Skipf("M10 impl pending: POST /api/v1/profiles returns 404")
		}
		if r.StatusCode != http.StatusCreated {
			t.Fatalf("create profile %s: status %d", p.id, r.StatusCode)
		}
	}

	// Pause "early" for 2s, "late" for 600s.
	if st, b := postProfilePause(t, n, "early", 2); st != http.StatusCreated && st != http.StatusOK {
		t.Fatalf("pause early: status %d: %s", st, b)
	}
	if st, b := postProfilePause(t, n, "late", 600); st != http.StatusCreated && st != http.StatusOK {
		t.Fatalf("pause late: status %d: %s", st, b)
	}

	// Both should be unblocked initially.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r1 := dnsQueryAsClient(t, n.DNSAddr, "doubleclick.net", dns.TypeA, "10.10.1.5")
		r2 := dnsQueryAsClient(t, n.DNSAddr, "doubleclick.net", dns.TypeA, "10.10.2.5")
		if r1.Rcode != dns.RcodeNameError && r2.Rcode != dns.RcodeNameError {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	assertNotBlockedAsClient(t, n, "10.10.1.5")
	assertNotBlockedAsClient(t, n, "10.10.2.5")

	// Sleep 3s so "early" expires but "late" does not.
	time.Sleep(3 * time.Second)

	// "early" client must now be blocked again.
	deadline2 := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline2) {
		r := dnsQueryAsClient(t, n.DNSAddr, "doubleclick.net", dns.TypeA, "10.10.1.5")
		if r.Rcode == dns.RcodeNameError {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	assertBlockedAsClient(t, n, "10.10.1.5")

	// "late" profile must still be paused — independent timer.
	assertNotBlockedAsClient(t, n, "10.10.2.5")
}

// ── Interaction tests ─────────────────────────────────────────────────────────

// FSID: FS-FilterPauseGlobalOverridesProfile
func TestFilterPauseGlobalOverridesProfile(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"doubleclick.net"}, "")

	// Create a profile; its pause state is irrelevant — the global pause must win.
	resp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":           "strict",
		"name":         "Strict",
		"blocklists":   []string{"ads"},
		"allowlist":    []string{},
		"client_cidrs": []string{"172.16.0.0/24"},
		"client_ips":   []string{},
	}))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M10 impl pending: POST /api/v1/profiles returns 404")
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create strict profile: status %d: %s", resp.StatusCode, readBody(t, resp))
	}

	// Profile client is blocked before global pause.
	assertBlockedAsClient(t, n, "172.16.0.5")

	// Activate global pause.
	status, body := postGlobalPause(t, n, 600)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST /api/v1/filtering/pause (global): want 201/200, got %d: %s", status, body)
	}

	// Profile client must now see the domain unblocked.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r := dnsQueryAsClient(t, n.DNSAddr, "doubleclick.net", dns.TypeA, "172.16.0.5")
		if r.Rcode != dns.RcodeNameError {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	assertNotBlockedAsClient(t, n, "172.16.0.5")
}

// ── Settings tests ────────────────────────────────────────────────────────────

// FSID: FS-FilterPauseCeilingEnforced
func TestFilterPauseCeilingEnforced(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"doubleclick.net"}, "")

	// Create a profile for the per-profile ceiling test.
	resp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":           "ceiling-test",
		"name":         "CeilingTest",
		"blocklists":   []string{"ads"},
		"allowlist":    []string{},
		"client_cidrs": []string{"10.99.0.0/24"},
		"client_ips":   []string{},
	}))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M10 impl pending: POST /api/v1/profiles returns 404")
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create ceiling-test profile: status %d: %s", resp.StatusCode, readBody(t, resp))
	}

	// Set ceiling to 5s.
	setPauseMaxSeconds(t, n, 5)

	// Global pause for 10s → 400.
	gStatus, gBody := postGlobalPause(t, n, 10)
	if gStatus != http.StatusBadRequest {
		t.Errorf("global pause 10s with ceiling=5: want 400, got %d: %s", gStatus, gBody)
	}

	// Profile pause for 10s → 400.
	pStatus, pBody := postProfilePause(t, n, "ceiling-test", 10)
	if pStatus != http.StatusBadRequest {
		t.Errorf("profile pause 10s with ceiling=5: want 400, got %d: %s", pStatus, pBody)
	}
}

// FSID: FS-FilterPauseFeatureDisabledWhenCeilingZero
func TestFilterPauseFeatureDisabledWhenCeilingZero(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"doubleclick.net"}, "")

	// Create a profile for the per-profile test.
	resp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":           "disabled-test",
		"name":         "DisabledTest",
		"blocklists":   []string{"ads"},
		"allowlist":    []string{},
		"client_cidrs": []string{"10.88.0.0/24"},
		"client_ips":   []string{},
	}))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M10 impl pending: POST /api/v1/profiles returns 404")
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create disabled-test profile: status %d: %s", resp.StatusCode, readBody(t, resp))
	}

	// Disable pause by setting ceiling=0.
	setPauseMaxSeconds(t, n, 0)

	// Global pause must be rejected.
	gStatus, gBody := postGlobalPause(t, n, 60)
	if gStatus != http.StatusBadRequest && gStatus != http.StatusForbidden {
		t.Errorf("global pause with ceiling=0: want 400 or 403, got %d: %s", gStatus, gBody)
	}

	// Profile pause must be rejected.
	pStatus, pBody := postProfilePause(t, n, "disabled-test", 60)
	if pStatus != http.StatusBadRequest && pStatus != http.StatusForbidden {
		t.Errorf("profile pause with ceiling=0: want 400 or 403, got %d: %s", pStatus, pBody)
	}

	// Domain remains blocked for a client in the disabled-test profile's CIDR.
	// (assertBlocked uses loopback which isn't in any profile; use the profile
	// client to confirm the blocklist is still active after ceiling=0.)
	assertBlockedAsClient(t, n, "10.88.0.1")
}

// ── Query log during profile pause ───────────────────────────────────────────

// FSID: FS-FilterPauseQueryLogMarkedDuringProfilePause
func TestFilterPauseQueryLogMarkedDuringProfilePause(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "ads", []string{"doubleclick.net"}, "")

	// Create two profiles: "paused-profile" and "unpaused-profile".
	for _, p := range []struct {
		id   string
		name string
		cidr string
	}{
		{"paused-profile", "PausedProfile", "10.50.1.0/24"},
		{"unpaused-profile", "UnpausedProfile", "10.50.2.0/24"},
	} {
		r := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
			"id":           p.id,
			"name":         p.name,
			"blocklists":   []string{"ads"},
			"allowlist":    []string{},
			"client_cidrs": []string{p.cidr},
			"client_ips":   []string{},
		}))
		r.Body.Close()
		if r.StatusCode == http.StatusNotFound {
			t.Skipf("M10 impl pending: POST /api/v1/profiles returns 404")
		}
		if r.StatusCode != http.StatusCreated {
			t.Fatalf("create profile %s: status %d", p.id, r.StatusCode)
		}
	}

	// Pause only "paused-profile".
	status, body := postProfilePause(t, n, "paused-profile", 60)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST /api/v1/profiles/paused-profile/pause: want 201/200, got %d: %s", status, body)
	}

	// Wait for pause to take effect for the paused profile.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r := dnsQueryAsClient(t, n.DNSAddr, "doubleclick.net", dns.TypeA, "10.50.1.5")
		if r.Rcode != dns.RcodeNameError {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Query from the paused profile client (10.50.1.5).
	dnsQueryAsClient(t, n.DNSAddr, "doubleclick.net", dns.TypeA, "10.50.1.5")

	// Wait for log entry from the paused profile client. During pause the
	// domain is forwarded.
	pausedEntry := waitForLog(t, n, "doubleclick.net", "forwarded", 3*time.Second)

	// The entry must have pause_active=true.
	if v, ok := pausedEntry["pause_active"]; !ok {
		t.Errorf("query log entry (paused profile client) missing pause_active field: %v", pausedEntry)
	} else if active, _ := v.(bool); !active {
		t.Errorf("query log (paused profile client): pause_active should be true, got %v", v)
	}

	// Query from the unpaused profile client (10.50.2.5 → domain blocked → outcome=blocked).
	dnsQueryAsClient(t, n.DNSAddr, "doubleclick.net", dns.TypeA, "10.50.2.5")

	// Find a log entry for the unpaused client; pause_active must be false.
	// We poll briefly to let the entry appear.
	deadline2 := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline2) {
		entries := getLog(t, n, "")
		for _, e := range entries {
			d, _ := e["domain"].(string)
			if d != "doubleclick.net" {
				continue
			}
			client, _ := e["client"].(string)
			// The unpaused client query is blocked → outcome=blocked; match on that.
			outcome, _ := e["outcome"].(string)
			if outcome == "blocked" || strings.HasPrefix(client, "10.50.2.") {
				if pa, _ := e["pause_active"].(bool); !pa {
					// pause_active absent or false — correct.
					return
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("no query log entry for unpaused profile client with pause_active=false")
}

// keep fmt imported so tests referencing it compile even if a future edit
// removes the only inline use (mirrors the pattern from profile_block_dynamic_test.go).
var _ = fmt.Sprintf
