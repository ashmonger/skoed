// Acceptance tests for cluster join via web UI.
//
// FSIDs covered:
//   FS-ClusterJoinWebUiFollowerDialog
//   FS-ClusterJoinWebUiHiddenWhenAlreadyMember
//   FS-ClusterJoinWebUiExpiredToken
//   FS-ClusterJoinWebUiInvalidPayload
//   FS-ClusterJoinWebUiMtls → TestClusterJoinWebUiMtls
package acceptance

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// nodeJoinCluster calls POST /api/v1/node/join-cluster on n with the given
// token and leader_address. Returns the HTTP response (caller must close body).
func nodeJoinCluster(t *testing.T, n *Node, token, leaderAddress string) *http.Response {
	t.Helper()
	return n.apiDo(t, "POST", "/api/v1/node/join-cluster", mustJSON(t, map[string]string{
		"token":          token,
		"leader_address": leaderAddress,
	}))
}

// waitForClusterMode polls GET /api/v1/cluster/health until mode matches want,
// or times out.
func waitForClusterMode(t *testing.T, n *Node, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp := n.apiDo(t, "GET", "/api/v1/cluster/health", "")
		if resp.StatusCode == http.StatusOK {
			var body struct {
				Mode string `json:"mode"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&body); err == nil && body.Mode == want {
				resp.Body.Close()
				return
			}
		}
		resp.Body.Close()
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("cluster mode did not become %q within %v", want, timeout)
}

// ── FS-ClusterJoinWebUiTokenDisplay ──────────────────────────────────────────

// FS-ClusterJoinWebUiTokenDisplay
// The leader's token endpoint returns a non-empty token, leader_address, and
// expires_at that the web UI can display as the join payload block.
func TestClusterJoinWebUiTokenDisplay(t *testing.T) {
	t.Parallel()

	c := startCluster(t, 1)
	leader := c.Leader(t)

	resp := leader.apiDo(t, "POST", "/api/v1/cluster/tokens", "")
	assertStatus(t, resp, 201)
	defer resp.Body.Close()

	var body struct {
		Token         string `json:"token"`
		ExpiresAt     string `json:"expires_at"`
		LeaderAddress string `json:"leader_address"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	if body.Token == "" {
		t.Fatal("token field is empty — web UI cannot display join payload")
	}
	if body.LeaderAddress == "" {
		t.Fatal("leader_address field is empty — web UI cannot display join payload")
	}
	if body.ExpiresAt == "" {
		t.Fatal("expires_at field is empty — web UI cannot display join payload")
	}
	if _, err := time.Parse(time.RFC3339, body.ExpiresAt); err != nil {
		t.Fatalf("expires_at %q is not ISO-8601: %v", body.ExpiresAt, err)
	}
}

// ── FS-ClusterJoinWebUiFollowerDialog ────────────────────────────────────────

// FS-ClusterJoinWebUiFollowerDialog
// A single-node cluster calls POST /api/v1/node/join-cluster with a valid
// token from an existing cluster leader and becomes a member.
func TestClusterJoinWebUiFollowerDialog(t *testing.T) {
	t.Parallel()

	// Start the leader cluster (1 node).
	leader := startCluster(t, 1)
	leaderNode := leader.Leader(t)

	// Generate a join token on the leader and capture leader_address.
	resp := leaderNode.apiDo(t, "POST", "/api/v1/cluster/tokens", "")
	assertStatus(t, resp, 201)
	var tokenBody struct {
		Token         string `json:"token"`
		LeaderAddress string `json:"leader_address"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenBody); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	resp.Body.Close()
	if tokenBody.Token == "" || tokenBody.LeaderAddress == "" {
		t.Fatalf("token or leader_address empty in response")
	}

	// Start the "new node" as its own single-node cluster with a distinct nodeID.
	// Using startCluster would give it nodeID="node-1" which conflicts with the
	// leader (same hostname, same default ID). Spawn it directly.
	newCluster := &Cluster{t: t, bin: skoedBinary(t)}
	newCN := newCluster.spawnNode(t, M2NodeConfig{
		NodeID:   "node-fresh",
		DNSPort:  freeUDPPort(t),
		APIPort:  freeTCPPort(t),
		RaftPort: freeRaftPort(t),
	})
	newCluster.nodes = append(newCluster.nodes, newCN)
	waitReady(t, newCN.Node)
	setupAuth(t, newCN.Node)
	newNode := newCN

	// New node must report single-node mode before the join.
	joinResp := newNode.apiDo(t, "GET", "/api/v1/cluster/health", "")
	assertStatus(t, joinResp, http.StatusOK)
	var before struct{ Mode string `json:"mode"` }
	if err := json.NewDecoder(joinResp.Body).Decode(&before); err != nil {
		t.Fatalf("decode health before join: %v", err)
	}
	joinResp.Body.Close()
	if before.Mode != "single-node" {
		t.Fatalf("expected single-node before join, got %q", before.Mode)
	}

	// Call POST /api/v1/node/join-cluster on the new node.
	jResp := nodeJoinCluster(t, newNode.Node, tokenBody.Token, tokenBody.LeaderAddress)
	defer jResp.Body.Close()
	if jResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(jResp.Body)
		t.Fatalf("node/join-cluster returned %d: %s", jResp.StatusCode, body)
	}

	// Both nodes must eventually show mode=cluster.
	waitForClusterMode(t, newNode.Node, "cluster", 30*time.Second)
	waitForClusterMode(t, leaderNode.Node, "cluster", 30*time.Second)

	// Leader's cluster status must list both nodes.
	status := leader.Status(t, leaderNode)
	if len(status.Nodes) != 2 {
		t.Fatalf("expected 2 nodes in cluster status after join, got %d", len(status.Nodes))
	}
}

// ── FS-ClusterJoinWebUiHiddenWhenAlreadyMember ───────────────────────────────

// FS-ClusterJoinWebUiHiddenWhenAlreadyMember
// POST /api/v1/node/join-cluster returns 409 when the node is already a
// multi-node cluster member.
func TestClusterJoinWebUiAlreadyMember(t *testing.T) {
	t.Parallel()

	// Start a 2-node cluster; every node is already a member.
	c := startCluster(t, 2)
	leader := c.Leader(t)

	// Generate a token so the request is otherwise valid.
	token := c.MustCreateToken(t)

	// Call join on the leader (which is already in a cluster).
	jResp := nodeJoinCluster(t, leader.Node, token, leader.APIBase)
	defer jResp.Body.Close()
	assertStatus(t, jResp, http.StatusConflict)
}

// ── FS-ClusterJoinWebUiExpiredToken ──────────────────────────────────────────

// FS-ClusterJoinWebUiExpiredToken
// POST /api/v1/node/join-cluster with a token that has been consumed (single-use)
// returns 403 from the leader, which the follower forwards.
func TestClusterJoinWebUiExpiredToken(t *testing.T) {
	t.Parallel()

	leader := startCluster(t, 1)
	leaderNode := leader.Leader(t)

	// Generate a token and consume it by calling the leader's join endpoint
	// directly (AddNode creates its own token internally, so we use a direct
	// call to ensure OUR token is consumed).
	token := leader.MustCreateToken(t)

	// Consume the token: join a node-2 with our token via AddNodeWithToken.
	// This node may not start up fully (that's fine; we just need the token consumed).
	joinedCN := leader.AddNodeWithToken(t, token)
	waitReady(t, joinedCN.Node)

	// Start a fresh single-node cluster that will try to join with the consumed token.
	freshCluster := &Cluster{t: t, bin: skoedBinary(t)}
	freshCN := freshCluster.spawnNode(t, M2NodeConfig{
		NodeID:   "node-fresh",
		DNSPort:  freeUDPPort(t),
		APIPort:  freeTCPPort(t),
		RaftPort: freeRaftPort(t),
	})
	freshCluster.nodes = append(freshCluster.nodes, freshCN)
	waitReady(t, freshCN.Node)
	setupAuth(t, freshCN.Node)

	jResp := nodeJoinCluster(t, freshCN.Node, token, leaderNode.APIBase)
	defer jResp.Body.Close()
	// Leader returns 403 (token consumed); the follower forwards it.
	assertStatus(t, jResp, http.StatusForbidden)
}

// ── FS-ClusterJoinWebUiInvalidPayload ────────────────────────────────────────

// FS-ClusterJoinWebUiInvalidPayload
// POST /api/v1/node/join-cluster returns 400 when required fields are missing.
func TestClusterJoinWebUiInvalidPayload(t *testing.T) {
	t.Parallel()

	n := startCluster(t, 1)
	node := n.Leader(t)

	// Missing leader_address.
	r1 := node.apiDo(t, "POST", "/api/v1/node/join-cluster", mustJSON(t, map[string]string{
		"token": "sometoken",
	}))
	defer r1.Body.Close()
	assertStatus(t, r1, http.StatusBadRequest)

	// Missing token.
	r2 := node.apiDo(t, "POST", "/api/v1/node/join-cluster", mustJSON(t, map[string]string{
		"leader_address": "http://127.0.0.1:8080",
	}))
	defer r2.Body.Close()
	assertStatus(t, r2, http.StatusBadRequest)
}

// ── FS-ClusterJoinWebUiMtls ──────────────────────────────────────────────────

// @fsid:FS-ClusterJoinWebUiMtls
// A single-node mTLS cluster on 127.0.0.3, with the CA it generated at
// start, joins a 1-node mTLS cluster on 127.0.0.2 through
// POST /api/v1/node/join-cluster. The distinct addresses make Raft and the
// peer API dial another address, as across hosts.
//
// "appears in the nodes table" is checked on GET /api/v1/cluster/status,
// which the nodes table shows. "the blocklist configuration matches" is
// checked on a blocklist created on the leader after the join.
func TestClusterJoinWebUiMtls(t *testing.T) {
	t.Parallel()

	c := startClusterMTLSAcrossAddresses(t, 1)
	leader := c.Leader(t)
	nc := startSingleNodeMTLSOn(t, "node-fresh", "127.0.0.3")
	newNode := nc.Node(0)
	if leader.Host == newNode.Host {
		t.Fatalf("leader and new node share the address %s", leader.Host)
	}

	tlsFile := func(n *ClusterNode, name string) string {
		return filepath.Join(n.DataDir, "tls", "cluster", name)
	}
	leaderCA, err := os.ReadFile(tlsFile(leader, "ca.crt"))
	if err != nil {
		t.Fatalf("read ca.crt of the leader: %v", err)
	}
	ownCA, err := os.ReadFile(tlsFile(newNode, "ca.crt"))
	if err != nil {
		t.Fatalf("read ca.crt of %s before the join: %v", newNode.NodeID, err)
	}
	if bytes.Equal(leaderCA, ownCA) {
		t.Fatalf("%s already holds the leader's CA before the join", newNode.NodeID)
	}

	// The join payload the leader's web UI shows.
	resp := leader.apiDo(t, "POST", "/api/v1/cluster/tokens", "")
	assertStatus(t, resp, http.StatusCreated)
	var payload struct {
		Token         string `json:"token"`
		LeaderAddress string `json:"leader_address"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	resp.Body.Close()
	if payload.Token == "" || payload.LeaderAddress == "" {
		t.Fatalf("token or leader_address empty in the join payload: %+v", payload)
	}

	// The join dialog of the new node.
	start := time.Now()
	jResp := nodeJoinCluster(t, newNode.Node, payload.Token, payload.LeaderAddress)
	if jResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(jResp.Body)
		jResp.Body.Close()
		t.Fatalf("node/join-cluster on %s returned %d: %s", newNode.NodeID, jResp.StatusCode, body)
	}
	jResp.Body.Close()

	// Within 30 seconds, both nodes list two members. The new node's
	// credentials come from the leader once it has joined, so its session
	// is renewed while it does not answer.
	deadline := start.Add(30 * time.Second)
	members := map[string]int{}
	for time.Now().Before(deadline) {
		done := true
		for _, n := range []*ClusterNode{leader, newNode} {
			st, ok := c.tryStatus(t, n)
			if !ok {
				if tok := loginSessionMaybe(t, n.Node, defaultUsername, defaultPassword); tok != "" {
					n.Node.sessionToken = tok
				}
				done = false
				continue
			}
			members[n.NodeID] = len(st.Nodes)
			if len(st.Nodes) != 2 {
				done = false
			}
		}
		if done {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if members[leader.NodeID] != 2 || members[newNode.NodeID] != 2 {
		t.Fatalf("members listed 30s after the join: %s %d, %s %d; want 2 on both",
			leader.NodeID, members[leader.NodeID], newNode.NodeID, members[newNode.NodeID])
	}

	// A blocklist created on the leader reaches the new node.
	resp = leader.apiDo(t, "POST", "/api/v1/blocklists", mustJSON(t, map[string]any{
		"id":      "webui-mtls-leader-bl",
		"name":    "Created on the leader",
		"enabled": true,
		"source":  map[string]string{"type": "inline", "format": "domainlist"},
		"domains": []string{"leader.webui-mtls.example.com"},
	}))
	if b := readBody(t, resp); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create blocklist on the leader: status %d: %s", resp.StatusCode, b)
	}
	waitForBlocklist(t, newNode, "webui-mtls-leader-bl", 10*time.Second)

	// A write sent to the new node is forwarded to the leader and applied.
	resp = newNode.apiDo(t, "POST", "/api/v1/blocklists", mustJSON(t, map[string]any{
		"id":      "webui-mtls-new-bl",
		"name":    "Sent to the new node",
		"enabled": true,
		"source":  map[string]string{"type": "inline", "format": "domainlist"},
		"domains": []string{"new.webui-mtls.example.com"},
	}))
	if b := readBody(t, resp); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create blocklist via %s: status %d: %s", newNode.NodeID, resp.StatusCode, b)
	}
	waitForBlocklist(t, leader, "webui-mtls-new-bl", 10*time.Second)

	// The new node holds the cluster CA and a leaf for its node ID signed
	// by it, and no longer the key of the CA it generated.
	gotCA, err := os.ReadFile(tlsFile(newNode, "ca.crt"))
	if err != nil {
		t.Fatalf("read ca.crt of %s: %v", newNode.NodeID, err)
	}
	if !bytes.Equal(gotCA, leaderCA) {
		t.Errorf("ca.crt of %s differs from the leader's after the join", newNode.NodeID)
	}
	leafPEM, err := os.ReadFile(tlsFile(newNode, "node.crt"))
	if err != nil {
		t.Fatalf("read node.crt of %s: %v", newNode.NodeID, err)
	}
	block, _ := pem.Decode(leafPEM)
	if block == nil {
		t.Fatalf("node.crt of %s holds no PEM block", newNode.NodeID)
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse node.crt of %s: %v", newNode.NodeID, err)
	}
	if leaf.Subject.CommonName != newNode.NodeID {
		t.Errorf("node.crt of %s has CN %q, want %q", newNode.NodeID, leaf.Subject.CommonName, newNode.NodeID)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(leaderCA) {
		t.Fatalf("ca.crt of the leader holds no PEM certificate")
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		t.Errorf("node.crt of %s is not signed by the cluster CA: %v", newNode.NodeID, err)
	}
	if _, err := os.Stat(tlsFile(newNode, "ca.key")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ca.key of %s still present after the join (stat: %v)", newNode.NodeID, err)
	}
}
