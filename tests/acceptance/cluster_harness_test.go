// Cluster harness for M2 acceptance tests.
//
// Adds multi-node fixtures on top of the existing M1 harness:
//   - startCluster(t, n)          — bootstrap node-1, then enrol n-1 followers
//   - cluster.Leader()            — current Raft leader
//   - cluster.Followers()         — non-leader members
//   - cluster.AddNode()           — enrol a fresh node via join token
//   - cluster.KillNode(i)         — kill node by index (data dir preserved)
//   - cluster.RestartNode(i)      — restart a killed node from its data dir
//   - cluster.WaitConverged()     — block until all nodes share leader's commit_index
//   - cluster.MustCreateToken()   — POST /cluster/tokens; return token string
//
// Black-box: every interaction goes through the public HTTP API and DNS port.
package acceptance

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	clusterConvergeTimeout  = 30 * time.Second
	clusterConvergeInterval = 100 * time.Millisecond
	tokenTTL                = 15 * time.Minute
)

// Cluster represents a set of dblock nodes participating in a single Raft cluster.
type Cluster struct {
	t     *testing.T
	bin   string
	nodes []*ClusterNode
	// defaultEnv is appended to every spawnNode's Env so tests can apply a
	// cluster-wide override (e.g. DBLOCK_TEST_AGGREGATE_FLUSH_SECONDS) without
	// threading env through every helper.
	defaultEnv []string
}

// ClusterNode wraps a Node with the bookkeeping needed to kill and restart it.
type ClusterNode struct {
	*Node
	NodeID   string
	DataDir  string
	RaftAddr string // host:port for Raft RPC
	DNSPort  int
	APIPort  int
	RaftPort int
	// Env is passed verbatim to every Start/Restart of this node so that
	// DBLOCK_TEST_* overrides survive process restarts.
	Env    []string
	cmd    *exec.Cmd
	killed bool
}

// M2NodeConfig drives the config.yaml written before starting an M2 node.
type M2NodeConfig struct {
	NodeID   string
	DNSPort  int
	APIPort  int
	RaftPort int
	// Bootstrap is empty for the first node (it self-bootstraps as single-node
	// cluster); for joining nodes it carries the leader's API address and the
	// single-use join token issued by the leader.
	BootstrapLeaderAddr string
	BootstrapToken      string
	// SkipWriteNodeYAML lets the M1-migration test pre-write its own files
	// without the harness clobbering them.
	SkipWriteNodeYAML bool
	// Env passes extra environment variables to the subprocess. Used by tests
	// that need DBLOCK_TEST_* overrides (token TTL, aggregate flush interval).
	Env []string
}

// startCluster bootstraps node-1 as a single-node Raft cluster, then enrols
// initialNodes-1 followers via the join-token flow. Returns once every node
// has converged to the same commit index.
func startCluster(t *testing.T, initialNodes int) *Cluster {
	return startClusterWithEnv(t, initialNodes, nil)
}

// startClusterWithEnv is like startCluster but applies the given env vars to
// every node it spawns (and to any future AddNode calls on the cluster).
func startClusterWithEnv(t *testing.T, initialNodes int, env []string) *Cluster {
	t.Helper()
	if initialNodes < 1 {
		t.Fatalf("startCluster: need at least 1 node, got %d", initialNodes)
	}

	bin := dblockBinary(t)
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skipf("dblock binary not found at %s (set DBLOCK_BINARY to override)", bin)
	}

	c := &Cluster{t: t, bin: bin, defaultEnv: env}
	c.bootstrapFirst(t)
	setupAuth(t, c.nodes[0].Node)

	for i := 1; i < initialNodes; i++ {
		c.AddNode(t)
	}

	if initialNodes > 1 {
		c.WaitConverged(t)
	}
	return c
}

// bootstrapFirst starts node-1 with no peers configured. The node initializes
// itself as a single-node Raft cluster and is immediately the leader.
func (c *Cluster) bootstrapFirst(t *testing.T) {
	t.Helper()
	cn := c.spawnNode(t, M2NodeConfig{
		NodeID:   "node-1",
		DNSPort:  freeUDPPort(t),
		APIPort:  freeTCPPort(t),
		RaftPort: freeTCPPort(t),
	})
	c.nodes = append(c.nodes, cn)
	waitReady(t, cn.Node)
}

