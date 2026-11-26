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
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	clusterConvergeTimeout  = 60 * time.Second
	clusterConvergeInterval = 100 * time.Millisecond
	tokenTTL                = 15 * time.Minute
)

// Cluster represents a set of skoed nodes participating in a single Raft cluster.
type Cluster struct {
	t     *testing.T
	bin   string
	nodes []*ClusterNode
	// defaultEnv is appended to every spawnNode's Env so tests can apply a
	// cluster-wide override (e.g. SKOED_TEST_AGGREGATE_FLUSH_SECONDS) without
	// threading env through every helper.
	defaultEnv []string
	// encryptedDNS opt-in: when true, every node spawned in this cluster gets
	// freshly-allocated DoH + DoT ports written into its config.yaml. M4 tests
	// flip this via startClusterEncrypted.
	encryptedDNS bool
	// mtls opt-in: when true, every node spawned in this cluster gets
	// node.cluster.mtls.enabled=true. M5.3 tests flip this via startClusterMTLS.
	mtls bool
	// distinctHosts opt-in: when true, the n-th node of the cluster binds its
	// Raft, peer and main API listeners to its own loopback address,
	// 127.0.0.(n+1), instead of 127.0.0.1, as nodes on separate hosts would.
	// startClusterMTLSAcrossAddresses flips it.
	distinctHosts bool
}

// ClusterNode wraps a Node with the bookkeeping needed to kill and restart it.
type ClusterNode struct {
	*Node
	NodeID  string
	DataDir string
	// Host is the address the node's Raft, peer and main API listeners
	// bind to: 127.0.0.1, or the node's own loopback address when the
	// cluster uses distinct hosts. DNS listens on every address.
	Host     string
	RaftAddr string // host:port for Raft RPC
	DNSPort  int
	APIPort  int
	RaftPort int
	// PeerAPIPort is the internal peer API port, RaftPort+1: the default
	// skoed derives when `node.cluster.peer_api_address` is unset, which the
	// harness never writes. freeRaftPort reserves it.
	PeerAPIPort int
	// Env is passed verbatim to every Start/Restart of this node so that
	// SKOED_TEST_* overrides survive process restarts.
	Env []string
	// UID is the user the skoed process runs as; 0 means the user running
	// the tests. A non-zero UID needs root (see AddNodeAsUser).
	UID    int
	cmd    *exec.Cmd
	killed bool
}

// M2NodeConfig drives the config.yaml written before starting an M2 node.
type M2NodeConfig struct {
	NodeID string
	// Host is written to raft_address and api_address; empty means
	// 127.0.0.1. The peer API keeps its default, <raft host>:<raft port + 1>.
	Host     string
	DNSPort  int
	APIPort  int
	RaftPort int
	// M4: optional encrypted DNS listeners. Zero/unset = disabled.
	DoHPort int
	DoTPort int
	// M8: optional DoH3 (HTTP/3 over QUIC) and DNSCrypt v2 listeners.
	DoH3Port     int
	DNSCryptPort int
	// M4 ACME: when set, written under node.dns.tls.acme.* in config.yaml.
	Acme *AcmeOpts
	// M4 FS-DohConfiguredCert: when both set, written under
	// node.dns.tls.cert_file / key_file. skoed then uses these PEMs
	// directly instead of generating a self-signed cert.
	TLSCertFile string
	TLSKeyFile  string
	// M3.6 DHCP: when set, written under node.dhcp.* in config.yaml.
	DHCP *DhcpOpts
	// M4.6 management-API TLS: when set, written under node.api.tls.*.
	APITLSEnabled  bool
	APITLSMode     string // "single_port" | "dual_port"
	APITLSHTTPSPort int   // when mode == dual_port
	APITLSHSTS     bool
	// M5.3 cluster mTLS: when true, written under node.cluster.mtls.enabled.
	ClusterMTLSEnabled bool
	// Bootstrap is empty for the first node (it self-bootstraps as single-node
	// cluster); for joining nodes it carries the leader's API address and the
	// single-use join token issued by the leader.
	BootstrapLeaderAddr string
	BootstrapToken      string
	// SkipWriteNodeYAML lets the M1-migration test pre-write its own files
	// without the harness clobbering them.
	SkipWriteNodeYAML bool
	// Env passes extra environment variables to the subprocess. Used by tests
	// that need SKOED_TEST_* overrides (token TTL, aggregate flush interval).
	Env []string
	// UID is copied to ClusterNode.UID: the process runs as this user when
	// non-zero. Not written to config.yaml.
	UID int
	// UpstreamResolvers is written to the top-level dns.upstream_resolvers
	// section (outside node:) so the DNS forwarder has a target. When empty
	// the node has no upstream and returns SERVFAIL for forwarded queries.
	UpstreamResolvers []string
}

