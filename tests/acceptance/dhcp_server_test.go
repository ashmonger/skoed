// Acceptance tests for the built-in DHCP server (M23.5).
//
// FSIDs covered:
//   FS-DhcpServerDisabledByDefault              → TestDhcpServerDisabledByDefault
//   FS-DhcpServerEnableViaApi                   → TestDhcpServerEnableViaApi
//   FS-DhcpServerDisableViaApi                  → TestDhcpServerDisableViaApi
//   FS-DhcpServerConfigPersisted                → TestDhcpServerConfigPersisted
//   FS-DhcpOptionsDelivered (config fields)     → TestDhcpServerStatusReflectsPoolConfig
//   FS-DhcpDnsOptionDefaultsToSelf              → TestDhcpDnsOptionDefaultsToSelf
//   FS-DhcpStaticAssignmentPersistedToConfig    → TestDhcpStaticAssignmentPersisted
//   FS-DhcpStaticAssignmentReplicatedViaRaft    → TestDhcpStaticAssignmentRaft
//   FS-DhcpStaticAssignmentDelete               → TestDhcpStaticAssignmentDelete
//   FS-DhcpLeaderOwnsListener                   → TestDhcpLeaderOwnsListener
//   FS-DhcpLeaseListApi                         → TestDhcpLeaseListApi
//   FS-DhcpServerStatusApi                      → TestDhcpServerStatusApi
//
// Packet-level scenarios (FS-DhcpDiscoverOfferRequestAck, FS-DhcpLeaseRenewal,
// FS-DhcpLeaseRelease, FS-DhcpLeaseExpiry, FS-DhcpPoolExhaustion,
// FS-DhcpArpConflictDetection, FS-DhcpStaticAssignmentHonoured,
// FS-DhcpStaticAssignmentPriorityOverPool, FS-DhcpLeaderFailoverTransfersOwnership)
// require raw DHCP packet injection and a root-owned UDP listener on port 67;
// they are validated via manual Proxmox 3-node testing per the project
// real-env policy.
//
// All tests interact exclusively through the HTTP management API — black-box.
package acceptance

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

// ─── shapes ──────────────────────────────────────────────────────────────────

type dhcpStatusResp struct {
	Enabled          bool   `json:"enabled"`
	IsLeader         bool   `json:"is_leader"`
	PoolStart        string `json:"pool_start"`
	PoolEnd          string `json:"pool_end"`
	Gateway          string `json:"gateway"`
	LeaseTimeSeconds int    `json:"lease_time_seconds"`
	Domain           string `json:"domain"`
	DNSServer        string `json:"dns_server"`
	LeasesActive     int    `json:"leases_active"`
	PoolTotal        int    `json:"pool_total"`
}

type dhcpStaticEntry struct {
	MAC      string `json:"mac"`
	IP       string `json:"ip"`
	Hostname string `json:"hostname"`
}

type dhcpLeaseEntry struct {
	IP       string `json:"ip"`
	MAC      string `json:"mac"`
	Hostname string `json:"hostname"`
	Origin   string `json:"origin"`
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// getDhcpStatus calls GET /api/v1/dhcp/server/status.
// Skips if the endpoint returns 404 (feature not yet implemented).
func getDhcpStatus(t *testing.T, n *Node) dhcpStatusResp {
	t.Helper()
	resp := n.apiDo(t, http.MethodGet, "/api/v1/dhcp/server/status", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("DHCP server feature not yet implemented (404)")
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/v1/dhcp/server/status: want 200, got %d: %s", resp.StatusCode, b)
	}
	var s dhcpStatusResp
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatalf("decode dhcp status: %v", err)
	}
	return s
}

func putDhcpSettings(t *testing.T, n *Node, settings map[string]any) *http.Response {
	t.Helper()
	return n.apiDo(t, http.MethodPut, "/api/v1/settings/dhcp", mustJSON(t, settings))
}

