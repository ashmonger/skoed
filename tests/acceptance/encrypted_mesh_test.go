// Acceptance tests for M5.3 — Encrypted cluster mesh.
//
// FSIDs covered:
//   FS-MtlsDefaultOff               → TestMtlsDefaultOffPlainStillWorks
//   FS-MtlsClusterFormsWhenEnabled  → TestMtlsClusterFormsAndReplicates ← 3-node
//                                   → TestMtlsClusterFormsAcrossAddresses ← 3-node
//   FS-MtlsClusterCAGenerated       → verified inside the same 3-node test
//   FS-MtlsJoinDistributesCA        → verified inside the same 3-node test
//   FS-MtlsInternalApiHTTPS         → TestMtlsInternalApiHTTPS ← 3-node
//   FS-MtlsRejectsUntrustedPeer     → TestMtlsRejectsUntrustedPeerAcrossAddresses
//
// FS-MtlsRejectsUntrustedPeer is also exercised implicitly: a 3-node cluster
// booting with mTLS requires every Raft TLS handshake to verify.
//
// The tests named "AcrossAddresses" put each node on its own loopback
// address (startClusterMTLSAcrossAddresses). On 127.0.0.1 only, a peer
// certificate checked against the dialed address passes, because every leaf
// carries 127.0.0.1, and the cross-host failure recorded in
// decisions/20261126-MtlsServerNameByNodeId.md does not show.
//
// FS-MtlsInternalApiHTTPS was listed here as covered implicitly until M50,
// which was wrong: no peer HTTP client used TLS (`apiBaseURL` always
// returned http://), so forwarded writes and the X-Cluster-Secret header
// went over plain HTTP. TestMtlsInternalApiHTTPS checks the peer listener
// from specs/technical/encrypted-cluster-mesh.md "Internal API". Instead of
// the hourly aggregate named in the scenario, it uses a write forwarded by a
// follower to the leader: both go through the same peer HTTP client, and a
// write can be sent on demand and checked on the leader.

package acceptance

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// FS-MtlsDefaultOff
// Existing M2 cluster tests all run with mtls.enabled defaulted off.
// This test asserts the default really IS off — boot a node with no
// mtls config and confirm no cluster CA materialises on disk.
func TestMtlsDefaultOffPlainStillWorks(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t)

	// No TLS material should have been generated.
	for _, name := range []string{"ca.crt", "ca.key", "node.crt", "node.key"} {
		p := filepath.Join(n.DataDir, "tls", "cluster", name)
		if _, err := os.Stat(p); err == nil {
			t.Errorf("default-off: %s should not exist, but does", p)
		}
	}
}

