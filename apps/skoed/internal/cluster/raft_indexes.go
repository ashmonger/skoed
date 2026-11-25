package cluster

import (
	"fmt"
	"strconv"
)

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
	first, err := n.logStore.FirstIndex()
	if err != nil {
		return RaftIndexes{}, fmt.Errorf("raft log first index: %w", err)
	}
	stats := n.r.Stats()
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
