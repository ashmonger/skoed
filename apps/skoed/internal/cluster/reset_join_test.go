package cluster

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"testing"
	"time"
)

// TestResetRaftForJoinKeepsTLSStreamLayer checks that, on an mTLS cluster,
// the Raft node rebuilt by ResetRaftForJoin still serves the Raft port over
// mutual TLS, so a node that resets to join speaks TLS to its peers
// (TS-EncryptedClusterMesh "Raft transport", FS-MtlsClusterFormsWhenEnabled).
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
	caCert, caKey, err := GenerateClusterCA(dataDir)
	if err != nil {
		t.Fatalf("GenerateClusterCA: %v", err)
	}
	c, err := New(node, Options{SuppressRaftLog: true, MTLSEnabled: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	if err := c.ResetRaftForJoin(); err != nil {
		t.Fatalf("ResetRaftForJoin: %v", err)
	}

	certPEM, keyPEM := testLeaf(t, caCert, caKey, "node-2")
	leaf, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caCert)

	d := &net.Dialer{Timeout: 2 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", raftAddr, &tls.Config{
		Certificates: []tls.Certificate{leaf},
		RootCAs:      pool,
		ServerName:   "node-1",
	})
	if err != nil {
		t.Fatalf("TLS handshake on the Raft port after reset: %v", err)
	}
	conn.Close()
}
