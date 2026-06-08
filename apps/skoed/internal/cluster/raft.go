package cluster

import (
	"fmt"
	"io"
	stdlog "log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
)

const (
	raftLogPath      = "raft/raft-log.bolt"
	raftStablePath   = "raft/raft-stable.bolt"
	raftSnapshotsDir = "raft/snapshots"
)

// raftNode wraps a hashicorp/raft.Raft with the bookkeeping our orchestrator
// needs: the bound transport address, the local node id, and the FSM the log
// is replayed into.
type raftNode struct {
	r         *raft.Raft
	transport *raft.NetworkTransport
	fsm       *fsm
	nodeID    raft.ServerID
	bindAddr  string
}

// raftOptions controls how the Raft library is initialised. Tests can override
// the heartbeat / election timeouts via the test-mode hooks.
type raftOptions struct {
	NodeID       string
	BindAddr     string
	AdvertiseAddr string
	DataDir      string
	// Bootstrap=true means: there is no existing Raft state on disk and no
	// peers configured. We create a fresh single-node configuration.
	Bootstrap bool
	Logger    io.Writer
	// M5.3: optional TLS stream layer. Non-nil ⇒ mTLS-wrapped Raft transport.
	TLSStreamLayer *TLSStreamLayer
}

func newRaftNode(opts raftOptions, f *fsm) (*raftNode, error) {
	if err := os.MkdirAll(filepath.Join(opts.DataDir, "raft"), 0700); err != nil {
		return nil, fmt.Errorf("create raft dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(opts.DataDir, raftSnapshotsDir), 0700); err != nil {
		return nil, fmt.Errorf("create snapshot dir: %w", err)
	}

	logStore, err := raftboltdb.NewBoltStore(filepath.Join(opts.DataDir, raftLogPath))
	if err != nil {
		return nil, fmt.Errorf("open raft log store: %w", err)
	}
	stableStore, err := raftboltdb.NewBoltStore(filepath.Join(opts.DataDir, raftStablePath))
	if err != nil {
		return nil, fmt.Errorf("open raft stable store: %w", err)
	}

	snaps, err := raft.NewFileSnapshotStore(filepath.Join(opts.DataDir, raftSnapshotsDir), 3, opts.Logger)
	if err != nil {
		return nil, fmt.Errorf("open snapshot store: %w", err)
	}

	advertise := opts.AdvertiseAddr
	if advertise == "" {
		advertise = opts.BindAddr
	}
	advAddr, err := net.ResolveTCPAddr("tcp", advertise)
	if err != nil {
		return nil, fmt.Errorf("resolve advertise %q: %w", advertise, err)
	}
	var transport *raft.NetworkTransport
	if opts.TLSStreamLayer != nil {
		// M5.3: TLS-wrapped Raft transport.
		transport = raft.NewNetworkTransport(opts.TLSStreamLayer, 3, 10*time.Second, opts.Logger)
	} else {
		transport, err = raft.NewTCPTransport(opts.BindAddr, advAddr, 3, 10*time.Second, opts.Logger)
		if err != nil {
			return nil, fmt.Errorf("raft transport: %w", err)
		}
	}

	cfg := raft.DefaultConfig()
	cfg.LocalID = raft.ServerID(opts.NodeID)
	if opts.Logger != nil {
		cfg.LogOutput = opts.Logger
	} else {
		cfg.LogOutput = io.Discard
	}
	// Take the test-mode timing overrides into account if present.
	if hb, ok := envDurationSeconds("SKOED_TEST_RAFT_HEARTBEAT_MS"); ok {
		cfg.HeartbeatTimeout = hb
		cfg.ElectionTimeout = 2 * hb
		cfg.LeaderLeaseTimeout = hb
	}

	r, err := raft.NewRaft(cfg, f, logStore, stableStore, snaps, transport)
	if err != nil {
		return nil, fmt.Errorf("create raft: %w", err)
	}

	if opts.Bootstrap {
		fut := r.BootstrapCluster(raft.Configuration{
			Servers: []raft.Server{{
				ID:      cfg.LocalID,
				Address: transport.LocalAddr(),
			}},
		})
		if err := fut.Error(); err != nil {
			return nil, fmt.Errorf("bootstrap single-node cluster: %w", err)
		}
	}

	return &raftNode{
		r:         r,
		transport: transport,
		fsm:       f,
		nodeID:    cfg.LocalID,
		bindAddr:  opts.BindAddr,
	}, nil
}

// ApplyCommand encodes the command and submits it to Raft. Returns once the
// log entry has been committed (or after timeout). Must be called on the
// leader; callers should resolve forwarding before calling.
func (n *raftNode) ApplyCommand(kind CommandKind, payload any, timeout time.Duration) error {
	data, err := Encode(kind, payload)
	if err != nil {
		return err
	}
	fut := n.r.Apply(data, timeout)
	if err := fut.Error(); err != nil {
		return fmt.Errorf("raft apply %s: %w", kind, err)
	}
	if rerr, ok := fut.Response().(error); ok && rerr != nil {
		return fmt.Errorf("fsm apply %s: %w", kind, rerr)
	}
	return nil
}

// AddVoter adds a new voting member. Returns once the new server has caught
// up to the leader's commit index.
func (n *raftNode) AddVoter(id, address string, timeout time.Duration) error {
	fut := n.r.AddVoter(raft.ServerID(id), raft.ServerAddress(address), 0, timeout)
	return fut.Error()
}

// RemoveServer removes a member from the configuration.
func (n *raftNode) RemoveServer(id string, timeout time.Duration) error {
	fut := n.r.RemoveServer(raft.ServerID(id), 0, timeout)
	return fut.Error()
}

// LeadershipTransfer triggers a transfer to the named follower. The target's
// server address is looked up in the current Raft configuration so the
// TimeoutNow RPC can be routed to it directly.
func (n *raftNode) LeadershipTransfer(targetID string, timeout time.Duration) error {
	var targetAddr raft.ServerAddress
	for _, s := range n.r.GetConfiguration().Configuration().Servers {
		if s.ID == raft.ServerID(targetID) {
			targetAddr = s.Address
			break
		}
	}
	if targetAddr == "" {
		return fmt.Errorf("node %q is not in the current raft configuration", targetID)
	}
	fut := n.r.LeadershipTransferToServer(raft.ServerID(targetID), targetAddr)
	done := make(chan error, 1)
	go func() { done <- fut.Error() }()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("leadership transfer to %s did not complete in %s", targetID, timeout)
	}
}

