// Acceptance tests for M35 — Filtering Pause Enhancements.
//
// FSIDs covered (one Go test per FSID):
//   FS-PerClientPauseActivates             → TestPerClientPauseActivates
//   FS-PerClientPauseStateVisible          → TestPerClientPauseStateVisible
//   FS-PerClientPauseCancelledEarly        → TestPerClientPauseCancelledEarly
//   FS-PerClientPauseOtherClientsUnaffected → TestPerClientPauseOtherClientsUnaffected
//   FS-PauseHistoryRecorded                → TestPauseHistoryRecorded
//   FS-PauseHistoryCappedAt50              → TestPauseHistoryCappedAt50
//   FS-PauseHistoryNotFoundForUnknownProfile → TestPauseHistoryNotFoundForUnknownProfile
//   FS-PauseExpiryWebhookFired             → TestPauseExpiryWebhookFired
//   FS-NewDynamicClientAlertEndpoint       → TestNewDynamicClientAlertEndpoint
//   FS-NewDynamicClientAlertDismissed      → TestNewDynamicClientAlertDismissed
//
// Strategy:
//   - Per-client pause tests use SKOED_TEST_MODE=1 + EDNS0 option 65500 to
//     simulate client IPs, exactly as per-profile pause tests do.
//   - Pause history tests create real pause events via the API and read them back.
//   - Webhook test creates a 2-second pause and waits for filter.pause_expired.
//   - New-dynamic-client tests make a DNS query from an unseen IP, then poll
//     the /api/v1/clients/new-dynamic endpoint.
//   - All tests skip cleanly when routes return 404 (M35 not yet deployed).

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// postProfilePauseWithClientIPs calls POST /api/v1/profiles/{id}/pause with
// duration_seconds and an optional client_ips restriction.
func postProfilePauseWithClientIPs(t *testing.T, n *Node, profileID string, seconds int, clientIPs []string) (int, string) {
	t.Helper()
	payload := map[string]any{"duration_seconds": seconds}
	if len(clientIPs) > 0 {
		payload["client_ips"] = clientIPs
	}
	resp := n.apiDo(t, "POST", fmt.Sprintf("/api/v1/profiles/%s/pause", profileID), mustJSON(t, payload))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNotImplemented {
		t.Skipf("M35 impl pending: POST /api/v1/profiles/%s/pause returned %d", profileID, resp.StatusCode)
	}
	return resp.StatusCode, readBody(t, resp)
}

// getPauseHistory calls GET /api/v1/profiles/{id}/pause/history.
// Skips the test if the route is not yet implemented.
// Returns the raw decoded slice.
func getPauseHistory(t *testing.T, n *Node, profileID string) (int, []map[string]any) {
	t.Helper()
	resp := n.apiDo(t, "GET", fmt.Sprintf("/api/v1/profiles/%s/pause/history", profileID), "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNotImplemented {
		t.Skipf("M35 impl pending: GET /api/v1/profiles/%s/pause/history returned %d", profileID, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /api/v1/profiles/%s/pause/history: unexpected %d", profileID, resp.StatusCode)
	}
	if resp.StatusCode == http.StatusNotFound {
		return http.StatusNotFound, nil
	}
	var out []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode pause history: %v", err)
	}
	return http.StatusOK, out
}

// getNewDynamicClients calls GET /api/v1/clients/new-dynamic.
// Skips if the route is not yet implemented.
func getNewDynamicClients(t *testing.T, n *Node) []map[string]any {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/clients/new-dynamic", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNotImplemented {
		t.Skipf("M35 impl pending: GET /api/v1/clients/new-dynamic returned %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/clients/new-dynamic: unexpected %d", resp.StatusCode)
	}
	var out []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode new-dynamic clients: %v", err)
	}
	return out
}

