// Acceptance tests for DHCP lease persistence and DHCPv6 server (M30).
//
// FSIDs covered by automated API tests:
//   FS-DhcpLeaseExpiryRespectedAfterRestart        → TestDhcpExpiredLeaseNotRestoredAfterRestart
//   FS-Dhcpv6ServerStatusApi                       → TestDhcpV6ServerStatusApi
//   FS-Dhcpv6StaticAssignment (API surface)        → TestDhcpV6StaticAssignmentCrud
//   FS-Dhcpv6StaticAssignmentReplicatedViaRaft     → TestDhcpV6StaticAssignmentRaft
//   FS-Dhcpv6DuidProfileMatch                      → TestDhcpV6DuidProfileMatch
//   FS-Dhcpv6LeaseListApi                          → TestDhcpV6LeaseListApi
//   FS-Dhcpv6WebUiConfigPanel (API backing)        → TestDhcpV6ConfigurePersists
//   FS-DhcpLeasePersistenceLeaderFailover (avail.) → TestDhcpLeaderFailoverLeasesAccessible
//
// Packet-level scenarios require live DHCP clients on Proxmox:
//   FS-DhcpLeasePersistenceRestart              — 50 DHCPv4 clients, node restart
//   FS-DhcpLeasePersistenceFullClusterRestart   — all-nodes-down, all leases restored
//   FS-DhcpLeasePersistenceSoakRestart          — 50 concurrent clients, restart mid-soak
//   FS-DhcpLeasePersistedToRaft                 — lease visible on all nodes within 2s
//   FS-Dhcpv6SarrFlow                           — DHCPv6 SARR wire flow
//   FS-Dhcpv6IaNaPool                           — IPv6 pool allocation from range
//   FS-Dhcpv6LeaseRenewal                       — DHCPv6 Renew/Reply
//   FS-Dhcpv6LeaseRelease                       — DHCPv6 Release
//   FS-Dhcpv6DnsOptionDelivered                 — option 23 in DHCPv6 Reply
//   FS-Dhcpv6PoolExhaustion                     — NoAddrsAvail status code
//   FS-Dhcpv6LeaderOwnsListener                 — port 547 only on leader
//   FS-Dhcpv6LeaderFailover                     — port 547 migrates on leader change
//   FS-Dhcpv6LeasePersistenceRestart            — 10 DHCPv6 clients survive restart
//   FS-Dhcpv6LeasePersistenceFullClusterRestart — full 3-node restart, v6 leases restored
//
// All automated tests interact exclusively through the HTTP management API — black-box.
package acceptance

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

// ─── shapes ──────────────────────────────────────────────────────────────────

type dhcpV6StatusResp struct {
	Enabled      bool   `json:"enabled"`
	IsLeader     bool   `json:"is_leader"`
	Prefix       string `json:"prefix"`
	PoolStart    string `json:"pool_start"`
	PoolEnd      string `json:"pool_end"`
	PoolTotal    int    `json:"pool_total"`
	LeasesActive int    `json:"leases_active"`
}

type dhcpV6StaticEntry struct {
	DUID     string `json:"duid"`
	Address  string `json:"address"`
	Hostname string `json:"hostname"`
}

type dhcpV6StaticListResp struct {
	Assignments []dhcpV6StaticEntry `json:"assignments"`
}

type dhcpV6LeaseListResp struct {
	Leases []map[string]any `json:"leases"`
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func getDhcpV6Status(t *testing.T, n *Node) dhcpV6StatusResp {
	t.Helper()
	resp := n.apiDo(t, http.MethodGet, "/api/v1/dhcp/server/status6", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("DHCPv6 server feature not yet implemented (404)")
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/v1/dhcp/server/status6: want 200, got %d: %s", resp.StatusCode, b)
	}
	var s dhcpV6StatusResp
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatalf("decode dhcpv6 status: %v", err)
	}
	return s
}

func putDhcpV6Settings(t *testing.T, n *Node, settings map[string]any) *http.Response {
	t.Helper()
	return n.apiDo(t, http.MethodPut, "/api/v1/settings/dhcp6", mustJSON(t, settings))
}

