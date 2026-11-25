package cluster

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// Unit tests for the peer HTTP rules of TS-EncryptedClusterMesh ("Internal
// API", "Tests") that black-box tests cannot reach without the cluster
// secret. FS-MtlsInternalApiHTTPS.

// testCA returns a fresh cluster CA (cert, key PEM).
func testCA(t *testing.T) (caCert, caKey []byte) {
	t.Helper()
	caCert, caKey, err := GenerateClusterCA(t.TempDir())
	if err != nil {
		t.Fatalf("GenerateClusterCA: %v", err)
	}
	return caCert, caKey
}

// testLeaf returns a leaf of nodeID signed by the CA.
func testLeaf(t *testing.T, caCert, caKey []byte, nodeID string) (cert, key []byte) {
	t.Helper()
	cert, key, err := IssueLeafCert(caCert, caKey, nodeID, nil)
	if err != nil {
		t.Fatalf("IssueLeafCert(%s): %v", nodeID, err)
	}
	return cert, key
}

// testPeerServer starts an HTTPS server that presents nodeID's leaf and
// requires a client certificate signed by the cluster CA, like the peer
// listener.
func testPeerServer(t *testing.T, caCert, caKey []byte, nodeID string, h http.Handler) *httptest.Server {
	t.Helper()
	certPEM, keyPEM := testLeaf(t, caCert, caKey, nodeID)
	leaf, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caCert)
	srv := httptest.NewUnstartedServer(h)
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{leaf},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// testStore opens a store holding the given member records.
func testStore(t *testing.T, members ...MemberUpsertPayload) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	for _, m := range members {
		raw, err := Encode(CmdMemberUpsert, m)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Apply(raw); err != nil {
			t.Fatalf("apply member %s: %v", m.NodeID, err)
		}
	}
	return s
}

