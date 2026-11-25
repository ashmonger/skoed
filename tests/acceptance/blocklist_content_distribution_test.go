// Acceptance tests for M50 — Blocklist content distribution from the leader.
//
// FSIDs covered:
//   FS-BlocklistContentConfigReplicates        → TestBlocklistContentConfigReplicates ← 3-node
//   FS-BlocklistContentOnlyLeaderDownloads     → TestBlocklistContentOnlyLeaderDownloads ← 3-node
//   FS-BlocklistContentFollowersCopyFromLeader → TestBlocklistContentFollowersCopyFromLeader ← 3-node
//   FS-BlocklistContentNotReplicated           → TestBlocklistContentNotReplicated ← 3-node
//   FS-BlocklistContentInlineStillReplicates   → TestBlocklistContentInlineStillReplicates ← 3-node
//   FS-BlocklistContentLeaderFailover          → TestBlocklistContentLeaderFailover ← 3-node
//   FS-BlocklistContentNewLeaderWithoutCopy    → TestBlocklistContentNewLeaderWithoutCopy ← 3-node
//   FS-BlocklistContentDownloadFailureKeepsCopy → TestBlocklistContentDownloadFailureKeepsCopy ← 3-node
//   FS-BlocklistContentFollowerCannotReachLeader → TestBlocklistContentFollowerCannotReachLeader ← 3-node, iptables
//   FS-BlocklistContentSurvivesRestartOffline  → TestBlocklistContentSurvivesRestartOffline ← 3-node
//   FS-BlocklistContentNewNodeCopiesOnJoin     → TestBlocklistContentNewNodeCopiesOnJoin ← 3-node + join
//   FS-BlocklistContentManualRefresh           → TestBlocklistContentManualRefresh ← 3-node
//   FS-BlocklistContentUpgradeKeepsFiltering   → TestBlocklistContentUpgradeKeepsFiltering ← needs SKOED_PREVIOUS_BINARY
//
// The lists are served by a local ListServer (no internet). It counts
// download requests per node from the User-Agent "skoed/<v> (node <id>)",
// which is how "the URL is reachable from the leader only" is checked: any
// request from a follower fails the test.
//
// The FS-RaftLog* scenarios of the same feature file are covered elsewhere.

package acceptance

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	bcdListID      = "ads"
	bcdTracker     = "tracker.example.com"
	bcdBackgroundN = 1000
)

// bcdEnv shortens the scheduler and sync ticks to 1 s.
var bcdEnv = []string{"SKOED_TEST_MODE=1"}

// bcdDomains returns n domains whose last one is tracker.example.com.
func bcdDomains(n int, prefix string) []string {
	return append(genDomains(n-1, prefix), bcdTracker)
}

// bcdBackground starts the 3-node cluster and the list server of the
// feature's Background: "ads" serves 1000 domains including
// tracker.example.com. The blocklist itself is created by each test.
func bcdBackground(t *testing.T) (*Cluster, *ListServer) {
	t.Helper()
	ls := startListServer(t, bcdDomains(bcdBackgroundN, "bg"))
	c := startClusterWithEnv(t, 3, bcdEnv)
	return c, ls
}

// bcdCreate creates the URL list "ads" through via and fails unless the API
// answers 201.
func bcdCreate(t *testing.T, via *ClusterNode, url string, intervalSeconds int) {
	t.Helper()
	resp := createURLBlocklist(t, via, bcdListID, url, intervalSeconds)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create URL blocklist %q via %s: status %d, body %s", bcdListID, via.NodeID, resp.StatusCode, body)
	}
}

// bcdAlive returns the nodes of c that are not killed.
func bcdAlive(c *Cluster) []*ClusterNode {
	var out []*ClusterNode
	for _, n := range c.nodes {
		if !n.killed {
			out = append(out, n)
		}
	}
	return out
}

// bcdIndex returns the index of n in c, for KillNode/RestartNode.
func bcdIndex(t *testing.T, c *Cluster, n *ClusterNode) int {
	t.Helper()
	for i, cn := range c.nodes {
		if cn == n {
			return i
		}
	}
	t.Fatalf("node %s is not part of the cluster", n.NodeID)
	return -1
}