// dismissNewDynamicClient calls POST /api/v1/clients/new-dynamic/dismiss.
// Skips if not yet implemented.
func dismissNewDynamicClient(t *testing.T, n *Node, clientIP string) {
	t.Helper()
	resp := n.apiDo(t, "POST", "/api/v1/clients/new-dynamic/dismiss",
		mustJSON(t, map[string]any{"client_ip": clientIP}))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNotImplemented {
		t.Skipf("M35 impl pending: POST /api/v1/clients/new-dynamic/dismiss returned %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /api/v1/clients/new-dynamic/dismiss: want 200/204, got %d", resp.StatusCode)
	}
}

// newDynamicContainsIP returns true when clientIP appears in the slice returned
// by getNewDynamicClients.
func newDynamicContainsIP(clients []map[string]any, clientIP string) bool {
	for _, c := range clients {
		if ip, _ := c["client_ip"].(string); ip == clientIP {
			return true
		}
	}
	return false
}

// startPerClientPauseNode starts a single node with SKOED_TEST_MODE=1 and
// seeds an ads blocklist. Returns the node.
func startPerClientPauseNode(t *testing.T) *Node {
	t.Helper()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	c := startClusterWithEnv(t, 1, []string{"SKOED_TEST_MODE=1"})
	n := c.Leader(t).Node

	// Wire the fake upstream.
	resp := n.apiDo(t, "PATCH", "/api/v1/settings", mustJSON(t, map[string]any{
		"dns": map[string]any{
			"mode":              "forwarding",
			"upstream_resolvers": []string{upstream},
		},
	}))
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	addInlineBlocklist(t, n, "ads", []string{"doubleclick.net"}, "")
	return n
}

// createM35KidsProfile creates the "m35kids" profile bound to 10.50.0.0/24 on node n.
func createM35KidsProfile(t *testing.T, n *Node) {
	t.Helper()
	resp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":           "m35kids",
		"name":         "M35 Kids",
		"blocklists":   []string{"ads"},
		"allowlist":    []string{},
		"client_cidrs": []string{"10.50.0.0/24"},
		"client_ips":   []string{},
	}))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M35 impl pending: POST /api/v1/profiles returned 404")
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create m35kids profile: status %d: %s", resp.StatusCode, readBody(t, resp))
	}
}

// ── FS-PerClientPauseActivates ────────────────────────────────────────────────

// TestPerClientPauseActivates verifies that a per-client pause suspends DNS
// filtering for the targeted IP while leaving other IPs in the same profile blocked.
//
// FSID: FS-PerClientPauseActivates
func TestPerClientPauseActivates(t *testing.T) {
	t.Parallel()
	n := startPerClientPauseNode(t)
	createM35KidsProfile(t, n)

	// Both clients should be blocked before the pause.
	assertBlockedAsClient(t, n, "10.50.0.5")
	assertBlockedAsClient(t, n, "10.50.0.6")

	// Set a per-client pause for 10.50.0.5 only.
	status, body := postProfilePauseWithClientIPs(t, n, "m35kids", 600, []string{"10.50.0.5"})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST pause with client_ips: want 200/201, got %d: %s", status, body)
	}

	// 10.50.0.5 should now resolve (pause active).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r := dnsQueryAsClient(t, n.DNSAddr, "doubleclick.net", dns.TypeA, "10.50.0.5")
		if r.Rcode != dns.RcodeNameError {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	assertNotBlockedAsClient(t, n, "10.50.0.5")

	// 10.50.0.6 must remain blocked.
	assertBlockedAsClient(t, n, "10.50.0.6")
}

// ── FS-PerClientPauseStateVisible ────────────────────────────────────────────

