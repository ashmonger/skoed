package cluster

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Unit tests for the certificate reload of a node that joins from the web
// UI (TS-EncryptedClusterMesh "Join through the web UI", steps 3-4;
// FS-ClusterJoinWebUiMtls): after the cluster CA and leaf replace the
// node's own on disk, the peer listener, the peer client and the Raft
// stream layer use them without a restart.

// writeClusterBundle writes a new CA and a leaf of nodeID signed by it to
// dataDir, and removes the CA key, as the web-UI join does with the bundle
// received from the leader. It returns the new CA cert and key.
func writeClusterBundle(t *testing.T, dataDir, nodeID string) (caCert, caKey []byte) {
	t.Helper()
	caCert, caKey = testCA(t)
	certPEM, keyPEM := testLeaf(t, caCert, caKey, nodeID)
	p := MtlsPaths(dataDir)
	for path, data := range map[string][]byte{p.CACertFile: caCert, p.NodeCert: certPEM, p.NodeKey: keyPEM} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(p.CAKeyFile); err != nil {
		t.Fatal(err)
	}
	return caCert, caKey
}

// testReloadCluster returns a Cluster of node "node-c" with mTLS enabled,
// whose own CA and leaf are on disk, holding the given member records.
func testReloadCluster(t *testing.T, members ...MemberUpsertPayload) (c *Cluster, caCert, caKey []byte) {
	t.Helper()
	dataDir := t.TempDir()
	caCert, caKey, err := GenerateClusterCA(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	leafCert, leafKey, err := EnsureNodeLeaf(dataDir, "node-c", caCert, caKey)
	if err != nil {
		t.Fatal(err)
	}
	c = &Cluster{
		node:         &NodeYAML{Node: NodeSection{ID: "node-c", RaftAddress: "127.0.0.1:1", DataDir: dataDir}},
		store:        testStore(t, members...),
		mtlsEnabled:  true,
		mtlsCA:       caCert,
		mtlsLeafCert: leafCert,
		mtlsLeafKey:  leafKey,
	}
	if err := c.initPeer(); err != nil {
		t.Fatal(err)
	}
	return c, caCert, caKey
}

// certPool returns a pool holding caCert.
func certPool(t *testing.T, caCert []byte) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCert) {
		t.Fatal("invalid CA PEM")
	}
	return pool
}

func TestReloadClusterCertsSwitchesPeerServer(t *testing.T) {
	c, oldCA, oldKey := testReloadCluster(t)
	cfg, err := c.PeerServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	url := "https://" + ln.Addr().String() + "/"

	// get sends a request with a client leaf signed by clientCA, trusting
	// serverCA for the server leaf of node-c.
	get := func(clientCA, clientKey, serverCA []byte) error {
		certPEM, keyPEM := testLeaf(t, clientCA, clientKey, "node-x")
		leaf, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			t.Fatal(err)
		}
		client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
			DisableKeepAlives: true,
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{leaf},
				RootCAs:      certPool(t, serverCA),
				ServerName:   "node-c",
			},
		}}
		resp, err := client.Get(url)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", resp.StatusCode)
		}
		return nil
	}

	if err := get(oldCA, oldKey, oldCA); err != nil {
		t.Fatalf("before reload, client of the node's own CA: %v", err)
	}

	newCA, newKey := writeClusterBundle(t, c.node.Node.DataDir, "node-c")
	if err := c.ReloadClusterCerts(); err != nil {
		t.Fatalf("ReloadClusterCerts: %v", err)
	}

	// A success also shows that the server presents a leaf of the new CA.
	if err := get(newCA, newKey, newCA); err != nil {
		t.Errorf("after reload, client leaf of the new CA rejected: %v", err)
	}
	if err := get(oldCA, oldKey, newCA); err == nil {
		t.Error("after reload, client leaf of the old CA accepted, want a TLS error")
	}
	if ca, _, _ := c.MTLSBundle(); string(ca) != string(newCA) {
		t.Error("MTLSBundle still returns the old CA after reload")
	}
}

