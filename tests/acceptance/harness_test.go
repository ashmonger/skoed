// Package acceptance contains black-box acceptance tests for skoed.
// Tests start the skoed binary as a subprocess and interact with it
// exclusively through port 53 (DNS) and the HTTP management API.
//
// Prerequisites:
//   - Build the skoed binary: cd apps/skoed && go build -o skoed .
//   - Set SKOED_BINARY to override the default binary path.
//
// Run: go test ./... -v
package acceptance

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"gopkg.in/yaml.v3"
)

// portCounter is an atomic counter for test port allocation. It starts in the
// range 20000–29999 — well below Linux's ephemeral port range (32768–60999) —
// so only this test suite ever uses these ports. Incrementing atomically gives
// each goroutine a unique port without any TOCTOU race: no two concurrent
// goroutines receive the same number, and there is no release-then-grab window
// that a racing process or sibling test goroutine can exploit.
var portCounter atomic.Int32

func init() { portCounter.Store(20000) }

const (
	// readyTimeout was 10s (flaky under sequential load), then 60s, then 90s.
	// With t.Parallel() and -parallel 4, up to 12 skoed processes can start
	// simultaneously (4 tests × 3-node clusters). On a loaded CI runner the
	// last process can take 90–110s to bind its API port due to disk I/O
	// contention on BBolt writes. 120s is the safe upper bound; anything longer
	// would mask a real regression.
	readyTimeout      = 120 * time.Second
	readyPollInterval = 100 * time.Millisecond
	dnsQueryTimeout   = 3 * time.Second

	defaultUsername = "admin"
	defaultPassword = "testpass1!"
)

// Node represents a running skoed instance under test.
type Node struct {
	DNSAddr      string // "127.0.0.1:port" — UDP/TCP DNS listener
	APIBase      string // "http://127.0.0.1:port" — management API
	BlockPageURL string // "http://127.0.0.1:port" — block page HTTP server; "" when policy != redirect
	DoHAddr      string // "127.0.0.1:port" — DoH HTTPS listener; "" when disabled
	DoTAddr      string // "127.0.0.1:port" — DoT TLS listener; "" when disabled
	AcmeHTTPAddr string // "127.0.0.1:port" — ACME HTTP-01 challenge listener; "" when ACME disabled
	// M3.6: URL for the internal lease-snapshot debug endpoint exposed
	// when a DHCP connector is configured. "" when DHCP is disabled or
	// the binary doesn't yet implement M3.6 (tests then auto-skip).
	LeaseSnapshotURL string
	// M4.6: HTTPS base URL for the management API when api.tls.enabled.
	// In single_port mode this is the same port as APIBase but with
	// https:// scheme; in dual_port mode it's a separate port.
	APIHTTPSBase string
	// M8: DoH3 (HTTP/3 over QUIC) and DNSCrypt v2 listeners; "" when disabled.
	DoH3Addr     string // "127.0.0.1:port"
	DNSCryptAddr string // "127.0.0.1:port"
	// sessionToken is the Bearer token obtained from POST /api/v1/auth/login
	// at node startup. Used by apiDo for all authenticated calls.
	sessionToken string
	cmd          *exec.Cmd
}

// NodeConfig drives what gets written to config.yaml before starting the node.
type NodeConfig struct {
	Mode             string   // "forwarding" (default) or "recursive"
	UpstreamResolvers []string // used when Mode="forwarding"
	TrustedSubnets   []string // used when Mode="recursive"
	BlockPolicy      string   // global default: "nxdomain" (default), "null", "nodata", "redirect"
	// Auth is always set up automatically with defaultUsername / defaultPassword.

	// M26: block page settings (only used when BlockPolicy="redirect").
	BlockPageIP   string // IPv4 to return for blocked A queries; default uses loopback
	BlockPagePort int    // port for the block page HTTP server; 0 = use default (8053)

	// Env holds extra environment variables to append to the child process
	// environment. Use "KEY=value" format. When non-nil, the child receives
	// os.Environ() + Env so all current vars are still visible.
	Env []string
}

// skoedBinary returns the path to the binary under test.
func skoedBinary(t *testing.T) string {
	t.Helper()
	if b := os.Getenv("SKOED_BINARY"); b != "" {
		return b
	}
	return filepath.Join("..", "..", "apps", "skoed", "skoed")
}