// TestPerClientPauseStateVisible verifies that GET /api/v1/profiles/{id}/pause
// returns the client_ips field when a per-client pause is active.
//
// FSID: FS-PerClientPauseStateVisible
func TestPerClientPauseStateVisible(t *testing.T) {
	t.Parallel()
	n := startPerClientPauseNode(t)
	createM35KidsProfile(t, n)

	status, body := postProfilePauseWithClientIPs(t, n, "m35kids", 600, []string{"10.50.0.5"})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST pause with client_ips: want 200/201, got %d: %s", status, body)
	}

	state := getProfilePause(t, n, "m35kids")
	if active, _ := state["active"].(bool); !active {
		t.Fatalf("pause GET: expected active=true, got %v", state)
	}

	rawIPs, ok := state["client_ips"]
	if !ok {
		t.Fatal("pause GET: client_ips field absent")
	}
	ipsSlice, ok := rawIPs.([]any)
	if !ok || len(ipsSlice) != 1 {
		t.Fatalf("pause GET: expected client_ips=[\"10.50.0.5\"], got %v", rawIPs)
	}
	if ipsSlice[0].(string) != "10.50.0.5" {
		t.Errorf("pause GET: client_ips[0] = %q, want %q", ipsSlice[0], "10.50.0.5")
	}
}

// ── FS-PerClientPauseCancelledEarly ──────────────────────────────────────────