// bcdCurrent reports whether n holds the replicated version of the list.
func bcdCurrent(t *testing.T, n *ClusterNode, id string) bool {
	t.Helper()
	v, ok := getBlocklist(t, n, id)
	return ok && v.ContentSHA256 != "" &&
		v.LocalContentSHA256 == v.ContentSHA256 && v.ContentStatus == "current"
}

// bcdWaitAllCurrent waits until every alive node holds the replicated
// version of the list ("every node holds the leader's copy").
func bcdWaitAllCurrent(t *testing.T, c *Cluster, id string, within time.Duration) {
	t.Helper()
	waitFor(t, within, func() bool {
		for _, n := range bcdAlive(c) {
			if !bcdCurrent(t, n, id) {
				return false
			}
		}
		return true
	}, "not every node holds the leader's copy of %q", id)
}

// bcdOnlyFrom fails the test when the list server saw a download request
// from any node other than want. It returns the count from want.
func bcdOnlyFrom(t *testing.T, ls *ListServer, want *ClusterNode) int {
	t.Helper()
	got := ls.RequestsByNode()
	for node, n := range got {
		if node != want.NodeID && n > 0 {
			t.Errorf("list URL received %d download request(s) from %q; only %s may download (all: %v)", n, node, want.NodeID, got)
		}
	}
	return got[want.NodeID]
}

// @fsid:FS-BlocklistContentConfigReplicates
func TestBlocklistContentConfigReplicates(t *testing.T) {
	t.Parallel()
	c, ls := bcdBackground(t)
	follower := c.Followers(t)[0]

	bcdCreate(t, follower, ls.URL, 3600)

	waitFor(t, 5*time.Second, func() bool {
		for _, n := range c.nodes {
			v, ok := getBlocklist(t, n, bcdListID)
			if !ok || v.Source.Type != "url" || v.Source.URL != ls.URL ||
				v.Source.Format == "" || v.RefreshIntervalSeconds != 3600 {
				return false
			}
		}
		return true
	}, "blocklist %q with URL %s and interval 3600 is not listed on every node", bcdListID, ls.URL)

	leaderView, _ := getBlocklist(t, c.Leader(t), bcdListID)
	for _, n := range c.nodes {
		v, _ := getBlocklist(t, n, bcdListID)
		if v.Source.Format != leaderView.Source.Format {
			t.Errorf("%s: format %q, leader has %q", n.NodeID, v.Source.Format, leaderView.Source.Format)
		}
	}

	waitFor(t, 30*time.Second, func() bool {
		for _, n := range c.nodes {
			if !isBlocked(t, n, bcdTracker) {
				return false
			}
		}
		return true
	}, "%s is not blocked on every node", bcdTracker)
}

// @fsid:FS-BlocklistContentOnlyLeaderDownloads
func TestBlocklistContentOnlyLeaderDownloads(t *testing.T) {
	t.Parallel()
	c, ls := bcdBackground(t)
	leader := c.Leader(t)

	bcdCreate(t, leader, ls.URL, 5)
	bcdWaitAllCurrent(t, c, bcdListID, 30*time.Second)
	ls.Reset()

	time.Sleep(12 * time.Second)

	if n := bcdOnlyFrom(t, ls, leader); n < 2 {
		t.Errorf("leader %s downloaded %d time(s) in 12 s with a 5 s interval, want at least 2", leader.NodeID, n)
	}
}