// State returns Raft's view of this node's current role.
func (n *raftNode) State() raft.RaftState { return n.r.State() }

// Leader returns the current leader's address as known to this node (empty
// string when no leader is yet elected).
func (n *raftNode) Leader() (raft.ServerAddress, raft.ServerID) {
	return n.r.LeaderWithID()
}

// CurrentTerm returns this node's view of the latest Raft term.
func (n *raftNode) CurrentTerm() uint64 {
	stats := n.r.Stats()
	if v, ok := stats["term"]; ok {
		var t uint64
		if _, err := fmt.Sscanf(v, "%d", &t); err == nil {
			return t
		}
	}
	return 0
}

// CommitIndex returns this node's latest committed log index.
func (n *raftNode) CommitIndex() uint64 { return n.r.LastIndex() }

// Configuration returns the current Raft configuration (the cluster's voter list).
func (n *raftNode) Configuration() raft.Configuration {
	return n.r.GetConfiguration().Configuration()
}

// Stats returns the raw Raft stats map for diagnostics.
func (n *raftNode) Stats() map[string]string { return n.r.Stats() }

// Shutdown closes Raft cleanly.
func (n *raftNode) Shutdown() error {
	if err := n.r.Shutdown().Error(); err != nil {
		return err
	}
	return n.transport.Close()
}

// WaitForLeader polls until LeaderWithID returns a non-empty address or the
// timeout elapses.
func (n *raftNode) WaitForLeader(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		addr, _ := n.r.LeaderWithID()
		if addr != "" {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("no leader elected within %s", timeout)
}

// IsLeader reports whether this node is currently the leader.
func (n *raftNode) IsLeader() bool { return n.r.State() == raft.Leader }

// hasExistingRaftState returns true when the on-disk Raft log/stable stores
// already contain data, meaning the binary should NOT bootstrap a fresh
// single-node cluster on this start.
func hasExistingRaftState(dataDir string) bool {
	for _, p := range []string{raftLogPath, raftStablePath} {
		if fi, err := os.Stat(filepath.Join(dataDir, p)); err == nil && fi.Size() > 0 {
			return true
		}
	}
	return false
}

// raftLogger returns a writer suitable for the Raft library. We funnel its
// log lines through the standard log package so they show up alongside the
// rest of skoed's output.
func raftLogger() io.Writer { return raftLogWriter{} }

type raftLogWriter struct{}

func (raftLogWriter) Write(p []byte) (int, error) {
	stdlog.Print("raft: ", string(p))
	return len(p), nil
}

// envDurationSeconds reads an int from env and returns it as a duration (in
// milliseconds). Used by tests via SKOED_TEST_RAFT_HEARTBEAT_MS.
func envDurationSeconds(key string) (time.Duration, bool) {
	v := os.Getenv(key)
	if v == "" {
		return 0, false
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n <= 0 {
		return 0, false
	}
	return time.Duration(n) * time.Millisecond, true
}
