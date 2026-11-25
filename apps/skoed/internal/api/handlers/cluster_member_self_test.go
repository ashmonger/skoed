package handlers

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/skoed/skoed/internal/cluster"
)

// TS-EncryptedClusterMesh "Member records", "Tests": members/self returns
// 403 when node_id differs from the client certificate's CN on the peer
// listener, and 404 for a node without a member record.
// FS-MtlsInternalApiHTTPS.

const testSecret = "the-right-secret"

// fakeSelfMemberCluster records the updates it is asked to apply.
type fakeSelfMemberCluster struct {
	applyErr error
	applied  []cluster.SelfMemberUpdate
}

func (f *fakeSelfMemberCluster) ValidateClusterSecret(s string) bool { return s == testSecret }

func (f *fakeSelfMemberCluster) ApplyForwardedSelfMemberUpdate(u cluster.SelfMemberUpdate) error {
	f.applied = append(f.applied, u)
	return f.applyErr
}

func selfMemberHandler(f *fakeSelfMemberCluster) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveSelfMember(w, r, f, func(msg string) { writeError(w, http.StatusConflict, msg) })
	})
}

func selfMemberRequest(nodeID string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cluster/_internal/members/self",
		strings.NewReader(`{"node_id":"`+nodeID+`","peer_api_address":"10.0.0.2:7001","capabilities":["blocklist-content-v1"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Cluster-Secret", testSecret)
	return req
}

// peerTLSClient starts an HTTPS server requiring a client certificate signed
// by a fresh cluster CA, and returns it with a client presenting a leaf of
// clientNodeID.
func peerTLSClient(t *testing.T, h http.Handler, clientNodeID string) (*httptest.Server, *http.Client) {
	t.Helper()
	caCert, caKey, err := cluster.GenerateClusterCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pair := func(nodeID string) tls.Certificate {
		certPEM, keyPEM, err := cluster.IssueLeafCert(caCert, caKey, nodeID, nil)
		if err != nil {
			t.Fatal(err)
		}
		c, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caCert)

	srv := httptest.NewUnstartedServer(h)
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{pair("node-leader")},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:      pool,
		ServerName:   "node-leader",
		Certificates: []tls.Certificate{pair(clientNodeID)},
	}}}
	return srv, client
}

func postSelfMember(t *testing.T, client *http.Client, url, nodeID string) int {
	t.Helper()
	body := `{"node_id":"` + nodeID + `","peer_api_address":"10.0.0.2:7001","capabilities":["blocklist-content-v1"]}`
	req, err := http.NewRequest(http.MethodPost, url+"/api/v1/cluster/_internal/members/self", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Cluster-Secret", testSecret)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestSelfMemberRefusesNodeIDOtherThanClientCertificate(t *testing.T) {
	f := &fakeSelfMemberCluster{}
	srv, client := peerTLSClient(t, selfMemberHandler(f), "node-b")

	if got := postSelfMember(t, client, srv.URL, "node-a"); got != http.StatusForbidden {
		t.Errorf("node-b's certificate updating node-a: status = %d, want 403", got)
	}
	if len(f.applied) != 0 {
		t.Errorf("CN mismatch: %d update(s) applied, want 0", len(f.applied))
	}

	if got := postSelfMember(t, client, srv.URL, "node-b"); got != http.StatusNoContent {
		t.Errorf("node-b's certificate updating node-b: status = %d, want 204", got)
	}
	if len(f.applied) != 1 {
		t.Errorf("CN match: %d update(s) applied, want 1", len(f.applied))
	}
}

func TestSelfMemberUnknownMemberIs404(t *testing.T) {
	f := &fakeSelfMemberCluster{applyErr: cluster.ErrUnknownMember}
	rec := httptest.NewRecorder()
	selfMemberHandler(f).ServeHTTP(rec, selfMemberRequest("node-removed"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown member: status = %d, want 404", rec.Code)
	}
}

func TestSelfMemberWrongSecretIs401(t *testing.T) {
	f := &fakeSelfMemberCluster{}
	req := selfMemberRequest("node-b")
	req.Header.Set("X-Cluster-Secret", "wrong")
	rec := httptest.NewRecorder()
	selfMemberHandler(f).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || len(f.applied) != 0 {
		t.Errorf("wrong secret: status = %d, %d update(s) applied; want 401 and none", rec.Code, len(f.applied))
	}
}
