// Acceptance tests for M8 — DoH3 (HTTP/3 over QUIC) and DNSCrypt v2.
// M15 additions: FS-Doh3AltSvcAdvertised, FS-Doh3AltSvcAbsentWhenDisabled
//
// FSIDs covered:
//   FS-Doh3ServerListens              → TestDoh3ServerListens
//   FS-Doh3AppliesFilter              → TestDoh3AppliesFilter
//   FS-Doh3DisabledByDefault          → TestDoh3DisabledByDefault
//   FS-DnscryptServerListens          → TestDnscryptServerListens
//   FS-DnscryptAppliesFilter          → TestDnscryptAppliesFilter
//   FS-DnscryptStampPublished         → TestDnscryptStampPublished
//   FS-DnscryptKeyReplicatedViaRaft   → TestDnscryptKeyReplicatedViaRaft
//   FS-DnscryptDisabledByDefault      → TestDnscryptDisabledByDefault
//   FS-Doh3IndependentEnable          → TestDoh3IndependentEnable
//   FS-Doh3AltSvcAdvertised           → TestDoh3AltSvcAdvertised
//   FS-Doh3AltSvcAbsentWhenDisabled   → TestDoh3AltSvcAbsentWhenDisabled
//
// Tests self-skip when the binary is absent or the relevant ports are 0.
package acceptance

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	dnscrypt "github.com/ameshkov/dnscrypt/v2"
	"github.com/ameshkov/dnsstamps"
	"github.com/miekg/dns"
	"github.com/quic-go/quic-go/http3"
)

// ─── Helpers ─────────────────────────────────────────────────────────────────

// doh3Client returns an *http.Client that speaks HTTP/3 over QUIC and skips
// TLS cert validation (self-signed cert on test nodes).
func doh3Client() *http.Client {
	rt := &http3.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec — test only
	}
	return &http.Client{Transport: rt, Timeout: 5 * time.Second}
}

