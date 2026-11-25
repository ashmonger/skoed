package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TS-EncryptedClusterMesh "Internal API", "Tests": with mTLS enabled, the
// main listener answers 401 to any request carrying X-Cluster-Secret,
// including with the right secret. FS-MtlsInternalApiHTTPS.

const testClusterSecret = "the-right-secret"

// secretChecker stands for an endpoint authenticated by the cluster secret:
// 204 with the right secret, 401 otherwise.
var secretChecker = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cluster-Secret") != testClusterSecret {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.WriteHeader(http.StatusNoContent)
})

func sendWithSecret(h http.Handler, secret string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cluster/_internal/aggregates", strings.NewReader("{}"))
	if secret != "" {
		req.Header.Set("X-Cluster-Secret", secret)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestMainListenerRejectsClusterSecretWithMTLS(t *testing.T) {
	mtlsOn := func() bool { return true }
	mainListener := rejectClusterSecretOffPeer(mtlsOn, secretChecker)

	for _, secret := range []string{testClusterSecret, "wrong"} {
		rec := sendWithSecret(mainListener, secret)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("main listener, secret %q: status = %d, want 401", secret, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "peer mTLS listener") {
			t.Errorf("main listener, secret %q: body = %q, want the peer-listener refusal (not the endpoint's own check)", secret, rec.Body.String())
		}
	}

	// A request without the secret still reaches the router.
	if rec := sendWithSecret(mainListener, ""); rec.Code != http.StatusUnauthorized || rec.Body.Len() != 0 {
		t.Errorf("main listener, no secret: status = %d body = %q, want the endpoint's own 401", rec.Code, rec.Body.String())
	}

	// The peer listener accepts the right secret.
	peer := markPeerListener(rejectClusterSecretOffPeer(mtlsOn, secretChecker))
	if rec := sendWithSecret(peer, testClusterSecret); rec.Code != http.StatusNoContent {
		t.Errorf("peer listener, right secret: status = %d, want 204", rec.Code)
	}
}

func TestMainListenerAcceptsClusterSecretWithMTLSOff(t *testing.T) {
	mainListener := rejectClusterSecretOffPeer(func() bool { return false }, secretChecker)
	if rec := sendWithSecret(mainListener, testClusterSecret); rec.Code != http.StatusNoContent {
		t.Errorf("mTLS off, right secret: status = %d, want 204", rec.Code)
	}
}
