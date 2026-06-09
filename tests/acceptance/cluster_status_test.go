// Acceptance tests for cluster status.
//
// Covers FSIDs:
//   FS-ClusterStatusListsAllNodes
//   FS-ClusterStatusShowsCommitIndex
//   FS-ClusterStatusShowsLaggingFollower
//   FS-ClusterStatusShowsUnreachableFollower
//   FS-ClusterStatusSameViewFromAnyNode
//   FS-ClusterStatusShowsRaftTerm
package acceptance

import (
	"syscall"
	"testing"
	"time"
)

// FS-ClusterStatusListsAllNodes
func TestClusterStatusListsAllNodes(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 3)

	for i := 0; i < c.Size(); i++ {
		n := c.Node(i)
		s := c.Status(t, n)
		if len(s.Nodes) != 3 {
			t.Fatalf("node %s reports %d entries, expected 3", n.NodeID, len(s.Nodes))
		}
		leaders, followers := 0, 0
		for _, e := range s.Nodes {
			switch e.Role {
			case "leader":
				leaders++
			case "follower":
				followers++
			}
		}
		if leaders != 1 || followers != 2 {
			t.Fatalf("node %s: expected 1 leader and 2 followers, got leaders=%d followers=%d",
				n.NodeID, leaders, followers)
		}
		// last_contact should be recent for every alive node.
		for _, e := range s.Nodes {
			if e.LastContact == "" {
				t.Fatalf("node %s in %s's view has empty last_contact", e.NodeID, n.NodeID)
			}
			lc, err := time.Parse(time.RFC3339, e.LastContact)
			if err != nil {
				t.Fatalf("parse last_contact %q: %v", e.LastContact, err)
			}
			if time.Since(lc) > 30*time.Second {
				t.Fatalf("node %s reports stale last_contact for %s: %v ago",
					n.NodeID, e.NodeID, time.Since(lc))
			}
		}
	}
}

// FS-ClusterStatusShowsCommitIndex
func TestClusterStatusShowsCommitIndex(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 3)
	leader := c.Leader(t)

	// Drive the commit index forward with a write.
	c.MustCreateBlocklist(t, leader, "bump", "bump.example.com")

	// Every node reports the SAME (or near-same) commit index.
	s := c.Status(t, leader)
	var leaderIdx int
	for _, e := range s.Nodes {
		if e.NodeID == leader.NodeID {
			leaderIdx = e.CommitIndex
			break
		}
	}
	if leaderIdx <= 0 {
		t.Fatalf("leader commit_index should be > 0 after a write, got %d", leaderIdx)
	}

	c.WaitConverged(t)

	for i := 0; i < c.Size(); i++ {
		n := c.Node(i)
		s := c.Status(t, n)
		for _, e := range s.Nodes {
			if e.CommitIndex < leaderIdx {
				t.Fatalf("from node %s's view, node %s reports commit_index=%d < leader's %d",
					n.NodeID, e.NodeID, e.CommitIndex, leaderIdx)
			}
		}
	}
}

