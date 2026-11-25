package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	bolt "go.etcd.io/bbolt"

	"github.com/skoed/skoed/internal/cluster"
)

const (
	// raftCompactLockTimeout bounds the wait for the bbolt file lock. The
	// daemon holds the lock for as long as it runs, so a timeout means it
	// is running.
	raftCompactLockTimeout = 1 * time.Second
	// raftCompactTxMaxSize is the amount of data bolt.Compact copies per
	// write transaction.
	raftCompactTxMaxSize = 64 << 20
)

// errRaftLogLocked is printed when the daemon holds raft-log.bolt
// (FS-RaftLogCompactRefusedWhileRunning).
var errRaftLogLocked = errors.New("raft-log.bolt is locked: stop the skoed daemon first")

func newRaftCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "raft",
		Short: "Offline maintenance of the node's Raft state",
	}
	c.AddCommand(newRaftCompactCmd())
	return c
}

func newRaftCompactCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "compact",
		Short: "Compact raft-log.bolt of a stopped node",
		Long: `Rewrite <data_dir>/raft/raft-log.bolt without the space freed by log
truncation. The daemon must be stopped. The original file is kept as
raft-log.bolt.pre-compact; delete it once the node has restarted and
rejoined the cluster.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dataDir, err := raftCompactDataDir(cmd)
			if err != nil {
				return err
			}
			return compactRaftLog(cmd, filepath.Join(dataDir, cluster.RaftLogPath))
		},
	}
	c.Flags().String("config", "config.yaml", "path to config.yaml, read for data_dir")
	c.Flags().String("data-dir", "", "node data directory (instead of --config)")
	c.MarkFlagsMutuallyExclusive("config", "data-dir")
	return c
}

// raftCompactDataDir returns --data-dir, or data_dir from --config.
func raftCompactDataDir(cmd *cobra.Command) (string, error) {
	if dir, _ := cmd.Flags().GetString("data-dir"); dir != "" {
		return dir, nil
	}
	cfgPath, _ := cmd.Flags().GetString("config")
	node, _, err := cluster.LoadConfig(cfgPath)
	if err != nil {
		return "", err
	}
	return node.Node.DataDir, nil
}

// compactRaftLog rewrites logPath with bolt.Compact (TS §9). The original
// is opened read-only, which fails when the daemon holds its lock, and stays
// open until the end. The compacted copy is written to logPath.compact; the
// original is then hard-linked to logPath.pre-compact and the copy renamed
// over logPath, so logPath exists at every point and a crash never leaves
// the node without its log. The lock belongs to the original inode, so it
// keeps the daemon away from logPath only until the rename; after it, the
// lock covers logPath.pre-compact and a daemon started then opens the
// compacted file. logPath.compact is created with O_EXCL, so a second
// compaction running at the same time stops there instead of removing the
// first one's copy.
func compactRaftLog(cmd *cobra.Command, logPath string) error {
	compactPath := logPath + ".compact"
	preCompactPath := logPath + ".pre-compact"

	orig, err := os.Stat(logPath)
	if err != nil {
		return err
	}
	src, err := bolt.Open(logPath, 0, &bolt.Options{Timeout: raftCompactLockTimeout, ReadOnly: true})
	if errors.Is(err, bolt.ErrTimeout) {
		return errRaftLogLocked
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", logPath, err)
	}
	defer src.Close()

	// A previous compaction's backup is kept until the operator deletes it.
	// os.Link below refuses it again if it appears meanwhile.
	if _, err := os.Stat(preCompactPath); err == nil {
		return errPreCompactExists(preCompactPath)
	}
	// The copy is created here, empty, so that only one compaction owns it.
	// bolt.Open initializes an empty file.
	f, err := os.OpenFile(compactPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, orig.Mode().Perm())
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s already exists: another compaction is running, or one was interrupted; delete it if none is running, then run the compaction again", compactPath)
	}
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(compactPath)
		return err
	}

	if err := writeCompactedCopy(src, compactPath, orig); err != nil {
		os.Remove(compactPath)
		return err
	}
	if err := os.Link(logPath, preCompactPath); err != nil {
		os.Remove(compactPath)
		if errors.Is(err, fs.ErrExist) {
			return errPreCompactExists(preCompactPath)
		}
		return err
	}
	if err := os.Rename(compactPath, logPath); err != nil {
		os.Remove(preCompactPath)
		os.Remove(compactPath)
		return err
	}
	// The rename is done: from here on, an error must not suggest the
	// compaction failed.
	if err := syncDir(filepath.Dir(logPath)); err != nil {
		return fmt.Errorf("%s compacted; original kept as %s; directory fsync failed: %w", logPath, preCompactPath, err)
	}

	compacted, err := os.Stat(logPath)
	if err != nil {
		return err
	}
	writeln(cmd, fmt.Sprintf("%s: %d → %d bytes (original kept as %s)",
		logPath, orig.Size(), compacted.Size(), preCompactPath))
	return nil
}

func errPreCompactExists(path string) error {
	return fmt.Errorf("%s already exists: delete it once the node has rejoined, then run the compaction again", path)
}

// writeCompactedCopy compacts src into a new bolt file at path, with the
// mode and ownership of orig, and syncs it. The mode and owner are set
// before the data is written, so the sync covers them too.
func writeCompactedCopy(src *bolt.DB, path string, orig os.FileInfo) error {
	dst, err := bolt.Open(path, orig.Mode().Perm(), &bolt.Options{Timeout: raftCompactLockTimeout})
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err := setModeAndOwner(path, orig); err != nil {
		dst.Close()
		return err
	}
	if err := bolt.Compact(dst, src, raftCompactTxMaxSize); err != nil {
		dst.Close()
		return fmt.Errorf("compact into %s: %w", path, err)
	}
	if err := dst.Sync(); err != nil {
		dst.Close()
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

// setModeAndOwner gives path the mode and owner of orig. bolt.Open applies
// the umask, so the mode is set again explicitly.
func setModeAndOwner(path string, orig os.FileInfo) error {
	if err := os.Chmod(path, orig.Mode().Perm()); err != nil {
		return err
	}
	if st, ok := orig.Sys().(*syscall.Stat_t); ok {
		if err := os.Chown(path, int(st.Uid), int(st.Gid)); err != nil {
			return err
		}
	}
	return nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