func listDhcpStaticEntries(t *testing.T, n *Node) []dhcpStaticEntry {
	t.Helper()
	resp := n.apiDo(t, http.MethodGet, "/api/v1/dhcp/static-assignments", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("DHCP static-assignments endpoint not yet implemented (404)")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/dhcp/static-assignments: want 200, got %d", resp.StatusCode)
	}
	var out []dhcpStaticEntry
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode static assignments: %v", err)
	}
	if out == nil {
		out = []dhcpStaticEntry{}
	}
	return out
}

func postDhcpStaticEntry(t *testing.T, n *Node, mac, ip, hostname string) *http.Response {
	t.Helper()
	body := mustJSON(t, map[string]string{"mac": mac, "ip": ip, "hostname": hostname})
	return n.apiDo(t, http.MethodPost, "/api/v1/dhcp/static-assignments", body)
}

func deleteDhcpStaticEntry(t *testing.T, n *Node, mac string) *http.Response {
	t.Helper()
	return n.apiDo(t, http.MethodDelete, fmt.Sprintf("/api/v1/dhcp/static-assignments/%s", mac), "")
}

// minPool returns a PUT body for a small valid pool in a non-routable range.
func minPool() map[string]any {
	return map[string]any{
		"pool_start":         "192.168.199.100",
		"pool_end":           "192.168.199.200",
		"gateway":            "192.168.199.1",
		"lease_time_seconds": 3600,
		"domain":             "test.local",
	}
}

// ─── tests ───────────────────────────────────────────────────────────────────

// FS-DhcpServerDisabledByDefault
func TestDhcpServerDisabledByDefault(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})
	setupAuth(t, n)

	status := getDhcpStatus(t, n)
	if status.Enabled {
		t.Error("DHCP server must be disabled by default; got enabled=true")
	}
}

// FS-DhcpServerEnableViaApi
func TestDhcpServerEnableViaApi(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})
	setupAuth(t, n)

	// configure pool first, then enable in a second call
	resp := putDhcpSettings(t, n, minPool())
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("DHCP settings endpoint not yet implemented (404)")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT pool config: want 200, got %d", resp.StatusCode)
	}

	resp = putDhcpSettings(t, n, map[string]any{"enabled": true})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT enabled=true: want 200, got %d", resp.StatusCode)
	}

	if status := getDhcpStatus(t, n); !status.Enabled {
		t.Error("expected enabled=true after PUT; got false")
	}
}

// FS-DhcpServerDisableViaApi
func TestDhcpServerDisableViaApi(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})
	setupAuth(t, n)

	pool := minPool()
	pool["enabled"] = true
	resp := putDhcpSettings(t, n, pool)
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("DHCP settings endpoint not yet implemented (404)")
	}

	resp = putDhcpSettings(t, n, map[string]any{"enabled": false})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT enabled=false: want 200, got %d", resp.StatusCode)
	}

	if status := getDhcpStatus(t, n); status.Enabled {
		t.Error("expected enabled=false after disable PUT; got true")
	}
}

// FS-DhcpServerConfigPersisted
func TestDhcpServerConfigPersisted(t *testing.T) {
	t.Parallel()
	cl := startCluster(t, 1)
	n := cl.Node(0).Node

	pool := minPool()
	pool["enabled"] = true
	resp := putDhcpSettings(t, n, pool)
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("DHCP settings endpoint not yet implemented (404)")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT pool+enabled: want 200, got %d", resp.StatusCode)
	}

	cl.KillNode(t, 0)
	cl.RestartNode(t, 0)
	waitReady(t, cl.Node(0).Node)
	cl.Node(0).Node.sessionToken = loginSession(t, cl.Node(0).Node, defaultUsername, defaultPassword)

	status := getDhcpStatus(t, cl.Node(0).Node)
	if !status.Enabled {
		t.Error("DHCP enabled flag not persisted across restart")
	}
	if status.PoolStart != "192.168.199.100" {
		t.Errorf("pool_start not persisted: got %q", status.PoolStart)
	}
	if status.PoolEnd != "192.168.199.200" {
		t.Errorf("pool_end not persisted: got %q", status.PoolEnd)
	}
}