// startNode starts a skoed node. The test is skipped if the binary is missing.
// Cleanup (process kill) is registered automatically via t.Cleanup.
func startNode(t *testing.T, cfg NodeConfig) *Node {
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

	// M26: assign a unique block page port when policy is redirect and no port set.
	if cfg.BlockPolicy == "redirect" && cfg.BlockPagePort == 0 {
		cfg.BlockPagePort = freeTCPPort(t)
	}

	writeConfig(t, dir, cfg, dnsPort, apiPort)

	cmd := exec.Command(bin, "--config", filepath.Join(dir, "config.yaml"))
	cmd.Dir = dir
	if len(cfg.Env) > 0 {
		cmd.Env = append(os.Environ(), cfg.Env...)
	}
	if testing.Verbose() {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start skoed: %v", err)
	}

	blockPageURL := ""
	if cfg.BlockPolicy == "redirect" {
		blockPageURL = fmt.Sprintf("http://127.0.0.1:%d", cfg.BlockPagePort)
	}

	n := &Node{
		DNSAddr:      fmt.Sprintf("127.0.0.1:%d", dnsPort),
		APIBase:      fmt.Sprintf("http://127.0.0.1:%d", apiPort),
		BlockPageURL: blockPageURL,
		cmd:          cmd,
	}

	t.Cleanup(func() {
		cmd.Process.Kill() //nolint:errcheck
		cmd.Wait()         //nolint:errcheck
	})

	waitReady(t, n)
	setupAuth(t, n) // also calls loginSession and sets n.sessionToken
	return n
}

// startFakeUpstream starts an in-process UDP DNS server whose handler you control.
// Returns the "host:port" address. Registered for cleanup via t.Cleanup.
func startFakeUpstream(t *testing.T, handler dns.HandlerFunc) string {
	t.Helper()

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen fake upstream: %v", err)
	}

	srv := &dns.Server{
		PacketConn: pc,
		Net:        "udp",
		Handler:    dns.HandlerFunc(handler),
	}

	started := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(started) }

	go srv.ActivateAndServe() //nolint:errcheck

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("fake upstream did not start in time")
	}

	t.Cleanup(func() { srv.Shutdown() }) //nolint:errcheck

	return pc.LocalAddr().String()
}

// fakeUpstreamReturnsA returns a handler that answers every A query with the given IP.
func fakeUpstreamReturnsA(ip string) dns.HandlerFunc {
	return func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		if len(r.Question) > 0 && r.Question[0].Qtype == dns.TypeA {
			m.Answer = append(m.Answer, &dns.A{
				Hdr: dns.RR_Header{
					Name:   r.Question[0].Name,
					Rrtype: dns.TypeA,
					Class:  dns.ClassINET,
					Ttl:    300,
				},
				A: net.ParseIP(ip).To4(),
			})
		}
		w.WriteMsg(m) //nolint:errcheck
	}
}

// dnsQuery sends a UDP DNS query to server and returns the response.
func dnsQuery(t *testing.T, server, name string, qtype uint16) *dns.Msg {
	t.Helper()
	c := &dns.Client{Net: "udp", Timeout: dnsQueryTimeout}
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	r, _, err := c.Exchange(m, server)
	if err != nil {
		t.Fatalf("DNS query %s %s @%s: %v", name, dns.TypeToString[qtype], server, err)
	}
	return r
}

// dnsQueryWithDO sends a DNS query with the DNSSEC OK bit set.
func dnsQueryWithDO(t *testing.T, server, name string, qtype uint16) *dns.Msg {
	t.Helper()
	c := &dns.Client{Net: "udp", Timeout: dnsQueryTimeout}
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	m.SetEdns0(4096, true) // DO bit
	r, _, err := c.Exchange(m, server)
	if err != nil {
		t.Fatalf("DNS query (DO) %s %s @%s: %v", name, dns.TypeToString[qtype], server, err)
	}
	return r
}

// apiDo sends an authenticated HTTP request using the node's session token.
// Pass body="" for requests with no body.
func (n *Node) apiDo(t *testing.T, method, path, body string) *http.Response {
	t.Helper()
	return n.apiDoBearer(t, method, path, body, n.sessionToken)
}

// apiDoAs exchanges username+password for a session token via
// POST /api/v1/auth/login, then sends the request with Bearer auth.
// Pass empty username to send without authentication.
func (n *Node) apiDoAs(t *testing.T, method, path, body, username, password string) *http.Response {
	t.Helper()
	var token string
	if username != "" {
		token = loginSessionMaybe(t, n, username, password)
	}
	return n.apiDoBearer(t, method, path, body, token)
}

// loginSession calls POST /api/v1/auth/login and returns the Bearer token.
// Fatal if the login fails.
func loginSession(t *testing.T, n *Node, username, password string) string {
	t.Helper()
	body := mustJSON(t, map[string]string{"username": username, "password": password})
	base := n.APIBase
	if n.APIHTTPSBase != "" {
		base = n.APIHTTPSBase
	}
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/auth/login", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build login request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec — test cert
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login returned %d, want 200", resp.StatusCode)
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	return payload.Token
}