// startCluster bootstraps node-1 as a single-node Raft cluster, then enrols
// initialNodes-1 followers via the join-token flow. Returns once every node
// has converged to the same commit index.
func startCluster(t *testing.T, initialNodes int) *Cluster {
	return startClusterWithEnv(t, initialNodes, nil)
}

// startClusterEncrypted is startCluster + every node opens DoH/DoT listeners
// on freshly-allocated TCP ports. M4 tests use this; everyone else stays on
// plain UDP/TCP DNS.
func startClusterEncrypted(t *testing.T, initialNodes int) *Cluster {
	c := startClusterWithEnvEncrypted(t, initialNodes, nil, true)
	return c
}

// startClusterMTLS is startCluster + every node boots with
// node.cluster.mtls.enabled=true. Cluster CA is generated on node-1;
// joining nodes receive the CA + a freshly-signed leaf in their join
// response. M5.3 tests use this.
func startClusterMTLS(t *testing.T, initialNodes int) *Cluster {
	t.Helper()
	return startClusterMTLSOn(t, initialNodes, false)
}

// startClusterMTLSAcrossAddresses is startClusterMTLS with every node on its
// own loopback address (node-1 on 127.0.0.2, node-2 on 127.0.0.3, ...), so
// that Raft and the peer API dial a different address for each peer, as
// they do across hosts. Linux routes all of 127.0.0.0/8 to the loopback
// interface, so no setup or root is needed.
//
// If the test fails, the TLS errors in each node's last lines of output are
// logged: across addresses, a peer certificate check by address shows up
// there and nowhere else.
func startClusterMTLSAcrossAddresses(t *testing.T, initialNodes int) *Cluster {
	t.Helper()
	return startClusterMTLSOn(t, initialNodes, true)
}

func startClusterMTLSOn(t *testing.T, initialNodes int, distinctHosts bool) *Cluster {
	t.Helper()
	if initialNodes < 1 {
		t.Fatalf("startClusterMTLS: need at least 1 node, got %d", initialNodes)
	}
	bin := skoedBinary(t)
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skipf("skoed binary not found at %s (set SKOED_BINARY to override)", bin)
	}
	c := &Cluster{t: t, bin: bin, mtls: true, distinctHosts: distinctHosts}
	if distinctHosts {
		t.Cleanup(func() { c.logTLSErrorsOnFailure(t) })
	}
	c.bootstrapFirst(t)
	setupAuth(t, c.nodes[0].Node)
	// setupAuth already called loginSession and set sessionToken.
	for i := 1; i < initialNodes; i++ {
		c.AddNode(t)
	}
	if initialNodes > 1 {
		c.WaitConverged(t)
	}
	return c
}

// AcmeOpts is the set of ACME knobs the harness writes under
// node.dns.tls.acme.* in the spawned node's config.yaml.
type AcmeOpts struct {
	Enabled           bool
	Email             string
	Domains           []string
	DirectoryURL      string
	HTTPChallengePort int // 0 = harness picks a free port; Node.AcmeHTTPAddr reads it back
}

// startClusterAcme spins a single-node cluster with DoH/DoT + ACME
// enabled per the given AcmeOpts. The harness picks a free TCP port for
// the HTTP-01 challenge listener when HTTPChallengePort is 0.
func startClusterAcme(t *testing.T, opts AcmeOpts) *Cluster {
	t.Helper()
	bin := skoedBinary(t)
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skipf("skoed binary not found at %s (set SKOED_BINARY to override)", bin)
	}
	c := &Cluster{t: t, bin: bin, encryptedDNS: true}
	cfg := M2NodeConfig{
		NodeID:   "node-1",
		DNSPort:  freeUDPPort(t),
		APIPort:  freeTCPPort(t),
		RaftPort: freeRaftPort(t),
		DoHPort:  freeTCPPort(t),
		DoTPort:  freeTCPPort(t),
		Acme:     &opts,
	}
	if opts.HTTPChallengePort == 0 {
		cfg.Acme.HTTPChallengePort = freeTCPPort(t)
	}
	cn := c.spawnNode(t, cfg)
	cn.Node.AcmeHTTPAddr = fmt.Sprintf("127.0.0.1:%d", cfg.Acme.HTTPChallengePort)
	c.nodes = append(c.nodes, cn)
	waitReady(t, cn.Node)
	setupAuth(t, c.nodes[0].Node)
	// setupAuth already called loginSession and set sessionToken.
	return c
}

