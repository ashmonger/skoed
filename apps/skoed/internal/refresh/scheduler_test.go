package refresh

import (
	"testing"
	"time"

	"github.com/skoed/skoed/internal/blocklistcache"
	"github.com/skoed/skoed/internal/cluster"
	"github.com/skoed/skoed/internal/config"
)

// A list refreshed by refreshLegacy, then converted by seedLegacy once the
// capability gate is met, must be due again as soon as the gate is lost:
// previous-version nodes have no domains for it until the leader downloads
// it with refreshLegacy (TS-BlocklistContentDistribution §4, capability
// gate lost after a rollback or the join of a previous-version node).
func TestDueLegacyAfterSeedLegacyAndGateLoss(t *testing.T) {
	for _, interval := range []int{0, 86400} {
		cache, err := blocklistcache.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		s := New(nil, Options{Cache: cache})
		s.download = func(string, string, time.Duration) ([]string, error) {
			return []string{"a.example", "b.example"}, nil
		}
		var stored config.Blocklist
		s.upsertBlocklist = func(bl config.Blocklist) error {
			stored = bl
			return nil
		}
		s.setBlocklistContent = func(p cluster.BlocklistContentSetPayload) error {
			// What the content_set entry does to the stored list.
			stored.Content = p.Content
			stored.Domains = nil
			stored.LastRefreshStatus = p.Status
			stored.LastRefreshAt = p.RefreshAt
			return nil
		}

		bl := config.Blocklist{
			ID:                     "ads",
			Name:                   "ads",
			Source:                 config.BlocklistSource{Type: "url", URL: "https://example.invalid/list", Format: "domains"},
			RefreshIntervalSeconds: interval,
		}

		// Gate not met: the legacy refresh replicates the domains.
		if err := s.refreshLegacy(bl); err != nil {
			t.Fatalf("interval %d: refreshLegacy: %v", interval, err)
		}
		if !stored.IsLegacyURLContent() {
			t.Fatalf("interval %d: after refreshLegacy, list is not legacy content: %+v", interval, stored)
		}
		_, before := s.lastRun(stored)
		if !before.legacy || before.failed {
			t.Fatalf("interval %d: attempt after refreshLegacy = %+v, want legacy success", interval, before)
		}

		// Gate met: seedLegacy converts the list to a content version.
		s.seedLegacy(stored)
		if stored.Content == nil || len(stored.Domains) != 0 {
			t.Fatalf("interval %d: seedLegacy did not convert the list: %+v", interval, stored)
		}
		_, after := s.lastRun(stored)
		if after.legacy {
			t.Fatalf("interval %d: attempt after seedLegacy is still legacy: %+v", interval, after)
		}
		if !after.at.Equal(before.at) {
			t.Fatalf("interval %d: seedLegacy moved the attempt time: %v → %v", interval, before.at, after.at)
		}

		// Gate lost: the list must be due on this tick.
		snap := &config.Config{}
		snap.Filtering.Blocklists = []config.Blocklist{stored}
		due := s.dueLegacy(snap, time.Now())
		if len(due) != 1 || due[0].ID != "ads" {
			t.Fatalf("interval %d: dueLegacy after gate loss = %v, want [ads]", interval, due)
		}
	}
}
