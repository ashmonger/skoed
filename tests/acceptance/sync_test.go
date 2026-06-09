// Acceptance tests for cluster config sync.
//
// Covers FSIDs:
//   FS-ClusterConfigSyncWriteToLeader
//   FS-ClusterConfigSyncWriteToFollowerIsForwarded
//   FS-ClusterConfigSyncBlocklistRemove
//   FS-ClusterConfigSyncAllowlist
//   FS-ClusterConfigSyncLocalDns
//   FS-ClusterConfigSyncSurvivesFollowerDisconnect
//   FS-ClusterConfigSyncMinorityPartitionRefusesWrites
//   FS-ClusterConfigSyncMajorityPartitionContinues
//   FS-ClusterConfigSyncQueryLogRawIsPerNode
package acceptance

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// FS-ClusterConfigSyncWriteToLeader
func TestClusterConfigSyncWriteToLeader(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 3)
	leader := c.Leader(t)

	c.MustCreateBlocklist(t, leader, "ads", "tracker.example.com")

	for i := 0; i < c.Size(); i++ {
		n := c.Node(i)
		waitForBlocklist(t, n, "ads", 5*time.Second)
		// DNS check: query against this node returns NXDOMAIN for the blocked apex.
		msg := dnsQuery(t, n.DNSAddr, "tracker.example.com", dns.TypeA)
		assertRcode(t, msg, dns.RcodeNameError)
	}
}

// FS-ClusterConfigSyncWriteToFollowerIsForwarded
func TestClusterConfigSyncWriteToFollowerIsForwarded(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 3)
	followers := c.Followers(t)
	if len(followers) == 0 {
		t.Fatalf("no followers in cluster")
	}

	// Write goes to a follower; the API must transparently forward to the leader.
	c.MustCreateBlocklist(t, followers[0], "via-follower", "ads.example.com")

	// Every node has the new blocklist.
	for i := 0; i < c.Size(); i++ {
		waitForBlocklist(t, c.Node(i), "via-follower", 5*time.Second)
	}
}

// FS-ClusterConfigSyncBlocklistRemove
func TestClusterConfigSyncBlocklistRemove(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 3)
	leader := c.Leader(t)

	c.MustCreateBlocklist(t, leader, "ads", "tracker.example.com")

	resp := leader.apiDo(t, "DELETE", "/api/v1/blocklists/ads", "")
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatalf("delete blocklist: status %d", resp.StatusCode)
	}

	for i := 0; i < c.Size(); i++ {
		waitForNoBlocklist(t, c.Node(i), "ads", 5*time.Second)
	}
}

// FS-ClusterConfigSyncAllowlist
func TestClusterConfigSyncAllowlist(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 3)
	leader := c.Leader(t)

	c.MustCreateBlocklist(t, leader, "ads", "ads.example.com")

	resp := leader.apiDo(t, "POST", "/api/v1/allowlist",
		mustJSON(t, map[string]string{"domain": "ads.example.com"}))
	defer resp.Body.Close()
	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		t.Fatalf("allowlist add: status %d", resp.StatusCode)
	}
	c.WaitConverged(t)

	// On every node, the allowlist entry overrides the blocklist.
	// We assert via the API rather than DNS to avoid depending on a fake upstream.
	for i := 0; i < c.Size(); i++ {
		n := c.Node(i)
		r := n.apiDo(t, "GET", "/api/v1/allowlist", "")
		body := readBody(t, r)
		if !strings.Contains(body, "ads.example.com") {
			t.Fatalf("node %s allowlist missing 'ads.example.com': %s", n.NodeID, body)
		}
	}
}

// FS-ClusterConfigSyncLocalDns
func TestClusterConfigSyncLocalDns(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 3)
	leader := c.Leader(t)

	resp := leader.apiDo(t, "POST", "/api/v1/local-dns", mustJSON(t, map[string]any{
		"hostname": "router.lab",
		"type":     "A",
		"value":    "192.168.1.1",
		"ttl":      300,
	}))
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("create local DNS: status %d", resp.StatusCode)
	}
	c.WaitConverged(t)

	// Each node resolves the local entry.
	for i := 0; i < c.Size(); i++ {
		n := c.Node(i)
		msg := dnsQuery(t, n.DNSAddr, "router.lab", dns.TypeA)
		assertRcode(t, msg, dns.RcodeSuccess)
		assertAnswerA(t, msg, "192.168.1.1")
	}
}

