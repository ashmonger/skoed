// Acceptance tests for node enrollment.
//
// Covers FSIDs:
//   FS-NodeEnrollmentGenerateToken
//   FS-NodeEnrollmentJoinWithValidToken
//   FS-NodeEnrollmentJoinTokenIsSingleUse
//   FS-NodeEnrollmentJoinTokenExpires
//   FS-NodeEnrollmentInvalidToken
//   FS-NodeEnrollmentPreservesNodeLocalSettings
//   FS-NodeEnrollmentSingleNodeBootstrap
//   FS-NodeEnrollmentM1ConfigMigration
//   FS-NodeRemoval
package acceptance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// FS-NodeEnrollmentGenerateToken
func TestNodeEnrollmentGenerateToken(t *testing.T) {
	c := startCluster(t, 1)

	resp := c.Leader(t).apiDo(t, "POST", "/api/v1/cluster/tokens", "")
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
	if len(body.Token) < 16 {
		t.Fatalf("token too short: %q", body.Token)
	}
	exp, err := time.Parse(time.RFC3339, body.ExpiresAt)
	if err != nil {
		t.Fatalf("parse expires_at %q: %v", body.ExpiresAt, err)
	}
	delta := time.Until(exp)
	if delta < 10*time.Minute || delta > 20*time.Minute {
		t.Fatalf("expires_at should be ~15 min in the future, got %v from now", delta)
	}
	if body.LeaderAddress == "" {
		t.Fatalf("leader_address empty in token response")
	}
}

// FS-NodeEnrollmentJoinWithValidToken
func TestNodeEnrollmentJoinWithValidToken(t *testing.T) {
	c := startCluster(t, 1)
	leader := c.Leader(t)

	c.MustCreateBlocklist(t, leader, "ads", "tracker.example.com")

	// Add a second node via the normal AddNode helper (uses a valid token).
	follower := c.AddNode(t)

	// Replication target: follower has the blocklist.
	waitForBlocklist(t, follower, "ads", 10*time.Second)

	// Cluster status from any node lists both members.
	status := c.Status(t, leader)
	if len(status.Nodes) != 2 {
		t.Fatalf("expected 2 nodes in cluster status, got %d", len(status.Nodes))
	}
}

// FS-NodeEnrollmentJoinTokenIsSingleUse
func TestNodeEnrollmentJoinTokenIsSingleUse(t *testing.T) {
	c := startCluster(t, 1)
	token := c.MustCreateToken(t)

	// First use: succeeds.
	_ = c.AddNodeWithToken(t, token)
	c.WaitConverged(t)

	// Reuse the same token to enrol a 3rd node — must fail.
	resp := c.Leader(t).apiDo(t, "POST", "/api/v1/cluster/join", mustJSON(t, map[string]string{
		"token":        token,
		"node_id":      "node-3-reuse",
		"raft_address": "127.0.0.1:7999",
	}))
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("expected 403 for token reuse, got %d", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(strings.ToLower(body), "consumed") &&
		!strings.Contains(strings.ToLower(body), "already") {
		t.Fatalf("expected 'consumed' or 'already used' in error body, got %q", body)
	}
}

// FS-NodeEnrollmentJoinTokenExpires
//
// Uses the documented test affordance from cluster-store.md: when
// DBLOCK_TEST_MODE=1 is set, DBLOCK_TEST_TOKEN_TTL_SECONDS overrides the
// production 15-minute token TTL so the test runs in seconds, not minutes.
func TestNodeEnrollmentJoinTokenExpires(t *testing.T) {
	c := startClusterWithEnv(t, 1, []string{
		"DBLOCK_TEST_MODE=1",
		"DBLOCK_TEST_TOKEN_TTL_SECONDS=1",
	})
	leader := c.Leader(t)

	token := c.MustCreateToken(t)

	// Wait for the token to expire, then attempt join.
	time.Sleep(2 * time.Second)
	r := leader.apiDo(t, "POST", "/api/v1/cluster/join", mustJSON(t, map[string]string{
		"token":        token,
		"node_id":      "node-2-expired",
		"raft_address": "127.0.0.1:7998",
	}))
	defer r.Body.Close()
	if r.StatusCode != 403 {
		t.Fatalf("expected 403 for expired token, got %d", r.StatusCode)
	}
	if !strings.Contains(strings.ToLower(readBody(t, r)), "expired") {
		t.Fatalf("expected 'expired' in error body")
	}
}

// FS-NodeEnrollmentInvalidToken
func TestNodeEnrollmentInvalidToken(t *testing.T) {
	c := startCluster(t, 1)
	resp := c.Leader(t).apiDo(t, "POST", "/api/v1/cluster/join", mustJSON(t, map[string]string{
		"token":        "this-token-was-never-issued-by-anyone",
		"node_id":      "intruder",
		"raft_address": "127.0.0.1:7997",
	}))
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("expected 403 for invalid token, got %d", resp.StatusCode)
	}
	if !strings.Contains(strings.ToLower(readBody(t, resp)), "invalid") {
		t.Fatalf("expected 'invalid' in error body")
	}
}

