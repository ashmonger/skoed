package cluster

import (
	"errors"
	"fmt"
	"strconv"

	bolt "go.etcd.io/bbolt"
)

// ErrRaftStoreClosed is returned by RaftIndexes once the Raft log store has
// been closed, which is expected while the node shuts down.
var ErrRaftStoreClosed = errors.New("raft log store is closed")

// RaftIndexes are the Raft log indexes of this node (TS-BlocklistContentDistribution
// §8, FS-RaftLogStaysBounded). The log stays bounded when
// LastSnapshot - First <= raftTrailingLogs.
type RaftIndexes struct {
	First        uint64
	Last         uint64
	LastSnapshot uint64
}

// RaftIndexes returns the first and last index of this node's Raft log
// store and the index of its latest snapshot.
func (c *Cluster) RaftIndexes() (RaftIndexes, error) {
	return c.raft.indexes()
}

func (n *raftNode) indexes() (RaftIndexes, error) {
	// Stats is read first: the log is only truncated after a snapshot, so
	// first is then never older than the snapshot index read here.
	stats := n.r.Stats()
	first, err := n.logStore.FirstIndex()
	if errors.Is(err, bolt.ErrDatabaseNotOpen) {
		return RaftIndexes{}, ErrRaftStoreClosed
	}
	if err != nil {
		return RaftIndexes{}, fmt.Errorf("raft log first index: %w", err)
	}
	last, err := strconv.ParseUint(stats["last_log_index"], 10, 64)
	if err != nil {
		return RaftIndexes{}, fmt.Errorf("raft stats last_log_index: %w", err)
	}
	snapshot, err := strconv.ParseUint(stats["last_snapshot_index"], 10, 64)
	if err != nil {
		return RaftIndexes{}, fmt.Errorf("raft stats last_snapshot_index: %w", err)
	}
	return RaftIndexes{First: first, Last: last, LastSnapshot: snapshot}, nil
}
