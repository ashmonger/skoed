package cluster

import (
	"encoding/json"
	"time"

	bolt "go.etcd.io/bbolt"
)

// DNSCryptKeys holds the serialised ResolverConfig (all key material encoded
// as hex strings inside the JSON) plus the validity window. The leader
// generates a new keypair before ExpiresAt and replicates it via Raft so
// every node serves queries with an identical cert.
type DNSCryptKeys struct {
	// Config is the JSON-marshalled dnscrypt.ResolverConfig. We store the
	// whole struct rather than individual fields so the library can
	// round-trip its own types without us duplicating its schema.
	Config    string    `json:"config"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SetDNSCryptKeys replicates a new DNSCrypt keypair through Raft.
// Must be called on the leader.
func (c *Cluster) SetDNSCryptKeys(keys DNSCryptKeys) error {
	return c.applyAsLeader(CmdDNSCryptKeysSet, keys, 0)
}

// GetDNSCryptKeys returns the current replicated DNSCrypt keypair, or
// (nil, nil) when no keypair has been generated yet.
func (c *Cluster) GetDNSCryptKeys() (*DNSCryptKeys, error) {
	return c.store.DNSCryptKeysGet()
}

// DNSCryptKeysGet reads the current keypair from bbolt.
func (s *Store) DNSCryptKeysGet() (*DNSCryptKeys, error) {
	var keys *DNSCryptKeys
	s.mu.RLock()
	defer s.mu.RUnlock()
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketDNSCryptKeys).Get([]byte("keys"))
		if v == nil {
			return nil
		}
		var k DNSCryptKeys
		if err := json.Unmarshal(v, &k); err != nil {
			return err
		}
		keys = &k
		return nil
	})
	return keys, err
}
