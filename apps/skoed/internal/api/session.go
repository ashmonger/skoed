package api

import (
	"sync"
	"time"
)

const defaultSessionTTL = 8 * time.Hour

// sessionTTLFromSeconds converts a configured seconds value to a duration.
// A zero or negative value falls back to the default 8-hour TTL.
func sessionTTLFromSeconds(s int) time.Duration {
	if s <= 0 {
		return defaultSessionTTL
	}
	return time.Duration(s) * time.Second
}

type sessionEntry struct {
	username  string
	expiresAt time.Time
}

type sessionStore struct {
	mu      sync.RWMutex
	entries map[string]sessionEntry
}

func newSessionStore() *sessionStore {
	return &sessionStore{entries: make(map[string]sessionEntry)}
}

func (s *sessionStore) create(token, username string, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[token] = sessionEntry{username: username, expiresAt: time.Now().Add(ttl)}
}

func (s *sessionStore) lookup(token string) (username string, ok bool) {
	s.mu.RLock()
	e, exists := s.entries[token]
	s.mu.RUnlock()
	if !exists || time.Now().After(e.expiresAt) {
		return "", false
	}
	return e.username, true
}

func (s *sessionStore) delete(token string) {
	s.mu.Lock()
	delete(s.entries, token)
	s.mu.Unlock()
}
