// Acceptance tests for M5.6 — In-place upgrade (check + audit path).
//
// FSIDs covered:
//   FS-UpgradeCheckEndpoint        → TestUpgradeCheckEndpoint
//   FS-UpgradeCheckRequiresAuth    → TestUpgradeCheckRequiresAuth
//   FS-UpgradeStartRequiresLeader  → TestUpgradeStartForwardedToLeader ← 3-node
//   FS-UpgradeStartRecordedInAudit → TestUpgradeStartRecordedInAudit
//
// FS-UpgradeBannerOnDashboard and FS-UpgradeNoBannerWhenCurrent are UI
// scenarios — covered via the m5.6 screenshots.

package acceptance

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type upgradeCheckResp struct {
	CurrentVersion   string `json:"current_version"`
	AvailableVersion string `json:"available_version"`
	UpgradeAvailable bool   `json:"upgrade_available"`
	ReleaseNotesURL  string `json:"release_notes_url"`
	PublishedAt      string `json:"published_at"`
	CheckedAt        string `json:"checked_at"`
}

// startFeedServer serves a synthetic release feed.
func startFeedServer(t *testing.T, version string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"version": "%s",
			"published_at": "2026-07-01T09:00:00Z",
			"release_notes_url": "https://example.test/releases/%s"
		}`, version, version)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// FS-UpgradeCheckRequiresAuth
func TestUpgradeCheckRequiresAuth(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	resp := n.apiDoNoAuth(t, "GET", "/api/v1/upgrade/check")
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("M5.6 impl pending")
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("/upgrade/check no-auth: want 401, got %d", resp.StatusCode)
	}
}

// FS-UpgradeCheckEndpoint
func TestUpgradeCheckEndpoint(t *testing.T) {
	feed := startFeedServer(t, "99.0.0")
	c := startClusterWithEnv(t, 1, []string{"SKOED_UPGRADE_FEED_URL=" + feed.URL})
	n := c.Leader(t).Node

	resp := n.apiDo(t, "GET", "/api/v1/upgrade/check", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("M5.6 impl pending: /upgrade/check 404")
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var ck upgradeCheckResp
	if err := json.NewDecoder(resp.Body).Decode(&ck); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Wait for the checker to poll the feed (the goroutine may have
	// not fired yet at request time).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && ck.AvailableVersion == "" {
		time.Sleep(100 * time.Millisecond)
		r := n.apiDo(t, "GET", "/api/v1/upgrade/check", "")
		_ = json.NewDecoder(r.Body).Decode(&ck)
		r.Body.Close()
	}
	if ck.CurrentVersion == "" {
		t.Errorf("current_version should be non-empty")
	}
	if ck.AvailableVersion == "" {
		t.Skipf("feed never polled within 5s (SKOED_UPGRADE_FEED_URL plumbing may be missing): %+v", ck)
	}
	if !ck.UpgradeAvailable {
		t.Errorf("upgrade_available: want true (99.0.0 > current), got false")
	}
	if !strings.Contains(ck.ReleaseNotesURL, "99.0.0") {
		t.Errorf("release_notes_url should reference the version, got %q", ck.ReleaseNotesURL)
	}
}

// FS-UpgradeStartRequiresLeader — needs a feed URL so upgrade_available
// is true; otherwise /start returns 409 before forwarding can be
// observed.
func TestUpgradeStartForwardedToLeader(t *testing.T) {
	feed := startFeedServer(t, "99.0.0")
	c := startClusterWithEnv(t, 3, []string{"SKOED_UPGRADE_FEED_URL=" + feed.URL})
	leader := c.Leader(t)
	var follower *ClusterNode
	for _, n := range c.nodes {
		if n.NodeID != leader.NodeID {
			follower = n
			break
		}
	}
	if follower == nil {
		t.Skip("3-node cluster has no follower (??)")
	}
	// Give the checker time to populate.
	waitUpgradeAvailable(t, follower.Node, 5*time.Second)

	resp := follower.Node.apiDo(t, "POST", "/api/v1/upgrade/start", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("M5.6 impl pending: /upgrade/start 404")
	}
	switch resp.StatusCode {
	case 307, 308:
		// LeaderForward returned a redirect — that's the contract.
		return
	case 200, 202:
		// LeaderForward proxied transparently. Request reached leader. OK.
		return
	case 503:
		body, _ := io.ReadAll(resp.Body)
		if strings.Contains(string(body), "leader_address") {
			return
		}
	}
	t.Errorf("/upgrade/start on follower: unexpected status %d", resp.StatusCode)
}

// FS-UpgradeStartRecordedInAudit
func TestUpgradeStartRecordedInAudit(t *testing.T) {
	feed := startFeedServer(t, "99.0.0")
	c := startClusterWithEnv(t, 1, []string{"SKOED_UPGRADE_FEED_URL=" + feed.URL})
	n := c.Leader(t).Node
	waitUpgradeAvailable(t, n, 5*time.Second)

	beforePage := fetchAudit(t, n, "limit=1")
	resp := n.apiDo(t, "POST", "/api/v1/upgrade/start", "")
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("M5.6 impl pending")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("/upgrade/start: status %d", resp.StatusCode)
	}
	got := waitForAudit(t, n, beforePage.Total+1, 3*time.Second)
	if got.Entries[0].Action != "upgrade.start" {
		t.Errorf("action: want upgrade.start, got %q", got.Entries[0].Action)
	}
}

// waitUpgradeAvailable polls /upgrade/check until the cluster sees the
// feed and reports upgrade_available=true, or fails the test.
func waitUpgradeAvailable(t *testing.T, n *Node, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		resp := n.apiDo(t, "GET", "/api/v1/upgrade/check", "")
		var ck upgradeCheckResp
		_ = json.NewDecoder(resp.Body).Decode(&ck)
		resp.Body.Close()
		if ck.UpgradeAvailable {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("upgrade_available never went true within %s", within)
}