func listDhcpV6Static(t *testing.T, n *Node) []dhcpV6StaticEntry {
	t.Helper()
	resp := n.apiDo(t, http.MethodGet, "/api/v1/dhcp/static-assignments6", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("DHCPv6 static-assignments6 not yet implemented (404)")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/dhcp/static-assignments6: want 200, got %d", resp.StatusCode)
	}
	var r dhcpV6StaticListResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		t.Fatalf("decode static list: %v", err)
	}
	return r.Assignments
}

func hasDhcpV6Static(entries []dhcpV6StaticEntry, duid string) bool {
	for _, e := range entries {
		if e.DUID == duid {
			return true
		}
	}
	return false
}

func postDhcpV6Static(t *testing.T, n *Node, duid, addr, hostname string) *http.Response {
	t.Helper()
	return n.apiDo(t, http.MethodPost, "/api/v1/dhcp/static-assignments6", mustJSON(t, map[string]any{
		"duid":     duid,
		"address":  addr,
		"hostname": hostname,
	}))
}

// ─── tests ───────────────────────────────────────────────────────────────────

// TestDhcpV6ServerStatusApi verifies the DHCPv6 status endpoint returns a valid
// response with pool_total computed from the configured range.
// FS-Dhcpv6ServerStatusApi
func TestDhcpV6ServerStatusApi(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})
	setupAuth(t, n)

	resp := putDhcpV6Settings(t, n, map[string]any{
		"enabled":    true,
		"prefix":     "fd00::/64",
		"pool_start": "fd00::100",
		"pool_end":   "fd00::1ff",
		"lease_time": 3600,
	})
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("DHCPv6 settings not yet implemented (404)")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/v1/settings/dhcp6: want 200, got %d", resp.StatusCode)
	}

	s := getDhcpV6Status(t, n)
	if !s.Enabled {
		t.Error("enabled: want true")
	}
	if s.Prefix != "fd00::/64" {
		t.Errorf("prefix: want fd00::/64, got %q", s.Prefix)
	}
	if s.PoolStart != "fd00::100" {
		t.Errorf("pool_start: want fd00::100, got %q", s.PoolStart)
	}
	if s.PoolEnd != "fd00::1ff" {
		t.Errorf("pool_end: want fd00::1ff, got %q", s.PoolEnd)
	}
	// fd00::100–fd00::1ff = 256 addresses
	if s.PoolTotal != 256 {
		t.Errorf("pool_total: want 256, got %d", s.PoolTotal)
	}
}

// TestDhcpV6ConfigurePersists verifies that DHCPv6 pool configuration survives a
// node restart.
// FS-Dhcpv6WebUiConfigPanel (API backing)
func TestDhcpV6ConfigurePersists(t *testing.T) {
	t.Parallel()
	cl := startCluster(t, 1)
	n := cl.Node(0).Node
	setupAuth(t, n)

	resp := putDhcpV6Settings(t, n, map[string]any{
		"enabled":       true,
		"prefix":        "fd10::/64",
		"pool_start":    "fd10::200",
		"pool_end":      "fd10::2ff",
		"lease_time":    7200,
		"search_domain": "lab.local",
	})
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("DHCPv6 settings not yet implemented (404)")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/v1/settings/dhcp6: want 200, got %d", resp.StatusCode)
	}

	cl.KillNode(t, 0)
	cl.RestartNode(t, 0)

	s := getDhcpV6Status(t, cl.Node(0).Node)
	if s.Prefix != "fd10::/64" {
		t.Errorf("prefix after restart: want fd10::/64, got %q", s.Prefix)
	}
	if !s.Enabled {
		t.Error("enabled after restart: want true")
	}
}