// startClusterWithEnvEncrypted is the full-knob form.
func startClusterWithEnvEncrypted(t *testing.T, initialNodes int, env []string, encryptedDNS bool) *Cluster {
	t.Helper()
	if initialNodes < 1 {
		t.Fatalf("startCluster: need at least 1 node, got %d", initialNodes)
	}
	bin := skoedBinary(t)
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skipf("skoed binary not found at %s (set SKOED_BINARY to override)", bin)
	}
	c := &Cluster{t: t, bin: bin, defaultEnv: env, encryptedDNS: encryptedDNS}
	c.bootstrapFirst(t)
	setupAuth(t, c.nodes[0].Node)
	// setupAuth already called loginSession and set sessionToken.
	for i := 1; i < initialNodes; i++ {
		c.AddNode(t)
	}
	if initialNodes > 1 {
		c.WaitConverged(t)
	}
	return c
}

// startClusterWithEnv is like startCluster but applies the given env vars to
// every node it spawns (and to any future AddNode calls on the cluster).
func startClusterWithEnv(t *testing.T, initialNodes int, env []string) *Cluster {
	t.Helper()
	return startClusterWithBinary(t, initialNodes, env, skoedBinary(t))
}

// startClusterWithBinary is startClusterWithEnv with the skoed binary at bin
// instead of SKOED_BINARY. The upgrade test uses it to start a cluster on the
// previous release, then switches to the binary under test with SetBinary.
func startClusterWithBinary(t *testing.T, initialNodes int, env []string, bin string) *Cluster {
	t.Helper()
	if initialNodes < 1 {
		t.Fatalf("startCluster: need at least 1 node, got %d", initialNodes)
	}

	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skipf("skoed binary not found at %s (set SKOED_BINARY, or SKOED_PREVIOUS_BINARY for the previous release)", bin)
	}

	c := &Cluster{t: t, bin: bin, defaultEnv: env}
	c.bootstrapFirst(t)
	setupAuth(t, c.nodes[0].Node)
	// setupAuth already called loginSession and set sessionToken.

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
	cfg := M2NodeConfig{
		NodeID:   "node-1",
		Host:     c.nextHost(),
		DNSPort:  freeUDPPort(t),
		APIPort:  freeTCPPort(t),
		RaftPort: freeRaftPort(t),
	}
	if c.encryptedDNS {
		cfg.DoHPort = freeTCPPort(t)
		cfg.DoTPort = freeTCPPort(t)
	}
	if c.mtls {
		cfg.ClusterMTLSEnabled = true
	}
	cn := c.spawnNode(t, cfg)
	c.nodes = append(c.nodes, cn)
	c.waitNodeReady(t, cn)
}

// waitNodeReady waits until cn answers on its API and DNS ports. If the
// process exits before, the test fails at once with the exit status and
// the last lines the process wrote.
func (c *Cluster) waitNodeReady(t *testing.T, cn *ClusterNode) {
	t.Helper()
	if err := waitReadyErr(cn.Node); err != nil {
		t.Fatalf("node %s: %v%s", cn.NodeID, err, exitReport(cn.Node))
	}
}

// AddNode generates a join token on the current leader and starts a fresh node
// configured to enrol with that token on first boot. Blocks until the new node
// reports converged commit index.
func (c *Cluster) AddNode(t *testing.T) *ClusterNode {
	t.Helper()
	return c.AddNodeAsUser(t, 0)
}

