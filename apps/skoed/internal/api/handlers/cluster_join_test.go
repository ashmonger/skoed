package handlers

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/skoed/skoed/internal/cluster"
)

// TS-EncryptedClusterMesh "Member records": a node ID peers cannot pin
// (localhost or an IP literal) is refused by join only on an mTLS cluster,
// and always by mtls-bootstrap. FS-MtlsInternalApiHTTPS.

// clusterOnlyApp is an AppState that only provides a cluster.
type clusterOnlyApp struct {
	AppState
	c *cluster.Cluster
}

func (a clusterOnlyApp) GetCluster() *cluster.Cluster { return a.c }

// leaderCluster starts a single-node cluster, with or without mTLS, and
// waits until it is the leader so a join reaches the token check.
func leaderCluster(t *testing.T, mtls bool) *cluster.Cluster {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	raftAddr := ln.Addr().String()
	ln.Close()
	node := &cluster.NodeYAML{Node: cluster.NodeSection{
		ID:          "node-1",
		RaftAddress: raftAddr,
		APIAddress:  "127.0.0.1:0",
		DataDir:     t.TempDir(),
	}}
	node.Node.Cluster.MTLS.Enabled = mtls
	c, err := cluster.New(node, cluster.Options{Bootstrap: true, SuppressRaftLog: true, MTLSEnabled: mtls})
	if err != nil {
		t.Fatalf("cluster.New: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	if c.MTLSEnabled() != mtls {
		t.Fatalf("MTLSEnabled() = %v, want %v", c.MTLSEnabled(), mtls)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !c.IsLeader() {
		if time.Now().After(deadline) {
			t.Fatal("single-node cluster did not become leader")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return c
}

func postCluster(h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

const joinLoopbackBody = `{"token":"not-a-token","node_id":"127.0.0.1","raft_address":"10.0.0.9:7000"}`

func TestClusterJoinRefusesUnpinnableNodeIDWithMTLS(t *testing.T) {
	h := New(clusterOnlyApp{c: leaderCluster(t, true)})
	rec := postCluster(h.ClusterJoin, joinLoopbackBody)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %q)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "must not be") {
		t.Errorf("body = %q, want the unpinnable node ID refusal", rec.Body.String())
	}
}

func TestClusterJoinAcceptsIPNodeIDWithoutMTLS(t *testing.T) {
	h := New(clusterOnlyApp{c: leaderCluster(t, false)})
	rec := postCluster(h.ClusterJoin, joinLoopbackBody)
	// The ID passes the node ID check and the request reaches the token
	// check, which refuses the unknown token.
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 from the token check (body %q)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid token") {
		t.Errorf("body = %q, want the token refusal", rec.Body.String())
	}
}

func TestClusterMTLSBootstrapRefusesUnpinnableNodeID(t *testing.T) {
	h := New(clusterOnlyApp{c: leaderCluster(t, true)})
	rec := postCluster(h.ClusterMTLSBootstrap, `{"token":"not-a-token","node_id":"localhost"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %q)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "must not be") {
		t.Errorf("body = %q, want the unpinnable node ID refusal", rec.Body.String())
	}
}