// TestPerClientPauseCancelledEarly verifies that DELETE /pause clears a per-
// client pause: the endpoint reports active=false and filtering resumes.
//
// FSID: FS-PerClientPauseCancelledEarly
func TestPerClientPauseCancelledEarly(t *testing.T) {
	t.Parallel()
	n := startPerClientPauseNode(t)
	createM35KidsProfile(t, n)

	status, body := postProfilePauseWithClientIPs(t, n, "m35kids", 600, []string{"10.50.0.5"})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST pause: want 200/201, got %d: %s", status, body)
	}

	// Wait until the pause is observed as active.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if profilePauseIsActive(t, n, "m35kids") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Cancel early.
	if st := deleteProfilePause(t, n, "m35kids"); st != http.StatusNoContent {
		t.Fatalf("DELETE pause: want 204, got %d", st)
	}

	state := getProfilePause(t, n, "m35kids")
	if active, _ := state["active"].(bool); active {
		t.Fatal("after DELETE: expected active=false, still true")
	}

	// DNS should be blocked again for the per-client IP.
	// pollUntilBlocked uses dnsQuery (no client IP), which would query from the
	// test host's IP — not in the m35kids profile — so we poll with the client IP.
	pausedIP := "10.50.0.5"
	deadline2 := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline2) {
		r := dnsQueryAsClient(t, n.DNSAddr, "doubleclick.net", dns.TypeA, pausedIP)
		if r.Rcode == dns.RcodeNameError {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	assertBlockedAsClient(t, n, pausedIP)
}

// ── FS-PerClientPauseOtherClientsUnaffected ───────────────────────────────────

// TestPerClientPauseOtherClientsUnaffected confirms that passing client_ips
// does not create a profile-wide pause: clients not in the list stay filtered.
//
// FSID: FS-PerClientPauseOtherClientsUnaffected
func TestPerClientPauseOtherClientsUnaffected(t *testing.T) {
	t.Parallel()
	n := startPerClientPauseNode(t)
	createM35KidsProfile(t, n)

	status, body := postProfilePauseWithClientIPs(t, n, "m35kids", 600, []string{"10.50.0.5"})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST pause with client_ips: want 200/201, got %d: %s", status, body)
	}

	// Allow pause to propagate to filter engine.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r := dnsQueryAsClient(t, n.DNSAddr, "doubleclick.net", dns.TypeA, "10.50.0.5")
		if r.Rcode != dns.RcodeNameError {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// 10.50.0.6 is in the same profile CIDR but not in the per-client list.
	assertBlockedAsClient(t, n, "10.50.0.6")

	// 10.50.0.5 is unblocked.
	assertNotBlockedAsClient(t, n, "10.50.0.5")
}

// TestPerClientPauseRejectsInvalidIP is the H-1 security regression guard: a
// per-client pause request carrying an unparseable client_ip must be rejected
// (400), never silently accepted. A silently-dropped invalid IP previously
// collapsed the pause to the whole profile — disabling filtering for everyone.
//
// FSID: FS-PerClientPauseOtherClientsUnaffected (security regression)
func TestPerClientPauseRejectsInvalidIP(t *testing.T) {
	t.Parallel()
	n := startPerClientPauseNode(t)
	createM35KidsProfile(t, n)

	// A CIDR, a hostname, and a typo — all invalid as bare IPs.
	for _, bad := range []string{"10.50.0.0/24", "not-an-ip", "10.50.0.5 "} {
		status, body := postProfilePauseWithClientIPs(t, n, "m35kids", 600, []string{bad})
		if status != http.StatusBadRequest {
			t.Fatalf("pause with invalid client_ip %q: want 400, got %d: %s", bad, status, body)
		}
	}

	// After the rejected requests, no pause is active, so the whole profile
	// must still be filtered (filtering was NOT silently disabled).
	if profilePauseIsActive(t, n, "m35kids") {
		t.Fatal("a rejected per-client pause left a pause active on the profile")
	}
	assertBlockedAsClient(t, n, "10.50.0.6")
}

// ── FS-PauseHistoryRecorded ───────────────────────────────────────────────────

// TestPauseHistoryRecorded verifies that GET .../pause/history returns at least
// one entry with started_at, ended_at, and scope after a pause is set and cleared.
//
// FSID: FS-PauseHistoryRecorded
func TestPauseHistoryRecorded(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})
	addInlineBlocklist(t, n, "ads", []string{"doubleclick.net"}, "")

	// Create a profile.
	resp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":         "hist-p1",
		"name":       "History P1",
		"blocklists": []string{"ads"},
	}))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M35 impl pending: POST /api/v1/profiles returned 404")
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create hist-p1: %d: %s", resp.StatusCode, readBody(t, resp))
	}

	// Enable pause ceiling.
	setPauseMaxSeconds(t, n, 86400)

	// Set then immediately clear a pause so history has a complete entry.
	if st, b := postProfilePause(t, n, "hist-p1", 600); st != http.StatusCreated && st != http.StatusOK {
		t.Fatalf("POST pause: %d: %s", st, b)
	}
	if st := deleteProfilePause(t, n, "hist-p1"); st != http.StatusNoContent {
		t.Fatalf("DELETE pause: %d", st)
	}

	status, entries := getPauseHistory(t, n, "hist-p1")
	if status != http.StatusOK {
		t.Fatalf("GET history: want 200, got %d", status)
	}
	if len(entries) == 0 {
		t.Fatal("pause history: expected at least one entry, got 0")
	}

	first := entries[0]
	if _, ok := first["started_at"]; !ok {
		t.Error("history entry missing started_at field")
	}
	if _, ok := first["ended_at"]; !ok {
		t.Error("history entry missing ended_at field")
	}
	if _, ok := first["scope"]; !ok {
		t.Error("history entry missing scope field")
	}
}

// ── FS-PauseHistoryCappedAt50 ─────────────────────────────────────────────────

// TestPauseHistoryCappedAt50 creates 60 pause events and verifies that
// GET .../pause/history returns exactly 50 entries (most recent first).
//
// FSID: FS-PauseHistoryCappedAt50
func TestPauseHistoryCappedAt50(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})

	resp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":   "hist-cap",
		"name": "History Cap",
	}))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M35 impl pending: POST /api/v1/profiles returned 404")
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create hist-cap: %d: %s", resp.StatusCode, readBody(t, resp))
	}

	setPauseMaxSeconds(t, n, 86400)

	// Create 60 pause+clear cycles to exceed the cap.
	for i := 0; i < 60; i++ {
		if st, b := postProfilePause(t, n, "hist-cap", 600); st != http.StatusCreated && st != http.StatusOK {
			t.Fatalf("POST pause #%d: %d: %s", i+1, st, b)
		}
		if st := deleteProfilePause(t, n, "hist-cap"); st != http.StatusNoContent {
			t.Fatalf("DELETE pause #%d: %d", i+1, st)
		}
	}

	status, entries := getPauseHistory(t, n, "hist-cap")
	if status != http.StatusOK {
		t.Fatalf("GET history: want 200, got %d", status)
	}
	if len(entries) != 50 {
		t.Errorf("history: expected exactly 50 entries, got %d", len(entries))
	}
}