// AddNodeAsUser is AddNode with the new node's process running as uid (0 =
// the user running the tests). The node's data dir is handed to that uid
// before every start. Running as another uid needs root; M50 uses it so an
// iptables owner match can cut one node's traffic only (cutUID).
func (c *Cluster) AddNodeAsUser(t *testing.T, uid int) *ClusterNode {
	t.Helper()
	leader := c.Leader(t)
	token := c.mustCreateToken(t, leader)

	cfg := M2NodeConfig{
		NodeID:              fmt.Sprintf("node-%d", len(c.nodes)+1),
		Host:                c.nextHost(),
		DNSPort:             freeUDPPort(t),
		APIPort:             freeTCPPort(t),
		RaftPort:            freeRaftPort(t),
		BootstrapLeaderAddr: leader.APIBase,
		BootstrapToken:      token,
		UID:                 uid,
	}
	if c.mtls {
		cfg.ClusterMTLSEnabled = true
	}
	cn := c.spawnNode(t, cfg)
	c.nodes = append(c.nodes, cn)
	c.waitNodeReady(t, cn)
	// Credentials are replicated via Raft; retry login until they arrive.
	cn.Node.sessionToken = loginWithRetry(t, cn.Node, defaultUsername, defaultPassword)
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
		Host:                c.nextHost(),
		DNSPort:             freeUDPPort(t),
		APIPort:             freeTCPPort(t),
		RaftPort:            freeRaftPort(t),
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

	n := &Node{
		DNSAddr: fmt.Sprintf("127.0.0.1:%d", cfg.DNSPort),
		APIBase: fmt.Sprintf("http://%s", net.JoinHostPort(nodeHost(cfg), strconv.Itoa(cfg.APIPort))),
	}
	if cfg.DoHPort > 0 {
		n.DoHAddr = fmt.Sprintf("127.0.0.1:%d", cfg.DoHPort)
	}
	if cfg.DoTPort > 0 {
		n.DoTAddr = fmt.Sprintf("127.0.0.1:%d", cfg.DoTPort)
	}
	if cfg.DoH3Port > 0 {
		n.DoH3Addr = fmt.Sprintf("127.0.0.1:%d", cfg.DoH3Port)
	}
	if cfg.DNSCryptPort > 0 {
		n.DNSCryptAddr = fmt.Sprintf("127.0.0.1:%d", cfg.DNSCryptPort)
	}
	cn := &ClusterNode{
		Node:        n,
		NodeID:      cfg.NodeID,
		DataDir:     dir,
		Host:        nodeHost(cfg),
		RaftAddr:    net.JoinHostPort(nodeHost(cfg), strconv.Itoa(cfg.RaftPort)),
		DNSPort:     cfg.DNSPort,
		APIPort:     cfg.APIPort,
		RaftPort:    cfg.RaftPort,
		PeerAPIPort: cfg.RaftPort + 1,
		Env:         env,
		UID:         cfg.UID,
	}
	n.ports = m2ConfigPorts(cfg)
	c.startProcess(t, cn)
	return cn
}

// spawnNodeInDir is like spawnNode but uses an existing directory (so callers
// can pre-populate it). Implies SkipWriteNodeYAML.
func (c *Cluster) spawnNodeInDir(t *testing.T, dir string, cfg M2NodeConfig) *ClusterNode {
	t.Helper()
	env := append([]string{}, c.defaultEnv...)
	env = append(env, cfg.Env...)
	n := &Node{
		DNSAddr: fmt.Sprintf("127.0.0.1:%d", cfg.DNSPort),
		APIBase: fmt.Sprintf("http://%s", net.JoinHostPort(nodeHost(cfg), strconv.Itoa(cfg.APIPort))),
	}
	if cfg.DoHPort > 0 {
		n.DoHAddr = fmt.Sprintf("127.0.0.1:%d", cfg.DoHPort)
	}
	if cfg.DoTPort > 0 {
		n.DoTAddr = fmt.Sprintf("127.0.0.1:%d", cfg.DoTPort)
	}
	if cfg.DoH3Port > 0 {
		n.DoH3Addr = fmt.Sprintf("127.0.0.1:%d", cfg.DoH3Port)
	}
	if cfg.DNSCryptPort > 0 {
		n.DNSCryptAddr = fmt.Sprintf("127.0.0.1:%d", cfg.DNSCryptPort)
	}
	cn := &ClusterNode{
		Node:        n,
		NodeID:      cfg.NodeID,
		DataDir:     dir,
		Host:        nodeHost(cfg),
		RaftAddr:    net.JoinHostPort(nodeHost(cfg), strconv.Itoa(cfg.RaftPort)),
		DNSPort:     cfg.DNSPort,
		APIPort:     cfg.APIPort,
		RaftPort:    cfg.RaftPort,
		PeerAPIPort: cfg.RaftPort + 1,
		Env:         env,
		UID:         cfg.UID,
	}
	n.ports = m2ConfigPorts(cfg)
	c.startProcess(t, cn)
	return cn
}