// @fsid:FS-BlocklistContentFollowersCopyFromLeader
func TestBlocklistContentFollowersCopyFromLeader(t *testing.T) {
	t.Parallel()
	c, ls := bcdBackground(t)
	leader := c.Leader(t)

	bcdCreate(t, leader, ls.URL, 0)

	waitFor(t, 30*time.Second, func() bool {
		for _, f := range c.Followers(t) {
			if !isBlocked(t, f, bcdTracker) {
				return false
			}
		}
		return true
	}, "followers do not block %s", bcdTracker)
	bcdWaitAllCurrent(t, c, bcdListID, 30*time.Second)

	ref, _ := getBlocklist(t, leader, bcdListID)
	if ref.DomainCount != bcdBackgroundN {
		t.Errorf("leader domain_count %d, want %d", ref.DomainCount, bcdBackgroundN)
	}
	for _, n := range c.nodes {
		v, _ := getBlocklist(t, n, bcdListID)
		if v.DomainCount != ref.DomainCount {
			t.Errorf("%s: domain_count %d, leader has %d", n.NodeID, v.DomainCount, ref.DomainCount)
		}
		if v.ContentSHA256 != ref.ContentSHA256 || v.LocalContentSHA256 != ref.ContentSHA256 {
			t.Errorf("%s: content_sha256 %q local %q, leader has %q", n.NodeID, v.ContentSHA256, v.LocalContentSHA256, ref.ContentSHA256)
		}
	}

	// The URL is reachable from the leader only: no follower may have
	// downloaded it.
	bcdOnlyFrom(t, ls, leader)
}

// @fsid:FS-BlocklistContentNotReplicated
func TestBlocklistContentNotReplicated(t *testing.T) {
	t.Parallel()
	const prefix = "notrepl"
	const big = 500000
	ls := startListServer(t, bcdDomains(big, prefix))
	c := startClusterWithEnv(t, 3, bcdEnv)
	leader := c.Leader(t)

	bcdCreate(t, leader, ls.URL, 0)
	bcdWaitAllCurrent(t, c, bcdListID, 60*time.Second)
	if v, _ := getBlocklist(t, leader, bcdListID); v.DomainCount != big {
		t.Fatalf("leader domain_count %d, want %d", v.DomainCount, big)
	}

	export := exportConfigText(t, leader)
	if !strings.Contains(export, bcdListID) || !strings.Contains(export, ls.URL) {
		t.Errorf("export does not contain blocklist %q with URL %s", bcdListID, ls.URL)
	}
	// Every generated domain starts with "<prefix>-"; one match means the
	// downloaded domains leaked into the export.
	if strings.Contains(export, prefix+"-") || strings.Contains(export, bcdTracker) {
		t.Errorf("export contains downloaded domains of %q", bcdListID)
	}
	if len(export) >= 1<<20 {
		t.Errorf("export is %d bytes, want under 1 MB", len(export))
	}

	// The replicated state is the FSM snapshot. Commit enough small
	// configuration changes to pass SnapshotThreshold (64), then wait for a
	// snapshot taken after the list content was recorded.
	idx, ok := raftIndexes(t, leader)
	if !ok {
		t.Fatalf("cluster status of %s does not expose raft indexes", leader.NodeID)
	}
	mark := idx.Last
	for i := 0; i < 80; i++ {
		body := mustJSON(t, map[string]any{
			"id":      fmt.Sprintf("snap-filler-%02d", i),
			"name":    fmt.Sprintf("snap filler %02d", i),
			"enabled": true,
			"domains": []string{fmt.Sprintf("filler-%02d.example.test", i)},
			"source":  map[string]string{"type": "inline", "format": "domainlist"},
		})
		resp := leader.apiDo(t, "POST", "/api/v1/blocklists", body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create filler blocklist %d: status %d", i, resp.StatusCode)
		}
	}
	waitFor(t, 45*time.Second, func() bool {
		idx, ok := raftIndexes(t, leader)
		return ok && idx.Snapshot > mark
	}, "no Raft snapshot after index %d on %s", mark, leader.NodeID)

	state := bcdNewestSnapshotState(t, leader)
	fi, err := os.Stat(state)
	if err != nil {
		t.Fatalf("stat %s: %v", state, err)
	}
	if fi.Size() >= 1<<20 {
		t.Errorf("replicated state (%s) is %d bytes, want under 1 MB", state, fi.Size())
	}
}

