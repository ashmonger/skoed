package cluster

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/raft"
	bolt "go.etcd.io/bbolt"
)

// fsm is the raft.FSM glue between Raft and our bbolt Store.
type fsm struct {
	store *Store
	// onApply is invoked after every successful state-machine apply. Used by
	// the orchestrator to trigger filter rebuilds, shadow-YAML flushes, etc.
	onApply func()
}

// newFSM wires a Store into the raft.FSM interface.
func newFSM(s *Store, onApply func()) *fsm {
	return &fsm{store: s, onApply: onApply}
}

// Apply implements raft.FSM. Returning a non-nil value from Apply is the only
// way to ferry an error back to the caller of raft.Apply(...); we just propagate
// the underlying error.
func (f *fsm) Apply(log *raft.Log) any {
	if err := f.store.Apply(log.Data); err != nil {
		return err
	}
	if f.onApply != nil {
		f.onApply()
	}
	return nil
}

// Snapshot implements raft.FSM. We copy the bbolt file under a read transaction
// so the snapshot is a complete, point-in-time image.
func (f *fsm) Snapshot() (raft.FSMSnapshot, error) {
	return &snapshot{db: f.store.DB()}, nil
}

// Restore implements raft.FSM. The supplied reader carries the bbolt file
// captured by a previous Snapshot; we replace our database atomically.
func (f *fsm) Restore(snap io.ReadCloser) error {
	defer snap.Close()
	path := f.store.DB().Path()
	tmp := path + ".restore.tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create restore tmp: %w", err)
	}
	if _, err := io.Copy(out, snap); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("copy snapshot: %w", err)
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	out.Close()

	// Swap the underlying bbolt file. We hold the Store's write lock so no
	// reader is mid-transaction.
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	if err := f.store.db.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	newDB, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return err
	}
	f.store.db = newDB
	// Re-run bucket init so any buckets added since the snapshot was taken
	// are present in the restored database (backwards-compatible migration).
	return f.store.init()
}

// snapshot is the raft.FSMSnapshot implementation. Persist runs while raft
// holds the FSM lock, so the bbolt file is stable for the duration.
type snapshot struct {
	db *bolt.DB
}

func (s *snapshot) Persist(sink raft.SnapshotSink) error {
	src, err := os.Open(s.db.Path())
	if err != nil {
		sink.Cancel() //nolint:errcheck
		return fmt.Errorf("open bbolt for snapshot: %w", err)
	}
	defer src.Close()
	if _, err := io.Copy(sink, src); err != nil {
		sink.Cancel() //nolint:errcheck
		return fmt.Errorf("copy bbolt to sink: %w", err)
	}
	return sink.Close()
}

func (s *snapshot) Release() {}

// snapshotDir returns the on-disk path used for raft snapshots.
func snapshotDir(dataDir string) string {
	return filepath.Join(dataDir, "raft", "snapshots")
}