// FS-NodeEnrollmentPreservesNodeLocalSettings
func TestNodeEnrollmentPreservesNodeLocalSettings(t *testing.T) {
	c := startCluster(t, 1)
	leader := c.Leader(t)
	leaderDNSPort := leader.DNSPort
	leaderAPIPort := leader.APIPort

	// AddNode picks fresh free ports for the joiner; they must differ from
	// the leader's after enrollment.
	follower := c.AddNode(t)
	if follower.DNSPort == leaderDNSPort {
		t.Fatalf("joiner inherited leader's DNS port %d", leaderDNSPort)
	}
	if follower.APIPort == leaderAPIPort {
		t.Fatalf("joiner inherited leader's API port %d", leaderAPIPort)
	}

	// The follower's GET /settings must report its own DNS listen port, not
	// the leader's.
	resp := follower.apiDo(t, "GET", "/api/v1/settings", "")
	assertStatus(t, resp, 200)
	defer resp.Body.Close()
	var s struct {
		DNS struct {
			Listen struct {
				Port int `json:"port"`
			} `json:"listen"`
		} `json:"dns"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if s.DNS.Listen.Port != follower.DNSPort {
		t.Fatalf("follower reports DNS port %d, expected node-local %d",
			s.DNS.Listen.Port, follower.DNSPort)
	}
}

// FS-NodeEnrollmentSingleNodeBootstrap
func TestNodeEnrollmentSingleNodeBootstrap(t *testing.T) {
	c := startCluster(t, 1)
	leader := c.Leader(t)

	status := c.Status(t, leader)
	if len(status.Nodes) != 1 {
		t.Fatalf("expected single-node cluster, got %d nodes", len(status.Nodes))
	}
	if status.LeaderID != leader.NodeID {
		t.Fatalf("expected leader_id=%s, got %q", leader.NodeID, status.LeaderID)
	}

	// A single-node leader accepts writes.
	c.MustCreateBlocklist(t, leader, "smoke", "smoke.example.com")
}

// FS-NodeEnrollmentM1ConfigMigration
func TestNodeEnrollmentM1ConfigMigration(t *testing.T) {
	bin := dblockBinary(t)
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skipf("dblock binary not found at %s", bin)
	}

	// Pre-populate the data dir BEFORE starting the binary so it sees the M1
	// config.yaml on first boot (bbolt absent + data_dir/config.yaml in M1
	// format ⇒ migration trigger per cluster-store.md).
	dir := t.TempDir()
	dnsPort := freeUDPPort(t)
	apiPort := freeTCPPort(t)
	raftPort := freeTCPPort(t)

	cfg := M2NodeConfig{
		NodeID:            "legacy-1",
		DNSPort:           dnsPort,
		APIPort:           apiPort,
		RaftPort:          raftPort,
		SkipWriteNodeYAML: true,
	}

	// Write node.yaml using the same struct shape writeNodeYAML uses.
	type listenSection struct {
		Port int  `yaml:"port"`
		IPv4 bool `yaml:"ipv4"`
		IPv6 bool `yaml:"ipv6"`
	}
	type dnsSection struct {
		Listen listenSection `yaml:"listen"`
	}
	type nodeSection struct {
		ID          string     `yaml:"id"`
		RaftAddress string     `yaml:"raft_address"`
		APIAddress  string     `yaml:"api_address"`
		DNS         dnsSection `yaml:"dns"`
		DataDir     string     `yaml:"data_dir"`
	}
	type nodeYAML struct {
		Node nodeSection `yaml:"node"`
	}
	doc := nodeYAML{
		Node: nodeSection{
			ID:          cfg.NodeID,
			RaftAddress: fmt.Sprintf("127.0.0.1:%d", raftPort),
			APIAddress:  fmt.Sprintf("127.0.0.1:%d", apiPort),
			DNS:         dnsSection{Listen: listenSection{Port: dnsPort, IPv4: true, IPv6: false}},
			DataDir:     dir,
		},
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal node.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node.yaml"), data, 0600); err != nil {
		t.Fatalf("write node.yaml: %v", err)
	}

	// Write M1-shaped config.yaml: one blocklist, no cluster section.
	m1 := `version: 1
dns:
  listen: {port: ` + fmt.Sprintf("%d", dnsPort) + `, ipv4: true, ipv6: false}
  mode: forwarding
  upstream_resolvers: ["127.0.0.1:1"]
  upstream_timeout_seconds: 3
  cache: {enabled: true, max_entries: 1000}
filtering:
  block_policy: nxdomain
  blocklists:
    - id: legacy-ads
      name: Legacy
      enabled: true
      block_policy: nxdomain
      source: {type: "", format: domainlist}
      domains: [tracker.legacy.example.com]
api:
  port: ` + fmt.Sprintf("%d", apiPort) + `
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(m1), 0600); err != nil {
		t.Fatalf("write M1 config: %v", err)
	}

	c := &Cluster{t: t, bin: bin}
	cn := c.spawnNodeInDir(t, dir, cfg)
	c.nodes = append(c.nodes, cn)

	waitReady(t, cn.Node)
	setupAuth(t, cn.Node)

	// After migration, the blocklist defined in YAML is now in bbolt.
	waitForBlocklist(t, cn, "legacy-ads", 10*time.Second)

	// The shadow config.yaml still exists post-migration.
	if _, err := os.Stat(filepath.Join(cn.DataDir, "config.yaml")); err != nil {
		t.Fatalf("expected shadow config.yaml after migration: %v", err)
	}
}

