package cluster

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// DefaultTokenTTL is the production-default join-token lifetime.
const DefaultTokenTTL = 15 * time.Minute

// GenerateToken returns a fresh single-use bearer token (32 random bytes,
// URL-safe hex). The plaintext is shown to the caller exactly once; the FSM
// only ever stores its SHA-256 hash.
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// HashToken returns the canonical hash used as the bbolt key for a token.
func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
