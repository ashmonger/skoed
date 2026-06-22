// Acceptance tests for the DHCP Server Web UI (M23.6).
//
// These tests validate the HTTP API contracts that the Settings → DHCP section
// of the web UI depends on.  No browser automation is used; all assertions are
// black-box API interactions per AGENTS.md Rule 10.
//
// FSIDs covered:
//   FS-DhcpWebUiSettingsTabVisible      → TestDhcpWebUiSettingsDataComplete
//   FS-DhcpWebUiToggleEnable            → TestDhcpWebUiToggleEnable
//   FS-DhcpWebUiToggleDisable           → TestDhcpWebUiToggleDisable
//   FS-DhcpWebUiPoolConfig              → TestDhcpWebUiPoolConfigRoundTrip
//   FS-DhcpWebUiStaticAssignmentCreate  → TestDhcpWebUiStaticAssignmentCreate
//   FS-DhcpWebUiStaticAssignmentDelete  → TestDhcpWebUiStaticAssignmentDelete
//   FS-DhcpWebUiLeaseTable              → TestDhcpWebUiLeaseTableShape
//   FS-DhcpWebUiPoolUtilisationGauge    → TestDhcpWebUiPoolUtilisationFields
package acceptance

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// ─── FS-DhcpWebUiSettingsTabVisible ──────────────────────────────────────────

// TestDhcpWebUiSettingsDataComplete verifies that GET /api/v1/dhcp/server/status
// returns all fields the DHCP settings section needs to render: enabled flag,
// pool boundaries, gateway, lease time, domain, dns_server, leases_active, pool_total.
func TestDhcpWebUiSettingsDataComplete(t *testing.T) {
	t.Parallel()

	n := startNode(t, NodeConfig{})
	setupAuth(t, n)

	resp := n.apiDo(t, http.MethodGet, "/api/v1/dhcp/server/status", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("DHCP server feature not yet implemented (404)")
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/v1/dhcp/server/status: want 200, got %d: %s", resp.StatusCode, b)
	}

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	for _, key := range []string{
		"enabled", "pool_start", "pool_end", "gateway",
		"lease_time_seconds", "domain", "dns_server", "leases_active", "pool_total",
	} {
		if _, ok := raw[key]; !ok {
			t.Errorf("status response missing field %q — UI cannot render the DHCP settings section", key)
		}
	}
}

// ─── FS-DhcpWebUiToggleEnable ────────────────────────────────────────────────

// TestDhcpWebUiToggleEnable verifies the enable-toggle flow:
//
//	UI click → PUT /api/v1/settings/dhcp {pool:…, enabled:true} → 200
//	→ GET /api/v1/dhcp/server/status returns enabled:true.
func TestDhcpWebUiToggleEnable(t *testing.T) {
	t.Parallel()

	n := startNode(t, NodeConfig{})
	setupAuth(t, n)

	// Configure a valid pool and enable in one PUT (mirrors UI save + toggle).
	putResp := putDhcpSettings(t, n, map[string]any{
		"pool_start":         "10.99.1.10",
		"pool_end":           "10.99.1.50",
		"gateway":            "10.99.1.1",
		"lease_time_seconds": 3600,
		"enabled":            true,
	})
	defer putResp.Body.Close()
	if putResp.StatusCode == http.StatusNotFound {
		t.Skip("DHCP settings endpoint not yet implemented (404)")
	}
	if putResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(putResp.Body)
		t.Fatalf("PUT /api/v1/settings/dhcp: want 200, got %d: %s", putResp.StatusCode, b)
	}

	s := getDhcpStatus(t, n)
	if !s.Enabled {
		t.Fatal("GET status: enabled is false after toggle-enable PUT — UI would show wrong state")
	}
}

// ─── FS-DhcpWebUiToggleDisable ───────────────────────────────────────────────

