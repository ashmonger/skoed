// Acceptance tests for M24 — Encrypted DNS Upstream Resolvers.
//
// FSIDs covered:
//   FS-DotUpstreamForwards         → TestDotUpstreamForwards
//   FS-DotUpstreamCertVerified     → TestDotUpstreamCertVerified
//   FS-DotUpstreamSkipVerify       → TestDotUpstreamSkipVerify (same as Forwards — uses skip_verify)
//   FS-DohUpstreamForwards         → TestDohUpstreamForwardsPost
//   FS-DohUpstreamGetMethod        → TestDohUpstreamForwardsGet
//   FS-DohUpstreamCertVerified     → TestDohUpstreamCertVerified
//   FS-MixedUpstreamFallback       → TestMixedUpstreamFallback
//   FS-AllUpstreamsFail            → TestAllEncryptedUpstreamsFail
//   FS-UpstreamSchemeValidation    → TestUpstreamSchemeValidation
//   FS-UpstreamConfigPersisted     → TestUpstreamConfigPersisted
//   FS-UpstreamStatusApi           → TestUpstreamStatusApi

package acceptance

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// startFakeDotServer starts a TLS DNS server (DoT) on a random port.
// The server answers every A query with ip. Returns the host:port address.
// Uses a self-signed certificate, so the caller must use ?skip_verify=true.
func startFakeDotServer(t *testing.T, ip string) string {
	t.Helper()
	cert := generateTestTLSCert(t)
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}}

	l, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	if err != nil {
		t.Fatalf("startFakeDotServer: listen: %v", err)
	}

	srv := &dns.Server{
		Listener: l,
		Net:      "tcp-tls",
		Handler:  dns.HandlerFunc(fakeUpstreamReturnsA(ip)),
	}
	started := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(started) }
	go srv.ActivateAndServe() //nolint:errcheck

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("fake DoT server did not start in time")
	}
	t.Cleanup(func() { srv.Shutdown() }) //nolint:errcheck
	return l.Addr().String()
}

// startFakeDohServer starts an HTTPS DoH server (RFC 8484) on a random port.
// Answers every A query with ip. Returns the base URL (e.g. https://127.0.0.1:PORT).
// Uses httptest's self-signed cert, so caller must use ?skip_verify=true.
func startFakeDohServer(t *testing.T, ip string) string {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var wire []byte
		var err error
		if r.Method == http.MethodGet {
			encoded := r.URL.Query().Get("dns")
			wire, err = base64.RawURLEncoding.DecodeString(encoded)
		} else {
			wire, err = io.ReadAll(r.Body)
		}
		if err != nil {
			http.Error(w, "bad wire", http.StatusBadRequest)
			return
		}
		req := new(dns.Msg)
		if err := req.Unpack(wire); err != nil {
			http.Error(w, "bad dns", http.StatusBadRequest)
			return
		}
		resp := new(dns.Msg)
		resp.SetReply(req)
		if len(req.Question) > 0 && req.Question[0].Qtype == dns.TypeA {
			resp.Answer = []dns.RR{&dns.A{
				Hdr: dns.RR_Header{
					Name:   req.Question[0].Name,
					Rrtype: dns.TypeA,
					Class:  dns.ClassINET,
					Ttl:    300,
				},
				A: net.ParseIP(ip).To4(),
			}}
		}
		out, _ := resp.Pack()
		w.Header().Set("Content-Type", "application/dns-message")
		w.WriteHeader(http.StatusOK)
		w.Write(out) //nolint:errcheck
	})
	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)
	return ts.URL
}

// generateTestTLSCert produces an ephemeral self-signed ECDSA certificate
// valid for 127.0.0.1.
func generateTestTLSCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generateTestTLSCert: keygen: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "skoed-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("generateTestTLSCert: sign: %v", err)
	}
	cert, err := tls.X509KeyPair(
		pemEncodeBytes("CERTIFICATE", certDER),
		pemEncodeECKey(t, key),
	)
	if err != nil {
		t.Fatalf("generateTestTLSCert: X509KeyPair: %v", err)
	}
	return cert
}