// ── FS-PauseHistoryNotFoundForUnknownProfile ──────────────────────────────────

// TestPauseHistoryNotFoundForUnknownProfile verifies that GET .../pause/history
// for a non-existent profile returns HTTP 404.
//
// FSID: FS-PauseHistoryNotFoundForUnknownProfile
func TestPauseHistoryNotFoundForUnknownProfile(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})

	resp := n.apiDo(t, "GET", "/api/v1/profiles/nonexistent-m35/pause/history", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotImplemented {
		t.Skip("M35 impl pending: history route not implemented")
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404 for unknown profile, got %d", resp.StatusCode)
	}
}

// ── FS-PauseExpiryWebhookFired ────────────────────────────────────────────────

// TestPauseExpiryWebhookFired verifies that a "filter.pause_expired" webhook
// event is delivered when a profile pause expires naturally.
//
// FSID: FS-PauseExpiryWebhookFired
func TestPauseExpiryWebhookFired(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})

	resp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":   "exp-hook",
		"name": "Expiry Hook",
	}))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M35 impl pending: POST /api/v1/profiles returned 404")
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create exp-hook: %d: %s", resp.StatusCode, readBody(t, resp))
	}

	setPauseMaxSeconds(t, n, 86400)

	receiver, ch := startFakeWebhookReceiver(t)
	createWebhook(t, n, receiver.URL, "expiry-secret", []string{"filter.pause_expired"})

	// Create a 2-second pause so it expires quickly.
	if st, b := postProfilePause(t, n, "exp-hook", 2); st != http.StatusCreated && st != http.StatusOK {
		t.Fatalf("POST pause: %d: %s", st, b)
	}

	// Wait up to 10 seconds for the expiry webhook.
	hook, ok := waitForHook(t, ch, 10*time.Second)
	if !ok {
		t.Fatal("no filter.pause_expired event received within 10s after pause expiry")
	}

	var payload webhookPayload
	if err := json.Unmarshal(hook.Body, &payload); err != nil {
		t.Fatalf("parse webhook body: %v\nbody: %s", err, hook.Body)
	}
	if payload.Event != "filter.pause_expired" {
		t.Errorf("event: got %q, want %q", payload.Event, "filter.pause_expired")
	}

	// The data field must contain profile_id and expired_at.
	var data map[string]any
	if err := json.Unmarshal(payload.Data, &data); err != nil {
		t.Fatalf("parse data: %v", err)
	}
	if _, ok := data["profile_id"]; !ok {
		t.Error("webhook data missing profile_id field")
	}
	if _, ok := data["expired_at"]; !ok {
		t.Error("webhook data missing expired_at field")
	}
}

// ── FS-NewDynamicClientAlertEndpoint ─────────────────────────────────────────

