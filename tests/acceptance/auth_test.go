// Acceptance tests for web UI authentication.
//
// FSIDs covered:
//   FS-WebUiAuthUnauthenticatedRequestRejected, FS-WebUiAuthUnauthenticatedUiRedirect,
//   FS-WebUiAuthValidCredentials, FS-WebUiAuthInvalidCredentials,
//   FS-WebUiAuthFirstRunSetup, FS-WebUiAuthPasswordChange,
//   FS-WebUiAuthDnsUnaffected

package acceptance

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// startNodeNoAuth starts a skoed node without calling setupAuth.
// Use this when the test needs to interact with the node before credentials exist.
func startNodeNoAuth(t *testing.T, cfg NodeConfig) *Node {
	t.Helper()

	bin := skoedBinary(t)
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skipf("skoed binary not found at %s (set SKOED_BINARY to override)", bin)
	}

	dir := t.TempDir()
	dnsPort := freeUDPPort(t)
	apiPort := freeTCPPort(t)

	if cfg.Mode == "" {
		cfg.Mode = "forwarding"
	}
	if cfg.BlockPolicy == "" {
		cfg.BlockPolicy = "nxdomain"
	}

	writeConfig(t, dir, cfg, dnsPort, apiPort)

	cmd := exec.Command(bin, "--config", filepath.Join(dir, "config.yaml"))
	cmd.Dir = dir
	if testing.Verbose() {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start skoed: %v", err)
	}

	n := &Node{
		DNSAddr: fmt.Sprintf("127.0.0.1:%d", dnsPort),
		APIBase: fmt.Sprintf("http://127.0.0.1:%d", apiPort),
		cmd:     cmd,
	}

	t.Cleanup(func() {
		cmd.Process.Kill() //nolint:errcheck
		cmd.Wait()         //nolint:errcheck
	})

	waitReady(t, n)
	// setupAuth is intentionally NOT called here.
	return n
}

// ── Tests ─────────────────────────────────────────────────────────────────

// FS-WebUiAuthUnauthenticatedRequestRejected
// A request to any management API endpoint without credentials returns 401
// and includes a WWW-Authenticate header.
func TestAuthUnauthenticatedRequestRejected(t *testing.T) {
	n := startNode(t, NodeConfig{})

	resp := n.apiDoNoAuth(t, "GET", "/api/v1/blocklists")
	defer resp.Body.Close()

	assertStatus(t, resp, http.StatusUnauthorized)

	if resp.Header.Get("WWW-Authenticate") == "" {
		t.Fatal("expected WWW-Authenticate header in 401 response, got none")
	}
}

// FS-WebUiAuthUnauthenticatedUiRedirect
// As of M2.6 the SPA is embedded and serves index.html on GET / without
// requiring auth — the SPA itself handles routing to the login form
// before issuing any API call. The acceptance contract becomes:
//
//   - GET / returns 200 with HTML (the SPA shell), OR 401/302 if the
//     deployment hasn't built the SPA yet (Go-only builds skip the embed).
//   - The SPA shell MUST contain a <div id="app"> root so the client-side
//     bootstrapper can mount; otherwise the auth flow is broken.
//
// API endpoints' auth behaviour is covered by sibling tests below.
func TestAuthUnauthenticatedUiRedirect(t *testing.T) {
	n := startNode(t, NodeConfig{})

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 5 * time.Second,
	}

	req, err := http.NewRequest(http.MethodGet, n.APIBase+"/", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		body := readBody(t, resp)
		if !strings.Contains(body, `id="app"`) {
			t.Fatalf(`200 response missing the SPA mount point <div id="app">: %s`, body[:min(200, len(body))])
		}
	case http.StatusUnauthorized, http.StatusFound, http.StatusNotFound:
		// Acceptable when the SPA has not been built (no embedded index.html).
	default:
		t.Fatalf("expected 200 (SPA), 401, 302 or 404 for unauthenticated GET /, got %d", resp.StatusCode)
	}
}

// FS-WebUiAuthValidCredentials
// A request with correct credentials returns 200.
func TestAuthValidCredentials(t *testing.T) {
	n := startNode(t, NodeConfig{})

	resp := n.apiDo(t, "GET", "/api/v1/blocklists", "")
	defer resp.Body.Close()

	assertStatus(t, resp, http.StatusOK)
}

// FS-WebUiAuthInvalidCredentials
// A request with a wrong password returns 401.
func TestAuthInvalidCredentials(t *testing.T) {
	n := startNode(t, NodeConfig{})

	resp := n.apiDoAs(t, "GET", "/api/v1/blocklists", "", defaultUsername, "wrongpassword")
	defer resp.Body.Close()

	assertStatus(t, resp, http.StatusUnauthorized)
}

// FS-WebUiAuthFirstRunSetup
// On a fresh node with no credentials, POST /api/v1/auth/setup (no auth required)
// creates credentials; subsequent requests require them.
func TestAuthFirstRunSetup(t *testing.T) {
	n := startNodeNoAuth(t, NodeConfig{})

	// Before setup: the setup endpoint itself must be reachable without auth.
	setupBody := mustJSON(t, map[string]string{
		"username": defaultUsername,
		"password": defaultPassword,
	})
	setupResp := n.apiDoAs(t, "POST", "/api/v1/auth/setup", setupBody, "", "")
	defer setupResp.Body.Close()

	if setupResp.StatusCode != http.StatusCreated {
		body := readBody(t, setupResp)
		t.Fatalf("expected 201 from /api/v1/auth/setup, got %d: %s", setupResp.StatusCode, body)
	}

	// After setup: a request without credentials must be rejected.
	noAuthResp := n.apiDoNoAuth(t, "GET", "/api/v1/blocklists")
	defer noAuthResp.Body.Close()
	assertStatus(t, noAuthResp, http.StatusUnauthorized)

	// After setup: a request with correct credentials must succeed.
	authResp := n.apiDo(t, "GET", "/api/v1/blocklists", "")
	defer authResp.Body.Close()
	assertStatus(t, authResp, http.StatusOK)
}

// FS-WebUiAuthPasswordChange
// PUT /api/v1/auth/password with current+new password changes the password;
// the old password is then rejected and the new password is accepted.
func TestAuthPasswordChange(t *testing.T) {
	n := startNode(t, NodeConfig{})

	newPassword := "newSecurePass99!"

	// Change the password.
	changeBody := mustJSON(t, map[string]string{
		"current_password": defaultPassword,
		"new_password":     newPassword,
	})
	changeResp := n.apiDo(t, "PUT", "/api/v1/auth/password", changeBody)
	defer changeResp.Body.Close()
	assertStatus(t, changeResp, http.StatusNoContent)

	// Old password must now return 401.
	oldPassResp := n.apiDoAs(t, "GET", "/api/v1/blocklists", "", defaultUsername, defaultPassword)
	defer oldPassResp.Body.Close()
	assertStatus(t, oldPassResp, http.StatusUnauthorized)

	// New password must return 200.
	newPassResp := n.apiDoAs(t, "GET", "/api/v1/blocklists", "", defaultUsername, newPassword)
	defer newPassResp.Body.Close()
	assertStatus(t, newPassResp, http.StatusOK)
}

// FS-WebUiAuthDnsUnaffected
// DNS queries on port 53 work regardless of auth state.
// Tested on a node that has NOT completed first-run setup.
func TestAuthDnsUnaffected(t *testing.T) {
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNodeNoAuth(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	// No credentials have been configured. DNS must still work.
	r := dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)

	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerA(t, r, "93.184.216.34")
}
