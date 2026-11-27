package cluster

import (
	"net"
	"testing"
)

// TestResetRaftForJoinKeepsTLSStreamLayer checks that, on an mTLS cluster,
// the Raft node rebuilt by ResetRaftForJoin still serves the Raft port over
// mutual TLS, so a node that resets to join speaks TLS to its peers
// (TS-EncryptedClusterMesh "Raft transport", FS-MtlsClusterFormsWhenEnabled),
// and that it uses the CA and leaf on disk at the time of the reset, not
// the node's own CA loaded at start (TS-EncryptedClusterMesh "Join through
// the web UI" step 4, FS-ClusterJoinWebUiMtls).
func TestResetRaftForJoinKeepsTLSStreamLayer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	raftAddr := ln.Addr().String()
	ln.Close()
	dataDir := t.TempDir()
	node := &NodeYAML{Node: NodeSection{
		ID:          "node-1",
		RaftAddress: raftAddr,
		APIAddress:  "127.0.0.1:0",
		DataDir:     dataDir,
	}}
	node.Node.Cluster.MTLS.Enabled = true
	// Start as a joining node does: no bootstrap, CA material already on
	// disk. A bootstrap node would also start registerSelfAfterLeader,
	// which reads c.raft without raftMu and races with the reset.
	oldCA, oldKey, err := GenerateClusterCA(dataDir)
	if err != nil {
		t.Fatalf("GenerateClusterCA: %v", err)
	}
	c, err := New(node, Options{SuppressRaftLog: true, MTLSEnabled: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	if err := dialRaft(t, raftAddr, oldCA, oldKey, oldCA); err != nil {
		t.Fatalf("before reset, Raft port with the node's own CA: %v", err)
	}

	// The web-UI join writes the cluster CA and leaf before the reset.
	newCA, newKey := writeClusterBundle(t, dataDir, "node-1")
	if err := c.ResetRaftForJoin(); err != nil {
		t.Fatalf("ResetRaftForJoin: %v", err)
	}

	// A success also shows that the Raft port presents a leaf of the new CA.
	if err := dialRaft(t, raftAddr, newCA, newKey, newCA); err != nil {
		t.Fatalf("after reset, mutual TLS with the cluster CA on the Raft port: %v", err)
	}
	if err := dialRaft(t, raftAddr, oldCA, oldKey, newCA); err == nil {
		t.Error("after reset, client leaf of the node's old CA accepted on the Raft port")
	}
}