// testMTLSCluster returns a Cluster with mTLS enabled for node "node-c",
// holding a leaf of node-c and the given member records, enough for the
// peer HTTP client.
func testMTLSCluster(t *testing.T, caCert, caKey []byte, members ...MemberUpsertPayload) *Cluster {
	t.Helper()
	certPEM, keyPEM := testLeaf(t, caCert, caKey, "node-c")
	cc, err := NewCertCache(caCert, certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	c := &Cluster{
		node:        &NodeYAML{Node: NodeSection{ID: "node-c", RaftAddress: "127.0.0.1:1"}},
		store:       testStore(t, members...),
		mtlsEnabled: true,
	}
	c.peer.certCache = cc
	return c
}

func TestMemberURLMTLSWithoutPeerAPIAddressUsesRaftPortPlusOne(t *testing.T) {
	m := Member{NodeID: "node-b", RaftAddress: "10.0.0.2:7000", APIAddress: "0.0.0.0:8080"}

	on := &Cluster{mtlsEnabled: true}
	if got, want := on.MemberAPIURL(m), "https://10.0.0.2:7001"; got != want {
		t.Errorf("mTLS on, no peer_api_address: MemberAPIURL = %q, want %q", got, want)
	}
	if got := on.memberURL(m, "10.0.0.2"); strings.HasPrefix(got, "http://") {
		t.Errorf("mTLS on: memberURL = %q, must never be plain HTTP", got)
	}

	withPeer := m
	withPeer.PeerAPIAddress = "0.0.0.0:9443"
	if got, want := on.MemberAPIURL(withPeer), "https://10.0.0.2:9443"; got != want {
		t.Errorf("mTLS on, recorded peer_api_address: MemberAPIURL = %q, want %q", got, want)
	}

	noPort := Member{NodeID: "node-b", RaftAddress: "10.0.0.2:65535"}
	if got := on.MemberAPIURL(noPort); got != "" {
		t.Errorf("mTLS on, raft port 65535: MemberAPIURL = %q, want \"\"", got)
	}

	off := &Cluster{mtlsEnabled: false}
	if got, want := off.MemberAPIURL(m), "http://10.0.0.2:8080"; got != want {
		t.Errorf("mTLS off: MemberAPIURL = %q, want %q", got, want)
	}
}

func TestPeerClientPinnedToNodeRejectsLeafOfAnotherNode(t *testing.T) {
	caCert, caKey := testCA(t)
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	// The address recorded for node-a is served by node-b's valid leaf.
	impostor := testPeerServer(t, caCert, caKey, "node-b", ok)
	genuine := testPeerServer(t, caCert, caKey, "node-b", ok)
	impostorAddr := strings.TrimPrefix(impostor.URL, "https://")
	realAddr := strings.TrimPrefix(genuine.URL, "https://")

	c := testMTLSCluster(t, caCert, caKey,
		MemberUpsertPayload{NodeID: "node-a", RaftAddress: "127.0.0.1:7000", PeerAPIAddress: impostorAddr},
		MemberUpsertPayload{NodeID: "node-b", RaftAddress: "127.0.0.1:7002", PeerAPIAddress: realAddr},
	)

	resp, err := c.PeerHTTPClient().Get(impostor.URL + "/")
	if err == nil {
		resp.Body.Close()
		t.Fatalf("request to node-a's address served by node-b's leaf succeeded (status %d), want a TLS error", resp.StatusCode)
	}
	var certErr x509.HostnameError
	if !errors.As(err, &certErr) {
		t.Errorf("error = %v, want an x509.HostnameError (leaf not valid for node-a)", err)
	}

	// Control: the same leaf is accepted where node-b is recorded.
	resp, err = c.PeerHTTPClient().Get(genuine.URL + "/")
	if err != nil {
		t.Fatalf("request to node-b: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("node-b status = %d, want 204", resp.StatusCode)
	}
}

func TestPeerClientDoesNotFollowRedirects(t *testing.T) {
	caCert, caKey := testCA(t)
	srv := testPeerServer(t, caCert, caKey, "node-b", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.invalid/", http.StatusFound)
	}))
	c := testMTLSCluster(t, caCert, caKey,
		MemberUpsertPayload{NodeID: "node-b", RaftAddress: "127.0.0.1:7002", PeerAPIAddress: strings.TrimPrefix(srv.URL, "https://")},
	)
	for name, client := range map[string]*http.Client{"PeerHTTPClient": c.PeerHTTPClient(), "PeerStreamHTTPClient": c.PeerStreamHTTPClient()} {
		resp, err := client.Get(srv.URL + "/")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Errorf("%s: status = %d, want 302 returned as is", name, resp.StatusCode)
		}
	}
}