// FS-DhcpOptionsDelivered — config fields reflected in status
func TestDhcpServerStatusReflectsPoolConfig(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})
	setupAuth(t, n)

	pool := map[string]any{
		"pool_start":         "10.0.5.10",
		"pool_end":           "10.0.5.50",
		"gateway":            "10.0.5.1",
		"lease_time_seconds": 7200,
		"domain":             "lab.internal",
		"dns_server":         "10.0.5.2",
	}
	resp := putDhcpSettings(t, n, pool)
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("DHCP settings endpoint not yet implemented (404)")
	}

	s := getDhcpStatus(t, n)
	if s.PoolStart != "10.0.5.10" {
		t.Errorf("pool_start: want 10.0.5.10, got %q", s.PoolStart)
	}
	if s.PoolEnd != "10.0.5.50" {
		t.Errorf("pool_end: want 10.0.5.50, got %q", s.PoolEnd)
	}
	if s.Gateway != "10.0.5.1" {
		t.Errorf("gateway: want 10.0.5.1, got %q", s.Gateway)
	}
	if s.LeaseTimeSeconds != 7200 {
		t.Errorf("lease_time_seconds: want 7200, got %d", s.LeaseTimeSeconds)
	}
	if s.Domain != "lab.internal" {
		t.Errorf("domain: want lab.internal, got %q", s.Domain)
	}
	if s.DNSServer != "10.0.5.2" {
		t.Errorf("dns_server: want 10.0.5.2, got %q", s.DNSServer)
	}
	// pool 10.0.5.10–10.0.5.50 = 41 addresses
	if s.PoolTotal != 41 {
		t.Errorf("pool_total: want 41, got %d", s.PoolTotal)
	}
}

// FS-DhcpDnsOptionDefaultsToSelf
func TestDhcpDnsOptionDefaultsToSelf(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})
	setupAuth(t, n)

	// pool without dns_server override
	resp := putDhcpSettings(t, n, minPool())
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("DHCP settings endpoint not yet implemented (404)")
	}

	s := getDhcpStatus(t, n)
	if s.DNSServer == "" {
		t.Error("dns_server must default to skoed's own listen address; got empty")
	}
}

// FS-DhcpServerStatusApi
func TestDhcpServerStatusApi(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})
	setupAuth(t, n)

	pool := minPool()
	pool["enabled"] = true
	resp := putDhcpSettings(t, n, pool)
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("DHCP settings endpoint not yet implemented (404)")
	}

	s := getDhcpStatus(t, n)
	if !s.Enabled {
		t.Error("enabled: want true")
	}
	if !s.IsLeader {
		t.Error("is_leader: want true for single-node (always leader)")
	}
	if s.PoolTotal == 0 {
		t.Error("pool_total must be > 0 when pool is configured")
	}
}