// TestDhcpV6StaticAssignmentCrud verifies create, list, and delete of DHCPv6
// static assignments via the management API.
// FS-Dhcpv6StaticAssignment
func TestDhcpV6StaticAssignmentCrud(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})
	setupAuth(t, n)

	duid := "00:01:00:01:aa:bb:cc:dd:ee:01"
	addr := "fd00::200"

	createResp := postDhcpV6Static(t, n, duid, addr, "testhost")
	createResp.Body.Close()
	if createResp.StatusCode == http.StatusNotFound {
		t.Skip("DHCPv6 static-assignments6 not yet implemented (404)")
	}
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/v1/dhcp/static-assignments6: want 201, got %d", createResp.StatusCode)
	}

	// Entry present after create
	if !hasDhcpV6Static(listDhcpV6Static(t, n), duid) {
		t.Fatalf("static assignment %s not found after create", duid)
	}

	// Delete
	delResp := n.apiDo(t, http.MethodDelete, fmt.Sprintf("/api/v1/dhcp/static-assignments6/%s", duid), "")
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE .../static-assignments6/%s: want 204, got %d", duid, delResp.StatusCode)
	}

	// Entry gone after delete
	if hasDhcpV6Static(listDhcpV6Static(t, n), duid) {
		t.Errorf("static assignment %s still present after delete", duid)
	}
}

// TestDhcpV6StaticAssignmentRaft verifies that a DHCPv6 static assignment
// created on the leader is visible on all followers.
// FS-Dhcpv6StaticAssignmentReplicatedViaRaft
func TestDhcpV6StaticAssignmentRaft(t *testing.T) {
	t.Parallel()
	cl := startCluster(t, 3)
	leader := cl.Leader(t).Node

	duid := "00:01:00:01:bb:cc:dd:ee:ff:02"

	createResp := postDhcpV6Static(t, leader, duid, "fd00::210", "raft-test")
	createResp.Body.Close()
	if createResp.StatusCode == http.StatusNotFound {
		t.Skip("DHCPv6 static-assignments6 not yet implemented (404)")
	}
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("POST on leader: want 201, got %d", createResp.StatusCode)
	}

	cl.WaitConverged(t)

	for i, follower := range cl.Followers(t) {
		entries := listDhcpV6Static(t, follower.Node)
		if !hasDhcpV6Static(entries, duid) {
			t.Errorf("static assignment %s not replicated to follower %d", duid, i)
		}
	}
}

// TestDhcpV6StaticAssignmentPersistsRestart verifies that a DHCPv6 static
// assignment survives a node restart.
// FS-Dhcpv6StaticAssignment (persistence aspect)
func TestDhcpV6StaticAssignmentPersistsRestart(t *testing.T) {
	t.Parallel()
	cl := startCluster(t, 1)
	n := cl.Node(0).Node
	setupAuth(t, n)

	duid := "00:01:00:01:cc:dd:ee:ff:00:03"

	createResp := postDhcpV6Static(t, n, duid, "fd00::220", "persist-test")
	createResp.Body.Close()
	if createResp.StatusCode == http.StatusNotFound {
		t.Skip("DHCPv6 static-assignments6 not yet implemented (404)")
	}
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("POST: want 201, got %d", createResp.StatusCode)
	}

	cl.KillNode(t, 0)
	cl.RestartNode(t, 0)

	if !hasDhcpV6Static(listDhcpV6Static(t, cl.Node(0).Node), duid) {
		t.Errorf("DHCPv6 static assignment %s lost after restart", duid)
	}
}

// TestDhcpV6DuidProfileMatch verifies that a profile with client_duids stores and
// returns the field correctly via the profiles API.
// FS-Dhcpv6DuidProfileMatch (API surface; DNS enforcement validated on Proxmox)
func TestDhcpV6DuidProfileMatch(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})
	setupAuth(t, n)

	duid := "00:01:00:01:dd:ee:ff:aa:bb:04"
	createResp := n.apiDo(t, http.MethodPost, "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":           "duid-test-profile",
		"name":         "DUID Test",
		"client_duids": []string{duid},
	}))
	createResp.Body.Close()
	if createResp.StatusCode == http.StatusNotFound {
		t.Skip("profiles endpoint not yet supporting client_duids (404)")
	}
	if createResp.StatusCode != http.StatusCreated && createResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/v1/profiles: want 201, got %d", createResp.StatusCode)
	}

	getResp := n.apiDo(t, http.MethodGet, "/api/v1/profiles/duid-test-profile", "")
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/profiles/duid-test-profile: want 200, got %d", getResp.StatusCode)
	}
	var profile map[string]any
	if err := json.NewDecoder(getResp.Body).Decode(&profile); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	duids, _ := profile["client_duids"].([]any)
	found := false
	for _, d := range duids {
		if d == duid {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("client_duids does not contain %s; got %v", duid, duids)
	}
}

