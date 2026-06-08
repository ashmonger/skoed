// Package acceptance contains black-box acceptance tests for dblock.
// Tests start the dblock binary as a subprocess and interact with it
// exclusively through port 53 (DNS) and the HTTP management API.
//
// Prerequisites:
//   - Build the dblock binary: cd apps/dblock && go build -o dblock .
//   - Set DBLOCK_BINARY to override the default binary path.
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
	"testing"
	"time"

	"github.com/miekg/dns"
	"gopkg.in/yaml.v3"
)

const (
	// readyTimeout was 10s — flaky under full-suite load. The suite
	// spawns ~50 dblock subprocesses over 7+ minutes; by mid-run the
	// kernel has thousands of TIME_WAIT sockets from prior tests and
	// /tmp has tens of MB of leftover bbolt files. Individual tests
	// boot in 1-3s, but a node started 5 minutes into the suite can
	// take 20-40s to bind its API listener. 60s is the empirical
	// upper bound — anything longer would mask a real regression.
	readyTimeout      = 60 * time.Second
	readyPollInterval = 100 * time.Millisecond
	dnsQueryTimeout   = 3 * time.Second

	defaultUsername = "admin"
	defaultPassword = "testpass1!"
)

// Node represents a running dblock instance under test.
type Node struct {
	DNSAddr      string // "127.0.0.1:port" — UDP/TCP DNS listener
	APIBase      string // "http://127.0.0.1:port" — management API
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
	cmd          *exec.Cmd
}

// NodeConfig drives what gets written to config.yaml before starting the node.
type NodeConfig struct {
	Mode             string   // "forwarding" (default) or "recursive"
	UpstreamResolvers []string // used when Mode="forwarding"
	TrustedSubnets   []string // used when Mode="recursive"
	BlockPolicy      string   // global default: "nxdomain" (default), "null", "nodata"
	// Auth is always set up automatically with defaultUsername / defaultPassword.
}

// dblockBinary returns the path to the binary under test.
func dblockBinary(t *testing.T) string {
	t.Helper()
	if b := os.Getenv("DBLOCK_BINARY"); b != "" {
		return b
	}
	return filepath.Join("..", "..", "apps", "dblock", "dblock")
}

// startNode starts a dblock node. The test is skipped if the binary is missing.
// Cleanup (process kill) is registered automatically via t.Cleanup.
func startNode(t *testing.T, cfg NodeConfig) *Node {
	t.Helper()

	bin := dblockBinary(t)
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skipf("dblock binary not found at %s (set DBLOCK_BINARY to override)", bin)
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
		t.Fatalf("start dblock: %v", err)
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

// apiDo sends an authenticated HTTP request and returns the response.
// Pass body="" for requests with no body.
func (n *Node) apiDo(t *testing.T, method, path, body string) *http.Response {
	t.Helper()
	return n.apiDoAs(t, method, path, body, defaultUsername, defaultPassword)
}

// apiDoAs sends an HTTP request with explicit credentials. When the
// node serves HTTPS (M4.6 — Node.APIHTTPSBase set), routes through
// the HTTPS URL with an InsecureSkipVerify client (test cert).
func (n *Node) apiDoAs(t *testing.T, method, path, body, username, password string) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	base := n.APIBase
	client := http.DefaultClient
	if n.APIHTTPSBase != "" {
		base = n.APIHTTPSBase
		client = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec — test code
			},
		}
	}
	req, err := http.NewRequest(method, base+path, bodyReader)
	if err != nil {
		t.Fatalf("build request %s %s: %v", method, path, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if username != "" {
		req.SetBasicAuth(username, password)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, path, err)
	}
	return resp
}

// apiDoNoAuth sends an HTTP request without any authentication.
func (n *Node) apiDoNoAuth(t *testing.T, method, path string) *http.Response {
	t.Helper()
	return n.apiDoAs(t, method, path, "", "", "")
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
	type filteringConfig struct {
		BlockPolicy string `yaml:"block_policy"`
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
		Filtering: filteringConfig{BlockPolicy: cfg.BlockPolicy},
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
	t.Fatalf("dblock API did not become ready within %s at %s", readyTimeout, n.APIBase)

dnsCheck:
	// Phase 2: DNS listener bound. main.go binds the DNS server AFTER
	// the API listener; a test that fires a DNS query the moment
	// waitReady returns can otherwise see "connection refused" on the
	// still-unbound port. dblock binds both UDP and TCP on the same
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
	t.Fatalf("dblock DNS did not become ready within %s at %s", readyTimeout, n.DNSAddr)
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
}

// freeUDPPort and freeTCPPort probe for a free port the subprocess can
// bind to.
//
// **They MUST probe on the same address scope the subprocess will use**,
// otherwise: probing on 127.0.0.1:0 only checks loopback-scope free-ness,
// while dblock's DNS server binds on 0.0.0.0:port (wildcard). Under
// suite load, port N can be free on 127.0.0.1 (no TIME_WAIT on loopback)
// but held by a previous test's TIME_WAIT on 0.0.0.0 — the subprocess
// then dies with "address already in use", the harness never sees
// /health respond, and the test fails with the misleading "did not
// become ready in 60s".
//
// Additionally, the DNS listener binds BOTH UDP **and** TCP on the same
// port (UDP and TCP have separate port namespaces in the kernel, so a
// port that's free as UDP may already be held as TCP). freeUDPPort
// probes BOTH protocols and returns a port free for both.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	for attempt := 0; attempt < 20; attempt++ {
		pc, err := net.ListenPacket("udp", "0.0.0.0:0")
		if err != nil {
			t.Fatalf("find free UDP port: %v", err)
		}
		port := pc.LocalAddr().(*net.UDPAddr).Port
		pc.Close()
		// Verify the port is also free as TCP. If not, try another.
		l, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
		if err == nil {
			l.Close()
			return port
		}
	}
	t.Fatalf("could not find a port free on both UDP and TCP after 20 attempts")
	return 0
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("find free TCP port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}
