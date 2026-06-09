package cluster

import (
	"encoding/json"
	"time"

	bolt "go.etcd.io/bbolt" //nolint:typecheck — used by APITokenList on Store
)

// APIToken is the replicated record for one bearer token. The raw token
// value is NEVER stored; only its SHA-256 hex digest is kept so the store
// can authenticate in-bound Bearer requests without bcrypt overhead.
type APIToken struct {
	ID         string     `json:"id"`                    // "tok_<12 hex>" — public, safe to log
	Hash       string     `json:"hash"`                  // hex(sha256(rawToken))
	Label      string     `json:"label"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// IsExpired reports whether the token is past its expiry. Tokens with no
// ExpiresAt never expire.
func (t *APIToken) IsExpired() bool {
	return t.ExpiresAt != nil && time.Now().After(*t.ExpiresAt)
}

// HasScope reports whether the token grants the requested scope.
func (t *APIToken) HasScope(s string) bool {
	for _, sc := range t.Scopes {
		if sc == s {
			return true
		}
	}
	return false
}

// Payload types for the FSM commands.

type APITokenUpsertPayload struct {
	Token APIToken `json:"token"`
}

type APITokenDeletePayload struct {
	ID string `json:"id"`
}

// ── Cluster helpers ───────────────────────────────────────────────────────

// UpsertAPIToken replicates a token create or update through Raft.
func (c *Cluster) UpsertAPIToken(tok APIToken) error {
	return c.applyAsLeader(CmdAPITokenUpsert, APITokenUpsertPayload{Token: tok}, 0)
}

// DeleteAPIToken removes a token by ID through Raft.
func (c *Cluster) DeleteAPIToken(id string) error {
	return c.applyAsLeader(CmdAPITokenDelete, APITokenDeletePayload{ID: id}, 0)
}

// APITokens returns all stored API tokens from bbolt (snapshot read).
func (c *Cluster) APITokens() ([]APIToken, error) {
	return c.store.APITokenList()
}

// ── Store read method ─────────────────────────────────────────────────────

// APITokenList returns every token in the api_tokens bucket.
func (s *Store) APITokenList() ([]APIToken, error) {
	var out []APIToken
	s.mu.RLock()
	defer s.mu.RUnlock()
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketAPITokens).ForEach(func(_, v []byte) error {
			var tok APIToken
			if err := json.Unmarshal(v, &tok); err != nil {
				return err
			}
			out = append(out, tok)
			return nil
		})
	})
	return out, err
}