func TestPeerClientRefusesPlainHTTPWithMTLS(t *testing.T) {
	var reached atomic.Bool
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Store(true)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(plain.Close)

	caCert, caKey := testCA(t)
	c := testMTLSCluster(t, caCert, caKey)
	for name, client := range map[string]*http.Client{"PeerHTTPClient": c.PeerHTTPClient(), "PeerStreamHTTPClient": c.PeerStreamHTTPClient()} {
		req, err := http.NewRequest(http.MethodPost, plain.URL+"/api/v1/cluster/_internal/aggregates", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Cluster-Secret", "the-secret")
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			t.Errorf("%s: http:// request with mTLS on succeeded (status %d), want an error", name, resp.StatusCode)
		}
	}
	if reached.Load() {
		t.Error("an http:// request reached the server with mTLS on, want none sent")
	}

	// Control: with mTLS off, the same client sends plain HTTP.
	off := &Cluster{}
	resp, err := off.PeerHTTPClient().Get(plain.URL + "/")
	if err != nil {
		t.Fatalf("mTLS off: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("mTLS off: status = %d, want 204", resp.StatusCode)
	}
}

func TestUnpinnableNodeID(t *testing.T) {
	for _, id := range []string{"localhost", "LocalHost", "localhost.", "LOCALHOST.", "127.0.0.1", "10.0.0.2", "10.0.0.2.", "::1", "fe80::1", "::ffff:10.0.0.2", "[::1]", "[127.0.0.1]", "::1%lo", "[::1%eth0]", "[::1]."} {
		if !UnpinnableNodeID(id) {
			t.Errorf("UnpinnableNodeID(%q) = false, want true", id)
		}
	}
	for _, id := range []string{"node-1", "localhost2", "my.localhost", "node-1.example.com", "10.0.0", "", "skoed1", "10.0.0.5.example", "[node-1]", "[", "[]"} {
		if UnpinnableNodeID(id) {
			t.Errorf("UnpinnableNodeID(%q) = true, want false", id)
		}
	}
}

func TestPlanSelfMemberSync(t *testing.T) {
	u := SelfMemberUpdate{NodeID: "node-a", PeerAPIAddress: "10.0.0.1:7001", Capabilities: []string{CapabilityBlocklistContentV1}}
	same := &Member{NodeID: "node-a", PeerAPIAddress: "10.0.0.1:7001", Capabilities: []string{CapabilityBlocklistContentV1}}
	old := &Member{NodeID: "node-a"}

	cases := []struct {
		name     string
		m        *Member
		isLeader bool
		want     selfMemberAction
	}{
		{"matching record, leader", same, true, selfMemberKeep},
		{"matching record, follower", same, false, selfMemberKeep},
		{"no record", nil, true, selfMemberKeep},
		{"stale record, leader", old, true, selfMemberApply},
		{"stale record, follower", old, false, selfMemberForward},
	}
	for _, tc := range cases {
		if got := planSelfMemberSync(tc.m, u, tc.isLeader); got != tc.want {
			t.Errorf("%s: planSelfMemberSync = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestApplyForwardedSelfMemberWritesOnlyWhenRecordDiffers(t *testing.T) {
	c := &Cluster{store: testStore(t,
		MemberUpsertPayload{NodeID: "node-a", RaftAddress: "10.0.0.1:7000", PeerAPIAddress: "10.0.0.1:7001", Capabilities: []string{CapabilityBlocklistContentV1}},
		MemberUpsertPayload{NodeID: "node-b", RaftAddress: "10.0.0.2:7000"},
	)}
	applied := 0
	apply := func(Member) error { applied++; return nil }

	matching := SelfMemberUpdate{NodeID: "node-a", PeerAPIAddress: "10.0.0.1:7001", Capabilities: []string{CapabilityBlocklistContentV1}}
	if err := c.applyForwardedSelfMember(matching, apply); err != nil {
		t.Fatalf("matching record: %v", err)
	}
	if applied != 0 {
		t.Errorf("matching record: %d Raft write(s), want 0", applied)
	}

	stale := SelfMemberUpdate{NodeID: "node-b", PeerAPIAddress: "10.0.0.2:7001", Capabilities: []string{CapabilityBlocklistContentV1}}
	if err := c.applyForwardedSelfMember(stale, apply); err != nil {
		t.Fatalf("stale record: %v", err)
	}
	if applied != 1 {
		t.Errorf("stale record: %d Raft write(s), want 1", applied)
	}

	unknown := SelfMemberUpdate{NodeID: "node-x", PeerAPIAddress: "10.0.0.9:7001"}
	if err := c.applyForwardedSelfMember(unknown, apply); !errors.Is(err, ErrUnknownMember) {
		t.Errorf("unknown member: err = %v, want ErrUnknownMember", err)
	}
	if applied != 1 {
		t.Errorf("unknown member: %d Raft write(s) in total, want 1", applied)
	}
}

func TestDefaultPeerAPIAddress(t *testing.T) {
	cases := map[string]string{
		"10.0.0.2:7000":  "10.0.0.2:7001",
		"[::1]:7000":     "[::1]:7001",
		"10.0.0.2:65535": "",
		"10.0.0.2":       "",
		"":               "",
	}
	for in, want := range cases {
		if got := DefaultPeerAPIAddress(in); got != want {
			t.Errorf("DefaultPeerAPIAddress(%q) = %q, want %q", in, got, want)
		}
	}
}
