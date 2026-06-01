package auth

import (
	"fmt"
	"sync"

	"github.com/dblock/dblock/internal/config"
	"golang.org/x/crypto/bcrypt"
)

// Store holds the admin credential and provides verification.
// It is safe for concurrent use.
type Store struct {
	mu           sync.RWMutex
	username     string
	passwordHash string
}

// NewStore creates a Store from an AuthConfig.
func NewStore(cfg config.AuthConfig) *Store {
	return &Store{
		username:     cfg.Username,
		passwordHash: cfg.PasswordHash,
	}
}

// IsConfigured returns true if a username and password hash are both set.
func (s *Store) IsConfigured() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.username != "" && s.passwordHash != ""
}

// Verify returns true if username and password match the stored credential.
func (s *Store) Verify(username, password string) bool {
	s.mu.RLock()
	storedUser := s.username
	storedHash := s.passwordHash
	s.mu.RUnlock()

	if username != storedUser {
		return false
	}
	err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(password))
	return err == nil
}

// SetPassword hashes password with bcrypt and stores it alongside username.
// Updates in-memory state only; the caller must persist the config.
func (s *Store) SetPassword(username, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.username = username
	s.passwordHash = string(hash)
	return nil
}

// ChangePassword verifies currentPassword first, then calls SetPassword.
func (s *Store) ChangePassword(currentPassword, newPassword string) error {
	s.mu.RLock()
	storedUser := s.username
	storedHash := s.passwordHash
	s.mu.RUnlock()

	if err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(currentPassword)); err != nil {
		return fmt.Errorf("current password is incorrect")
	}
	return s.SetPassword(storedUser, newPassword)
}

// SetHashedCredentials writes a pre-hashed credential pair into the store.
// Used by the cluster apply path to push replicated auth state into the
// in-memory verifier without re-bcrypting (the hash already comes from
// another node's bcrypt run).
func (s *Store) SetHashedCredentials(username, passwordHash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.username = username
	s.passwordHash = passwordHash
}

// Export returns an AuthConfig reflecting the current in-memory state.
func (s *Store) Export() config.AuthConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return config.AuthConfig{
		Username:     s.username,
		PasswordHash: s.passwordHash,
	}
}
