package api

import (
	"sync"
	"time"
)

const sessionTTL = 8 * time.Hour

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

func (s *sessionStore) create(token, username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[token] = sessionEntry{username: username, expiresAt: time.Now().Add(sessionTTL)}
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