func TestReloadClusterCertsSwitchesPeerClient(t *testing.T) {
	// The peer server of node-b switches between a leaf of the old CA and
	// a leaf of the new CA, and accepts a request only when the client
	// leaf was verified against the same CA on the connection used.
	var srvCfg atomic.Pointer[tls.Config]
	var wantPool atomic.Pointer[x509.CertPool]
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := r.TLS.PeerCertificates[0].Verify(x509.VerifyOptions{
			Roots:     wantPool.Load(),
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
		})
		if err != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	srv.TLS = &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
		return srvCfg.Load(), nil
	}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	serveAs := func(caCert, caKey []byte) {
		certPEM, keyPEM := testLeaf(t, caCert, caKey, "node-b")
		leaf, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			t.Fatal(err)
		}
		pool := certPool(t, caCert)
		srvCfg.Store(&tls.Config{
			Certificates: []tls.Certificate{leaf},
			ClientAuth:   tls.RequireAndVerifyClientCert,
			ClientCAs:    pool,
		})
		wantPool.Store(pool)
	}

	addr := strings.TrimPrefix(srv.URL, "https://")
	c, oldCA, oldKey := testReloadCluster(t,
		MemberUpsertPayload{NodeID: "node-b", RaftAddress: "127.0.0.1:7002", PeerAPIAddress: addr},
	)
	get := func() (int, error) {
		resp, err := c.PeerHTTPClient().Get(srv.URL + "/")
		if err != nil {
			return 0, err
		}
		resp.Body.Close()
		return resp.StatusCode, nil
	}

	serveAs(oldCA, oldKey)
	if code, err := get(); err != nil || code != http.StatusNoContent {
		t.Fatalf("before reload: status %d, err %v; want 204", code, err)
	}

	newCA, newKey := writeClusterBundle(t, c.node.Node.DataDir, "node-c")
	if err := c.ReloadClusterCerts(); err != nil {
		t.Fatalf("ReloadClusterCerts: %v", err)
	}

	// The transport built for the old CA (and its idle connection) must
	// not be used any more: a server leaf of the old CA is not trusted.
	if code, err := get(); err == nil {
		t.Errorf("after reload, server leaf of the old CA trusted (status %d), want a TLS error", code)
	}
	serveAs(newCA, newKey)
	if code, err := get(); err != nil || code != http.StatusNoContent {
		t.Errorf("after reload, server of the new CA: status %d, err %v; want 204 (new leaf presented, new CA trusted)", code, err)
	}
}

func TestReloadClusterCertsRefusesLeafOfAnotherCA(t *testing.T) {
	c, oldCA, _ := testReloadCluster(t)
	p := MtlsPaths(c.node.Node.DataDir)
	otherCA, _ := testCA(t)
	if err := os.WriteFile(p.CACertFile, otherCA, 0600); err != nil {
		t.Fatal(err)
	}
	err := c.ReloadClusterCerts()
	if err == nil {
		t.Fatal("ReloadClusterCerts accepted a leaf not signed by the CA on disk")
	}
	if ca, _, _ := c.MTLSBundle(); string(ca) != string(oldCA) {
		t.Error("CA changed after a failed reload")
	}
	if !strings.Contains(err.Error(), "not signed by the cluster CA") {
		t.Errorf("error = %v, want a CA mismatch", err)
	}
}

// dialRaft opens a TLS connection to the Raft port with a client leaf of
// clientCA, trusting serverCA, and reports whether the server accepted the
// client leaf: a Raft server waits for an RPC after the handshake, so a
// read that times out means accepted, a TLS alert means rejected.
func dialRaft(t *testing.T, addr string, clientCA, clientKey, serverCA []byte) error {
	t.Helper()
	certPEM, keyPEM := testLeaf(t, clientCA, clientKey, "node-2")
	leaf, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	d := &net.Dialer{Timeout: 2 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", addr, &tls.Config{
		Certificates: []tls.Certificate{leaf},
		RootCAs:      certPool(t, serverCA),
		ServerName:   "node-1",
	})
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, err = conn.Read(make([]byte, 1))
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return nil
	}
	return err
}
