// Acceptance tests for cluster join via web UI.
//
// FSIDs covered:
//   FS-ClusterJoinWebUiFollowerDialog
//   FS-ClusterJoinWebUiHiddenWhenAlreadyMember
//   FS-ClusterJoinWebUiExpiredToken
//   FS-ClusterJoinWebUiInvalidPayload
package acceptance

import (
	"encoding/json"
	"io"
	"net/http"
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
		RaftPort: freeTCPPort(t),
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
		RaftPort: freeTCPPort(t),
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