// loginSessionMaybe is like loginSession but returns "" (no token) when the
// login request returns non-200 — used by apiDoAs to test auth rejection.
func loginSessionMaybe(t *testing.T, n *Node, username, password string) string {
	t.Helper()
	body := mustJSON(t, map[string]string{"username": username, "password": password})
	base := n.APIBase
	if n.APIHTTPSBase != "" {
		base = n.APIHTTPSBase
	}
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/auth/login", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build login request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec — test cert
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "" // caller sees no-auth → expects 401
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	return payload.Token
}

// loginWithRetry retries POST /api/v1/auth/login until it returns a token or
// the timeout expires. Used by cluster nodes that may be waiting for credentials
// to be replicated from the leader before login is possible.
func loginWithRetry(t *testing.T, n *Node, username, password string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if tok := loginSessionMaybe(t, n, username, password); tok != "" {
			return tok
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("could not login to %s within 10s — credentials not yet replicated?", n.APIBase)
	return ""
}

// tryLogin attempts POST /api/v1/auth/login and returns the Bearer token on
// success, or "" on any error (network error, non-200 status). Never calls
// t.Fatalf — safe to use on nodes that may not yet be accepting connections.
func tryLogin(n *Node, username, password string) string {
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		return ""
	}
	req, err := http.NewRequest(http.MethodPost, n.APIBase+"/api/v1/auth/login", bytes.NewReader(body))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "" // connection refused or other network error — node not ready
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ""
	}
	return payload.Token
}

// apiDoNoAuth sends an HTTP request without any authentication.
func (n *Node) apiDoNoAuth(t *testing.T, method, path string) *http.Response {
	t.Helper()
	return n.apiDoBearer(t, method, path, "", "")
}

// apiDoBearer sends an HTTP request with a Bearer token in the Authorization header.
// Used by M7 API token acceptance tests.
func (n *Node) apiDoBearer(t *testing.T, method, path, body, token string) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, n.APIBase+path, bodyReader)
	if err != nil {
		t.Fatalf("build request %s %s: %v", method, path, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, path, err)
	}
	return resp
}

// mustJSON encodes v as JSON or fails the test.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return string(b)
}

// readBody reads and closes the response body, returning it as a string.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return string(b)
}

// assertStatus fails the test if the response status code does not match.
func assertStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		body := readBody(t, resp)
		t.Fatalf("expected HTTP %d, got %d: %s", want, resp.StatusCode, body)
	}
}

// assertRcode fails the test if the DNS response rcode does not match.
func assertRcode(t *testing.T, msg *dns.Msg, want int) {
	t.Helper()
	if msg.Rcode != want {
		t.Fatalf("expected DNS rcode %s, got %s", dns.RcodeToString[want], dns.RcodeToString[msg.Rcode])
	}
}

// assertAnswerA fails the test if the first A record in the response does not match ip.
func assertAnswerA(t *testing.T, msg *dns.Msg, ip string) {
	t.Helper()
	for _, rr := range msg.Answer {
		if a, ok := rr.(*dns.A); ok {
			if a.A.String() == ip {
				return
			}
			t.Fatalf("expected A record %s, got %s", ip, a.A.String())
		}
	}
	t.Fatalf("no A record in response: %v", msg)
}

// assertAnswerAAAA fails if no AAAA record matching ip is present.
func assertAnswerAAAA(t *testing.T, msg *dns.Msg, ip string) {
	t.Helper()
	for _, rr := range msg.Answer {
		if aaaa, ok := rr.(*dns.AAAA); ok {
			if aaaa.AAAA.String() == ip {
				return
			}
			t.Fatalf("expected AAAA record %s, got %s", ip, aaaa.AAAA.String())
		}
	}
	t.Fatalf("no AAAA record in response: %v", msg)
}

// assertNoAnswer fails if the response contains any answer records.
func assertNoAnswer(t *testing.T, msg *dns.Msg) {
	t.Helper()
	if len(msg.Answer) > 0 {
		t.Fatalf("expected empty answer section, got: %v", msg.Answer)
	}
}

// ── Internal helpers ──────────────────────────────────────────────────────