// bcdNewestSnapshotState returns the state.bin of the newest Raft snapshot
// of n (hashicorp/raft FileSnapshotStore under <data_dir>/raft/snapshots).
func bcdNewestSnapshotState(t *testing.T, n *ClusterNode) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(n.DataDir, "raft", "snapshots", "snapshots", "*", "state.bin"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("no Raft snapshot under %s (err %v)", n.DataDir, err)
	}
	// Snapshot directory names are "<term>-<index>-<ms>": compare by
	// modification time instead of parsing them.
	sort.Slice(matches, func(i, j int) bool {
		a, _ := os.Stat(matches[i])
		b, _ := os.Stat(matches[j])
		return a.ModTime().Before(b.ModTime())
	})
	return matches[len(matches)-1]
}

// @fsid:FS-BlocklistContentInlineStillReplicates
func TestBlocklistContentInlineStillReplicates(t *testing.T) {
	t.Parallel()
	c, _ := bcdBackground(t)
	leader := c.Leader(t)

	body := mustJSON(t, map[string]any{
		"id":      "manual",
		"name":    "manual",
		"enabled": true,
		"domains": []string{"bad.example.com"},
		"source":  map[string]string{"type": "inline", "format": "domainlist"},
	})
	resp := leader.apiDo(t, "POST", "/api/v1/blocklists", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create inline blocklist: status %d, body %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	waitFor(t, 5*time.Second, func() bool {
		for _, n := range c.nodes {
			if !isBlocked(t, n, "bad.example.com") {
				return false
			}
		}
		return true
	}, "bad.example.com is not blocked on every node")

	if export := exportConfigText(t, leader); !strings.Contains(export, "bad.example.com") {
		t.Errorf("export does not contain the inline domain bad.example.com")
	}
}

// @fsid:FS-BlocklistContentLeaderFailover
func TestBlocklistContentLeaderFailover(t *testing.T) {
	t.Parallel()
	c, ls := bcdBackground(t)
	oldLeader := c.Leader(t)

	bcdCreate(t, oldLeader, ls.URL, 5)
	bcdWaitAllCurrent(t, c, bcdListID, 30*time.Second)

	c.KillNode(t, bcdIndex(t, c, oldLeader))
	ls.Reset()

	newLeader := c.Leader(t)
	if newLeader == oldLeader {
		t.Fatalf("killed leader %s still reported as leader", oldLeader.NodeID)
	}
	var follower *ClusterNode
	for _, n := range bcdAlive(c) {
		if n != newLeader {
			follower = n
		}
	}

	// Change the content so the remaining follower has something to copy.
	ls.SetDomains(append(bcdDomains(bcdBackgroundN, "bg"), "failover.example.com"))

	waitFor(t, 30*time.Second, func() bool {
		return isBlocked(t, newLeader, "failover.example.com")
	}, "new leader %s did not download the new version", newLeader.NodeID)
	waitFor(t, 30*time.Second, func() bool {
		return isBlocked(t, follower, "failover.example.com") && bcdCurrent(t, follower, bcdListID)
	}, "follower %s did not copy the new version from %s", follower.NodeID, newLeader.NodeID)

	if n := bcdOnlyFrom(t, ls, newLeader); n == 0 {
		t.Errorf("new leader %s never downloaded %q", newLeader.NodeID, bcdListID)
	}
}

// @fsid:FS-BlocklistContentNewLeaderWithoutCopy
func TestBlocklistContentNewLeaderWithoutCopy(t *testing.T) {
	t.Parallel()
	c, ls := bcdBackground(t)
	leader := c.Leader(t)

	// Interval 0: the list is never due by interval, so any download by the
	// new leader comes from it holding no copy.
	bcdCreate(t, leader, ls.URL, 0)
	bcdWaitAllCurrent(t, c, bcdListID, 30*time.Second)

	// Stop every node, delete the copies of the two followers, and start
	// only those two: whichever becomes leader holds no copy, and the old
	// leader, the only node with one, stays down.
	followers := c.Followers(t)
	for _, n := range c.nodes {
		c.KillNode(t, bcdIndex(t, c, n))
	}
	for _, f := range followers {
		if err := os.RemoveAll(filepath.Join(f.DataDir, "blocklists")); err != nil {
			t.Fatalf("remove copy of %s: %v", f.NodeID, err)
		}
	}
	ls.Reset()
	for _, f := range followers {
		c.RestartNode(t, bcdIndex(t, c, f))
	}

	newLeader := c.Leader(t)
	waitFor(t, 30*time.Second, func() bool {
		return ls.RequestsByNode()[newLeader.NodeID] > 0
	}, "new leader %s did not download %q", newLeader.NodeID, bcdListID)
	waitFor(t, 30*time.Second, func() bool {
		return isBlocked(t, newLeader, bcdTracker)
	}, "new leader %s does not block %s", newLeader.NodeID, bcdTracker)
	bcdOnlyFrom(t, ls, newLeader)
}

// @fsid:FS-BlocklistContentDownloadFailureKeepsCopy
func TestBlocklistContentDownloadFailureKeepsCopy(t *testing.T) {
	t.Parallel()
	c, ls := bcdBackground(t)
	leader := c.Leader(t)

	bcdCreate(t, leader, ls.URL, 5)
	bcdWaitAllCurrent(t, c, bcdListID, 30*time.Second)

	ls.SetStatus(http.StatusInternalServerError)
	ls.Reset()

	waitFor(t, 30*time.Second, func() bool {
		for _, n := range c.nodes {
			v, ok := getBlocklist(t, n, bcdListID)
			if !ok || v.LastRefreshStatus != "error" {
				return false
			}
		}
		return true
	}, "not every node reports last_refresh_status error")

	for _, n := range c.nodes {
		v, _ := getBlocklist(t, n, bcdListID)
		if !strings.Contains(v.LastRefreshError, "500") {
			t.Errorf("%s: last_refresh_error %q does not name HTTP 500", n.NodeID, v.LastRefreshError)
		}
		if !isBlocked(t, n, bcdTracker) {
			t.Errorf("%s no longer blocks %s after the failed download", n.NodeID, bcdTracker)
		}
		if v.DomainCount != bcdBackgroundN {
			t.Errorf("%s: domain_count %d after the failed download, want %d", n.NodeID, v.DomainCount, bcdBackgroundN)
		}
	}

	// The leader keeps trying at each refresh; followers never download.
	first := ls.RequestsByNode()[leader.NodeID]
	waitFor(t, 15*time.Second, func() bool {
		return ls.RequestsByNode()[leader.NodeID] > first
	}, "leader %s did not download %q again at its next refresh", leader.NodeID, bcdListID)
	bcdOnlyFrom(t, ls, leader)
}

// @fsid:FS-BlocklistContentFollowerCannotReachLeader
func TestBlocklistContentFollowerCannotReachLeader(t *testing.T) {
	t.Parallel()
	requireIptables(t)
	ls := startListServer(t, bcdDomains(bcdBackgroundN, "bg"))
	c := startClusterWithEnv(t, 2, bcdEnv)
	cut := c.AddNodeAsUser(t, cutUID)
	leader := c.Leader(t)
	if cut == leader {
		t.Fatalf("node %s started as uid %d became leader; the scenario needs it to be a follower", cut.NodeID, cutUID)
	}
	var other *ClusterNode
	for _, f := range c.Followers(t) {
		if f != cut {
			other = f
		}
	}

	bcdCreate(t, leader, ls.URL, 0)
	bcdWaitAllCurrent(t, c, bcdListID, 30*time.Second)

	var once sync.Once
	unblock := blockUIDToPort(t, cutUID, leader.APIPort)
	restore := func() { once.Do(unblock) }
	t.Cleanup(restore)

	// The new version drops tracker.example.com, so a node still blocking it
	// is serving its previous copy.
	ls.SetDomains(append(genDomains(bcdBackgroundN-1, "bg"), "newer.example.com"))
	resp := refreshBlocklist(t, leader, bcdListID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh %q on leader: status %d, body %s", bcdListID, resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	waitFor(t, 30*time.Second, func() bool {
		return bcdCurrent(t, other, bcdListID) && isBlocked(t, other, "newer.example.com")
	}, "reachable follower %s did not copy the new version", other.NodeID)
	waitFor(t, 10*time.Second, func() bool {
		v, ok := getBlocklist(t, cut, bcdListID)
		return ok && v.ContentStatus == "out_of_date"
	}, "cut follower %s does not report its copy as out_of_date", cut.NodeID)

	// Several sync ticks later it still serves the previous copy.
	time.Sleep(5 * time.Second)
	if v, _ := getBlocklist(t, cut, bcdListID); v.ContentStatus != "out_of_date" {
		t.Errorf("cut follower %s: content_status %q, want out_of_date", cut.NodeID, v.ContentStatus)
	}
	if !isBlocked(t, cut, bcdTracker) {
		t.Errorf("cut follower %s no longer blocks %s from its previous copy", cut.NodeID, bcdTracker)
	}
	if isBlocked(t, cut, "newer.example.com") {
		t.Errorf("cut follower %s blocks newer.example.com without reaching the leader", cut.NodeID)
	}

	restore()
	waitFor(t, 30*time.Second, func() bool {
		return bcdCurrent(t, cut, bcdListID) && isBlocked(t, cut, "newer.example.com")
	}, "cut follower %s did not copy the new version within 30 s of the leader becoming reachable", cut.NodeID)

	bcdOnlyFrom(t, ls, leader)
}