// FS-MtlsClusterFormsWhenEnabled (+ FS-MtlsClusterCAGenerated,
// FS-MtlsJoinDistributesCA) — boots a 3-node cluster with mTLS on,
// verifies every node ends up with the same CA bundle and that a
// blocklist created on the leader replicates to every node.
func TestMtlsClusterFormsAndReplicates(t *testing.T) {
	t.Parallel()
	c := startClusterMTLS(t, 3)
	if c == nil {
		t.Skip("M5.3 impl pending: startClusterMTLS unavailable")
	}

	// Every node should have ca.crt + node.crt + node.key on disk.
	for _, cn := range c.nodes {
		for _, name := range []string{"ca.crt", "node.crt", "node.key"} {
			p := filepath.Join(cn.DataDir, "tls", "cluster", name)
			if _, err := os.Stat(p); err != nil {
				t.Errorf("node %s: missing %s: %v", cn.NodeID, name, err)
			}
		}
	}

	// CA cert must be IDENTICAL across all nodes.
	var firstCA []byte
	for _, cn := range c.nodes {
		ca, err := os.ReadFile(filepath.Join(cn.DataDir, "tls", "cluster", "ca.crt"))
		if err != nil {
			t.Fatalf("node %s read ca.crt: %v", cn.NodeID, err)
		}
		if firstCA == nil {
			firstCA = ca
			continue
		}
		if string(firstCA) != string(ca) {
			t.Errorf("node %s ca.crt diverges from leader's", cn.NodeID)
		}
	}

	// Replicate a blocklist; every node should see it via /api/v1/blocklists.
	leader := c.Leader(t).Node
	body := mustJSON(t, map[string]any{
		"id":      "mtls-bl",
		"name":    "mTLS cluster blocklist",
		"source":  map[string]string{"type": "inline"},
		"domains": []string{"mtls.example.com"},
	})
	resp := leader.apiDo(t, "POST", "/api/v1/blocklists", body)
	resp.Body.Close()

	deadline := time.Now().Add(5 * time.Second)
	for _, cn := range c.nodes {
		seen := false
		for time.Now().Before(deadline) {
			r := cn.Node.apiDo(t, "GET", "/api/v1/blocklists", "")
			b := readBody(t, r)
			if strings.Contains(b, "mtls-bl") {
				seen = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !seen {
			t.Errorf("node %s never saw the replicated blocklist", cn.NodeID)
		}
	}
}

// peerAPIAddr is the peer listener address of a node with mTLS on. The
// default is <raft host>:<raft port + 1> (encrypted-cluster-mesh.md
// "Internal API"); the harness reserves that port as PeerAPIPort so no other
// listener of the test run takes it.
func peerAPIAddr(n *ClusterNode) string {
	return net.JoinHostPort(n.Host, strconv.Itoa(n.PeerAPIPort))
}

// tlsRefusal matches the error a TLS client gets when the server ends the
// handshake with an alert, e.g. "remote error: tls: certificate required"
// (TLS 1.3) or "remote error: tls: bad certificate" (TLS 1.2). A refused
// connection or a plain-HTTP answer does not match.
var tlsRefusal = regexp.MustCompile(`remote error: tls: `)

// peerHTTPClient returns an HTTPS client that trusts the cluster CA of the
// node in caDir and verifies the server as serverName. When certDir is not
// empty, it presents that node's leaf (node.crt/node.key) as client
// certificate.
func peerHTTPClient(t *testing.T, caDir, certDir, serverName string) *http.Client {
	t.Helper()
	caPEM, err := os.ReadFile(filepath.Join(caDir, "tls", "cluster", "ca.crt"))
	if err != nil {
		t.Fatalf("read cluster ca.crt: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatalf("cluster ca.crt holds no PEM certificate")
	}
	cfg := &tls.Config{RootCAs: pool, ServerName: serverName, MinVersion: tls.VersionTLS12}
	if certDir != "" {
		leaf, err := tls.LoadX509KeyPair(
			filepath.Join(certDir, "tls", "cluster", "node.crt"),
			filepath.Join(certDir, "tls", "cluster", "node.key"))
		if err != nil {
			t.Fatalf("load node leaf: %v", err)
		}
		cfg.Certificates = []tls.Certificate{leaf}
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: cfg, DisableKeepAlives: true},
	}
}

// @fsid:FS-MtlsInternalApiHTTPS
// With mTLS on, the peer listener accepts only clients holding a cluster
// certificate, and a follower that cannot reach the leader's main API still
// forwards a write to the leader, so the forward goes through the peer
// listener over HTTPS with both nodes presenting their cluster certificate.
//
// The technical-spec rule "the main listener refuses X-Cluster-Secret with
// mTLS on" is not covered by this test: it is not a Then-step of this
// scenario, and the test cannot read the real cluster secret (it is stored
// in the node's bbolt database, locked by the daemon), while an invalid
// secret is already refused today.
func TestMtlsInternalApiHTTPS(t *testing.T) {
	requireIptables(t)
	t.Parallel()
	c := startClusterMTLS(t, 2)
	// The third node runs as cutUID so iptables can cut its traffic only.
	// It joins through the leader's main API, so the cut comes after.
	cut := c.AddNodeAsUser(t, cutUID)
	leader := c.Leader(t)
	if cut == leader {
		t.Fatalf("node %s, just added, is the leader", cut.NodeID)
	}
	peerURL := "https://" + peerAPIAddr(leader)

	// The peer listener must be open for the handshake checks below to
	// mean anything.
	conn, err := net.DialTimeout("tcp", peerAPIAddr(leader), 5*time.Second)
	if err != nil {
		t.Fatalf("leader %s: no peer API listener on %s: %v", leader.NodeID, peerAPIAddr(leader), err)
	}
	conn.Close()

	// (1) A TLS client without a cluster certificate is refused during the
	// handshake. With TLS 1.3, the server reports the missing certificate
	// after the client side of the handshake, so the refusal shows up on
	// the request.
	noCert := peerHTTPClient(t, leader.DataDir, "", leader.NodeID)
	if resp, err := noCert.Get(peerURL + "/api/v1/health"); err == nil {
		resp.Body.Close()
		t.Errorf("peer API answered %d to a client without a cluster certificate, want a TLS refusal", resp.StatusCode)
	} else if !tlsRefusal.MatchString(err.Error()) {
		t.Errorf("peer API without a cluster certificate: got %q, want a TLS alert from the server", err)
	}

	// (2) A client presenting a cluster leaf completes the handshake. The
	// peer listener serves the main router, and protected routes still
	// answer 401 without the cluster secret or a token.
	withCert := peerHTTPClient(t, leader.DataDir, cut.DataDir, leader.NodeID)
	resp, err := withCert.Get(peerURL + "/api/v1/health")
	if err != nil {
		t.Fatalf("peer API with a cluster certificate: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("peer API /api/v1/health: status %d, want 200", resp.StatusCode)
	}
	resp, err = withCert.Get(peerURL + "/api/v1/blocklists")
	if err != nil {
		t.Fatalf("peer API /api/v1/blocklists: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("peer API /api/v1/blocklists without credentials: status %d, want 401", resp.StatusCode)
	}

	// (3) Cut the follower from the leader's main API, then send it a
	// write. The follower forwards it to the leader; with the main API
	// unreachable, the write can only reach the leader through the peer
	// listener.
	blockUIDToPort(t, cutUID, leader.APIPort)
	body := mustJSON(t, map[string]any{
		"id":      "mtls-forwarded-bl",
		"name":    "mTLS forwarded blocklist",
		"enabled": true,
		"source":  map[string]string{"type": "inline", "format": "domainlist"},
		"domains": []string{"forwarded.mtls.example.com"},
	})
	resp = cut.apiDo(t, "POST", "/api/v1/blocklists", body)
	b := readBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create blocklist via %s (cut from the leader's main API): status %d, want 201; body: %s",
			cut.NodeID, resp.StatusCode, b)
	}
	waitForBlocklist(t, leader, "mtls-forwarded-bl", 10*time.Second)
}

// @fsid:FS-MtlsClusterFormsWhenEnabled
// The scenario on nodes with distinct addresses, as across hosts: the
// cluster forms with one leader, a write on the leader reaches both
// followers, and a follower killed and restarted rejoins and catches up on a
// write made while it was down.
func TestMtlsClusterFormsAcrossAddresses(t *testing.T) {
	t.Parallel()
	c := startClusterMTLSAcrossAddresses(t, 3)

	leader := c.Leader(t)
	st := c.Status(t, leader)
	if len(st.Nodes) != 3 {
		t.Fatalf("cluster status on %s lists %d nodes, want 3: %+v", leader.NodeID, len(st.Nodes), st.Nodes)
	}
	leaders := 0
	for _, e := range st.Nodes {
		if e.Role == "leader" {
			leaders++
		}
	}
	if leaders != 1 {
		t.Errorf("cluster status on %s lists %d leaders, want 1: %+v", leader.NodeID, leaders, st.Nodes)
	}
	// The test means something only if each node is known to the others by
	// its own address.
	hosts := map[string]string{}
	for _, cn := range c.nodes {
		if other, dup := hosts[cn.Host]; dup {
			t.Fatalf("nodes %s and %s share the address %s", other, cn.NodeID, cn.Host)
		}
		hosts[cn.Host] = cn.NodeID
		found := false
		for _, e := range st.Nodes {
			if e.NodeID == cn.NodeID {
				found = true
				if e.RaftAddress != cn.RaftAddr {
					t.Errorf("node %s: raft_address %q in cluster status, want %q", cn.NodeID, e.RaftAddress, cn.RaftAddr)
				}
			}
		}
		if !found {
			t.Errorf("node %s missing from the cluster status on %s", cn.NodeID, leader.NodeID)
		}
	}

	// A write on the leader replicates to both followers. MustCreateBlocklist
	// waits until every node reaches the leader's commit index.
	c.MustCreateBlocklist(t, leader, "mtls-across-bl", "across.mtls.example.com")
	followers := c.Followers(t)
	if len(followers) != 2 {
		t.Fatalf("%d followers, want 2", len(followers))
	}
	for _, f := range followers {
		waitForBlocklist(t, f, "mtls-across-bl", 10*time.Second)
	}

	// A follower killed and restarted rejoins and catches up on a write
	// made while it was down.
	down := -1
	for i, cn := range c.nodes {
		if cn == followers[0] {
			down = i
		}
	}
	c.KillNode(t, down)
	c.MustCreateBlocklist(t, leader, "mtls-across-bl-2", "across2.mtls.example.com")
	c.RestartNode(t, down)
	waitForBlocklist(t, c.Node(down), "mtls-across-bl-2", 30*time.Second)
	c.WaitConverged(t)
}

// @fsid:FS-MtlsRejectsUntrustedPeer
// On distinct addresses, the Raft listener of a cluster refuses a peer that
// presents a leaf signed by another cluster's CA during the TLS handshake,
// and accepts the same connection made with a leaf of its own cluster. The
// foreign leaf comes from a second cluster, which has its own CA. The dial
// comes from another loopback address, as from another host.
//
// "node-1's log records a TLS verification error" is not checked: the test
// checks the refusal the peer receives.
func TestMtlsRejectsUntrustedPeerAcrossAddresses(t *testing.T) {
	t.Parallel()
	c := startClusterMTLSAcrossAddresses(t, 1)
	foreign := startClusterMTLSAcrossAddresses(t, 1)
	target := c.Leader(t)
	stranger := foreign.Leader(t)

	caA, err := os.ReadFile(filepath.Join(target.DataDir, "tls", "cluster", "ca.crt"))
	if err != nil {
		t.Fatalf("read ca.crt of %s: %v", target.NodeID, err)
	}
	caB, err := os.ReadFile(filepath.Join(stranger.DataDir, "tls", "cluster", "ca.crt"))
	if err != nil {
		t.Fatalf("read ca.crt of the foreign %s: %v", stranger.NodeID, err)
	}
	if string(caA) == string(caB) {
		t.Fatalf("the two clusters share their CA, so the foreign leaf is trusted")
	}

	// Raft waits for the peer to speak first, so a read on an accepted
	// connection times out, while a refused one gets the server's alert.
	// With TLS 1.3, the server checks the client certificate after the
	// client side of the handshake, so the alert can come on that read.
	raftDial := func(certDir string) error {
		cfg := peerHTTPClient(t, target.DataDir, certDir, target.NodeID).Transport.(*http.Transport).TLSClientConfig
		dialer := &net.Dialer{
			Timeout:   5 * time.Second,
			LocalAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.250")},
		}
		conn, err := tls.DialWithDialer(dialer, "tcp", target.RaftAddr, cfg)
		if err != nil {
			return err
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, err = conn.Read(make([]byte, 1))
		return err
	}

	if err := raftDial(stranger.DataDir); err == nil || !tlsRefusal.MatchString(err.Error()) {
		t.Errorf("Raft on %s with a foreign leaf: got %v, want a TLS alert from the server", target.RaftAddr, err)
	}
	err = raftDial(target.DataDir)
	var nerr net.Error
	if !errors.As(err, &nerr) || !nerr.Timeout() {
		t.Errorf("Raft on %s with a leaf of the cluster: got %v, want the read to time out on an accepted connection", target.RaftAddr, err)
	}
}