// AddNode generates a join token on the current leader and starts a fresh node
// configured to enrol with that token on first boot. Blocks until the new node
// reports converged commit index.
func (c *Cluster) AddNode(t *testing.T) *ClusterNode {
	t.Helper()
	leader := c.Leader(t)
	token := c.mustCreateToken(t, leader)

	nodeID := fmt.Sprintf("node-%d", len(c.nodes)+1)
	cn := c.spawnNode(t, M2NodeConfig{
		NodeID:              nodeID,
		DNSPort:             freeUDPPort(t),
		APIPort:             freeTCPPort(t),
		RaftPort:            freeTCPPort(t),
		BootstrapLeaderAddr: leader.APIBase,
		BootstrapToken:      token,
	})
	c.nodes = append(c.nodes, cn)
	waitReady(t, cn.Node)
	c.WaitConverged(t)
	return cn
}

// AddNodeWithToken starts a fresh node using the caller-supplied token (used
// by tests that need to assert behaviour on invalid/expired/reused tokens).
// The node may fail to come up — the caller decides what to assert.
func (c *Cluster) AddNodeWithToken(t *testing.T, token string) *ClusterNode {
	t.Helper()
	leader := c.Leader(t)
	cn := c.spawnNode(t, M2NodeConfig{
		NodeID:              fmt.Sprintf("node-%d", len(c.nodes)+1),
		DNSPort:             freeUDPPort(t),
		APIPort:             freeTCPPort(t),
		RaftPort:            freeTCPPort(t),
		BootstrapLeaderAddr: leader.APIBase,
		BootstrapToken:      token,
	})
	c.nodes = append(c.nodes, cn)
	return cn
}

// spawnNode writes config.yaml, starts the process, and registers cleanup.
// SkipWriteNodeYAML lets callers pre-populate the data dir themselves
// (used by the M1 migration test).
func (c *Cluster) spawnNode(t *testing.T, cfg M2NodeConfig) *ClusterNode {
	t.Helper()
	dir := t.TempDir()
	if !cfg.SkipWriteNodeYAML {
		writeConfigYAML(t, dir, cfg)
	}

	env := append([]string{}, c.defaultEnv...)
	env = append(env, cfg.Env...)

	cn := &ClusterNode{
		Node: &Node{
			DNSAddr: fmt.Sprintf("127.0.0.1:%d", cfg.DNSPort),
			APIBase: fmt.Sprintf("http://127.0.0.1:%d", cfg.APIPort),
		},
		NodeID:   cfg.NodeID,
		DataDir:  dir,
		RaftAddr: fmt.Sprintf("127.0.0.1:%d", cfg.RaftPort),
		DNSPort:  cfg.DNSPort,
		APIPort:  cfg.APIPort,
		RaftPort: cfg.RaftPort,
		Env:      env,
	}
	c.startProcess(t, cn)
	return cn
}

// spawnNodeInDir is like spawnNode but uses an existing directory (so callers
// can pre-populate it). Implies SkipWriteNodeYAML.
func (c *Cluster) spawnNodeInDir(t *testing.T, dir string, cfg M2NodeConfig) *ClusterNode {
	t.Helper()
	env := append([]string{}, c.defaultEnv...)
	env = append(env, cfg.Env...)
	cn := &ClusterNode{
		Node: &Node{
			DNSAddr: fmt.Sprintf("127.0.0.1:%d", cfg.DNSPort),
			APIBase: fmt.Sprintf("http://127.0.0.1:%d", cfg.APIPort),
		},
		NodeID:   cfg.NodeID,
		DataDir:  dir,
		RaftAddr: fmt.Sprintf("127.0.0.1:%d", cfg.RaftPort),
		DNSPort:  cfg.DNSPort,
		APIPort:  cfg.APIPort,
		RaftPort: cfg.RaftPort,
		Env:      env,
	}
	c.startProcess(t, cn)
	return cn
}

