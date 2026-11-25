package cluster

// M50 — blocklist content version (TS-BlocklistContentDistribution §3).

import (
	"encoding/json"
	"fmt"

	"github.com/skoed/skoed/internal/config"
	bolt "go.etcd.io/bbolt"
)

// SetBlocklistContent records the result of one download of a URL
// blocklist by the leader. The entry carries the content version only,
// never the domains.
func (c *Cluster) SetBlocklistContent(p BlocklistContentSetPayload) error {
	return c.applyAsLeader(CmdBlocklistContentSet, p, 0)
}

// MembersLackingCapability returns the IDs of the servers in the Raft
// configuration whose member record does not list capability. A server
// without a member record yet is included.
func (c *Cluster) MembersLackingCapability(capability string) []string {
	var out []string
	for _, s := range c.raft.Configuration().Servers {
		if !c.memberHasCapability(string(s.ID), capability) {
			out = append(out, string(s.ID))
		}
	}
	return out
}

// storedBlocklist reads one blocklist from the bucket inside tx.
func storedBlocklist(tx *bolt.Tx, id string) (config.Blocklist, bool) {
	v := tx.Bucket(bucketBlocklists).Get([]byte(id))
	if v == nil {
		return config.Blocklist{}, false
	}
	var bl config.Blocklist
	if err := json.Unmarshal(v, &bl); err != nil {
		return config.Blocklist{}, false
	}
	return bl, true
}

// keepStoredContent returns incoming with the content version and refresh
// state of stored when incoming was built from an older view of the same
// URL list: no Content and no domains, or Content older than the stored
// one. This way, a `config.import` built from a snapshot taken before a
// concurrent `blocklist.content_set` does not erase it. A changed URL or
// format keeps incoming as is, so the list is downloaded again. An entry
// carrying domains (written while the capability gate is not met) also
// wins, so nodes running a previous version keep filtering.
func keepStoredContent(stored, incoming config.Blocklist) config.Blocklist {
	if incoming.Source.Type != "url" || stored.Source.Type != "url" || stored.Content == nil {
		return incoming
	}
	if stored.Source.URL != incoming.Source.URL || stored.Source.Format != incoming.Source.Format {
		return incoming
	}
	var stale bool
	if incoming.Content == nil {
		stale = len(incoming.Domains) == 0
	} else {
		stale = incoming.Content.FetchedAt < stored.Content.FetchedAt
	}
	if !stale {
		return incoming
	}
	c := *stored.Content
	incoming.Content = &c
	incoming.Domains = nil
	incoming.LastUpdated = stored.LastUpdated
	incoming.LastRefreshAt = stored.LastRefreshAt
	incoming.LastRefreshStatus = stored.LastRefreshStatus
	incoming.LastRefreshError = stored.LastRefreshError
	return incoming
}

// applyBlocklistContentSet records one download result of the leader. An
// unknown ID is a no-op: the list was deleted while it was downloaded. So
// is a result for another URL or format than the stored ones: the source
// was changed while it was downloaded, the change wins and the list stays
// due.
func applyBlocklistContentSet(tx *bolt.Tx, p BlocklistContentSetPayload) error {
	bl, ok := storedBlocklist(tx, p.ID)
	if !ok {
		return nil
	}
	if bl.Source.URL != p.URL || bl.Source.Format != p.Format {
		return nil
	}
	bl.LastRefreshAt = p.RefreshAt
	bl.LastRefreshStatus = p.Status
	bl.LastRefreshError = p.Error
	if p.Status == "ok" {
		if p.Content == nil {
			return fmt.Errorf("blocklist.content_set %q: status ok without content", p.ID)
		}
		c := *p.Content
		bl.Content = &c
		bl.LastUpdated = c.FetchedAt
		bl.Domains = nil
	}
	v, err := json.Marshal(bl)
	if err != nil {
		return err
	}
	return tx.Bucket(bucketBlocklists).Put([]byte(bl.ID), v)
}