// nodeHost is the address cfg binds the Raft and main API listeners to.
func nodeHost(cfg M2NodeConfig) string {
	if cfg.Host == "" {
		return "127.0.0.1"
	}
	return cfg.Host
}

// nextHost returns the address of the next node added to c: empty (that is,
// 127.0.0.1) unless c uses distinct hosts, then 127.0.0.(n+1) for the n-th
// node, so node-1 gets 127.0.0.2 and no node shares 127.0.0.1 with the other
// listeners of the test run.
func (c *Cluster) nextHost() string {
	if !c.distinctHosts {
		return ""
	}
	return fmt.Sprintf("127.0.0.%d", len(c.nodes)+2)
}

// logTLSErrorsOnFailure logs, when t has failed, the lines of each node's
// last output that report a TLS error, once per node and message: Raft
// retries a failing dial several times a second.
func (c *Cluster) logTLSErrorsOnFailure(t *testing.T) {
	if !t.Failed() {
		return
	}
	for _, cn := range c.nodes {
		if cn.Node.output == nil {
			continue
		}
		seen := map[string]bool{}
		for _, line := range strings.Split(cn.Node.output.String(), "\n") {
			if !strings.Contains(line, "tls:") && !strings.Contains(line, "x509:") {
				continue
			}
			// Drop the leading timestamp to compare the messages.
			_, msg, _ := strings.Cut(strings.TrimSpace(line), " ")
			if !seen[msg] {
				seen[msg] = true
				t.Logf("node %s (%s): %s", cn.NodeID, cn.Host, line)
			}
		}
	}
}

// m2ConfigPorts lists every port cfg makes a node listen on, for
// portReport.
func m2ConfigPorts(cfg M2NodeConfig) []int {
	ports := []int{cfg.DNSPort, cfg.APIPort, cfg.RaftPort, cfg.RaftPort + 1}
	for _, p := range []int{cfg.DoHPort, cfg.DoTPort, cfg.DoH3Port, cfg.DNSCryptPort, cfg.APITLSHTTPSPort} {
		if p > 0 {
			ports = append(ports, p)
		}
	}
	if cfg.Acme != nil && cfg.Acme.HTTPChallengePort > 0 {
		ports = append(ports, cfg.Acme.HTTPChallengePort)
	}
	return ports
}

// startProcess starts (or restarts) the skoed process for a ClusterNode,
// re-using its DataDir and Env. Used by both spawnNode and RestartNode so
// env-var overrides survive restarts.
func (c *Cluster) startProcess(t *testing.T, cn *ClusterNode) {
	t.Helper()
	cmd := exec.Command(c.bin, "--config", filepath.Join(cn.DataDir, "config.yaml"))
	cmd.Dir = cn.DataDir
	if len(cn.Env) > 0 {
		cmd.Env = append(os.Environ(), cn.Env...)
	}
	output := captureOutput(cmd)
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	if cn.UID != 0 {
		requireExecutableByUID(t, c.bin, cn.UID)
		giveDataDirToUID(t, cn.DataDir, cn.UID)
		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(cn.UID), Gid: uint32(cn.UID)}
	}
	exited, err := startReaped(cmd)
	if err != nil {
		t.Fatalf("start skoed (node %s): %v", cn.NodeID, err)
	}
	cn.cmd = cmd
	cn.Node.cmd = cmd
	cn.Node.exited = exited
	cn.Node.output = output
	cn.killed = false

	t.Cleanup(func() {
		if !cn.killed {
			killAndReap(cmd, exited)
		}
	})
}

// requireExecutableByUID skips the test unless a process running as uid:uid
// (with no supplementary groups, as startProcess sets it) can execute bin:
// every parent directory needs the search bit and bin the execute bit for
// that uid. Otherwise the start fails with a bare "permission denied", e.g.
// when SKOED_BINARY points into a 0700 home directory.
func requireExecutableByUID(t *testing.T, bin string, uid int) {
	t.Helper()
	abs, err := filepath.Abs(bin)
	if err != nil {
		t.Fatalf("resolve %s: %v", bin, err)
	}
	check := func(p string) {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if !modeAllowsExec(fi, uid) {
			t.Skipf("the skoed binary %s (SKOED_BINARY or SKOED_PREVIOUS_BINARY) must be executable by uid %d, but %s is %v: put it where that uid can reach it (e.g. /tmp or /usr/local/bin with mode 0755)",
				abs, uid, p, fi.Mode())
		}
	}
	for p := filepath.Dir(abs); ; p = filepath.Dir(p) {
		check(p)
		if p == filepath.Dir(p) {
			break
		}
	}
	check(abs)
}