// startProcess starts (or restarts) the dblock process for a ClusterNode,
// re-using its DataDir and Env. Used by both spawnNode and RestartNode so
// env-var overrides survive restarts.
func (c *Cluster) startProcess(t *testing.T, cn *ClusterNode) {
	t.Helper()
	cmd := exec.Command(c.bin, "--config", filepath.Join(cn.DataDir, "config.yaml"))
	cmd.Dir = cn.DataDir
	if len(cn.Env) > 0 {
		cmd.Env = append(os.Environ(), cn.Env...)
	}
	if testing.Verbose() {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start dblock (node %s): %v", cn.NodeID, err)
	}
	cn.cmd = cmd
	cn.Node.cmd = cmd
	cn.killed = false

	t.Cleanup(func() {
		if !cn.killed {
			cmd.Process.Kill() //nolint:errcheck
			cmd.Wait()         //nolint:errcheck
		}
	})
}

// writeConfigYAML writes the per-node config.yaml consumed by an M2 node.
// The file has a top-level `node:` section (id, addresses, dns listen,
// data_dir) and an optional `bootstrap:` section (consumed once on first
// boot). Cluster-replicated state — blocklists, allowlist, settings, auth,
// etc. — lives in bbolt and is mirrored back into this same file by the
// shadow YAML writer; the harness intentionally leaves those sections empty
// so the binary treats first boot as "no seed needed".
func writeConfigYAML(t *testing.T, dir string, cfg M2NodeConfig) {
	t.Helper()

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
	type bootstrapSection struct {
		LeaderAddress string `yaml:"leader_address,omitempty"`
		Token         string `yaml:"token,omitempty"`
	}
	type configYAML struct {
		Node      nodeSection      `yaml:"node"`
		Bootstrap bootstrapSection `yaml:"bootstrap,omitempty"`
	}

	doc := configYAML{
		Node: nodeSection{
			ID:          cfg.NodeID,
			RaftAddress: fmt.Sprintf("127.0.0.1:%d", cfg.RaftPort),
			APIAddress:  fmt.Sprintf("127.0.0.1:%d", cfg.APIPort),
			DNS:         dnsSection{Listen: listenSection{Port: cfg.DNSPort, IPv4: true, IPv6: false}},
			DataDir:     dir,
		},
		Bootstrap: bootstrapSection{
			LeaderAddress: cfg.BootstrapLeaderAddr,
			Token:         cfg.BootstrapToken,
		},
	}

	data, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal config.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0600); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}
}