// FS-ClusterStatusShowsLaggingFollower
//
// Induce real lag by SIGSTOP-ing a follower process so it can't apply
// AppendEntries RPCs from the leader. After writes on the leader, the leader's
// status must report the stopped follower with commit_index < leader's AND
// sync_state != "in_sync". After SIGCONT and convergence, sync_state returns
// to "in_sync". This guarantees a buggy implementation that always returns
// "in_sync" cannot pass.
func TestClusterStatusShowsLaggingFollower(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 3)
	leader := c.Leader(t)

	// Drive the commit index forward once so we have a baseline.
	c.MustCreateBlocklist(t, leader, "baseline", "baseline.example.com")

	// Pause one follower so it can't apply further log entries.
	followers := c.Followers(t)
	if len(followers) < 1 {
		t.Fatalf("no followers")
	}
	victim := followers[0]
	if err := victim.cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatalf("SIGSTOP victim: %v", err)
	}

	// Always unpause, even on failure.
	defer func() { _ = victim.cmd.Process.Signal(syscall.SIGCONT) }()

	// Commit a few writes on the leader. The paused follower can't keep up.
	// Use apiDoNonFatal directly — MustCreateBlocklist's embedded WaitConverged
	// would block on the paused follower.
	for _, id := range []string{"lag-1", "lag-2", "lag-3"} {
		body := mustJSON(t, map[string]any{
			"id":      id,
			"name":    "lag",
			"enabled": true,
			"domains": []string{id + ".example.com"},
			"source":  map[string]string{"format": "domainlist"},
		})
		resp, err := leader.apiDoNonFatal("POST", "/api/v1/blocklists", body)
		if err != nil {
			t.Fatalf("commit %s: %v", id, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 201 {
			t.Fatalf("commit %s: status %d", id, resp.StatusCode)
		}
	}

	// The paused follower should be visibly behind the leader. Poll briefly:
	// give Raft up to a couple of heartbeat intervals to update last_contact.
	deadline := time.Now().Add(5 * time.Second)
	var sawLag bool
	for time.Now().Before(deadline) {
		s := c.Status(t, leader)
		var leaderIdx, victimIdx int
		var victimSync string
		for _, e := range s.Nodes {
			if e.NodeID == leader.NodeID {
				leaderIdx = e.CommitIndex
			}
			if e.NodeID == victim.NodeID {
				victimIdx = e.CommitIndex
				victimSync = e.SyncState
			}
		}
		if victimIdx < leaderIdx && victimSync != "in_sync" {
			sawLag = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !sawLag {
		t.Fatalf("leader never reported paused follower as behind/unreachable")
	}

	// Resume and converge: everyone in_sync.
	if err := victim.cmd.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatalf("SIGCONT: %v", err)
	}
	c.WaitConverged(t)
	final := c.Status(t, leader)
	for _, e := range final.Nodes {
		if e.Role == "follower" && e.SyncState != "in_sync" {
			t.Fatalf("after resume + convergence, follower %s is %s, want in_sync",
				e.NodeID, e.SyncState)
		}
	}
}

// FS-ClusterStatusShowsUnreachableFollower
func TestClusterStatusShowsUnreachableFollower(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 3)
	followers := c.Followers(t)
	if len(followers) < 1 {
		t.Fatalf("need at least one follower")
	}
	victim := followers[0]
	victimIdx := indexOf(c, victim)

	c.KillNode(t, victimIdx)

	// Wait for the leader to notice (Raft heartbeat default 1s; allow 15s).
	deadline := time.Now().Add(15 * time.Second)
	saw := false
	for time.Now().Before(deadline) && !saw {
		s := c.Status(t, c.Leader(t))
		for _, e := range s.Nodes {
			if e.NodeID == victim.NodeID && e.SyncState == "unreachable" {
				saw = true
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !saw {
		t.Fatalf("leader did not flag killed follower %s as unreachable within 15s", victim.NodeID)
	}
}

// FS-ClusterStatusSameViewFromAnyNode
func TestClusterStatusSameViewFromAnyNode(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 3)
	leader := c.Leader(t)

	c.MustCreateBlocklist(t, leader, "shared-view", "view.example.com")
	c.WaitConverged(t)

	leaderView := c.Status(t, leader)
	for i := 0; i < c.Size(); i++ {
		n := c.Node(i)
		if n == leader {
			continue
		}
		followerView := c.Status(t, n)
		if followerView.LeaderID != leaderView.LeaderID {
			t.Fatalf("follower %s sees leader_id=%q, leader sees %q",
				n.NodeID, followerView.LeaderID, leaderView.LeaderID)
		}
		if len(followerView.Nodes) != len(leaderView.Nodes) {
			t.Fatalf("follower %s reports %d nodes, leader reports %d",
				n.NodeID, len(followerView.Nodes), len(leaderView.Nodes))
		}
		// Set of node IDs should match.
		set := func(s ClusterStatus) map[string]string {
			m := make(map[string]string)
			for _, e := range s.Nodes {
				m[e.NodeID] = e.Role
			}
			return m
		}
		ls := set(leaderView)
		fs := set(followerView)
		for id, role := range ls {
			if fs[id] != role {
				t.Fatalf("node %s: leader sees %s=%s, follower sees %s=%s",
					n.NodeID, id, role, id, fs[id])
			}
		}
	}
}

// FS-ClusterStatusShowsRaftTerm
func TestClusterStatusShowsRaftTerm(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 3)
	leader := c.Leader(t)
	s := c.Status(t, leader)
	if s.RaftTerm <= 0 {
		t.Fatalf("raft_term should be > 0, got %d", s.RaftTerm)
	}

	// After a forced election (kill leader, await new leader), term must increase.
	originalTerm := s.RaftTerm
	originalLeaderIdx := indexOf(c, leader)
	c.KillNode(t, originalLeaderIdx)

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		for i := 0; i < c.Size(); i++ {
			if i == originalLeaderIdx {
				continue
			}
			role, ok := c.nodeRole(t, c.Node(i))
			if ok && role == "leader" {
				s := c.Status(t, c.Node(i))
				if s.RaftTerm > originalTerm {
					return
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("raft term did not increase after leader kill (started at %d)", originalTerm)
}