// modeAllowsExec reports whether uid, running with gid uid and no
// supplementary groups, has the execute (or search) bit on fi.
func modeAllowsExec(fi os.FileInfo, uid int) bool {
	perm := fi.Mode().Perm()
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return perm&0o001 != 0
	}
	switch {
	case int(st.Uid) == uid:
		return perm&0o100 != 0
	case int(st.Gid) == uid:
		return perm&0o010 != 0
	default:
		return perm&0o001 != 0
	}
}

// giveDataDirToUID chowns dir and everything under it to uid:uid, and adds
// the search bit for others on the parent directories that t.TempDir created
// (mode 0700, owned by the test user), so a process running as uid can reach
// dir.
//
// Precondition: no process running as uid may write under dir during the
// walk. Such a process could swap a file under dir for a symlink between the
// walk reading it and the chown, and redirect the chown to a file outside dir
// (Lchown only covers a symlink that is already there). startProcess calls it
// before every start, and KillNode always waits for the process to exit, so
// the node that owns dir is never running during the walk. Other processes
// running as the same uid can be alive, because parallel tests use cutUID
// too, but they are skoed processes under test from other tests: they use
// their own data directories and do not race with this one.
func giveDataDirToUID(t *testing.T, dir string, uid int) {
	t.Helper()
	err := filepath.Walk(dir, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, uid, uid)
	})
	if err != nil {
		t.Fatalf("chown %s to uid %d: %v", dir, uid, err)
	}
	tmp := filepath.Clean(os.TempDir())
	for p := filepath.Dir(dir); strings.HasPrefix(p, tmp+string(filepath.Separator)); p = filepath.Dir(p) {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if err := os.Chmod(p, fi.Mode().Perm()|0o001); err != nil {
			t.Fatalf("chmod o+x %s: %v", p, err)
		}
	}
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
		Port         int  `yaml:"port"`
		IPv4         bool `yaml:"ipv4"`
		IPv6         bool `yaml:"ipv6"`
		DoHPort      int  `yaml:"doh_port,omitempty"`
		DoTPort      int  `yaml:"dot_port,omitempty"`
		DoH3Port     int  `yaml:"doh3_port,omitempty"`
		DNSCryptPort int  `yaml:"dnscrypt_port,omitempty"`
	}
	type acmeSection struct {
		Enabled           bool     `yaml:"enabled"`
		Email             string   `yaml:"email,omitempty"`
		Domains           []string `yaml:"domains,omitempty"`
		DirectoryURL      string   `yaml:"directory_url,omitempty"`
		HTTPChallengePort int      `yaml:"http_challenge_port,omitempty"`
	}
	type dhcpSection struct {
		Enabled        bool   `yaml:"enabled"`
		Kind           string `yaml:"kind,omitempty"`
		URL            string `yaml:"url,omitempty"`
		FilePath       string `yaml:"file_path,omitempty"`
		Username       string `yaml:"username,omitempty"`
		Password       string `yaml:"password,omitempty"`
		RefreshSeconds int    `yaml:"refresh_seconds,omitempty"`
	}
	type apiTLSSection struct {
		Enabled      bool   `yaml:"enabled"`
		Mode         string `yaml:"mode,omitempty"`
		HTTPSAddress string `yaml:"https_address,omitempty"`
		HSTS         bool   `yaml:"hsts,omitempty"`
	}
	type apiSection struct {
		TLS *apiTLSSection `yaml:"tls,omitempty"`
	}
	type clusterMTLSSection struct {
		Enabled bool `yaml:"enabled"`
	}
	type clusterSection struct {
		MTLS *clusterMTLSSection `yaml:"mtls,omitempty"`
	}
	type tlsSection struct {
		CertFile string       `yaml:"cert_file,omitempty"`
		KeyFile  string       `yaml:"key_file,omitempty"`
		Acme     *acmeSection `yaml:"acme,omitempty"`
	}
	type dnsSection struct {
		Listen listenSection `yaml:"listen"`
		TLS    *tlsSection   `yaml:"tls,omitempty"`
	}
	type nodeSection struct {
		ID          string          `yaml:"id"`
		RaftAddress string          `yaml:"raft_address"`
		APIAddress  string          `yaml:"api_address"`
		DNS         dnsSection      `yaml:"dns"`
		DataDir     string          `yaml:"data_dir"`
		DHCP        *dhcpSection    `yaml:"dhcp,omitempty"`
		API         *apiSection     `yaml:"api,omitempty"`
		Cluster     *clusterSection `yaml:"cluster,omitempty"`
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
			RaftAddress: net.JoinHostPort(nodeHost(cfg), strconv.Itoa(cfg.RaftPort)),
			APIAddress:  net.JoinHostPort(nodeHost(cfg), strconv.Itoa(cfg.APIPort)),
			DNS: dnsSection{
				Listen: listenSection{
					Port: cfg.DNSPort, IPv4: true, IPv6: false,
					DoHPort:      cfg.DoHPort,
					DoTPort:      cfg.DoTPort,
					DoH3Port:     cfg.DoH3Port,
					DNSCryptPort: cfg.DNSCryptPort,
				},
				TLS: func() *tlsSection {
					if cfg.Acme == nil && cfg.TLSCertFile == "" && cfg.TLSKeyFile == "" {
						return nil
					}
					t := &tlsSection{
						CertFile: cfg.TLSCertFile,
						KeyFile:  cfg.TLSKeyFile,
					}
					if cfg.Acme != nil {
						t.Acme = &acmeSection{
							Enabled:           cfg.Acme.Enabled,
							Email:             cfg.Acme.Email,
							Domains:           cfg.Acme.Domains,
							DirectoryURL:      cfg.Acme.DirectoryURL,
							HTTPChallengePort: cfg.Acme.HTTPChallengePort,
						}
					}
					return t
				}(),
			},
			DataDir: dir,
			API: func() *apiSection {
				if !cfg.APITLSEnabled {
					return nil
				}
				ts := &apiTLSSection{
					Enabled: true,
					Mode:    cfg.APITLSMode,
					HSTS:    cfg.APITLSHSTS,
				}
				if cfg.APITLSHTTPSPort > 0 {
					ts.HTTPSAddress = fmt.Sprintf("127.0.0.1:%d", cfg.APITLSHTTPSPort)
				}
				return &apiSection{TLS: ts}
			}(),
			DHCP: func() *dhcpSection {
				if cfg.DHCP == nil {
					return nil
				}
				return &dhcpSection{
					Enabled:        true,
					Kind:           cfg.DHCP.Kind,
					URL:            cfg.DHCP.URL,
					FilePath:       cfg.DHCP.FilePath,
					Username:       cfg.DHCP.Username,
					Password:       cfg.DHCP.Password,
					RefreshSeconds: cfg.DHCP.RefreshSeconds,
				}
			}(),
			Cluster: func() *clusterSection {
				if !cfg.ClusterMTLSEnabled {
					return nil
				}
				return &clusterSection{MTLS: &clusterMTLSSection{Enabled: true}}
			}(),
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

	// Append a top-level dns: section for upstream_resolvers when set.
	// The cluster node config is parsed merged — node: + top-level config.Config
	// fields inline — so upstream_resolvers lives outside node: at the root.
	if len(cfg.UpstreamResolvers) > 0 {
		type topDNS struct {
			UpstreamResolvers []string `yaml:"upstream_resolvers"`
		}
		type topLevel struct {
			DNS topDNS `yaml:"dns"`
		}
		extra, err := yaml.Marshal(topLevel{DNS: topDNS{UpstreamResolvers: cfg.UpstreamResolvers}})
		if err != nil {
			t.Fatalf("marshal dns upstream section: %v", err)
		}
		data = append(data, extra...)
	}

	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0600); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}
}

