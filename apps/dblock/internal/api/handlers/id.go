package handlers

import (
	"crypto/rand"
	"encoding/hex"
)

// newID generates a random 16-byte hex string suitable for use as a resource ID.
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Extremely unlikely; return whatever bytes we have.
		return hex.EncodeToString(b)
	}
	return hex.EncodeToString(b)
}
