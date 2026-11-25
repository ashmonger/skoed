// Acceptance tests for M50 — Raft log bounds and offline compaction
// (specs/functional/blocklist-content-distribution.feature,
// specs/technical/blocklist-content-distribution.md §8 and §9).
//
// FSIDs covered:
//   FS-RaftLogStaysBounded               → TestRaftLogStaysBounded
//   FS-RaftLogCompactOffline             → TestRaftLogCompactOffline
//   FS-RaftLogCompactRefusedWhileRunning → TestRaftLogCompactRefusedWhileRunning
//
// The Raft indexes are read from GET /api/v1/cluster/status (`raft.*`,
// TS §8) through `raftIndexes`. The compaction command is
// `skoed raft compact --data-dir <dir>` (TS §9).

package acceptance

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	// raftTrailingLogs is the bound FS-RaftLogStaysBounded sets on the
	// number of entries kept before the latest snapshot.
	raftTrailingLogs = 256
	// raftLogFile is the raft-boltdb store, relative to the data dir.
	raftLogFile = "raft/raft-log.bolt"
)

// commitCustomRuleWrites sends `count` PUT /api/v1/custom-rules requests to
// `via`, each one a distinct Raft entry, with a few requests in flight at a
// time. It fails the test if any request errors or is not answered 2xx.
func commitCustomRuleWrites(t *testing.T, via *ClusterNode, count int, tag string) {
	t.Helper()
	const workers = 8
	var (
		next     atomic.Int64
		failures atomic.Int64
		firstErr atomic.Value
		wg       sync.WaitGroup
	)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := next.Add(1)
				if i > int64(count) {
					return
				}
				body := fmt.Sprintf(`{"rules":"%s-%d.example.com"}`, tag, i)
				resp, err := via.apiDoNonFatal("PUT", "/api/v1/custom-rules", body)
				if err != nil {
					failures.Add(1)
					firstErr.CompareAndSwap(nil, err.Error())
					continue
				}
				resp.Body.Close()
				if resp.StatusCode/100 != 2 {
					failures.Add(1)
					firstErr.CompareAndSwap(nil, fmt.Sprintf("status %d", resp.StatusCode))
				}
			}
		}()
	}
	wg.Wait()
	if n := failures.Load(); n > 0 {
		t.Fatalf("%d of %d custom-rules writes via %s failed (first: %v)", n, count, via.NodeID, firstErr.Load())
	}
}

// clusterNodeIndex returns the position of n in the cluster, as used by
// KillNode and RestartNode.
func clusterNodeIndex(t *testing.T, c *Cluster, n *ClusterNode) int {
	t.Helper()
	for i := 0; i < c.Size(); i++ {
		if c.Node(i) == n {
			return i
		}
	}
	t.Fatalf("node %s is not part of the cluster", n.NodeID)
	return -1
}

// runRaftCompact runs `skoed raft compact --data-dir <dataDir>` and returns
// its exit code and combined output. A command that does not finish within
// 60 s fails the test.
func runRaftCompact(t *testing.T, dataDir string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, skoedBinary(t), "raft", "compact", "--data-dir", dataDir)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("skoed raft compact did not finish within 60s; output:\n%s", out)
	}
	if err == nil {
		return 0, string(out)
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), string(out)
	}
	t.Fatalf("run skoed raft compact: %v", err)
	return -1, ""
}

// @fsid:FS-RaftLogStaysBounded
// After 2000 committed configuration changes, every node keeps at most
// 256 Raft log entries older than its latest snapshot.
func TestRaftLogStaysBounded(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 3)
	leader := c.Leader(t)

	commitCustomRuleWrites(t, leader, 2000, "bounded")
	c.WaitConverged(t)

	for i := 0; i < c.Size(); i++ {
		n := c.Node(i)
		var last RaftIndexes
		waitFor(t, 60*time.Second, func() bool {
			idx, ok := raftIndexes(t, n)
			if !ok {
				return false
			}
			last = idx
			return idx.Snapshot > 0 && idx.Snapshot-idx.First <= raftTrailingLogs
		}, "node %s: Raft log not truncated to %d entries before its snapshot", n.NodeID, raftTrailingLogs)
		t.Logf("node %s: first=%d last=%d snapshot=%d", n.NodeID, last.First, last.Last, last.Snapshot)
		if last.Last < 2000 {
			t.Errorf("node %s: last_log_index = %d, want >= 2000 after 2000 changes", n.NodeID, last.Last)
		}
	}
}