// Leader returns the node currently reporting role=leader. Polls briefly to
// handle the case where leadership has just transferred.
func (c *Cluster) Leader(t *testing.T) *ClusterNode {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
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
	t.Fatalf("no leader found within 30s")
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

// SetBinary makes every later start of a node in this cluster (RestartNode,
// AddNode) use the skoed binary at bin. Running nodes are not touched. The
// upgrade test starts a cluster on the previous release, then kills, switches
// and restarts each node in turn.
func (c *Cluster) SetBinary(bin string) {
	c.bin = bin
}

// Size returns the number of nodes that have ever been added (including killed).
func (c *Cluster) Size() int { return len(c.nodes) }

// KillNode stops a node's process without removing its data directory, so a
// later RestartNode can revive it with the same Raft identity.
//
// If the process had already exited on its own, the test fails (without
// stopping) with the exit status and the last lines the process wrote: a
// node that crashes during a test must not go unnoticed.
func (c *Cluster) KillNode(t *testing.T, i int) {
	t.Helper()
	n := c.Node(i)
	if n.killed {
		return
	}
	// ErrProcessDone: the process already exited and was reaped. The exit
	// status below tells that case apart from a kill.
	if err := n.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("kill node %s: %v", n.NodeID, err)
	}
	<-n.Node.exited
	n.killed = true
	if !killedBySIGKILL(n.cmd.ProcessState) {
		t.Errorf("node %s exited on its own before KillNode%s", n.NodeID, exitReport(n.Node))
	}
}