// @fsid:FS-BlocklistContentSurvivesRestartOffline
func TestBlocklistContentSurvivesRestartOffline(t *testing.T) {
	t.Parallel()
	c, ls := bcdBackground(t)
	leader := c.Leader(t)

	bcdCreate(t, leader, ls.URL, 0)
	bcdWaitAllCurrent(t, c, bcdListID, 30*time.Second)

	follower := c.Followers(t)[0]
	fi := bcdIndex(t, c, follower)
	c.KillNode(t, fi)
	// Nothing is reachable: the URL fails and every other node is down.
	ls.SetStatus(http.StatusServiceUnavailable)
	for i, n := range c.nodes {
		if i != fi {
			c.KillNode(t, bcdIndex(t, c, n))
		}
	}

	// Start the process directly: RestartNode also logs in, and the check
	// must run as soon as DNS answers.
	c.startProcess(t, follower)
	waitReady(t, follower.Node)
	if !isBlocked(t, follower, bcdTracker) {
		t.Errorf("restarted follower %s does not block %s once it answers DNS", follower.NodeID, bcdTracker)
	}
}

// @fsid:FS-BlocklistContentNewNodeCopiesOnJoin
func TestBlocklistContentNewNodeCopiesOnJoin(t *testing.T) {
	t.Parallel()
	c, ls := bcdBackground(t)
	leader := c.Leader(t)

	bcdCreate(t, leader, ls.URL, 0)
	bcdWaitAllCurrent(t, c, bcdListID, 30*time.Second)
	ls.Reset()

	joined := c.AddNode(t)
	waitFor(t, 30*time.Second, func() bool {
		return isBlocked(t, joined, bcdTracker)
	}, "new node %s does not block %s within 30 s of joining", joined.NodeID, bcdTracker)
	if !bcdCurrent(t, joined, bcdListID) {
		t.Errorf("new node %s does not hold the leader's copy of %q", joined.NodeID, bcdListID)
	}
	if n := ls.RequestsByNode()[joined.NodeID]; n > 0 {
		t.Errorf("new node %s downloaded %q from its URL %d time(s); it must copy from the leader", joined.NodeID, bcdListID, n)
	}
}