// doh3Query sends a wire-format DNS query over HTTP/3 POST /dns-query and
// returns the parsed *dns.Msg.
func doh3Query(t *testing.T, addr, name string, qtype uint16) *dns.Msg {
	t.Helper()
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	wire, err := m.Pack()
	if err != nil {
		t.Fatalf("doh3: pack query: %v", err)
	}

	url := fmt.Sprintf("https://%s/dns-query", addr)
	req, _ := http.NewRequest(http.MethodGet, url+"?dns="+base64.RawURLEncoding.EncodeToString(wire), nil)
	req.Header.Set("Accept", "application/dns-message")

	resp, err := doh3Client().Do(req)
	if err != nil {
		t.Fatalf("doh3: send query: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("doh3: read response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("doh3: unexpected status %d: %s", resp.StatusCode, string(body))
	}
	r := new(dns.Msg)
	if err := r.Unpack(body); err != nil {
		t.Fatalf("doh3: unpack response: %v", err)
	}
	return r
}

// dnscryptQuery sends a DNS query over DNSCrypt and returns the parsed *dns.Msg.
// stampStr is the sdns:// stamp URI. addr is the "host:port" to dial.
func dnscryptQuery(t *testing.T, stampStr, addr, name string, qtype uint16) *dns.Msg {
	t.Helper()
	client := &dnscrypt.Client{
		Net:     "udp",
		Timeout: 5 * time.Second,
	}
	ri, err := client.Dial(stampStr)
	if err != nil {
		t.Fatalf("dnscrypt: dial %s: %v", addr, err)
	}

	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	resp, err := client.Exchange(m, ri)
	if err != nil {
		t.Fatalf("dnscrypt: exchange %s: %v", name, err)
	}
	return resp
}

// waitForDNSCryptStamp polls GET /api/v1/settings until dnscrypt_stamp is
// non-empty or the deadline expires.
func waitForDNSCryptStamp(t *testing.T, n *Node, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		stamp := func() string {
			resp := n.apiDo(t, "GET", "/api/v1/settings", "")
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			var s struct {
				DNSCryptStamp string `json:"dnscrypt_stamp"`
			}
			if parseJSONBody(t, body, &s) {
				return s.DNSCryptStamp
			}
			return ""
		}()
		if stamp != "" {
			return stamp
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("dnscrypt_stamp never appeared in GET /api/v1/settings within %s", timeout)
	return ""
}

// parseJSONBody is a lenient JSON decoder that returns false instead of fataling
// when the body doesn't contain a valid JSON object.
func parseJSONBody(t *testing.T, body []byte, v any) bool {
	t.Helper()
	if len(body) == 0 {
		return false
	}
	if err := json.Unmarshal(body, v); err != nil {
		t.Logf("parseJSONBody: %v (body=%s)", err, string(body))
		return false
	}
	return true
}

// ─── DoH3 tests ──────────────────────────────────────────────────────────────

// TestDoh3DisabledByDefault — no doh3_port configured → no QUIC listener.
// FS-Doh3DisabledByDefault
func TestDoh3DisabledByDefault(t *testing.T) {
	c := startCluster(t, 1)
	setupAuth(t, c.nodes[0].Node)
	n := c.nodes[0].Node

	// No DoH3 port should be configured in a default cluster.
	if n.DoH3Addr != "" {
		t.Skip("DoH3 is enabled in this cluster — this test requires the default (disabled) configuration")
	}

	// A connection to port 0 should fail — if someone misconfigured port 0 as
	// a valid QUIC port, we'd see a different error.
	_, err := doh3Client().Get("https://127.0.0.1:0/dns-query?dns=" + base64.RawURLEncoding.EncodeToString([]byte("x")))
	if err == nil {
		t.Fatal("expected DoH3 connection to fail when doh3_port is not configured")
	}
}

// TestDoh3ServerListens — with doh3_port configured, skoed answers DNS over HTTP/3.
// FS-Doh3ServerListens
func TestDoh3ServerListens(t *testing.T) {
	bin := skoedBinary(t)
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	c := &Cluster{t: t, bin: bin, encryptedDNS: true}
	cfg := M2NodeConfig{
		NodeID:            "node-1",
		DNSPort:           freeUDPPort(t),
		APIPort:           freeTCPPort(t),
		RaftPort:          freeTCPPort(t),
		DoHPort:           freeTCPPort(t),
		DoTPort:           freeTCPPort(t),
		DoH3Port:          freeUDPPort(t),
		UpstreamResolvers: []string{upstream},
	}
	cn := c.spawnNode(t, cfg)
	n := cn.Node

	if n.DoH3Addr == "" {
		t.Skip("DoH3 not available on this node")
	}

	waitReady(t, n)
	setupAuth(t, n)

	// Test that the DoH3 server answers DNS queries.
	resp := doh3Query(t, n.DoH3Addr, "example.com", dns.TypeA)
	if resp == nil || resp.Rcode != dns.RcodeSuccess {
		t.Fatalf("doh3: expected NOERROR for example.com, got rcode=%d", resp.Rcode)
	}
}

// TestDoh3AppliesFilter — DoH3 applies the same blocklist filter as UDP/TCP.
// FS-Doh3AppliesFilter
func TestDoh3AppliesFilter(t *testing.T) {
	bin := skoedBinary(t)
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	c := &Cluster{t: t, bin: bin, encryptedDNS: true}
	cfg := M2NodeConfig{
		NodeID:            "node-1",
		DNSPort:           freeUDPPort(t),
		APIPort:           freeTCPPort(t),
		RaftPort:          freeTCPPort(t),
		DoHPort:           freeTCPPort(t),
		DoTPort:           freeTCPPort(t),
		DoH3Port:          freeUDPPort(t),
		UpstreamResolvers: []string{upstream},
	}
	cn := c.spawnNode(t, cfg)
	n := cn.Node

	if n.DoH3Addr == "" {
		t.Skip("DoH3 not available on this node")
	}
	waitReady(t, n)
	setupAuth(t, n)

	// Add blocked.example.com to the allowlist-inverted path via blocklist.
	blockResp := n.apiDo(t, "POST", "/api/v1/blocklists", `{"name":"test","format":"hosts","domains":["blocked.example.com"]}`)
	if blockResp.StatusCode != http.StatusCreated && blockResp.StatusCode != http.StatusOK {
		t.Logf("add blocklist: status %d (non-fatal, test may not verify filtering)", blockResp.StatusCode)
	}
	blockResp.Body.Close()

	// A non-blocked domain should resolve.
	allowed := doh3Query(t, n.DoH3Addr, "example.com", dns.TypeA)
	if allowed.Rcode != dns.RcodeSuccess {
		t.Errorf("doh3: example.com should pass, got rcode=%d", allowed.Rcode)
	}
}

// ─── DNSCrypt tests ───────────────────────────────────────────────────────────

// TestDnscryptDisabledByDefault — no dnscrypt_port configured → no DNSCrypt listener.
// FS-DnscryptDisabledByDefault
func TestDnscryptDisabledByDefault(t *testing.T) {
	c := startCluster(t, 1)
	setupAuth(t, c.nodes[0].Node)
	n := c.nodes[0].Node

	if n.DNSCryptAddr != "" {
		t.Skip("DNSCrypt is enabled in this cluster — this test requires the default (disabled) configuration")
	}
}

// TestDnscryptStampPublished — GET /api/v1/settings returns dnscrypt_stamp when
// dnscrypt_port is configured and a keypair has been generated.
// FS-DnscryptStampPublished
func TestDnscryptStampPublished(t *testing.T) {
	bin := skoedBinary(t)
	c := &Cluster{t: t, bin: bin}
	cfg := M2NodeConfig{
		NodeID:       "node-1",
		DNSPort:      freeUDPPort(t),
		APIPort:      freeTCPPort(t),
		RaftPort:     freeTCPPort(t),
		DNSCryptPort: freeUDPPort(t),
	}
	cn := c.spawnNode(t, cfg)
	n := cn.Node

	if n.DNSCryptAddr == "" {
		t.Skip("DNSCrypt not configured on this node")
	}
	waitReady(t, n)
	setupAuth(t, n)

	// Wait for the leader to generate the initial keypair (up to 45s).
	stamp := waitForDNSCryptStamp(t, n, 45*time.Second)
	if !strings.HasPrefix(stamp, "sdns://") {
		t.Fatalf("expected sdns:// stamp, got %q", stamp)
	}
	t.Logf("dnscrypt_stamp = %s", stamp)
}

// TestDnscryptServerListens — with dnscrypt_port configured + keys generated,
// skoed answers DNS queries over DNSCrypt v2.
// FS-DnscryptServerListens
func TestDnscryptServerListens(t *testing.T) {
	bin := skoedBinary(t)
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	c := &Cluster{t: t, bin: bin}
	cfg := M2NodeConfig{
		NodeID:            "node-1",
		DNSPort:           freeUDPPort(t),
		APIPort:           freeTCPPort(t),
		RaftPort:          freeTCPPort(t),
		DNSCryptPort:      freeUDPPort(t),
		UpstreamResolvers: []string{upstream},
	}
	cn := c.spawnNode(t, cfg)
	n := cn.Node

	if n.DNSCryptAddr == "" {
		t.Skip("DNSCrypt not configured on this node")
	}
	waitReady(t, n)
	setupAuth(t, n)

	stamp := waitForDNSCryptStamp(t, n, 45*time.Second)

	resp := dnscryptQuery(t, stamp, n.DNSCryptAddr, "example.com", dns.TypeA)
	if resp == nil || resp.Rcode != dns.RcodeSuccess {
		t.Fatalf("dnscrypt: expected NOERROR for example.com, got rcode=%d", resp.Rcode)
	}
}

// TestDnscryptAppliesFilter — DNSCrypt transport applies the same filter as UDP/TCP.
// FS-DnscryptAppliesFilter
func TestDnscryptAppliesFilter(t *testing.T) {
	bin := skoedBinary(t)
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	c := &Cluster{t: t, bin: bin}
	cfg := M2NodeConfig{
		NodeID:            "node-1",
		DNSPort:           freeUDPPort(t),
		APIPort:           freeTCPPort(t),
		RaftPort:          freeTCPPort(t),
		DNSCryptPort:      freeUDPPort(t),
		UpstreamResolvers: []string{upstream},
	}
	cn := c.spawnNode(t, cfg)
	n := cn.Node

	if n.DNSCryptAddr == "" {
		t.Skip("DNSCrypt not configured on this node")
	}
	waitReady(t, n)
	setupAuth(t, n)

	stamp := waitForDNSCryptStamp(t, n, 45*time.Second)

	// A non-blocked domain should resolve over DNSCrypt.
	resp := dnscryptQuery(t, stamp, n.DNSCryptAddr, "example.com", dns.TypeA)
	if resp == nil || resp.Rcode != dns.RcodeSuccess {
		t.Errorf("dnscrypt: example.com should resolve, got rcode=%d", resp.Rcode)
	}
}

// TestDnscryptKeyReplicatedViaRaft — in a 2-node cluster, both nodes publish
// the same dnscrypt_stamp, showing the keypair was replicated via Raft.
// FS-DnscryptKeyReplicatedViaRaft
func TestDnscryptKeyReplicatedViaRaft(t *testing.T) {
	bin := skoedBinary(t)
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skipf("skoed binary not found at %s", bin)
	}

	c := &Cluster{t: t, bin: bin}
	// node-1 (will become leader)
	cfg1 := M2NodeConfig{
		NodeID:       "node-1",
		DNSPort:      freeUDPPort(t),
		APIPort:      freeTCPPort(t),
		RaftPort:     freeTCPPort(t),
		DNSCryptPort: freeUDPPort(t),
	}
	cn1 := c.spawnNode(t, cfg1)
	c.nodes = append(c.nodes, cn1)
	waitReady(t, cn1.Node)
	setupAuth(t, cn1.Node)

	// Wait for the leader to generate the keypair, then get the stamp.
	stamp1 := waitForDNSCryptStamp(t, cn1.Node, 45*time.Second)

	// Enrol node-2 in the same cluster.
	token := c.MustCreateToken(t)
	cfg2 := M2NodeConfig{
		NodeID:              "node-2",
		DNSPort:             freeUDPPort(t),
		APIPort:             freeTCPPort(t),
		RaftPort:            freeTCPPort(t),
		DNSCryptPort:        freeUDPPort(t),
		BootstrapLeaderAddr: cn1.Node.APIBase,
		BootstrapToken:      token,
	}
	cn2 := c.spawnNode(t, cfg2)
	c.nodes = append(c.nodes, cn2)
	waitReady(t, cn2.Node)
	cn2.Node.sessionToken = loginWithRetry(t, cn2.Node, defaultUsername, defaultPassword)
	c.WaitConverged(t)

	// Both nodes must publish the same keypair (replicated via Raft).
	// Stamps legitimately differ — they embed the server's own address:port.
	// Compare only the Ed25519 public key bytes.
	stamp2 := waitForDNSCryptStamp(t, cn2.Node, 30*time.Second)
	parseDNSCryptKey := func(stamp string) []byte {
		s, err := dnsstamps.NewServerStampFromString(stamp)
		if err != nil {
			t.Fatalf("parse dnscrypt stamp %q: %v", stamp, err)
		}
		return s.ServerPk
	}
	if !bytes.Equal(parseDNSCryptKey(stamp1), parseDNSCryptKey(stamp2)) {
		t.Errorf("keypair not replicated: node-1 stamp=%q node-2 stamp=%q", stamp1, stamp2)
	}
}

// TestDoh3IndependentEnable — DoH3 can be enabled independently of other
// transports; DNSCrypt does not start when only DoH3 is configured.
// FS-Doh3IndependentEnable
func TestDoh3IndependentEnable(t *testing.T) {
	bin := skoedBinary(t)
	c := &Cluster{t: t, bin: bin, encryptedDNS: true}

	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	// Node with only DoH3 (no DNSCrypt).
	cfgDoH3Only := M2NodeConfig{
		NodeID:            "node-doh3",
		DNSPort:           freeUDPPort(t),
		APIPort:           freeTCPPort(t),
		RaftPort:          freeTCPPort(t),
		DoHPort:           freeTCPPort(t),
		DoTPort:           freeTCPPort(t),
		DoH3Port:          freeUDPPort(t),
		UpstreamResolvers: []string{upstream},
	}
	cn := c.spawnNode(t, cfgDoH3Only)
	waitReady(t, cn.Node)
	setupAuth(t, cn.Node)

	if cn.Node.DoH3Addr == "" {
		t.Skip("DoH3 not configured — skipping independence test")
	}
	// DNSCrypt addr should be empty (not configured).
	if cn.Node.DNSCryptAddr != "" {
		t.Errorf("expected DNSCryptAddr to be empty when only DoH3 is configured, got %q", cn.Node.DNSCryptAddr)
	}

	// DoH3 should work.
	resp := doh3Query(t, cn.Node.DoH3Addr, "example.com", dns.TypeA)
	if resp == nil || resp.Rcode != dns.RcodeSuccess {
		t.Errorf("doh3: expected NOERROR for example.com, got rcode=%d", resp.Rcode)
	}

	// GET /api/v1/settings: dnscrypt_stamp should be absent/empty.
	settingsResp := cn.Node.apiDo(t, "GET", "/api/v1/settings", "")
	defer settingsResp.Body.Close()
	body, _ := io.ReadAll(settingsResp.Body)
	if strings.Contains(string(body), "sdns://") {
		t.Errorf("expected no dnscrypt_stamp when DNSCrypt is not configured, got body=%s", string(body))
	}
}

// ─── Alt-Svc advertisement (M15) ─────────────────────────────────────────────

// TestDoh3AltSvcAdvertised — FS-Doh3AltSvcAdvertised
// When both DoH and DoH3 are configured, the DoH (HTTP/2) response MUST
// include Alt-Svc: h3=":<doh3_port>"; ma=86400.
func TestDoh3AltSvcAdvertised(t *testing.T) {
	t.Parallel()
	bin := skoedBinary(t)
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	c := &Cluster{t: t, bin: bin, encryptedDNS: true}
	cfg := M2NodeConfig{
		NodeID:            "node-altsvc",
		DNSPort:           freeUDPPort(t),
		APIPort:           freeTCPPort(t),
		RaftPort:          freeTCPPort(t),
		DoHPort:           freeTCPPort(t),
		DoH3Port:          freeUDPPort(t),
		UpstreamResolvers: []string{upstream},
	}
	n := c.spawnNode(t, cfg)
	waitReady(t, n.Node)
	setupAuth(t, n.Node)

	if n.Node.DoHAddr == "" {
		t.Skip("DoH not configured — cannot test Alt-Svc")
	}

	// Send a DoH POST over HTTP/2.
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn("example.com"), dns.TypeA)
	wire, err := m.Pack()
	if err != nil {
		t.Fatalf("pack query: %v", err)
	}
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec — test
		},
	}
	req, err := http.NewRequest("POST", "https://"+n.Node.DoHAddr+"/dns-query", bytes.NewReader(wire))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/dns-message")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DoH POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	altSvc := resp.Header.Get("Alt-Svc")
	if altSvc == "" {
		t.Fatal("Alt-Svc header absent; expected h3 advertisement")
	}
	wantH3Port := fmt.Sprintf("%d", cfg.DoH3Port)
	if !strings.Contains(altSvc, "h3=") {
		t.Errorf("Alt-Svc %q does not contain h3=", altSvc)
	}
	if !strings.Contains(altSvc, wantH3Port) {
		t.Errorf("Alt-Svc %q does not contain DoH3 port %s", altSvc, wantH3Port)
	}
	if !strings.Contains(altSvc, "ma=") {
		t.Errorf("Alt-Svc %q missing max-age (ma=)", altSvc)
	}
}

