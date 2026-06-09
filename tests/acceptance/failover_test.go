// Acceptance tests for leader failover.
//
// Covers FSIDs:
//   FS-LeaderFailoverAutomaticElection
//   FS-LeaderFailoverNoSplitBrainAcrossPartition
//   FS-LeaderFailoverFormerLeaderRejoinsAsFollower
//   FS-LeaderFailoverWritesDuringTransition
//   FS-LeaderFailoverManualTransfer
package acceptance

import (
	"testing"
	"time"
)

// FS-LeaderFailoverAutomaticElection
func TestLeaderFailoverAutomaticElection(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 3)

	originalLeader := c.Leader(t)
	originalLeaderIdx := indexOf(c, originalLeader)

	// Kill the leader.
	c.KillNode(t, originalLeaderIdx)

	// A new leader is elected within Raft's normal election timeout window.
	// hashicorp/raft defaults: heartbeat 1s, election 1s + jitter. 10s is generous.
	deadline := time.Now().Add(10 * time.Second)
	var newLeader *ClusterNode
	for time.Now().Before(deadline) {
		// Skip the killed node; look at survivors only.
		for i := 0; i < c.Size(); i++ {
			if i == originalLeaderIdx {
				continue
			}
			role, ok := c.nodeRole(t, c.Node(i))
			if ok && role == "leader" {
				newLeader = c.Node(i)
				break
			}
		}
		if newLeader != nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if newLeader == nil {
		t.Fatalf("no new leader elected within 10s after killing %s", originalLeader.NodeID)
	}

	// The new leader accepts writes.
	c.MustCreateBlocklist(t, newLeader, "post-failover", "post.example.com")
}

// FS-LeaderFailoverNoSplitBrainAcrossPartition
//
// Acceptance harness limitation: true network partitions require iptables /
// network namespaces. We approximate by killing a follower (so the surviving
// 2-node majority continues) and verify the killed node, once restarted, does
// NOT elect itself a leader (because it sees the existing leader's higher
// term).
func TestLeaderFailoverNoSplitBrainAcrossPartition(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 3)
	followers := c.Followers(t)
	victimIdx := indexOf(c, followers[0])

	c.KillNode(t, victimIdx)
	// Majority of 2 continues.
	time.Sleep(2 * time.Second)
	leader := c.Leader(t)

	// Write something via the majority so the term advances.
	c.MustCreateBlocklist(t, leader, "majority-only", "ok.example.com")

	// Restart the killed node — it must rejoin as a follower, not declare itself leader.
	c.RestartNode(t, victimIdx)
	waitForRole(t, c, c.Node(victimIdx), "follower", 10*time.Second)

	// Cluster still has exactly one leader.
	s := c.Status(t, leader)
	leaderCount := 0
	for _, e := range s.Nodes {
		if e.Role == "leader" {
			leaderCount++
		}
	}
	if leaderCount != 1 {
		t.Fatalf("expected exactly 1 leader, got %d", leaderCount)
	}
}

// FS-LeaderFailoverFormerLeaderRejoinsAsFollower
func TestLeaderFailoverFormerLeaderRejoinsAsFollower(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 3)

	originalLeader := c.Leader(t)
	originalLeaderIdx := indexOf(c, originalLeader)

	c.KillNode(t, originalLeaderIdx)

	// Survivors elect a new leader.
	deadline := time.Now().Add(10 * time.Second)
	var newLeader *ClusterNode
	for time.Now().Before(deadline) && newLeader == nil {
		for i := 0; i < c.Size(); i++ {
			if i == originalLeaderIdx {
				continue
			}
			role, ok := c.nodeRole(t, c.Node(i))
			if ok && role == "leader" {
				newLeader = c.Node(i)
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if newLeader == nil {
		t.Fatalf("no new leader elected after killing original")
	}

	// Commit something via the new leader.
	c.MustCreateBlocklist(t, newLeader, "while-old-down", "x.example.com")

	// Restart the former leader. It must come back as a follower.
	c.RestartNode(t, originalLeaderIdx)
	waitForRole(t, c, c.Node(originalLeaderIdx), "follower", 10*time.Second)

	// The former leader catches up to the cluster's commit index.
	c.WaitConverged(t)
	waitForBlocklist(t, c.Node(originalLeaderIdx), "while-old-down", 10*time.Second)
}

// FS-LeaderFailoverWritesDuringTransition
//
// We submit a write right before killing the leader, then verify the cluster
// either succeeds the retry or returns a clear "no leader" status that the
// admin could act on. Either outcome satisfies the spec.
func TestLeaderFailoverWritesDuringTransition(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 3)
	originalLeader := c.Leader(t)
	originalLeaderIdx := indexOf(c, originalLeader)

	// Start the write request in the background. Use a small async indirection
	// rather than racing exactly with the kill — the goal is "request submitted
	// while transition was happening", not "submit at the exact same instant".
	type result struct {
		statusCode int
		err        error
	}
	resCh := make(chan result, 1)
	go func() {
		resp, err := originalLeader.apiDoNonFatal("POST", "/api/v1/blocklists", mustJSON(t, map[string]any{
			"id":      "transition-write",
			"name":    "during transition",
			"enabled": true,
			"domains": []string{"transition.example.com"},
			"source":  map[string]string{"format": "domainlist"},
		}))
		if err != nil {
			// connection error — server died mid-request, treated as failure
			resCh <- result{statusCode: 0, err: err}
			return
		}
		defer resp.Body.Close()
		resCh <- result{statusCode: resp.StatusCode}
	}()

	// Kill the leader immediately.
	time.Sleep(10 * time.Millisecond)
	c.KillNode(t, originalLeaderIdx)

	// Either the original write succeeded before the kill (201), or it failed
	// with 503 / 504 / connection error. In ALL failure cases, after the new
	// leader is elected, a retry succeeds.
	select {
	case r := <-resCh:
		if r.statusCode == 201 {
			return // succeeded before transition; nothing more to verify
		}
		// expected: 5xx or connection error
	case <-time.After(15 * time.Second):
		t.Fatalf("write request never returned")
	}

	// Retry against the new leader after election.
	deadline := time.Now().Add(15 * time.Second)
	var newLeader *ClusterNode
	for time.Now().Before(deadline) && newLeader == nil {
		for i := 0; i < c.Size(); i++ {
			if i == originalLeaderIdx {
				continue
			}
			role, ok := c.nodeRole(t, c.Node(i))
			if ok && role == "leader" {
				newLeader = c.Node(i)
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if newLeader == nil {
		t.Fatalf("no new leader elected for retry")
	}
	c.MustCreateBlocklist(t, newLeader, "transition-retry", "retry.example.com")
}

// FS-LeaderFailoverManualTransfer
func TestLeaderFailoverManualTransfer(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 3)
	originalLeader := c.Leader(t)
	followers := c.Followers(t)
	if len(followers) < 1 {
		t.Fatalf("need at least one follower for transfer test")
	}
	target := followers[0]

	resp := originalLeader.apiDo(t, "POST", "/api/v1/cluster/leadership/transfer",
		mustJSON(t, map[string]string{"target_node_id": target.NodeID}))
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatalf("transfer leadership: status %d body=%s",
			resp.StatusCode, readBody(t, resp))
	}

	// Within 5s, the target reports itself as leader.
	waitForRole(t, c, target, "leader", 5*time.Second)
	// The original leader reports as follower.
	waitForRole(t, c, originalLeader, "follower", 5*time.Second)

	// Sanity: the cluster still functions.
	c.MustCreateBlocklist(t, target, "after-transfer", "tx.example.com")
}
