package cluster

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// Unit tests for the mTLS bundle helpers of TS-EncryptedClusterMesh
// ("Join through the web UI"), used by FS-ClusterJoinWebUiMtls and the
// config bootstrap join.

// testBundle returns a bundle for nodeID signed by a fresh cluster CA.
func testBundle(t *testing.T, nodeID string) *MTLSBundle {
	t.Helper()
	caCert, caKey := testCA(t)
	cert, key := testLeaf(t, caCert, caKey, nodeID)
	return &MTLSBundle{CACertPEM: caCert, LeafCertPEM: cert, LeafKeyPEM: key}
}

// fastBootstrapRetries shortens the fetch retry policy for one test.
func fastBootstrapRetries(t *testing.T) {
	t.Helper()
	d, rt, rd := mtlsBootstrapDeadline, mtlsBootstrapRequestTimeout, mtlsBootstrapRetryDelay
	mtlsBootstrapDeadline, mtlsBootstrapRequestTimeout, mtlsBootstrapRetryDelay = 2*time.Second, time.Second, 10*time.Millisecond
	t.Cleanup(func() {
		mtlsBootstrapDeadline, mtlsBootstrapRequestTimeout, mtlsBootstrapRetryDelay = d, rt, rd
	})
}

func TestFetchMTLSBundleSendsTokenAndNodeID(t *testing.T) {
	want := testBundle(t, "node-b")
	var got struct {
		Token  string `json:"token"`
		NodeID string `json:"node_id"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/cluster/mtls-bootstrap" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	b, err := FetchMTLSBundle(srv.URL+"/", "tok", "node-b")
	if err != nil {
		t.Fatalf("FetchMTLSBundle: %v", err)
	}
	if got.Token != "tok" || got.NodeID != "node-b" {
		t.Fatalf("request body = %+v, want token=tok node_id=node-b", got)
	}
	if !bytes.Equal(b.CACertPEM, want.CACertPEM) || !bytes.Equal(b.LeafCertPEM, want.LeafCertPEM) || !bytes.Equal(b.LeafKeyPEM, want.LeafKeyPEM) {
		t.Fatal("returned bundle differs from the leader's answer")
	}
}

func TestFetchMTLSBundleRetriesServerErrors(t *testing.T) {
	fastBootstrapRetries(t)
	want := testBundle(t, "node-b")
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	if _, err := FetchMTLSBundle(srv.URL, "tok", "node-b"); err != nil {
		t.Fatalf("FetchMTLSBundle: %v", err)
	}
	if n := calls.Load(); n != 3 {
		t.Fatalf("calls = %d, want 3", n)
	}
}

func TestFetchMTLSBundleRejectionIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, `{"error":"invalid token"}`, http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := FetchMTLSBundle(srv.URL, "bad", "node-b")
	var rej *MTLSBootstrapRejectedError
	if !errors.As(err, &rej) || rej.StatusCode != http.StatusForbidden {
		t.Fatalf("err = %v, want *MTLSBootstrapRejectedError with 403", err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("calls = %d, want 1", n)
	}
}

func TestFetchMTLSBundleGivesUpOnUnreachableLeader(t *testing.T) {
	fastBootstrapRetries(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	if _, err := FetchMTLSBundle(url, "tok", "node-b"); err == nil {
		t.Fatal("FetchMTLSBundle succeeded against a closed server")
	}
}

func TestInstallMTLSBundleWritesFilesAndRemovesOwnCAKey(t *testing.T) {
	dataDir := t.TempDir()
	// The node runs as a single-node cluster with its own CA.
	if _, _, err := GenerateClusterCA(dataDir); err != nil {
		t.Fatal(err)
	}
	p := MtlsPaths(dataDir)
	b := testBundle(t, "node-b")

	if err := InstallMTLSBundle(dataDir, b); err != nil {
		t.Fatalf("InstallMTLSBundle: %v", err)
	}
	for _, f := range []struct {
		path string
		data []byte
		mode os.FileMode
	}{
		{p.CACertFile, b.CACertPEM, 0644},
		{p.NodeCert, b.LeafCertPEM, 0644},
		{p.NodeKey, b.LeafKeyPEM, 0600},
	} {
		data, err := os.ReadFile(f.path)
		if err != nil {
			t.Fatalf("read %s: %v", f.path, err)
		}
		if !bytes.Equal(data, f.data) {
			t.Errorf("%s does not hold the bundle's content", f.path)
		}
		fi, err := os.Stat(f.path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != f.mode {
			t.Errorf("%s mode = %o, want %o", f.path, fi.Mode().Perm(), f.mode)
		}
		if _, err := os.Stat(f.path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s.tmp left behind", f.path)
		}
	}
	if _, err := os.Stat(p.CAKeyFile); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("own CA key still present (stat err = %v)", err)
	}
	if fi, err := os.Stat(p.Dir); err != nil || fi.Mode().Perm() != 0700 {
		t.Errorf("dir mode: %v %v", fi, err)
	}
}

func TestInstallMTLSBundleWithoutCAKey(t *testing.T) {
	// The config bootstrap path: a fresh data dir with no CA key at all.
	dataDir := t.TempDir()
	if err := InstallMTLSBundle(dataDir, testBundle(t, "node-b")); err != nil {
		t.Fatalf("InstallMTLSBundle: %v", err)
	}
	if _, err := os.Stat(MtlsPaths(dataDir).NodeKey); err != nil {
		t.Fatalf("leaf key not written: %v", err)
	}
}

func TestInstallMTLSBundleRejectsMalformedBundle(t *testing.T) {
	dataDir := t.TempDir()
	caCert, caKey, err := GenerateClusterCA(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	b := testBundle(t, "node-b")
	b.LeafKeyPEM = []byte("not a key")

	if err := InstallMTLSBundle(dataDir, b); err == nil {
		t.Fatal("InstallMTLSBundle accepted a malformed leaf key")
	}
	assertOwnCAUnchanged(t, dataDir, caCert, caKey)
}

func TestFailedFetchWritesNothing(t *testing.T) {
	dataDir := t.TempDir()
	caCert, caKey, err := GenerateClusterCA(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid token", http.StatusForbidden)
	}))
	defer srv.Close()

	// The caller installs only what a successful fetch returned.
	if b, err := FetchMTLSBundle(srv.URL, "bad", "node-b"); err == nil {
		_ = InstallMTLSBundle(dataDir, b)
		t.Fatal("FetchMTLSBundle succeeded on a 403")
	}
	assertOwnCAUnchanged(t, dataDir, caCert, caKey)
}

// assertOwnCAUnchanged checks that dataDir still holds only the node's
// own CA, as GenerateClusterCA wrote it.
func assertOwnCAUnchanged(t *testing.T, dataDir string, caCert, caKey []byte) {
	t.Helper()
	p := MtlsPaths(dataDir)
	if got, _ := os.ReadFile(p.CACertFile); !bytes.Equal(got, caCert) {
		t.Error("CA cert changed")
	}
	if got, _ := os.ReadFile(p.CAKeyFile); !bytes.Equal(got, caKey) {
		t.Error("CA key changed or removed")
	}
	entries, err := os.ReadDir(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("files in %s = %v, want only ca.crt and ca.key", p.Dir, names)
	}
}
