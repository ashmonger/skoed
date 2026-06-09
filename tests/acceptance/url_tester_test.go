// Acceptance tests for M5.9.5 — URL tester (public landing page).
//
// FSIDs covered:
//   FS-UrlTesterCliSubcommand                      → re-exposes TestCliBlocklistTest (M5.9.1)
//   FS-UrlTesterPublicEndpointReturnsCountAndFormat → TestPublicTestBlocklistOK
//   FS-UrlTesterRefusesPrivateAddress              → TestPublicTestBlocklistRefusesPrivateIP
//   FS-UrlTesterRateLimited                        → TestPublicTestBlocklistRateLimit
//   FS-UrlTesterOperatorCanDisable                 → TestPublicLandingDisabledReturnsLogin
//   FS-UrlTesterPublicLandingShown                 → covered by the M5.9.5 Playwright screenshot
//   FS-UrlTesterLoginButtonLeadsToAdminUi          → covered by the M5.9.5 Playwright screenshot

package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// startNodeForPublicTester boots a single node with the M5.9.5 public
// landing toggled either ON (default) or OFF. The test harness writes
// a config.yaml that carries the `node.api.public_landing.enabled:`
// key — the merged loader in cluster/node.go picks it up.
//
// We can't use the existing startNode() helper because it writes the
// legacy M1-shape config without a `node:` section.
//
// allowPrivate=true sets SKOED_PUBLIC_TESTER_ALLOW_PRIVATE=1 in the
// daemon's environment so the SSRF guard accepts the httptest server's
// 127.0.0.1 binding for the OK-path test. Production binaries leave
// this unset.
func startNodeForPublicTester(t *testing.T, publicLandingEnabled *bool, allowPrivate bool) *Node {
	t.Helper()

	bin := skoedBinary(t)
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skipf("skoed binary not found at %s", bin)
	}

	dir := t.TempDir()
	dnsPort := freeUDPPort(t)
	apiPort := freeTCPPort(t)

	enabledLine := ""
	if publicLandingEnabled != nil {
		enabledLine = fmt.Sprintf("      enabled: %t\n", *publicLandingEnabled)
	}

	configYAML := fmt.Sprintf(`version: 1
node:
  id: testnode-public
  api_address: :%d
  data_dir: %s
  dns:
    listen:
      port: %d
      ipv4: true
      ipv6: false
  api:
    public_landing:
%s
dns:
  listen:
    port: %d
    ipv4: true
    ipv6: false
  mode: forwarding
  upstream_timeout_seconds: 3
  cache:
    enabled: true
    max_entries: 1000
filtering:
  block_policy: nxdomain
api:
  port: %d
`, apiPort, dir, dnsPort, enabledLine, dnsPort, apiPort)

	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(configYAML), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := exec.Command(bin, "--config", cfgPath)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	if allowPrivate {
		cmd.Env = append(cmd.Env, "SKOED_PUBLIC_TESTER_ALLOW_PRIVATE=1")
	}
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
	setupAuth(t, n)
	return n
}

// testBlocklistRequest is one POST body for /api/v1/_public/test-blocklist.
type testBlocklistRequest struct {
	URL    string `json:"url"`
	Format string `json:"format,omitempty"`
}

// testBlocklistResponse mirrors the handler's JSON output.
type testBlocklistResponse struct {
	OK        bool   `json:"ok"`
	Count     int    `json:"count"`
	Format    string `json:"format"`
	ElapsedMs int64  `json:"elapsed_ms"`
	Error     string `json:"error"`
}

// postPublicTest issues an unauthenticated POST to the public endpoint.
// X-Forwarded-For is set when xff != "" so the rate-limit test can
// pretend different source IPs.
func postPublicTest(t *testing.T, n *Node, body testBlocklistRequest, xff string) (*http.Response, testBlocklistResponse) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, n.APIBase+"/api/v1/_public/test-blocklist", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/v1/_public/test-blocklist: %v", err)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	var out testBlocklistResponse
	if len(bodyBytes) > 0 {
		_ = json.Unmarshal(bodyBytes, &out)
	}
	return resp, out
}