func pemEncodeBytes(typ string, b []byte) []byte {
	return []byte("-----BEGIN " + typ + "-----\n" +
		base64.StdEncoding.EncodeToString(b) + "\n" +
		"-----END " + typ + "-----\n")
}

func pemEncodeECKey(t *testing.T, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("pemEncodeECKey: %v", err)
	}
	return pemEncodeBytes("EC PRIVATE KEY", der)
}

// ─── FS-DotUpstreamForwards / FS-DotUpstreamSkipVerify ───────────────────────

// TestDotUpstreamForwards verifies that skoed forwards queries to a DoT upstream.
// skip_verify=true is required because the test server uses a self-signed cert.
func TestDotUpstreamForwards(t *testing.T) {
	t.Parallel()
	dotAddr := startFakeDotServer(t, "10.0.0.1")
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{"tls://" + dotAddr + "?skip_verify=true"},
	})

	r := dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerA(t, r, "10.0.0.1")
}

// ─── FS-DotUpstreamCertVerified ───────────────────────────────────────────────

// TestDotUpstreamCertVerified verifies that a DoT upstream with an untrusted cert
// is skipped (no skip_verify=true) and SERVFAIL is returned when no other
// upstream succeeds.
func TestDotUpstreamCertVerified(t *testing.T) {
	t.Parallel()
	dotAddr := startFakeDotServer(t, "10.0.0.2")
	// No skip_verify — TLS verification will fail against our self-signed cert.
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{"tls://" + dotAddr},
	})

	r := dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeServerFailure)
}

// ─── FS-DohUpstreamForwards ───────────────────────────────────────────────────

// TestDohUpstreamForwardsPost verifies POST-mode DoH upstream forwarding.
func TestDohUpstreamForwardsPost(t *testing.T) {
	t.Parallel()
	dohBase := startFakeDohServer(t, "10.0.1.1")
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{dohBase + "/dns-query?skip_verify=true"},
	})

	r := dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerA(t, r, "10.0.1.1")
}

// ─── FS-DohUpstreamGetMethod ──────────────────────────────────────────────────

// TestDohUpstreamForwardsGet verifies GET-mode DoH upstream forwarding.
func TestDohUpstreamForwardsGet(t *testing.T) {
	t.Parallel()
	dohBase := startFakeDohServer(t, "10.0.1.2")
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{dohBase + "/dns-query?skip_verify=true&method=get"},
	})

	r := dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerA(t, r, "10.0.1.2")
}

// ─── FS-DohUpstreamCertVerified ───────────────────────────────────────────────

// TestDohUpstreamCertVerified verifies that a DoH upstream with an untrusted cert
// is skipped and SERVFAIL is returned when no other upstream succeeds.
func TestDohUpstreamCertVerified(t *testing.T) {
	t.Parallel()
	dohBase := startFakeDohServer(t, "10.0.1.3")
	// No skip_verify — TLS verification will fail against httptest self-signed cert.
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{dohBase + "/dns-query"},
	})

	r := dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeServerFailure)
}

// ─── FS-MixedUpstreamFallback ─────────────────────────────────────────────────

// TestMixedUpstreamFallback verifies fallback across DoT → DoH → plain when
// earlier upstreams are unreachable.
func TestMixedUpstreamFallback(t *testing.T) {
	t.Parallel()
	liveDoH := startFakeDohServer(t, "10.0.2.1")
	n := startNode(t, NodeConfig{
		Mode: "forwarding",
		UpstreamResolvers: []string{
			"tls://127.0.0.1:1?skip_verify=true",          // dead DoT
			liveDoH + "/dns-query?skip_verify=true",        // live DoH
		},
	})

	r := dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerA(t, r, "10.0.2.1")
}

// ─── FS-AllUpstreamsFail ──────────────────────────────────────────────────────

