package config

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// BackupStore is the subset of *cluster.Store needed by BackupScheduler.
// Defined here to avoid an import cycle (config -> cluster).
type BackupStore interface {
	BackupConfig() (BackupConfig, error)
	BackupEntries() ([]BackupEntry, error)
	UpsertBackupEntry(BackupEntry) error
	DeleteBackupEntry(id string) error
	BackupLastHash() (string, error)
	SetBackupLastHash(string) error
}

// BackupScheduler runs periodic config exports according to BackupConfig
// stored in bbolt. It deduplicates backups by hashing the exported config
// content — independent of background Raft activity.
type BackupScheduler struct {
	store   BackupStore
	getCfg  func() *Config
	dataDir string
	done    chan struct{}
}

// NewBackupScheduler creates a scheduler wired to the given store,
// live-config accessor, and data directory.
func NewBackupScheduler(
	store BackupStore,
	getCfg func() *Config,
	dataDir string,
) *BackupScheduler {
	return &BackupScheduler{
		store:   store,
		getCfg:  getCfg,
		dataDir: dataDir,
		done:    make(chan struct{}),
	}
}

// Start spawns the background scheduler goroutine.
func (s *BackupScheduler) Start() { go s.loop() }

// Stop signals the scheduler to exit cleanly.
func (s *BackupScheduler) Stop() { close(s.done) }

// TriggerOnce runs one backup cycle immediately. Returns true if a new backup
// was created, false if the cycle was deduped or the scheduler is disabled.
func (s *BackupScheduler) TriggerOnce() (bool, error) { return s.runOnce() }

func (s *BackupScheduler) loop() {
	for {
		cfg, err := s.store.BackupConfig()
		if err != nil || !cfg.Enabled || cfg.IntervalHours <= 0 {
			select {
			case <-s.done:
				return
			case <-time.After(30 * time.Second):
				continue
			}
		}
		select {
		case <-s.done:
			return
		case <-time.After(time.Duration(cfg.IntervalHours) * time.Hour):
			s.runOnce() //nolint:errcheck
		}
	}
}

func (s *BackupScheduler) runOnce() (bool, error) {
	cfg, err := s.store.BackupConfig()
	if err != nil {
		return false, fmt.Errorf("backup: read config: %w", err)
	}
	if !cfg.Enabled {
		return false, nil
	}

	// Export config to an in-memory buffer so we can hash it for dedup.
	var buf bytes.Buffer
	if err := Export(s.getCfg(), &buf); err != nil {
		return false, fmt.Errorf("backup: export for hash: %w", err)
	}
	data := buf.Bytes()

	sum := sha256.Sum256(data)
	currentHash := hex.EncodeToString(sum[:])

	lastHash, err := s.store.BackupLastHash()
	if err != nil {
		return false, fmt.Errorf("backup: read last hash: %w", err)
	}
	if currentHash == lastHash {
		return false, nil // dedup — config content unchanged since last backup
	}

	id := newBackupID()
	backupDir := filepath.Join(s.dataDir, "backups")
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return false, fmt.Errorf("backup: create backup dir: %w", err)
	}
	path := filepath.Join(backupDir, id+".tar.gz")

	f, err := os.Create(path)
	if err != nil {
		return false, fmt.Errorf("backup: create file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path) //nolint:errcheck
		return false, fmt.Errorf("backup: write file: %w", err)
	}
	if err := f.Close(); err != nil {
		return false, fmt.Errorf("backup: close file: %w", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		return false, fmt.Errorf("backup: stat file: %w", err)
	}

	entry := BackupEntry{
		ID:        id,
		CreatedAt: time.Now().UTC(),
		SizeBytes: fi.Size(),
	}
	if err := s.store.UpsertBackupEntry(entry); err != nil {
		return false, fmt.Errorf("backup: store entry: %w", err)
	}
	if err := s.store.SetBackupLastHash(currentHash); err != nil {
		return false, fmt.Errorf("backup: update last hash: %w", err)
	}

	retainCount := cfg.RetainCount
	if retainCount <= 0 {
		retainCount = 10
	}
	s.pruneBackups(retainCount, backupDir) //nolint:errcheck
	return true, nil
}

func (s *BackupScheduler) pruneBackups(retain int, backupDir string) error {
	entries, err := s.store.BackupEntries()
	if err != nil {
		return err
	}
	if len(entries) <= retain {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].CreatedAt.Before(entries[j].CreatedAt)
	})
	for _, e := range entries[:len(entries)-retain] {
		for _, ext := range []string{".tar.gz", ".age"} {
			os.Remove(filepath.Join(backupDir, e.ID+ext)) //nolint:errcheck
		}
		if err := s.store.DeleteBackupEntry(e.ID); err != nil {
			return err
		}
	}
	return nil
}

// newBackupID generates a 16-byte random hex string for a backup filename.
func newBackupID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("backup: generate id: %v", err))
	}
	return hex.EncodeToString(b)
}