// TestNewDynamicClientAlertEndpoint verifies that GET /api/v1/clients/new-dynamic
// returns an entry with first_seen when a new client IP is seen for the first time.
// The "new" IP is introduced via a DNS query using the EDNS0 test affordance.
//
// FSID: FS-NewDynamicClientAlertEndpoint
func TestNewDynamicClientAlertEndpoint(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	c := startClusterWithEnv(t, 1, []string{"SKOED_TEST_MODE=1"})
	n := c.Leader(t).Node
	// Wire fake upstream so DNS queries resolve quickly.
	resp := n.apiDo(t, "PATCH", "/api/v1/settings", mustJSON(t, map[string]any{
		"dns": map[string]any{"mode": "forwarding", "upstream_resolvers": []string{upstream}},
	}))
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	newClientIP := "10.97.88.11"

	// Trigger "first seen" by making a DNS query from the new IP.
	dnsQueryAsClient(t, n.DNSAddr, "example.com", dns.TypeA, newClientIP)

	// Poll up to 5 seconds for the client to appear in the new-dynamic list.
	var found bool
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		clients := getNewDynamicClients(t, n)
		if newDynamicContainsIP(clients, newClientIP) {
			found = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !found {
		t.Fatalf("GET /api/v1/clients/new-dynamic: %s not found within 5s", newClientIP)
	}

	// Entry must include first_seen.
	clients := getNewDynamicClients(t, n)
	for _, c := range clients {
		if ip, _ := c["client_ip"].(string); ip == newClientIP {
			if _, ok := c["first_seen"]; !ok {
				t.Error("new-dynamic entry missing first_seen field")
			}
			return
		}
	}
	t.Errorf("client %s disappeared from new-dynamic list between polls", newClientIP)
}

// ── FS-NewDynamicClientAlertDismissed ────────────────────────────────────────

// TestNewDynamicClientAlertDismissed verifies that after a dismiss, the client
// IP no longer appears in GET /api/v1/clients/new-dynamic.
//
// FSID: FS-NewDynamicClientAlertDismissed
func TestNewDynamicClientAlertDismissed(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	c := startClusterWithEnv(t, 1, []string{"SKOED_TEST_MODE=1"})
	n := c.Leader(t).Node
	resp := n.apiDo(t, "PATCH", "/api/v1/settings", mustJSON(t, map[string]any{
		"dns": map[string]any{"mode": "forwarding", "upstream_resolvers": []string{upstream}},
	}))
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	newClientIP := "10.97.88.22"

	dnsQueryAsClient(t, n.DNSAddr, "example.com", dns.TypeA, newClientIP)

	// Wait for it to appear.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		clients := getNewDynamicClients(t, n)
		if newDynamicContainsIP(clients, newClientIP) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Dismiss.
	dismissNewDynamicClient(t, n, newClientIP)

	// Verify it is gone.
	clients := getNewDynamicClients(t, n)
	if newDynamicContainsIP(clients, newClientIP) {
		t.Errorf("client %s still in new-dynamic list after dismiss", newClientIP)
	}
}

// TestNewDynamicClientDismissIsDurable is the H-4 regression guard: after a
// client is dismissed, further DNS queries from that same IP must NOT bring it
// back into the alert list. Previously dismiss deleted the key while per-query
// tracking re-inserted it within seconds, making dismiss useless.
//
// FSID: FS-NewDynamicClientAlertDismissed (durability regression)
func TestNewDynamicClientDismissIsDurable(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	c := startClusterWithEnv(t, 1, []string{"SKOED_TEST_MODE=1"})
	n := c.Leader(t).Node
	resp := n.apiDo(t, "PATCH", "/api/v1/settings", mustJSON(t, map[string]any{
		"dns": map[string]any{"mode": "forwarding", "upstream_resolvers": []string{upstream}},
	}))
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	newClientIP := "10.97.88.33"

	// Appear, then dismiss.
	dnsQueryAsClient(t, n.DNSAddr, "example.com", dns.TypeA, newClientIP)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if newDynamicContainsIP(getNewDynamicClients(t, n), newClientIP) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	dismissNewDynamicClient(t, n, newClientIP)

	// Now hammer the DNS server with more queries from the dismissed IP.
	for i := 0; i < 5; i++ {
		dnsQueryAsClient(t, n.DNSAddr, "example.com", dns.TypeA, newClientIP)
		time.Sleep(100 * time.Millisecond)
	}

	// It must NOT have been resurrected.
	if newDynamicContainsIP(getNewDynamicClients(t, n), newClientIP) {
		t.Errorf("dismissed client %s was resurrected by subsequent DNS queries", newClientIP)
	}
}