// TestAllEncryptedUpstreamsFail verifies SERVFAIL when all encrypted upstreams fail.
func TestAllEncryptedUpstreamsFail(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{
		Mode: "forwarding",
		UpstreamResolvers: []string{
			"tls://127.0.0.1:1?skip_verify=true",
			"https://127.0.0.1:2/dns-query?skip_verify=true",
		},
	})

	r := dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeServerFailure)
}

// ─── FS-UpstreamSchemeValidation ──────────────────────────────────────────────

// TestUpstreamSchemeValidation verifies that PATCH /api/v1/settings rejects
// an unsupported upstream scheme with 400.
func TestUpstreamSchemeValidation(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})

	resp := n.apiDo(t, http.MethodPatch, "/api/v1/settings", mustJSON(t, map[string]any{
		"dns": map[string]any{
			"upstream_resolvers": []string{"ftp://1.1.1.1"},
		},
	}))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("PATCH with ftp:// scheme: want 400, got %d: %s", resp.StatusCode, body)
	}
	var errResp map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err == nil {
		if !strings.Contains(errResp["error"], "scheme") && !strings.Contains(errResp["error"], "ftp") {
			t.Errorf("error message should mention scheme or ftp, got: %q", errResp["error"])
		}
	}
}

// ─── FS-UpstreamConfigPersisted ───────────────────────────────────────────────

// TestUpstreamConfigPersisted verifies that an encrypted upstream set via PATCH
// is persisted in the config store and reflected in subsequent GET calls, and
// that DNS forwarding immediately uses the new upstream.
func TestUpstreamConfigPersisted(t *testing.T) {
	t.Parallel()
	// Start with a plain upstream so the node comes up healthy.
	plain := startFakeUpstream(t, fakeUpstreamReturnsA("10.0.3.0"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{plain},
	})

	// Now switch to a DoT upstream.
	dotAddr := startFakeDotServer(t, "10.0.3.1")
	upstream := "tls://" + dotAddr + "?skip_verify=true"

	resp := n.apiDo(t, http.MethodPatch, "/api/v1/settings", mustJSON(t, map[string]any{
		"dns": map[string]any{
			"upstream_resolvers": []string{upstream},
		},
	}))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH settings: want 200, got %d", resp.StatusCode)
	}

	// GET must reflect the new value immediately (persisted in config store).
	settResp := n.apiDo(t, http.MethodGet, "/api/v1/settings", "")
	defer settResp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(settResp.Body).Decode(&body); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	dnsSettings, _ := body["dns"].(map[string]any)
	resolvers, _ := dnsSettings["upstream_resolvers"].([]any)
	if len(resolvers) == 0 || resolvers[0] != upstream {
		t.Errorf("upstream_resolvers = %v, want [%s]", resolvers, upstream)
	}

	// DNS queries must now route through the DoT upstream.
	r := dnsQuery(t, n.DNSAddr, "example.com", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerA(t, r, "10.0.3.1")
}

// ─── FS-UpstreamStatusApi ─────────────────────────────────────────────────────

// TestUpstreamStatusApi verifies that GET /api/v1/settings returns the full
// upstream URL including scheme.
func TestUpstreamStatusApi(t *testing.T) {
	t.Parallel()
	dotAddr := startFakeDotServer(t, "10.0.4.1")
	upstream := "tls://" + dotAddr + "?skip_verify=true"

	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	resp := n.apiDo(t, http.MethodGet, "/api/v1/settings", "")
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	dnsSettings, _ := body["dns"].(map[string]any)
	resolvers, _ := dnsSettings["upstream_resolvers"].([]any)
	if len(resolvers) == 0 {
		t.Fatal("upstream_resolvers is empty in GET /api/v1/settings")
	}
	got, _ := resolvers[0].(string)
	if !strings.HasPrefix(got, "tls://") {
		t.Errorf("upstream_resolvers[0] = %q, want tls:// prefix", got)
	}
}