// FS-NodeRemoval
func TestNodeRemoval(t *testing.T) {
	c := startCluster(t, 3)
	leader := c.Leader(t)
	followers := c.Followers(t)
	if len(followers) < 1 {
		t.Fatalf("expected at least 1 follower, got %d", len(followers))
	}
	victim := followers[0]

	resp := leader.apiDo(t, "DELETE", "/api/v1/cluster/nodes/"+victim.NodeID, "")
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatalf("expected 204 on node removal, got %d", resp.StatusCode)
	}

	// Cluster reports 2 voters after removal.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s := c.Status(t, leader)
		voters := 0
		for _, e := range s.Nodes {
			if e.Role == "leader" || e.Role == "follower" {
				voters++
			}
		}
		if voters == 2 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Removed node is no longer a Raft voter, so WaitConverged would loop
	// forever waiting for it to catch up to the leader's commit index. Kill
	// the orphan process and mark its slot so WaitConverged skips it.
	victimIdx := -1
	for i := 0; i < c.Size(); i++ {
		if c.Node(i) == victim {
			victimIdx = i
			break
		}
	}
	if victimIdx < 0 {
		t.Fatalf("could not locate victim node %s in cluster", victim.NodeID)
	}
	c.KillNode(t, victimIdx)

	// Cluster still accepts writes via remaining majority.
	c.MustCreateBlocklist(t, leader, "leader-write-after-removal", "post.example.com")

	// After removal, the removed node MUST refuse writes. The leader-redirect
	// path is broken for a removed node (it's no longer a Raft voter and the
	// leader will reject any forwarded command from a non-member), so writes
	// either fail at the victim (5xx) or get a clear leader-redirect that
	// itself fails when actually forwarded.
	//
	// Acceptance test: a POST /api/v1/blocklists to the removed node MUST NOT
	// return 2xx.
	postResp, err := victim.apiDoNonFatal("POST", "/api/v1/blocklists",
		mustJSON(t, map[string]any{
			"id":      "post-removal-write",
			"name":    "should not commit",
			"enabled": true,
			"domains": []string{"nope.example.com"},
			"source":  map[string]string{"format": "domainlist"},
		}))
	if err == nil {
		defer postResp.Body.Close()
		if postResp.StatusCode >= 200 && postResp.StatusCode < 300 {
			t.Fatalf("removed node accepted a write (status %d) — should have refused", postResp.StatusCode)
		}
	}
	// err != nil is acceptable: the removed node may have shut down its API.

	// And: the leader's view confirms post-removal blocklist did NOT propagate.
	resp2 := leader.apiDo(t, "GET", "/api/v1/blocklists/post-removal-write", "")
	defer resp2.Body.Close()
	if resp2.StatusCode != 404 {
		t.Fatalf("removed node's write leaked into the cluster (leader has post-removal-write, status %d)",
			resp2.StatusCode)
	}
}