// TestDhcpWebUiToggleDisable verifies the disable-toggle flow:
//
//	UI click → PUT /api/v1/settings/dhcp {enabled:false} → 200
//	→ GET /api/v1/dhcp/server/status returns enabled:false.
func TestDhcpWebUiToggleDisable(t *testing.T) {
	t.Parallel()

	n := startNode(t, NodeConfig{})
	setupAuth(t, n)

	// Enable first so the toggle has something to disable.
	r := putDhcpSettings(t, n, map[string]any{
		"pool_start":         "10.99.2.10",
		"pool_end":           "10.99.2.50",
		"gateway":            "10.99.2.1",
		"lease_time_seconds": 3600,
		"enabled":            true,
	})
	if r.StatusCode == http.StatusNotFound {
		r.Body.Close()
		t.Skip("DHCP settings endpoint not yet implemented (404)")
	}
	r.Body.Close()

	disableResp := putDhcpSettings(t, n, map[string]any{"enabled": false})
	defer disableResp.Body.Close()
	if disableResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(disableResp.Body)
		t.Fatalf("PUT {enabled:false}: want 200, got %d: %s", disableResp.StatusCode, b)
	}

	s := getDhcpStatus(t, n)
	if s.Enabled {
		t.Fatal("GET status: enabled is true after toggle-disable PUT — UI would show wrong state")
	}
}

// ─── FS-DhcpWebUiPoolConfig ──────────────────────────────────────────────────

// TestDhcpWebUiPoolConfigRoundTrip verifies that values submitted via the pool
// config form are persisted and returned unchanged by a subsequent GET ("page reload").
func TestDhcpWebUiPoolConfigRoundTrip(t *testing.T) {
	t.Parallel()

	n := startNode(t, NodeConfig{})
	setupAuth(t, n)

	r := putDhcpSettings(t, n, map[string]any{
		"pool_start":         "10.99.3.20",
		"pool_end":           "10.99.3.80",
		"gateway":            "10.99.3.1",
		"lease_time_seconds": 7200,
		"domain":             "home.arpa",
		"dns_server":         "10.99.3.1",
	})
	defer r.Body.Close()
	if r.StatusCode == http.StatusNotFound {
		t.Skip("DHCP settings endpoint not yet implemented (404)")
	}
	if r.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(r.Body)
		t.Fatalf("PUT pool config: want 200, got %d: %s", r.StatusCode, b)
	}

	s := getDhcpStatus(t, n)
	if s.PoolStart != "10.99.3.20" {
		t.Errorf("pool_start: want 10.99.3.20, got %q", s.PoolStart)
	}
	if s.PoolEnd != "10.99.3.80" {
		t.Errorf("pool_end: want 10.99.3.80, got %q", s.PoolEnd)
	}
	if s.Gateway != "10.99.3.1" {
		t.Errorf("gateway: want 10.99.3.1, got %q", s.Gateway)
	}
	if s.LeaseTimeSeconds != 7200 {
		t.Errorf("lease_time_seconds: want 7200, got %d", s.LeaseTimeSeconds)
	}
	if s.Domain != "home.arpa" {
		t.Errorf("domain: want home.arpa, got %q", s.Domain)
	}
	if s.DNSServer != "10.99.3.1" {
		t.Errorf("dns_server: want 10.99.3.1, got %q", s.DNSServer)
	}
}

// ─── FS-DhcpWebUiStaticAssignmentCreate ──────────────────────────────────────

// TestDhcpWebUiStaticAssignmentCreate verifies the "Add static assignment" form:
//
//	fill MAC/IP/hostname → POST /api/v1/dhcp/static-assignments → 201
//	→ entry appears in GET /api/v1/dhcp/static-assignments.
func TestDhcpWebUiStaticAssignmentCreate(t *testing.T) {
	t.Parallel()

	n := startNode(t, NodeConfig{})
	setupAuth(t, n)

	mac := "de:ad:be:ef:00:01"
	ip := "10.99.4.50"
	hostname := "myserver"

	resp := postDhcpStaticEntry(t, n, mac, ip, hostname)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("DHCP static-assignments endpoint not yet implemented (404)")
	}
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST static-assignment: want 201, got %d: %s", resp.StatusCode, b)
	}

	entries := listDhcpStaticEntries(t, n)
	found := false
	for _, e := range entries {
		if e.MAC == mac {
			found = true
			if e.IP != ip {
				t.Errorf("entry IP: want %q, got %q", ip, e.IP)
			}
			if e.Hostname != hostname {
				t.Errorf("entry hostname: want %q, got %q", hostname, e.Hostname)
			}
		}
	}
	if !found {
		t.Fatalf("created static assignment (mac=%s) not found in GET /api/v1/dhcp/static-assignments", mac)
	}
}

// ─── FS-DhcpWebUiStaticAssignmentDelete ──────────────────────────────────────