func writeConfig(t *testing.T, dir string, cfg NodeConfig, dnsPort, apiPort int) {
	t.Helper()

	type listenConfig struct {
		Port int  `yaml:"port"`
		IPv4 bool `yaml:"ipv4"`
		IPv6 bool `yaml:"ipv6"`
	}
	type cacheConfig struct {
		Enabled    bool `yaml:"enabled"`
		MaxEntries int  `yaml:"max_entries"`
	}
	type dnsConfig struct {
		Listen            listenConfig `yaml:"listen"`
		Mode              string       `yaml:"mode"`
		UpstreamResolvers []string     `yaml:"upstream_resolvers,omitempty"`
		TrustedSubnets    []string     `yaml:"trusted_subnets,omitempty"`
		UpstreamTimeout   int          `yaml:"upstream_timeout_seconds"`
		Cache             cacheConfig  `yaml:"cache"`
	}
	type blockPageConfig struct {
		IP   string `yaml:"ip,omitempty"`
		Port int    `yaml:"port,omitempty"`
	}
	type filteringConfig struct {
		BlockPolicy string          `yaml:"block_policy"`
		BlockPage   blockPageConfig `yaml:"block_page,omitempty"`
	}
	type apiConfig struct {
		Port int `yaml:"port"`
	}
	type config struct {
		Version   int             `yaml:"version"`
		DNS       dnsConfig       `yaml:"dns"`
		Filtering filteringConfig `yaml:"filtering"`
		API       apiConfig       `yaml:"api"`
	}

	bp := blockPageConfig{
		IP:   cfg.BlockPageIP,
		Port: cfg.BlockPagePort,
	}

	c := config{
		Version: 1,
		DNS: dnsConfig{
			Listen:            listenConfig{Port: dnsPort, IPv4: true, IPv6: false},
			Mode:              cfg.Mode,
			UpstreamResolvers: cfg.UpstreamResolvers,
			TrustedSubnets:    cfg.TrustedSubnets,
			UpstreamTimeout:   3,
			Cache:             cacheConfig{Enabled: true, MaxEntries: 1000},
		},
		Filtering: filteringConfig{BlockPolicy: cfg.BlockPolicy, BlockPage: bp},
		API:       apiConfig{Port: apiPort},
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func waitReady(t *testing.T, n *Node) {
	t.Helper()
	deadline := time.Now().Add(readyTimeout)
	// Phase 1: HTTP /health responds. The API listener binds first.
	for time.Now().Before(deadline) {
		resp, err := http.Get(n.APIBase + "/api/v1/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 || resp.StatusCode == 401 {
				// 401 means auth not set up yet — API is running.
				goto dnsCheck
			}
		}
		time.Sleep(readyPollInterval)
	}
	t.Fatalf("skoed API did not become ready within %s at %s", readyTimeout, n.APIBase)

dnsCheck:
	// Phase 2: DNS listener bound. main.go binds the DNS server AFTER
	// the API listener; a test that fires a DNS query the moment
	// waitReady returns can otherwise see "connection refused" on the
	// still-unbound port. skoed binds both UDP and TCP on the same
	// DNS port, so a TCP dial proves the listener is up WITHOUT
	// sending a DNS message — no query-log pollution, no upstream
	// contact, no SafeSearch rewrite.
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", n.DNSAddr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(readyPollInterval)
	}
	t.Fatalf("skoed DNS did not become ready within %s at %s", readyTimeout, n.DNSAddr)
}

func setupAuth(t *testing.T, n *Node) {
	t.Helper()
	body := mustJSON(t, map[string]string{
		"username": defaultUsername,
		"password": defaultPassword,
	})
	// In M4.6 HTTPS modes the API listener serves TLS only. Prefer the
	// HTTPS URL when set (skipping cert verification — test cert).
	url := n.APIBase + "/api/v1/auth/setup"
	if n.APIHTTPSBase != "" {
		url = n.APIHTTPSBase + "/api/v1/auth/setup"
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("build auth setup request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Test-only client that accepts the harness's self-signed cert when
	// the request goes over HTTPS.
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec — test code
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("auth setup: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 201 && resp.StatusCode != 409 {
		t.Fatalf("auth setup returned unexpected status %d", resp.StatusCode)
	}
	// Always establish a session after setup so apiDo calls on this node work.
	n.sessionToken = loginSession(t, n, defaultUsername, defaultPassword)
}

// freeUDPPort and freeTCPPort probe for a free port the subprocess can
// bind to.
//
// **They MUST probe on the same address scope the subprocess will use**,
// otherwise: probing on 127.0.0.1:0 only checks loopback-scope free-ness,
// while skoed's DNS server binds on 0.0.0.0:port (wildcard). Under
// suite load, port N can be free on 127.0.0.1 (no TIME_WAIT on loopback)
// but held by a previous test's TIME_WAIT on 0.0.0.0 — the subprocess
// then dies with "address already in use", the harness never sees
// /health respond, and the test fails with the misleading "did not
// become ready in 60s".
//
// freeUDPPort returns a port number guaranteed to be unique within this test
// run. The port counter approach avoids the TOCTOU race of "bind-close-pass":
// the old scheme released the OS-assigned socket before skoed could claim it,
// letting any concurrent test goroutine (or OS) grab it first.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	return int(portCounter.Add(1))
}

// freeTCPPort returns a port number guaranteed to be unique within this test run.
func freeTCPPort(t *testing.T) int {
	t.Helper()
	return int(portCounter.Add(1))
}
