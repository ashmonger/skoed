// doh_resolvers.go — TS-DohResolverDb persistence helpers.
//
// One bbolt bucket (`doh_resolvers`) holds the single replicated
// snapshot document plus its refresh metadata keys. Keys are flat
// strings, values are JSON. Operators never edit this state directly;
// only the leader's scheduler writes it, replicated via Raft.

package cluster

import (
	"encoding/json"
	"fmt"

	"github.com/skoed/skoed/internal/dohresolvers"
	bolt "go.etcd.io/bbolt"
)

var (
	bucketDohResolvers = []byte("doh_resolvers")

	dohKeySnapshot             = []byte("snapshot")
	dohKeyLastRefreshAttemptAt = []byte("last_refresh_attempt_at")
	dohKeyLastRefreshError     = []byte("last_refresh_error")
)

// DohResolverSnapshot returns the current snapshot, or (nil, nil) when
// no snapshot has ever been written (cold boot before the first refresh
// or bundled seed has landed).
//
// The bbolt bucket is created lazily by Apply() the first time a
// CmdDohResolverSnapshotReplace or CmdDohResolverRefreshFailure commits,
// so this method tolerates the bucket being absent.
func (s *Store) DohResolverSnapshot() (*dohresolvers.Snapshot, error) {
	var out *dohresolvers.Snapshot
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketDohResolvers)
		if b == nil {
			return nil
		}
		raw := b.Get(dohKeySnapshot)
		if raw == nil {
			return nil
		}
		var snap dohresolvers.Snapshot
		if err := json.Unmarshal(raw, &snap); err != nil {
			return fmt.Errorf("decode doh snapshot: %w", err)
		}
		// Overlay the failure-only fields (they are stored in their own
		// keys so a failure update doesn't have to rewrite the whole
		// snapshot blob).
		if v := b.Get(dohKeyLastRefreshAttemptAt); v != nil {
			snap.LastRefreshAttemptAt = string(v)
		}
		if v := b.Get(dohKeyLastRefreshError); v != nil {
			snap.LastRefreshError = string(v)
		}
		out = &snap
		return nil
	})
	return out, err
}

// applyDohResolverSnapshotReplaceFromPayload converts the wire payload
// into the typed snapshot and writes it. Called by the FSM from a
// Raft-replicated command.
func applyDohResolverSnapshotReplaceFromPayload(tx *bolt.Tx, p DohResolverSnapshotReplacePayload) error {
	snap := dohresolvers.Snapshot{
		SnapshotID:           p.SnapshotID,
		SourceURL:            p.SourceURL,
		FetchedAt:            p.FetchedAt,
		LastRefreshAttemptAt: p.LastRefreshAttemptAt,
		LastRefreshSuccessAt: p.LastRefreshSuccessAt,
		LastRefreshError:     p.LastRefreshError,
		Resolvers:            make([]dohresolvers.ResolverEntry, len(p.Resolvers)),
	}
	for i, e := range p.Resolvers {
		snap.Resolvers[i] = dohresolvers.ResolverEntry{
			ID:        e.ID,
			Name:      e.Name,
			IPv4:      e.IPv4,
			IPv6:      e.IPv6,
			SourceURL: e.SourceURL,
		}
	}
	return writeDohSnapshot(tx, snap)
}

// writeDohSnapshot overwrites the snapshot blob and refreshes the
// derived failure-only keys so the next read sees a consistent view.
func writeDohSnapshot(tx *bolt.Tx, snap dohresolvers.Snapshot) error {
	b, err := tx.CreateBucketIfNotExists(bucketDohResolvers)
	if err != nil {
		return err
	}
	v, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	if err := b.Put(dohKeySnapshot, v); err != nil {
		return err
	}
	if err := b.Put(dohKeyLastRefreshAttemptAt, []byte(snap.LastRefreshAttemptAt)); err != nil {
		return err
	}
	return b.Put(dohKeyLastRefreshError, []byte(snap.LastRefreshError))
}


// applyDohResolverRefreshFailure updates only the failure-only metadata
// keys; the snapshot blob is left intact so consumers keep returning
// the prior good list (FS-DohResolverDbUpstreamFailureKeepsLastGoodSnapshot).
func applyDohResolverRefreshFailure(tx *bolt.Tx, attemptedAt, reason string) error {
	b, err := tx.CreateBucketIfNotExists(bucketDohResolvers)
	if err != nil {
		return err
	}
	if err := b.Put(dohKeyLastRefreshAttemptAt, []byte(attemptedAt)); err != nil {
		return err
	}
	return b.Put(dohKeyLastRefreshError, []byte(reason))
}
