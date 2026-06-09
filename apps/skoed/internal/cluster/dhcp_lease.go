// dhcp_lease.go — TS-LeaseRepl persistence helpers.
//
// One bbolt bucket (`dhcp_lease`) holds the single replicated lease
// snapshot document plus a sub-keyspace for anti-spoof anomalies. Only
// the elected leader appends to these via Raft; followers consume them
// through FSM.Apply and rebuild their in-memory views from bbolt.
package cluster

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/skoed/skoed/internal/dhcp"
	bolt "go.etcd.io/bbolt"
)

var (
	bucketDhcpLease = []byte("dhcp_lease")

	dhcpKeySnapshot      = []byte("snapshot")
	dhcpAnomalyKeyPrefix = []byte("anom:")
)

// LeaseSnapshot returns the latest replicated lease snapshot, or
// (nil, nil) when no snapshot has ever been committed.
func (s *Store) LeaseSnapshot() (*LeasesReplacePayload, error) {
	var out *LeasesReplacePayload
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketDhcpLease)
		if b == nil {
			return nil
		}
		raw := b.Get(dhcpKeySnapshot)
		if raw == nil {
			return nil
		}
		var snap LeasesReplacePayload
		if err := json.Unmarshal(raw, &snap); err != nil {
			return fmt.Errorf("decode dhcp lease snapshot: %w", err)
		}
		out = &snap
		return nil
	})
	return out, err
}

// LeaseAnomalies returns the replicated anomalies, oldest first.
func (s *Store) LeaseAnomalies() ([]dhcp.Anomaly, error) {
	var out []dhcp.Anomaly
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketDhcpLease)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			if !startsWith(k, dhcpAnomalyKeyPrefix) {
				return nil
			}
			var a dhcp.Anomaly
			if err := json.Unmarshal(v, &a); err != nil {
				return nil // skip malformed rows; don't fail entire scan
			}
			out = append(out, a)
			return nil
		})
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].DetectedAt.Before(out[j].DetectedAt) })
	return out, err
}

// applyLeasesReplace writes the snapshot to bbolt. The snapshot is a
// single replaceable key so churn doesn't fragment the bucket.
func applyLeasesReplace(tx *bolt.Tx, p LeasesReplacePayload) error {
	b, err := tx.CreateBucketIfNotExists(bucketDhcpLease)
	if err != nil {
		return err
	}
	v, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return b.Put(dhcpKeySnapshot, v)
}

// applyAnomalyAppend inserts (or upserts) one anomaly under its id key.
// Deterministic — no time.Now() or any other non-replicated input.
func applyAnomalyAppend(tx *bolt.Tx, a dhcp.Anomaly) error {
	if a.ID == "" {
		return fmt.Errorf("anomaly id empty")
	}
	b, err := tx.CreateBucketIfNotExists(bucketDhcpLease)
	if err != nil {
		return err
	}
	v, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return b.Put(anomalyKey(a.ID), v)
}

// applyAnomalyAck flips acknowledged_at on the named anomaly.
func applyAnomalyAck(tx *bolt.Tx, id string, ackUnix int64) error {
	b := tx.Bucket(bucketDhcpLease)
	if b == nil {
		return fmt.Errorf("dhcp_lease bucket missing")
	}
	raw := b.Get(anomalyKey(id))
	if raw == nil {
		return fmt.Errorf("anomaly %q not found", id)
	}
	var a dhcp.Anomaly
	if err := json.Unmarshal(raw, &a); err != nil {
		return err
	}
	at := time.Unix(ackUnix, 0).UTC()
	a.AcknowledgedAt = &at
	v, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return b.Put(anomalyKey(id), v)
}

// applyAnomalySweep removes anomaly entries detected before the cutoff.
func applyAnomalySweep(tx *bolt.Tx, beforeUnix int64) error {
	b := tx.Bucket(bucketDhcpLease)
	if b == nil {
		return nil
	}
	cutoff := time.Unix(beforeUnix, 0)
	var toDelete [][]byte
	_ = b.ForEach(func(k, v []byte) error {
		if !startsWith(k, dhcpAnomalyKeyPrefix) {
			return nil
		}
		var a dhcp.Anomaly
		if err := json.Unmarshal(v, &a); err != nil {
			return nil
		}
		if a.DetectedAt.Before(cutoff) {
			toDelete = append(toDelete, append([]byte(nil), k...))
		}
		return nil
	})
	for _, k := range toDelete {
		if err := b.Delete(k); err != nil {
			return err
		}
	}
	return nil
}

func anomalyKey(id string) []byte {
	out := make([]byte, 0, len(dhcpAnomalyKeyPrefix)+len(id))
	out = append(out, dhcpAnomalyKeyPrefix...)
	out = append(out, []byte(id)...)
	return out
}

func startsWith(b, prefix []byte) bool {
	if len(b) < len(prefix) {
		return false
	}
	return strings.HasPrefix(string(b), string(prefix))
}