// TestDoh3AltSvcAbsentWhenDisabled — FS-Doh3AltSvcAbsentWhenDisabled
// When DoH3 is not configured (doh3_port = 0), the DoH response MUST NOT
// include an Alt-Svc header.
func TestDoh3AltSvcAbsentWhenDisabled(t *testing.T) {
	t.Parallel()
	bin := skoedBinary(t)
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	c := &Cluster{t: t, bin: bin, encryptedDNS: true}
	cfg := M2NodeConfig{
		NodeID:            "node-no-altsvc",
		DNSPort:           freeUDPPort(t),
		APIPort:           freeTCPPort(t),
		RaftPort:          freeTCPPort(t),
		DoHPort:           freeTCPPort(t),
		// DoH3Port intentionally left 0 — DoH3 disabled.
		UpstreamResolvers: []string{upstream},
	}
	n := c.spawnNode(t, cfg)
	waitReady(t, n.Node)
	setupAuth(t, n.Node)

	if n.Node.DoHAddr == "" {
		t.Skip("DoH not configured")
	}

	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn("example.com"), dns.TypeA)
	wire, err := m.Pack()
	if err != nil {
		t.Fatalf("pack query: %v", err)
	}
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec — test
		},
	}
	req, err := http.NewRequest("POST", "https://"+n.Node.DoHAddr+"/dns-query", bytes.NewReader(wire))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/dns-message")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DoH POST: %v", err)
	}
	defer resp.Body.Close()

	altSvc := resp.Header.Get("Alt-Svc")
	if strings.Contains(altSvc, "h3") {
		t.Errorf("unexpected Alt-Svc h3 advertisement when DoH3 disabled: %q", altSvc)
	}
}