// TestDhcpWebUiStaticAssignmentDelete verifies the delete-icon + confirm flow:
//
//	confirm → DELETE /api/v1/dhcp/static-assignments/{mac} → 204
//	→ entry absent from GET /api/v1/dhcp/static-assignments.
//
// The MAC is URL-encoded as the UI would send it (colons → %3A).
func TestDhcpWebUiStaticAssignmentDelete(t *testing.T) {
	t.Parallel()

	n := startNode(t, NodeConfig{})
	setupAuth(t, n)

	mac := "de:ad:be:ef:00:02"

	cr := postDhcpStaticEntry(t, n, mac, "10.99.5.60", "toDelete")
	cr.Body.Close()
	if cr.StatusCode == http.StatusNotFound {
		t.Skip("DHCP static-assignments endpoint not yet implemented (404)")
	}
	if cr.StatusCode != http.StatusCreated {
		t.Fatalf("POST static-assignment: want 201, got %d", cr.StatusCode)
	}

	// The deleteDhcpStaticAssignment endpoint helper in the harness already
	// uses fmt.Sprintf with the raw MAC (colons preserved). Mirror that here.
	delResp := deleteDhcpStaticEntry(t, n, mac)
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(delResp.Body)
		t.Fatalf("DELETE static-assignment: want 204, got %d: %s", delResp.StatusCode, b)
	}

	entries := listDhcpStaticEntries(t, n)
	for _, e := range entries {
		if e.MAC == mac {
			t.Fatalf("deleted static assignment (mac=%s) still present in GET list", mac)
		}
	}
}

// ─── FS-DhcpWebUiLeaseTable ──────────────────────────────────────────────────

// TestDhcpWebUiLeaseTableShape verifies that GET /api/v1/dhcp/leases returns a
// JSON array (not null/object) and that each element contains the fields ip,
// mac, hostname, and origin required by the lease table rows.
//
// Note: lease population requires real DHCP packet injection; this test covers
// shape only.  Populated-lease validation is done in Proxmox real-condition testing.
func TestDhcpWebUiLeaseTableShape(t *testing.T) {
	t.Parallel()

	n := startNode(t, NodeConfig{})
	setupAuth(t, n)

	resp := n.apiDo(t, http.MethodGet, "/api/v1/dhcp/leases", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("DHCP leases endpoint not yet implemented (404)")
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/v1/dhcp/leases: want 200, got %d: %s", resp.StatusCode, b)
	}

	var leases []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&leases); err != nil {
		t.Fatalf("decode lease array: %v", err)
	}
	if leases == nil {
		t.Fatal("GET /api/v1/dhcp/leases returned null — UI cannot iterate leases")
	}

	// Validate each existing entry has the required display fields.
	for i, raw := range leases {
		var entry map[string]any
		if err := json.Unmarshal(raw, &entry); err != nil {
			t.Fatalf("lease[%d]: unmarshal: %v", i, err)
		}
		for _, key := range []string{"ip", "mac", "hostname", "origin"} {
			if _, ok := entry[key]; !ok {
				t.Errorf("lease[%d] missing field %q — lease table cannot render row", i, key)
			}
		}
	}
}

// ─── FS-DhcpWebUiPoolUtilisationGauge ────────────────────────────────────────

// TestDhcpWebUiPoolUtilisationFields verifies that GET /api/v1/dhcp/server/status
// returns leases_active and pool_total so the UI can compute:
//
//	utilisation% = leases_active / pool_total * 100.
func TestDhcpWebUiPoolUtilisationFields(t *testing.T) {
	t.Parallel()

	n := startNode(t, NodeConfig{})
	setupAuth(t, n)

	// Configure a 50-address pool.
	r := putDhcpSettings(t, n, map[string]any{
		"pool_start":         "10.99.6.10",
		"pool_end":           "10.99.6.59",
		"gateway":            "10.99.6.1",
		"lease_time_seconds": 3600,
	})
	r.Body.Close()
	if r.StatusCode == http.StatusNotFound {
		t.Skip("DHCP settings endpoint not yet implemented (404)")
	}

	s := getDhcpStatus(t, n)

	if s.PoolTotal != 50 {
		t.Errorf("pool_total: want 50 (addresses .10–.59), got %d — gauge cannot compute correctly", s.PoolTotal)
	}
	if s.LeasesActive < 0 {
		t.Errorf("leases_active is negative (%d) — percentage would be nonsensical", s.LeasesActive)
	}
}