// FS-DhcpLeaseListApi
func TestDhcpLeaseListApi(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})
	setupAuth(t, n)

	resp := n.apiDo(t, http.MethodGet, "/api/v1/dhcp/leases", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("DHCP leases endpoint not yet implemented (404)")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/dhcp/leases: want 200, got %d", resp.StatusCode)
	}
	var result struct {
		Leases []dhcpLeaseEntry `json:"leases"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode leases: %v", err)
	}
	// fresh node: leases field must not be null
	if result.Leases == nil {
		t.Error("leases field must be a JSON array (not null)")
	}
}

// FS-DhcpStaticAssignmentPersistedToConfig
func TestDhcpStaticAssignmentPersisted(t *testing.T) {
	t.Parallel()
	cl := startCluster(t, 1)
	n := cl.Node(0).Node

	// gate: skip if feature not present
	listDhcpStaticEntries(t, n)

	resp := postDhcpStaticEntry(t, n, "aa:bb:cc:dd:ee:01", "192.168.1.50", "printer")
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST static-assignments: want 201, got %d", resp.StatusCode)
	}

	entries := listDhcpStaticEntries(t, n)
	found := false
	for _, e := range entries {
		if e.MAC == "aa:bb:cc:dd:ee:01" {
			found = true
			if e.IP != "192.168.1.50" {
				t.Errorf("IP: want 192.168.1.50, got %q", e.IP)
			}
			if e.Hostname != "printer" {
				t.Errorf("hostname: want printer, got %q", e.Hostname)
			}
		}
	}
	if !found {
		t.Fatal("created static assignment not found in list")
	}

	// restart and verify persistence
	cl.KillNode(t, 0)
	cl.RestartNode(t, 0)
	waitReady(t, cl.Node(0).Node)
	cl.Node(0).Node.sessionToken = loginSession(t, cl.Node(0).Node, defaultUsername, defaultPassword)

	entries = listDhcpStaticEntries(t, cl.Node(0).Node)
	found = false
	for _, e := range entries {
		if e.MAC == "aa:bb:cc:dd:ee:01" {
			found = true
		}
	}
	if !found {
		t.Error("static assignment not present after node restart")
	}
}

// FS-DhcpStaticAssignmentDelete
func TestDhcpStaticAssignmentDelete(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})
	setupAuth(t, n)

	// gate
	listDhcpStaticEntries(t, n)

	resp := postDhcpStaticEntry(t, n, "aa:bb:cc:dd:ee:02", "192.168.1.51", "nas")
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST static-assignment: want 201, got %d", resp.StatusCode)
	}

	resp = deleteDhcpStaticEntry(t, n, "aa:bb:cc:dd:ee:02")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE: want 204, got %d", resp.StatusCode)
	}

	for _, e := range listDhcpStaticEntries(t, n) {
		if e.MAC == "aa:bb:cc:dd:ee:02" {
			t.Error("deleted static assignment still present")
		}
	}

	// second delete → 404
	resp = deleteDhcpStaticEntry(t, n, "aa:bb:cc:dd:ee:02")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("second DELETE: want 404, got %d", resp.StatusCode)
	}
}

// FS-DhcpStaticAssignmentReplicatedViaRaft
func TestDhcpStaticAssignmentRaft(t *testing.T) {
	t.Parallel()
	cl := startCluster(t, 3)
	leader := cl.Leader(t).Node

	// gate
	listDhcpStaticEntries(t, leader)

	resp := postDhcpStaticEntry(t, leader, "aa:bb:cc:dd:ee:03", "192.168.1.52", "switch")
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST on leader: want 201, got %d", resp.StatusCode)
	}

	cl.WaitConverged(t)

	for i, follower := range cl.Followers(t) {
		entries := listDhcpStaticEntries(t, follower.Node)
		found := false
		for _, e := range entries {
			if e.MAC == "aa:bb:cc:dd:ee:03" {
				found = true
			}
		}
		if !found {
			t.Errorf("static assignment not replicated to follower %d", i)
		}
	}
}

// FS-DhcpLeaderOwnsListener
func TestDhcpLeaderOwnsListener(t *testing.T) {
	t.Parallel()
	cl := startCluster(t, 3)
	leader := cl.Leader(t).Node

	pool := minPool()
	pool["enabled"] = true
	resp := putDhcpSettings(t, leader, pool)
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("DHCP settings endpoint not yet implemented (404)")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT pool+enabled on leader: want 200, got %d", resp.StatusCode)
	}

	leaderStatus := getDhcpStatus(t, leader)
	if !leaderStatus.IsLeader {
		t.Error("leader: is_leader must be true")
	}
	if !leaderStatus.Enabled {
		t.Error("leader: enabled must be true")
	}

	for i, follower := range cl.Followers(t) {
		s := getDhcpStatus(t, follower.Node)
		if s.IsLeader {
			t.Errorf("follower %d: is_leader must be false", i)
		}
		// enabled reflects shared cluster config — all nodes report true
		if !s.Enabled {
			t.Errorf("follower %d: enabled should be true (shared config)", i)
		}
	}
}