// killedBySIGKILL reports whether the process ended on SIGKILL, the signal
// KillNode sends, rather than on its own.
func killedBySIGKILL(ps *os.ProcessState) bool {
	ws, ok := ps.Sys().(syscall.WaitStatus)
	return ok && ws.Signaled() && ws.Signal() == syscall.SIGKILL
}

// RestartNode brings a previously-killed node back up using the same data dir
// (so it rejoins as the same Raft member) and the same env vars.
// The in-memory session store is cleared on restart, so the session token is
// refreshed here so subsequent apiDo calls on this node continue to work.
func (c *Cluster) RestartNode(t *testing.T, i int) {
	t.Helper()
	n := c.Node(i)
	if !n.killed {
		t.Fatalf("RestartNode: node %s is not killed", n.NodeID)
	}
	c.startProcess(t, n)
	c.waitNodeReady(t, n)
	n.Node.sessionToken = loginSession(t, n.Node, defaultUsername, defaultPassword)
}

// WaitConverged blocks until every alive node reports the leader's
// commit_index from GET /api/v1/cluster/status.
// It also lazily acquires session tokens for nodes that joined without one
// (e.g. via AddNodeWithToken), waiting until credentials are replicated.
func (c *Cluster) WaitConverged(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(clusterConvergeTimeout)
	for time.Now().Before(deadline) {
		// Lazily acquire session tokens for nodes that don't have one yet
		// (e.g. added via AddNodeWithToken). Use a non-fatal direct HTTP
		// request so a not-yet-started node doesn't abort the test.
		for _, n := range c.nodes {
			if !n.killed && n.Node.sessionToken == "" {
				if tok := tryLogin(n.Node, defaultUsername, defaultPassword); tok != "" {
					n.Node.sessionToken = tok
				}
			}
		}
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
// Uses APIHTTPSBase + InsecureSkipVerify when the node serves HTTPS so that
// the single-port listener's HTTP→HTTPS redirect doesn't cause a TLS cert
// failure (self-signed test certs have no IP SANs).
func (c *Cluster) nodeRole(t *testing.T, n *ClusterNode) (string, bool) {
	t.Helper()
	base := n.Node.APIBase
	client := http.DefaultClient
	if n.Node.APIHTTPSBase != "" {
		base = n.Node.APIHTTPSBase
		client = tlsAPIClient()
	}
	req, err := http.NewRequest(http.MethodGet, base+"/api/v1/cluster/status", nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("Authorization", "Bearer "+n.Node.sessionToken)
	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
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
// answering yet or returns non-200 (used during convergence polling).
// Does not call t.Fatalf — uses a raw HTTP request so connection refused
// is treated as "not yet ready" rather than a test failure.
func (c *Cluster) tryStatus(t *testing.T, n *ClusterNode) (ClusterStatus, bool) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, n.Node.APIBase+"/api/v1/cluster/status", nil)
	if err != nil {
		return ClusterStatus{}, false
	}
	req.Header.Set("Authorization", "Bearer "+n.Node.sessionToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ClusterStatus{}, false // connection refused or timeout
	}
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
		Source:  src{Type: "inline", Format: "domainlist"},
	}
	resp := via.apiDo(t, "POST", "/api/v1/blocklists", mustJSON(t, body))
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("create blocklist %s via %s: status %d: %s", id, via.NodeID, resp.StatusCode, readBody(t, resp))
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
	if n.sessionToken != "" {
		req.Header.Set("Authorization", "Bearer "+n.sessionToken)
	}
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