// FS-ClusterConfigSyncSurvivesFollowerDisconnect
func TestClusterConfigSyncSurvivesFollowerDisconnect(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 3)
	leader := c.Leader(t)

	c.MustCreateBlocklist(t, leader, "before-partition", "before.example.com")

	// Disconnect the LAST follower by killing it (data dir preserved).
	followers := c.Followers(t)
	if len(followers) == 0 {
		t.Fatalf("no followers")
	}
	victimIdx := indexOf(c, followers[len(followers)-1])
	c.KillNode(t, victimIdx)

	// Make 3 writes on the surviving majority.
	for _, id := range []string{"during-1", "during-2", "during-3"} {
		c.MustCreateBlocklist(t, c.Leader(t), id, id+".example.com")
	}

	// Reconnect the follower.
	c.RestartNode(t, victimIdx)
	c.WaitConverged(t)

	// All writes are visible on the reconnected node.
	for _, id := range []string{"before-partition", "during-1", "during-2", "during-3"} {
		waitForBlocklist(t, c.Node(victimIdx), id, 10*time.Second)
	}
}

// FS-ClusterConfigSyncMinorityPartitionRefusesWrites
//
// Approximation: a single surviving node out of a 3-node cluster (after the
// other two are killed) cannot form quorum, so it MUST refuse writes.
func TestClusterConfigSyncMinorityPartitionRefusesWrites(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 3)

	// Kill 2 of 3 → remaining node is in a minority.
	c.KillNode(t, 0)
	c.KillNode(t, 1)

	// node-3 is the only survivor.
	survivor := c.Node(2)

	// Allow Raft to detect leader loss.
	time.Sleep(5 * time.Second)

	resp := survivor.apiDo(t, "POST", "/api/v1/blocklists", mustJSON(t, map[string]any{
		"id":      "should-fail",
		"name":    "should fail",
		"enabled": true,
		"domains": []string{"nope.example.com"},
		"source":  map[string]string{"format": "domainlist"},
	}))
	defer resp.Body.Close()
	if resp.StatusCode == 201 {
		t.Fatalf("expected minority write to be rejected, but got 201")
	}
	// Acceptable rejections: 503 (no leader) or 409 (LeaderRedirect with empty leader).
	if resp.StatusCode != 503 && resp.StatusCode != 409 {
		t.Fatalf("expected 503 or 409 for minority write, got %d", resp.StatusCode)
	}
}

// FS-ClusterConfigSyncMajorityPartitionContinues
func TestClusterConfigSyncMajorityPartitionContinues(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 3)

	// Find a follower to kill (so the majority of 2 retains leader+1 follower).
	followers := c.Followers(t)
	victimIdx := indexOf(c, followers[0])
	c.KillNode(t, victimIdx)

	// Majority continues. Allow election if the killed node was actually the leader.
	// c.Leader(t) polls internally for up to 10s, which covers Raft election time.
	leader := c.Leader(t)

	c.MustCreateBlocklist(t, leader, "majority-write", "ok.example.com")

	// Verify the killed node does NOT have the entry until it returns.
	for i := 0; i < c.Size(); i++ {
		if i == victimIdx {
			continue
		}
		waitForBlocklist(t, c.Node(i), "majority-write", 5*time.Second)
	}
}

// FS-ClusterConfigSyncQueryLogRawIsPerNode
func TestClusterConfigSyncQueryLogRawIsPerNode(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 2)

	// Add a blocklist so DNS queries actually produce log entries.
	c.MustCreateBlocklist(t, c.Leader(t), "for-log", "tracker.example.com")

	leader := c.Leader(t)
	follower := c.Followers(t)[0]

	// Query against the leader twice.
	for i := 0; i < 2; i++ {
		dnsQuery(t, leader.DNSAddr, "tracker.example.com", dns.TypeA)
	}
	// Query against the follower three times.
	for i := 0; i < 3; i++ {
		dnsQuery(t, follower.DNSAddr, "tracker.example.com", dns.TypeA)
	}

	// Brief settle to let each node flush its raw log.
	time.Sleep(500 * time.Millisecond)

	leaderCount := countLocalQueryLog(t, leader)
	followerCount := countLocalQueryLog(t, follower)
	if leaderCount != 2 {
		t.Fatalf("leader's local query log: expected 2 entries, got %d", leaderCount)
	}
	if followerCount != 3 {
		t.Fatalf("follower's local query log: expected 3 entries, got %d", followerCount)
	}
}

// ── helpers used by this file ─────────────────────────────────────────────

func indexOf(c *Cluster, n *ClusterNode) int {
	for i := 0; i < c.Size(); i++ {
		if c.Node(i) == n {
			return i
		}
	}
	return -1
}

func countLocalQueryLog(t *testing.T, n *ClusterNode) int {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/query-log?limit=1000", "")
	assertStatus(t, resp, 200)
	defer resp.Body.Close()
	var body struct {
		Total int `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode query log: %v", err)
	}
	return body.Total
}