// @fsid:FS-BlocklistContentManualRefresh
func TestBlocklistContentManualRefresh(t *testing.T) {
	t.Parallel()
	c, ls := bcdBackground(t)
	leader := c.Leader(t)

	bcdCreate(t, leader, ls.URL, 0)
	bcdWaitAllCurrent(t, c, bcdListID, 30*time.Second)

	ls.SetDomains(bcdDomains(2000, "bg"))
	ls.Reset()

	follower := c.Followers(t)[0]
	resp := refreshBlocklist(t, follower, bcdListID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh %q via follower %s: status %d, body %s", bcdListID, follower.NodeID, resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	if n := bcdOnlyFrom(t, ls, leader); n == 0 {
		t.Errorf("leader %s did not download %q on the manual refresh", leader.NodeID, bcdListID)
	}
	waitFor(t, 30*time.Second, func() bool {
		for _, n := range c.nodes {
			v, ok := getBlocklist(t, n, bcdListID)
			if !ok || v.DomainCount != 2000 {
				return false
			}
		}
		return true
	}, "not every node reports domain_count 2000")
}

// @fsid:FS-BlocklistContentUpgradeKeepsFiltering
func TestBlocklistContentUpgradeKeepsFiltering(t *testing.T) {
	t.Parallel()
	prev := os.Getenv("SKOED_PREVIOUS_BINARY")
	if prev == "" {
		t.Skip("SKOED_PREVIOUS_BINARY is not set: this test needs a pre-M50 skoed binary that stores downloaded domains in the replicated state")
	}
	if _, err := os.Stat(prev); err != nil {
		t.Fatalf("SKOED_PREVIOUS_BINARY: %v", err)
	}
	current := skoedBinary(t)
	if _, err := os.Stat(current); err != nil {
		t.Skipf("skoed binary not found at %s (set SKOED_BINARY to override)", current)
	}

	const prefix = "upgrade"
	ls := startListServer(t, bcdDomains(bcdBackgroundN, prefix))
	c := bcdStartClusterWithBinary(t, 3, bcdEnv, prev)
	leader := c.Leader(t)

	// Interval 0, so neither version downloads it by interval.
	bcdCreate(t, leader, ls.URL, 0)
	waitFor(t, 30*time.Second, func() bool {
		for _, n := range c.nodes {
			if !isBlocked(t, n, bcdTracker) {
				return false
			}
		}
		return true
	}, "previous version does not block %s on every node", bcdTracker)

	// The URL becomes unreachable, then every node is upgraded in turn.
	ls.SetStatus(http.StatusServiceUnavailable)
	ls.Reset()
	c.bin = current
	for i := range c.nodes {
		c.KillNode(t, i)
		c.RestartNode(t, i)
		c.WaitConverged(t)
	}

	waitFor(t, 30*time.Second, func() bool {
		for _, n := range c.nodes {
			if !isBlocked(t, n, bcdTracker) {
				return false
			}
		}
		return true
	}, "upgraded nodes do not all block %s", bcdTracker)

	// The leader records the version from its local copy, which clears the
	// domains from the replicated state.
	bcdWaitAllCurrent(t, c, bcdListID, 30*time.Second)
	for _, n := range c.nodes {
		export := exportConfigText(t, n)
		if strings.Contains(export, prefix+"-") || strings.Contains(export, bcdTracker) {
			t.Errorf("%s: export still contains the downloaded domains of %q", n.NodeID, bcdListID)
		}
		if strings.Contains(string(readShadowYAML(t, n)), prefix+"-") {
			t.Errorf("%s: shadow config.yaml still contains the downloaded domains of %q", n.NodeID, bcdListID)
		}
	}

	if got := ls.RequestsByNode(); len(got) > 0 {
		t.Errorf("upgraded nodes downloaded %q from its URL: %v", bcdListID, got)
	}
}

// bcdStartClusterWithBinary is startClusterWithEnv with an explicit skoed
// binary, so the upgrade test can start the previous version and later set
// c.bin to the current one before RestartNode.
func bcdStartClusterWithBinary(t *testing.T, initialNodes int, env []string, bin string) *Cluster {
	t.Helper()
	c := &Cluster{t: t, bin: bin, defaultEnv: env}
	c.bootstrapFirst(t)
	setupAuth(t, c.nodes[0].Node)
	for i := 1; i < initialNodes; i++ {
		c.AddNode(t)
	}
	if initialNodes > 1 {
		c.WaitConverged(t)
	}
	return c
}