// @fsid:FS-RaftLogCompactOffline
// A follower's Raft log first holds large entries (inline blocklists),
// which truncation then frees. With the daemon stopped, `skoed raft compact`
// shrinks the file, keeps the original as raft-log.bolt.pre-compact, and
// the node rejoins with the same blocklists.
func TestRaftLogCompactOffline(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 3)
	leader := c.Leader(t)

	// Large entries first: each inline blocklist is one Raft entry of
	// roughly 100 KB.
	const (
		lists          = 20
		domainsPerList = 3000
	)
	var ids []string
	for i := 0; i < lists; i++ {
		id := fmt.Sprintf("compact-bl-%02d", i)
		c.MustCreateBlocklist(t, leader, id, genDomains(domainsPerList, id)...)
		ids = append(ids, id)
	}

	followerNode := c.Followers(t)[0]
	idx := clusterNodeIndex(t, c, followerNode)
	afterLarge, ok := raftIndexes(t, followerNode)
	if !ok {
		t.Fatalf("node %s: raft indexes missing from GET /api/v1/cluster/status", followerNode.NodeID)
	}

	// Then enough small entries for snapshots to truncate every large one.
	commitCustomRuleWrites(t, leader, 4*raftTrailingLogs, "compact")
	c.WaitConverged(t)
	waitFor(t, 60*time.Second, func() bool {
		cur, ok := raftIndexes(t, followerNode)
		return ok && cur.First > afterLarge.Last
	}, "node %s: Raft log still holds the blocklist entries (first_log_index <= %d)", followerNode.NodeID, afterLarge.Last)

	c.KillNode(t, idx)

	logPath := filepath.Join(followerNode.DataDir, raftLogFile)
	before, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat %s: %v", logPath, err)
	}

	code, out := runRaftCompact(t, followerNode.DataDir)
	if code != 0 {
		t.Fatalf("skoed raft compact exit %d, want 0; output:\n%s", code, out)
	}

	after, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat %s after compaction: %v", logPath, err)
	}
	t.Logf("raft-log.bolt: %d → %d bytes", before.Size(), after.Size())
	if after.Size() >= before.Size() {
		t.Errorf("raft-log.bolt is %d bytes after compaction, want less than %d", after.Size(), before.Size())
	}
	preCompact := logPath + ".pre-compact"
	if st, err := os.Stat(preCompact); err != nil {
		t.Errorf("%s missing after compaction: %v", preCompact, err)
	} else if st.Size() != before.Size() {
		t.Errorf("%s is %d bytes, want the original %d", preCompact, st.Size(), before.Size())
	}

	c.RestartNode(t, idx)
	c.WaitConverged(t)
	for _, id := range ids {
		var view BlocklistView
		waitFor(t, 30*time.Second, func() bool {
			v, ok := getBlocklist(t, followerNode, id)
			view = v
			return ok
		}, "node %s: blocklist %s missing after restart", followerNode.NodeID, id)
		if view.DomainCount != domainsPerList {
			t.Errorf("node %s: blocklist %s has %d domains after restart, want %d", followerNode.NodeID, id, view.DomainCount, domainsPerList)
		}
	}
}

// @fsid:FS-RaftLogCompactRefusedWhileRunning
// While the daemon holds raft-log.bolt, `skoed raft compact` exits non-zero,
// says the daemon must be stopped, and leaves the file as it was.
func TestRaftLogCompactRefusedWhileRunning(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t)

	logPath := filepath.Join(n.DataDir, raftLogFile)
	before, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat %s: %v", logPath, err)
	}

	code, out := runRaftCompact(t, n.DataDir)
	if code == 0 {
		t.Fatalf("skoed raft compact exit 0 on a running node, want non-zero; output:\n%s", out)
	}
	if !regexp.MustCompile(`(?i)stop`).MatchString(out) || !regexp.MustCompile(`(?i)locked`).MatchString(out) {
		t.Errorf("output should say the file is locked and the daemon must be stopped; got:\n%s", out)
	}

	after, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat %s after refused compaction: %v", logPath, err)
	}
	if after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("raft-log.bolt changed: size %d → %d, mtime %s → %s",
			before.Size(), after.Size(), before.ModTime(), after.ModTime())
	}
	for _, suffix := range []string{".compact", ".pre-compact"} {
		if _, err := os.Stat(logPath + suffix); err == nil {
			t.Errorf("%s was created by a refused compaction", logPath+suffix)
		}
	}
}