// FS-UrlTesterPublicEndpointReturnsCountAndFormat — POST a real
// hosts list to the daemon and expect a parsed count back. The test
// runs the daemon with SKOED_PUBLIC_TESTER_ALLOW_PRIVATE=1 so the
// SSRF guard accepts httptest.NewServer's 127.0.0.1 binding; production
// builds never set that env var.
func TestPublicTestBlocklistOK(t *testing.T) {
	t.Parallel()
	n := startNodeForPublicTester(t, nil, true) // default: enabled, allow-private for httptest

	hits := &atomic.Uint64{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, "0.0.0.0 ad-domain-1.example\n0.0.0.0 ad-domain-2.example\n0.0.0.0 ad-domain-3.example\n")
	}))
	t.Cleanup(srv.Close)

	resp, body := postPublicTest(t, n, testBlocklistRequest{URL: srv.URL, Format: "hosts"}, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST returned status %d (body: %+v)", resp.StatusCode, body)
	}
	if !body.OK {
		t.Fatalf("expected ok=true; got %+v", body)
	}
	if body.Count != 3 {
		t.Errorf("expected count=3, got %d", body.Count)
	}
	if body.Format != "hosts" {
		t.Errorf("expected format=hosts, got %q", body.Format)
	}
	if hits.Load() == 0 {
		t.Errorf("daemon did not fetch the test server")
	}
}

// FS-UrlTesterRefusesPrivateAddress — explicit 127.0.0.1 URL must be
// refused by the SSRF guard before any fetch is attempted.
func TestPublicTestBlocklistRefusesPrivateIP(t *testing.T) {
	t.Parallel()
	n := startNodeForPublicTester(t, nil, false) // no test bypass — production posture

	cases := []string{
		"http://127.0.0.1:99/x",
		"http://10.0.0.1/x",
		"http://192.168.1.1/x",
		"http://169.254.169.254/latest/meta-data/", // EC2 metadata
		"http://[::1]/x",
	}
	for _, target := range cases {
		t.Run(target, func(t *testing.T) {
			resp, body := postPublicTest(t, n, testBlocklistRequest{URL: target}, "")
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("expected 403, got %d (body: %+v)", resp.StatusCode, body)
			}
			if body.OK {
				t.Errorf("expected ok=false, got %+v", body)
			}
			if !strings.Contains(strings.ToLower(body.Error), "refusing") &&
				!strings.Contains(strings.ToLower(body.Error), "private") {
				t.Errorf("error should explain the refusal: %q", body.Error)
			}
		})
	}
}

// FS-UrlTesterRateLimited — issue more requests than the burst allows
// from the same source IP; at least one MUST return 429.
func TestPublicTestBlocklistRateLimit(t *testing.T) {
	t.Parallel()
	n := startNodeForPublicTester(t, nil, false)

	// Use an obviously bogus public URL — the SSRF guard accepts it
	// (resolves to a public IP), the fetch fails, but that doesn't
	// matter: we care about the limiter's response, not the parse.
	// Sending an SSRF-refused URL would never reach the rate limiter's
	// counter for "ok" requests, but since the rate limiter runs
	// BEFORE the SSRF check, an SSRF-refused URL still consumes a
	// token — which is exactly what we want for this test.

	const burst = 10
	statuses := make(map[int]int)
	for i := 0; i < burst; i++ {
		resp, _ := postPublicTest(t, n,
			testBlocklistRequest{URL: "http://127.0.0.1:1/" + fmt.Sprintf("attempt-%d", i)},
			"203.0.113.42") // fake "public" source IP via XFF
		statuses[resp.StatusCode]++
	}

	if statuses[http.StatusTooManyRequests] == 0 {
		t.Errorf("burst of %d requests from one source IP saw 0 × 429 (statuses=%v) — limiter not engaging", burst, statuses)
	}
}

// FS-UrlTesterOperatorCanDisable — with public_landing.enabled=false:
//   - POST /api/v1/_public/test-blocklist returns 404
//   - GET / redirects (or 404s) — i.e. the public landing surface is gone
func TestPublicLandingDisabledReturnsLogin(t *testing.T) {
	t.Parallel()
	off := false
	n := startNodeForPublicTester(t, &off, false)

	// Endpoint returns 404.
	resp, _ := postPublicTest(t, n, testBlocklistRequest{URL: "http://example.com/x"}, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 from disabled endpoint, got %d", resp.StatusCode)
	}

	// GET / either redirects to /login OR returns 404. The Go default
	// http.Client follows redirects; build one that doesn't so we can
	// see the redirect code itself.
	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	getResp, err := client.Get(n.APIBase + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer getResp.Body.Close()
	switch getResp.StatusCode {
	case http.StatusFound, http.StatusSeeOther, http.StatusMovedPermanently, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		loc := getResp.Header.Get("Location")
		if !strings.HasSuffix(loc, "/login") {
			t.Errorf("redirect target should be /login, got %q", loc)
		}
	case http.StatusNotFound:
		// Acceptable per the test spec ("redirects to /login OR returns 404").
	default:
		t.Errorf("GET / with public_landing disabled: want redirect or 404, got %d", getResp.StatusCode)
	}
}