// Leader returns the node currently reporting role=leader. Polls briefly to
// handle the case where leadership has just transferred.
func (c *Cluster) Leader(t *testing.T) *ClusterNode {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, n := range c.nodes {
			if n.killed {
				continue
			}
			role, ok := c.nodeRole(t, n)
			if ok && role == "leader" {
				return n
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no leader found within 10s")
	return nil
}

// Followers returns every non-leader, non-killed node.
func (c *Cluster) Followers(t *testing.T) []*ClusterNode {
	t.Helper()
	leader := c.Leader(t)
	var out []*ClusterNode
	for _, n := range c.nodes {
		if n != leader && !n.killed {
			out = append(out, n)
		}
	}
	return out
}

// Node returns the i-th node (0-indexed). Killed nodes are still indexed.
func (c *Cluster) Node(i int) *ClusterNode {
	if i < 0 || i >= len(c.nodes) {
		c.t.Fatalf("Node(%d): out of range (cluster size %d)", i, len(c.nodes))
	}
	return c.nodes[i]
}

// Size returns the number of nodes that have ever been added (including killed).
func (c *Cluster) Size() int { return len(c.nodes) }

// KillNode stops a node's process without removing its data directory, so a
// later RestartNode can revive it with the same Raft identity.
func (c *Cluster) KillNode(t *testing.T, i int) {
	t.Helper()
	n := c.Node(i)
	if n.killed {
		return
	}
	if err := n.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill node %s: %v", n.NodeID, err)
	}
	n.cmd.Wait() //nolint:errcheck
	n.killed = true
}

// RestartNode brings a previously-killed node back up using the same data dir
// (so it rejoins as the same Raft member) and the same env vars.
func (c *Cluster) RestartNode(t *testing.T, i int) {
	t.Helper()
	n := c.Node(i)
	if !n.killed {
		t.Fatalf("RestartNode: node %s is not killed", n.NodeID)
	}
	c.startProcess(t, n)
	waitReady(t, n.Node)
}

// WaitConverged blocks until every alive node reports the leader's
// commit_index from GET /api/v1/cluster/status.
func (c *Cluster) WaitConverged(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(clusterConvergeTimeout)
	for time.Now().Before(deadline) {
		leaderIdx, ok := c.leaderCommitIndex(t)
		if ok && c.allAtCommitIndex(t, leaderIdx) {
			return
		}
		time.Sleep(clusterConvergeInterval)
	}
	t.Fatalf("cluster did not converge within %s", clusterConvergeTimeout)
}

// mustCreateToken posts to /cluster/tokens on the given node and returns the
// plaintext token. Fails the test on non-201 response.
func (c *Cluster) mustCreateToken(t *testing.T, n *ClusterNode) string {
	t.Helper()
	resp := n.apiDo(t, "POST", "/api/v1/cluster/tokens", "")
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("create token: status %d", resp.StatusCode)
	}
	var body struct {
		Token         string `json:"token"`
		ExpiresAt     string `json:"expires_at"`
		LeaderAddress string `json:"leader_address"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	if body.Token == "" {
		t.Fatalf("create token: empty token in response")
	}
	return body.Token
}

// MustCreateToken is the exported form used by individual test files.
func (c *Cluster) MustCreateToken(t *testing.T) string {
	return c.mustCreateToken(t, c.Leader(t))
}

// ClusterStatus is the JSON shape returned by GET /api/v1/cluster/status.
type ClusterStatus struct {
	ClusterID string             `json:"cluster_id"`
	RaftTerm  int                `json:"raft_term"`
	LeaderID  string             `json:"leader_id"`
	Nodes     []ClusterNodeEntry `json:"nodes"`
}

// ClusterNodeEntry is a single node row inside ClusterStatus.
type ClusterNodeEntry struct {
	NodeID      string `json:"node_id"`
	Role        string `json:"role"`
	RaftAddress string `json:"raft_address"`
	APIAddress  string `json:"api_address"`
	LastContact string `json:"last_contact"`
	CommitIndex int    `json:"commit_index"`
	SyncState   string `json:"sync_state"`
}

// Status fetches /cluster/status from the named node.
func (c *Cluster) Status(t *testing.T, n *ClusterNode) ClusterStatus {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/cluster/status", "")
	assertStatus(t, resp, 200)
	defer resp.Body.Close()
	var out ClusterStatus
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode cluster status from %s: %v", n.NodeID, err)
	}
	return out
}

// nodeRole returns the role this node reports for ITSELF (peeked from its
// own /cluster/status). Returns ok=false if the API isn't responding yet.
func (c *Cluster) nodeRole(t *testing.T, n *ClusterNode) (string, bool) {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/cluster/status", "")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", false
	}
	var s ClusterStatus
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return "", false
	}
	for _, e := range s.Nodes {
		if e.NodeID == n.NodeID {
			return e.Role, true
		}
	}
	return "", false
}

func (c *Cluster) leaderCommitIndex(t *testing.T) (int, bool) {
	t.Helper()
	for _, n := range c.nodes {
		if n.killed {
			continue
		}
		role, ok := c.nodeRole(t, n)
		if !ok || role != "leader" {
			continue
		}
		s := c.Status(t, n)
		for _, e := range s.Nodes {
			if e.NodeID == n.NodeID {
				return e.CommitIndex, true
			}
		}
	}
	return 0, false
}

func (c *Cluster) allAtCommitIndex(t *testing.T, target int) bool {
	t.Helper()
	for _, n := range c.nodes {
		if n.killed {
			continue
		}
		s, ok := c.tryStatus(t, n)
		if !ok {
			return false
		}
		var found bool
		for _, e := range s.Nodes {
			if e.NodeID == n.NodeID {
				if e.CommitIndex < target {
					return false
				}
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// tryStatus is the non-fatal variant: returns ok=false if the API isn't
// answering yet (used during convergence polling).
func (c *Cluster) tryStatus(t *testing.T, n *ClusterNode) (ClusterStatus, bool) {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/cluster/status", "")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ClusterStatus{}, false
	}
	var s ClusterStatus
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return ClusterStatus{}, false
	}
	return s, true
}

// blockRaftPort installs an iptables-equivalent on the test host by closing
// the listening port from outside. We approximate by killing the node — tests
// that need true partitions are documented as such; for acceptance purposes,
// kill is observationally equivalent to "node is unreachable".
//
// (No-op stub kept for parity with the spec's partition language.)
func (c *Cluster) blockRaftPort(_ *testing.T, _ int) {}

// MustCreateBlocklist creates a simple blocklist via the given node and waits
// for the cluster to converge.
func (c *Cluster) MustCreateBlocklist(t *testing.T, via *ClusterNode, id string, domains ...string) {
	t.Helper()
	type src struct {
		Type   string `json:"type"`
		Format string `json:"format"`
	}
	body := struct {
		ID      string   `json:"id"`
		Name    string   `json:"name"`
		Enabled bool     `json:"enabled"`
		Domains []string `json:"domains"`
		Source  src      `json:"source"`
	}{
		ID:      id,
		Name:    "Test " + id,
		Enabled: true,
		Domains: domains,
		Source:  src{Type: "", Format: "domainlist"},
	}
	resp := via.apiDo(t, "POST", "/api/v1/blocklists", mustJSON(t, body))
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("create blocklist %s via %s: status %d", id, via.NodeID, resp.StatusCode)
	}
	c.WaitConverged(t)
}

// waitForBlocklist polls a node's GET /api/v1/blocklists/{id} until present
// or the deadline elapses.
func waitForBlocklist(t *testing.T, n *ClusterNode, id string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		resp := n.apiDo(t, "GET", "/api/v1/blocklists/"+id, "")
		resp.Body.Close()
		if resp.StatusCode == 200 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("blocklist %s did not appear on %s within %s", id, n.NodeID, within)
}

// waitForNoBlocklist polls until the named blocklist returns 404.
func waitForNoBlocklist(t *testing.T, n *ClusterNode, id string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		resp := n.apiDo(t, "GET", "/api/v1/blocklists/"+id, "")
		resp.Body.Close()
		if resp.StatusCode == 404 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("blocklist %s still present on %s after %s", id, n.NodeID, within)
}

// waitForRole blocks until the named node reports the given role.
func waitForRole(t *testing.T, c *Cluster, n *ClusterNode, want string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		role, ok := c.nodeRole(t, n)
		if ok && role == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("node %s did not reach role %q within %s", n.NodeID, want, within)
}

// readShadowYAML returns the path to the on-disk shadow YAML for a node, and
// reads its bytes. Used by the shadow_yaml_test.go suite.
func readShadowYAML(t *testing.T, n *ClusterNode) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(n.DataDir, "config.yaml"))
	if err != nil {
		t.Fatalf("read shadow yaml at %s: %v", n.NodeID, err)
	}
	return b
}

// apiDoNonFatal sends an authenticated request and returns the response or
// the connection error WITHOUT calling t.Fatal. Tests that submit a request
// expected to fail because the server is being killed must use this helper —
// calling t.Fatal from a goroutine is unsafe.
func (n *ClusterNode) apiDoNonFatal(method, path, body string) (*http.Response, error) {
	var br io.Reader
	if body != "" {
		br = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, n.APIBase+path, br)
	if err != nil {
		return nil, err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.SetBasicAuth(defaultUsername, defaultPassword)
	return http.DefaultClient.Do(req)
}

// newRequestWithHeaders builds an *http.Request with a JSON body (if non-empty)
// and the supplied headers. Used by tests that pass test-only X-Test-* headers.
func newRequestWithHeaders(t *testing.T, method, url, body string, headers map[string]string) *http.Request {
	t.Helper()
	var br io.Reader
	if body != "" {
		br = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, br)
	if err != nil {
		t.Fatalf("build request %s %s: %v", method, url, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

// httpDefaultClient is a thin wrapper that lets tests share http.DefaultClient
// without importing net/http directly.
func httpDefaultClient() *http.Client { return http.DefaultClient }

// freePortStr returns a free TCP port as a decimal string (used when writing
// raw YAML config templates).
func freePortStr(t *testing.T) string {
	return strconv.Itoa(freeTCPPort(t))
}

// _ keeps net imported even if a future change drops the only use above.
var _ = net.IPv4zero
