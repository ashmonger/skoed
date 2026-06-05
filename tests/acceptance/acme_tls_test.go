// Acceptance tests for M4 ACME / Let's Encrypt integration.
//
// FSIDs covered:
//   FS-AcmeEnabledFromConfig         → TestAcmeEnabledFromConfigBoots
//   FS-AcmeCustomDirectory           → covered by TestAcmeEnabledFromConfigBoots
//                                       (the test points at a deliberately
//                                       unreachable directory URL and asserts
//                                       startup still succeeds)
//   FS-AcmeChallengeListener         → TestAcmeChallengeListener
//   FS-AcmeCacheReuse                → TestAcmeCacheDirectoryCreated
//   FS-AcmeFallsBackOnFailure        → TestAcmeFallsBackOnUnreachableDirectory
//   FS-AcmeDisabledByDefault         → covered by TestDohSelfSignedCertOnFirstBoot
//
// Strategy: these tests verify the operator-visible **wiring** — config
// is honoured, ports listen, cache directories appear, fallback works.
// They do NOT exercise the live ACME issuance flow — that requires a
// real CA (or Pebble running as a sidecar). The demo recipe in
// DEMO_NOTE_M4-acme.md covers the live flow against Pebble.
//
// Every test self-skips when the harness can't yet spawn an ACME node
// (M2NodeConfig sentinel).

package acceptance

import (
	"crypto/tls"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const acmeTestTimeout = 5 * time.Second

func requireAcmeHarness(t *testing.T, n *Node) {
	t.Helper()
	if n.DoHAddr == "" {
		t.Skipf("M4 ACME impl pending: harness does not yet start nodes with DoH+ACME")
	}
}

// FS-AcmeEnabledFromConfig (wiring half) + FS-AcmeCustomDirectory.
//
// Boots a node with acme.enabled=true pointing at a custom directory
// URL. We don't expect ACME to succeed against the unreachable URL;
// what we DO expect is that the node still binds DoH (fallback) and
// the ACME config didn't crash startup.
func TestAcmeEnabledFromConfigBoots(t *testing.T) {
	c := startClusterAcme(t, AcmeOpts{
		Enabled:      true,
		Email:        "ops@example.test",
		Domains:      []string{"dns.example.test"},
		DirectoryURL: "http://127.0.0.1:1/intentionally-unreachable",
	})
	n := c.Leader(t).Node
	requireAcmeHarness(t, n)

	// DoH must still respond — autocert manager falls back to the
	// self-signed cert when ACME can't issue.
	conn, err := tls.Dial("tcp", n.DoHAddr, &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec — test code
		ServerName:         "dns.example.test",
	})
	if err != nil {
		t.Fatalf("DoH dial: %v", err)
	}
	defer conn.Close()
	if len(conn.ConnectionState().PeerCertificates) == 0 {
		t.Fatalf("no peer cert served")
	}
}

// FS-AcmeChallengeListener
func TestAcmeChallengeListener(t *testing.T) {
	c := startClusterAcme(t, AcmeOpts{
		Enabled:           true,
		Email:             "ops@example.test",
		Domains:           []string{"dns.example.test"},
		DirectoryURL:      "http://127.0.0.1:1/intentionally-unreachable",
		HTTPChallengePort: 0, // harness picks a free port; reads back via Node.AcmeHTTPAddr
	})
	n := c.Leader(t).Node
	requireAcmeHarness(t, n)
	if n.AcmeHTTPAddr == "" {
		t.Skipf("M4 ACME impl pending: harness does not yet expose AcmeHTTPAddr")
	}

	// Non-challenge path → 404 (autocert only handles /.well-known/acme-challenge/*).
	resp, err := http.Get("http://" + n.AcmeHTTPAddr + "/some-other-path")
	if err != nil {
		t.Fatalf("http get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("non-challenge path: want 404, got %d", resp.StatusCode)
	}
}

// FS-AcmeCacheReuse (light): verify the cache directory exists at the
// documented path. Reuse-across-restarts is covered manually by the
// demo recipe (re-running with the same data_dir).
func TestAcmeCacheDirectoryCreated(t *testing.T) {
	c := startClusterAcme(t, AcmeOpts{
		Enabled:      true,
		Email:        "ops@example.test",
		Domains:      []string{"dns.example.test"},
		DirectoryURL: "http://127.0.0.1:1/intentionally-unreachable",
	})
	cn := c.Leader(t)
	requireAcmeHarness(t, cn.Node)

	cachePath := filepath.Join(cn.DataDir, "tls", "acme-cache")
	deadline := time.Now().Add(acmeTestTimeout)
	for time.Now().Before(deadline) {
		if fi, err := os.Stat(cachePath); err == nil && fi.IsDir() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("acme cache dir not created at %s", cachePath)
}

// FS-AcmeFallsBackOnFailure (the explicit version — same shape as the
// boot test but documents the fallback intent loudly).
func TestAcmeFallsBackOnUnreachableDirectory(t *testing.T) {
	c := startClusterAcme(t, AcmeOpts{
		Enabled:      true,
		Email:        "ops@example.test",
		Domains:      []string{"dns.example.test"},
		DirectoryURL: "http://127.0.0.1:1/unreachable",
	})
	n := c.Leader(t).Node
	requireAcmeHarness(t, n)

	conn, err := tls.Dial("tcp", n.DoHAddr, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec
	if err != nil {
		t.Fatalf("DoH dial after unreachable ACME: %v", err)
	}
	defer conn.Close()
	if len(conn.ConnectionState().PeerCertificates) == 0 {
		t.Fatalf("no cert served on fallback")
	}
}