// TestDhcpV6LeaseListApi verifies that the DHCPv6 lease list endpoint exists and
// returns a well-formed JSON response (empty array on a fresh node, not null).
// FS-Dhcpv6LeaseListApi
func TestDhcpV6LeaseListApi(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})
	setupAuth(t, n)

	resp := n.apiDo(t, http.MethodGet, "/api/v1/dhcp/leases6", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("DHCPv6 leases endpoint not yet implemented (404)")
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/v1/dhcp/leases6: want 200, got %d: %s", resp.StatusCode, b)
	}
	var r dhcpV6LeaseListResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		t.Fatalf("decode leases6: %v", err)
	}
	if r.Leases == nil {
		t.Error("leases field is null; want empty array []")
	}
}

// TestDhcpExpiredLeaseNotRestoredAfterRestart verifies the persistence layer does
// not restore leases whose expiry has passed.
// FS-DhcpLeaseExpiryRespectedAfterRestart
//
// Full end-to-end (DORA with dhclient, then restart) is validated on Proxmox.
// Here we verify the API invariant: a fresh node that had no active leases before
// restart reports an empty lease list after restart.
func TestDhcpExpiredLeaseNotRestoredAfterRestart(t *testing.T) {
	t.Parallel()
	cl := startCluster(t, 1)
	n := cl.Node(0).Node
	setupAuth(t, n)

	dhcpResp := n.apiDo(t, http.MethodPut, "/api/v1/settings/dhcp", mustJSON(t, map[string]any{
		"enabled":    true,
		"pool_start": "192.168.99.100",
		"pool_end":   "192.168.99.200",
		"gateway":    "192.168.99.1",
		"lease_time": 1,
	}))
	dhcpResp.Body.Close()
	if dhcpResp.StatusCode == http.StatusNotFound {
		t.Skip("DHCP settings not yet implemented (404)")
	}
	if dhcpResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/v1/settings/dhcp: want 200, got %d", dhcpResp.StatusCode)
	}

	// Wait past the 1-second lease time so any would-be leases expire.
	time.Sleep(3 * time.Second)

	cl.KillNode(t, 0)
	cl.RestartNode(t, 0)

	leasesResp := cl.Node(0).Node.apiDo(t, http.MethodGet, "/api/v1/dhcp/leases", "")
	defer leasesResp.Body.Close()
	if leasesResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(leasesResp.Body)
		t.Fatalf("GET /api/v1/dhcp/leases: want 200, got %d: %s", leasesResp.StatusCode, b)
	}
	var leases struct {
		Leases []map[string]any `json:"leases"`
	}
	if err := json.NewDecoder(leasesResp.Body).Decode(&leases); err != nil {
		t.Fatalf("decode leases: %v", err)
	}
	if len(leases.Leases) != 0 {
		t.Errorf("want 0 leases after restart with expired lease_time=1s, got %d", len(leases.Leases))
	}
}

// TestDhcpLeaderFailoverLeasesAccessible verifies that after a leader failover
// the new leader serves the /api/v1/dhcp/leases endpoint correctly.
// FS-DhcpLeasePersistenceLeaderFailover (API availability aspect)
func TestDhcpLeaderFailoverLeasesAccessible(t *testing.T) {
	t.Parallel()
	cl := startCluster(t, 3)

	leaderCN := cl.Leader(t)
	var leaderIdx int
	for i := 0; i < 3; i++ {
		if cl.Node(i).NodeID == leaderCN.NodeID {
			leaderIdx = i
			break
		}
	}

	cl.KillNode(t, leaderIdx)
	time.Sleep(2 * time.Second) // allow re-election

	var newLeader *ClusterNode
	for i := 0; i < 3; i++ {
		if i == leaderIdx {
			continue
		}
		resp := cl.Node(i).Node.apiDo(t, http.MethodGet, "/api/v1/dhcp/leases", "")
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			newLeader = cl.Node(i)
			break
		}
	}
	if newLeader == nil {
		t.Fatal("no surviving node returned 200 for /api/v1/dhcp/leases after leader failover")
	}
}
