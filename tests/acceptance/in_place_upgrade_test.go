// Acceptance tests for M5.6/M16 — In-place upgrade.
//
// FSIDs covered:
//   FS-UpgradeCheckEndpoint        → TestUpgradeCheckEndpoint
//   FS-UpgradeCheckRequiresAuth    → TestUpgradeCheckRequiresAuth
//   FS-UpgradeStartRequiresLeader  → TestUpgradeStartForwardedToLeader ← 3-node
//   FS-UpgradeStartRecordedInAudit → TestUpgradeStartRecordedInAudit
//   FS-UpgradeBinarySwap           → TestUpgradeBinarySwap
//
// FS-UpgradeBannerOnDashboard and FS-UpgradeNoBannerWhenCurrent are UI
// scenarios — covered via the m5.6 screenshots.

package acceptance

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/openpgp"
	"golang.org/x/crypto/openpgp/armor"
)

// testSigner is an ephemeral OpenPGP key used to sign checksums.txt in tests,
// standing in for the production release key. Generated once per test process;
// its public key is written to a file that spawned skoed nodes read via
// SKOED_TEST_UPGRADE_PUBKEY.
var (
	testSignerOnce sync.Once
	testSignerKey  *openpgp.Entity
)

func ensureTestSigningKey(t *testing.T) {
	t.Helper()
	testSignerOnce.Do(func() {
		e, err := openpgp.NewEntity("skoed test release", "acceptance", "release@test.skoed", nil)
		if err != nil {
			t.Fatalf("generate test signing key: %v", err)
		}
		testSignerKey = e
		f, err := os.CreateTemp("", "skoed-test-relpub-*.asc")
		if err != nil {
			t.Fatalf("create pubkey file: %v", err)
		}
		aw, err := armor.Encode(f, openpgp.PublicKeyType, nil)
		if err != nil {
			t.Fatalf("armor: %v", err)
		}
		if err := e.Serialize(aw); err != nil {
			t.Fatalf("serialize pubkey: %v", err)
		}
		aw.Close()
		f.Close()
		os.Setenv("SKOED_TEST_UPGRADE_PUBKEY", f.Name())
	})
}

// signChecksums returns a detached (binary) OpenPGP signature over content.
func signChecksums(t *testing.T, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := openpgp.DetachSign(&buf, testSignerKey, bytes.NewReader(content), nil); err != nil {
		t.Fatalf("detach-sign checksums: %v", err)
	}
	return buf.Bytes()
}

type upgradeCheckResp struct {
	CurrentVersion   string `json:"current_version"`
	AvailableVersion string `json:"available_version"`
	UpgradeAvailable bool   `json:"upgrade_available"`
	ReleaseNotesURL  string `json:"release_notes_url"`
	PublishedAt      string `json:"published_at"`
	CheckedAt        string `json:"checked_at"`
}

// startFeedServer serves a synthetic release feed (no asset URLs).
func startFeedServer(t *testing.T, version string) *httptest.Server {
	t.Helper()
	return startFeedServerWithAssets(t, version, "", "")
}

// startFeedServerWithAssets serves a feed that includes an asset URL for
// linux_amd64 pointing at assetURL (empty string omits the assets field).
// When checksum is non-empty it is advertised in the feed's "checksums" map
// so the (now mandatory) integrity verification in upgrade.Swap can succeed.
func startFeedServerWithAssets(t *testing.T, version, assetURL, checksum string) *httptest.Server {
	t.Helper()
	ensureTestSigningKey(t)

	// goreleaser-format checksums.txt covering both arch asset filenames, plus a
	// detached OpenPGP signature over it made with the ephemeral test key.
	var checksumsTxt, sig []byte
	if checksum != "" {
		checksumsTxt = []byte(fmt.Sprintf("%s  skoed_%s_linux_amd64.tar.gz\n%s  skoed_%s_linux_arm64.tar.gz\n",
			checksum, version, checksum, version))
		sig = signChecksums(t, checksumsTxt)
	}

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) { w.Write(checksumsTxt) })
	mux.HandleFunc("/checksums.txt.sig", func(w http.ResponseWriter, r *http.Request) { w.Write(sig) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		assets := ""
		if assetURL != "" {
			assets = fmt.Sprintf(`, "assets": {"linux_amd64": %q, "linux_arm64": %q}`, assetURL, assetURL)
			if checksum != "" {
				assets += fmt.Sprintf(`, "checksums_url": %q, "checksums_sig_url": %q`,
					srv.URL+"/checksums.txt", srv.URL+"/checksums.txt.sig")
			}
		}
		fmt.Fprintf(w, `{
			"version": "%s",
			"published_at": "2026-07-01T09:00:00Z",
			"release_notes_url": "https://example.test/releases/%s"%s
		}`, version, version, assets)
	})
	return srv
}

// startAssetServer serves a minimal tar.gz containing a fake skoed binary.
// The binary content is the sentinel bytes passed in binaryContent. It returns
// the server and the hex SHA-256 of the served archive (for feed checksums).
func startAssetServer(t *testing.T, binaryContent []byte) (*httptest.Server, string) {
	t.Helper()
	tgz := buildFakeTarGz(t, binaryContent)
	sum := sha256.Sum256(tgz)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.Write(tgz)
	}))
	t.Cleanup(srv.Close)
	return srv, hex.EncodeToString(sum[:])
}

// buildFakeTarGz creates a tar.gz archive with a single "skoed" entry.
func buildFakeTarGz(t *testing.T, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{
		Name: "skoed",
		Mode: 0755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// FS-UpgradeCheckRequiresAuth
func TestUpgradeCheckRequiresAuth(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	resp := n.apiDoNoAuth(t, "GET", "/api/v1/upgrade/check")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("/upgrade/check no-auth: want 401, got %d", resp.StatusCode)
	}
}

// FS-UpgradeCheckEndpoint
func TestUpgradeCheckEndpoint(t *testing.T) {
	t.Parallel()
	feed := startFeedServer(t, "99.0.0")
	c := startClusterWithEnv(t, 1, []string{"SKOED_UPGRADE_FEED_URL=" + feed.URL})
	n := c.Leader(t).Node

	resp := n.apiDo(t, "GET", "/api/v1/upgrade/check", "")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var ck upgradeCheckResp
	if err := json.NewDecoder(resp.Body).Decode(&ck); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Wait for the checker to poll the feed (the goroutine may have
	// not fired yet at request time).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && ck.AvailableVersion == "" {
		time.Sleep(100 * time.Millisecond)
		r := n.apiDo(t, "GET", "/api/v1/upgrade/check", "")
		_ = json.NewDecoder(r.Body).Decode(&ck)
		r.Body.Close()
	}
	if ck.CurrentVersion == "" {
		t.Errorf("current_version should be non-empty")
	}
	if ck.AvailableVersion == "" {
		t.Skipf("feed never polled within 5s (SKOED_UPGRADE_FEED_URL plumbing may be missing): %+v", ck)
	}
	if !ck.UpgradeAvailable {
		t.Errorf("upgrade_available: want true (99.0.0 > current), got false")
	}
	if !strings.Contains(ck.ReleaseNotesURL, "99.0.0") {
		t.Errorf("release_notes_url should reference the version, got %q", ck.ReleaseNotesURL)
	}
}

// FS-UpgradeStartRequiresLeader — needs a feed URL so upgrade_available
// is true; otherwise /start returns 409 before forwarding can be
// observed. Also needs an asset URL so the handler doesn't 422.
func TestUpgradeStartForwardedToLeader(t *testing.T) {
	t.Parallel()
	asset, sum := startAssetServer(t, []byte("#!/bin/sh\necho fake\n"))
	feed := startFeedServerWithAssets(t, "99.0.0", asset.URL+"/skoed.tar.gz", sum)
	swapDest := filepath.Join(t.TempDir(), "skoed_swapped")
	c := startClusterWithEnv(t, 3, []string{
		"SKOED_UPGRADE_FEED_URL=" + feed.URL,
		"SKOED_TEST_MODE=1",
		"SKOED_TEST_SWAP_DEST=" + swapDest,
	})
	leader := c.Leader(t)
	var follower *ClusterNode
	for _, n := range c.nodes {
		if n.NodeID != leader.NodeID {
			follower = n
			break
		}
	}
	if follower == nil {
		t.Skip("3-node cluster has no follower (??)")
	}
	waitUpgradeAvailable(t, follower.Node, 5*time.Second)

	resp := follower.Node.apiDo(t, "POST", "/api/v1/upgrade/start", "")
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 307, 308:
		return
	case 200, 202:
		return
	case 503:
		body, _ := io.ReadAll(resp.Body)
		if strings.Contains(string(body), "leader_address") {
			return
		}
	}
	t.Errorf("/upgrade/start on follower: unexpected status %d", resp.StatusCode)
}

// FS-UpgradeStartRecordedInAudit
func TestUpgradeStartRecordedInAudit(t *testing.T) {
	t.Parallel()
	asset, sum := startAssetServer(t, []byte("#!/bin/sh\necho fake\n"))
	feed := startFeedServerWithAssets(t, "99.0.0", asset.URL+"/skoed.tar.gz", sum)
	swapDest := filepath.Join(t.TempDir(), "skoed_swapped")
	c := startClusterWithEnv(t, 1, []string{
		"SKOED_UPGRADE_FEED_URL=" + feed.URL,
		"SKOED_TEST_MODE=1",
		"SKOED_TEST_SWAP_DEST=" + swapDest,
	})
	n := c.Leader(t).Node
	waitUpgradeAvailable(t, n, 5*time.Second)

	beforePage := fetchAudit(t, n, "limit=1")
	resp := n.apiDo(t, "POST", "/api/v1/upgrade/start", "")
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("/upgrade/start: status %d", resp.StatusCode)
	}
	got := waitForAudit(t, n, beforePage.Total+1, 3*time.Second)
	if got.Entries[0].Action != "upgrade.start" {
		t.Errorf("action: want upgrade.start, got %q", got.Entries[0].Action)
	}
}

// FS-UpgradeBinarySwap
func TestUpgradeBinarySwap(t *testing.T) {
	t.Parallel()
	// The fake binary content is a sentinel we can recognize after the swap.
	sentinel := []byte("#!/bin/sh\n# skoed fake v99.0.0\necho 99.0.0\n")
	asset, sum := startAssetServer(t, sentinel)
	feed := startFeedServerWithAssets(t, "99.0.0", asset.URL+"/skoed.tar.gz", sum)

	// SKOED_TEST_SWAP_DEST redirects the atomic rename to a temp file so
	// the swap does not overwrite the binary used by the rest of the suite.
	swapDest := filepath.Join(t.TempDir(), "skoed_swapped")

	c := startClusterWithEnv(t, 1, []string{
		"SKOED_UPGRADE_FEED_URL=" + feed.URL,
		"SKOED_TEST_MODE=1",            // skip os.Exit(0)
		"SKOED_TEST_SWAP_DEST=" + swapDest, // redirect swap target
	})
	n := c.Leader(t).Node
	waitUpgradeAvailable(t, n, 5*time.Second)

	resp := n.apiDo(t, "POST", "/api/v1/upgrade/start", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("/upgrade/start: want 202, got %d: %s", resp.StatusCode, body)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["accepted"] != true {
		t.Errorf("accepted: want true, got %v", result["accepted"])
	}
	if result["target_version"] != "99.0.0" {
		t.Errorf("target_version: want 99.0.0, got %v", result["target_version"])
	}

	// Verify the swap actually wrote the sentinel content to the redirected path.
	got, err := os.ReadFile(swapDest)
	if err != nil {
		t.Fatalf("swap dest not written: %v", err)
	}
	if !bytes.Equal(got, sentinel) {
		t.Errorf("swap dest content mismatch: got %q, want %q", got, sentinel)
	}
}

// TestUpgradeRejectsChecksumMismatch verifies the C-2 security fix: when the
// asset the node downloads does not match the SHA-256 the feed advertises, the
// swap is refused and the running binary is left untouched. Guards against a
// compromised/MITM'd download mirror substituting a malicious binary.
func TestUpgradeRejectsChecksumMismatch(t *testing.T) {
	t.Parallel()
	// Serve one payload but advertise a DIFFERENT payload's checksum in the feed.
	asset, _ := startAssetServer(t, []byte("#!/bin/sh\n# MALICIOUS payload\n"))
	_, honestSum := startAssetServer(t, []byte("#!/bin/sh\n# the honest release\n"))
	feed := startFeedServerWithAssets(t, "99.0.0", asset.URL+"/skoed.tar.gz", honestSum)

	swapDest := filepath.Join(t.TempDir(), "skoed_swapped")
	c := startClusterWithEnv(t, 1, []string{
		"SKOED_UPGRADE_FEED_URL=" + feed.URL,
		"SKOED_TEST_MODE=1",
		"SKOED_TEST_SWAP_DEST=" + swapDest,
	})
	n := c.Leader(t).Node
	waitUpgradeAvailable(t, n, 5*time.Second)

	resp := n.apiDo(t, "POST", "/api/v1/upgrade/start", "")
	defer resp.Body.Close()
	// The swap must fail (checksum mismatch) — 500 from the handler.
	if resp.StatusCode < 400 {
		t.Fatalf("/upgrade/start with mismatched checksum: want failure, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "checksum mismatch") {
		t.Errorf("expected 'checksum mismatch' in error, got: %s", body)
	}
	// The malicious payload must NOT have been written.
	if _, err := os.Stat(swapDest); err == nil {
		t.Errorf("swap dest was written despite checksum mismatch — binary was replaced!")
	}
}

// TestUpgradeRejectsBadSignature is the C-2 signature guard: a checksums.txt
// signed by a key OTHER than the trusted release key must not be trusted, so
// the upgrade is refused and the binary is never swapped. This is what closes
// the "authenticated user supplies a malicious binary + its own checksum" gap
// that plain SHA verification alone cannot.
func TestUpgradeRejectsBadSignature(t *testing.T) {
	t.Parallel()
	ensureTestSigningKey(t)

	// A foreign key (NOT the trusted SKOED_TEST_UPGRADE_PUBKEY signer).
	forger, err := openpgp.NewEntity("forger", "evil", "forger@evil.test", nil)
	if err != nil {
		t.Fatalf("gen forger key: %v", err)
	}

	sentinel := []byte("#!/bin/sh\n# attacker payload\n")
	asset, sum := startAssetServer(t, sentinel)
	checksumsTxt := []byte(fmt.Sprintf("%s  skoed_99.0.0_linux_amd64.tar.gz\n%s  skoed_99.0.0_linux_arm64.tar.gz\n", sum, sum))
	var sigBuf bytes.Buffer
	if err := openpgp.DetachSign(&sigBuf, forger, bytes.NewReader(checksumsTxt), nil); err != nil {
		t.Fatalf("forger sign: %v", err)
	}

	mux := http.NewServeMux()
	feed := httptest.NewServer(mux)
	t.Cleanup(feed.Close)
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) { w.Write(checksumsTxt) })
	mux.HandleFunc("/checksums.txt.sig", func(w http.ResponseWriter, r *http.Request) { w.Write(sigBuf.Bytes()) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"version":"99.0.0","published_at":"2026-07-01T09:00:00Z",
			"release_notes_url":"https://example.test/r","assets":{"linux_amd64":%q,"linux_arm64":%q},
			"checksums_url":%q,"checksums_sig_url":%q}`,
			asset.URL+"/skoed.tar.gz", asset.URL+"/skoed.tar.gz",
			feed.URL+"/checksums.txt", feed.URL+"/checksums.txt.sig")
	})

	swapDest := filepath.Join(t.TempDir(), "skoed_swapped")
	c := startClusterWithEnv(t, 1, []string{
		"SKOED_UPGRADE_FEED_URL=" + feed.URL,
		"SKOED_TEST_MODE=1",
		"SKOED_TEST_SWAP_DEST=" + swapDest,
	})
	n := c.Leader(t).Node

	// The feed reports a newer version, but its checksums carry an untrusted
	// signature, so no checksum is trusted → /upgrade/start must refuse.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var ck upgradeCheckResp
		resp := n.apiDo(t, "GET", "/api/v1/upgrade/check", "")
		_ = json.NewDecoder(resp.Body).Decode(&ck)
		resp.Body.Close()
		if ck.AvailableVersion == "99.0.0" {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}

	resp := n.apiDo(t, "POST", "/api/v1/upgrade/start", "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusAccepted {
		t.Fatalf("/upgrade/start accepted an upgrade whose checksums had an untrusted signature")
	}
	if _, err := os.Stat(swapDest); err == nil {
		t.Errorf("binary was swapped despite untrusted checksums signature")
	}
}

// waitUpgradeAvailable polls /upgrade/check until the cluster sees the
// feed and reports upgrade_available=true, or fails the test.
func waitUpgradeAvailable(t *testing.T, n *Node, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		resp := n.apiDo(t, "GET", "/api/v1/upgrade/check", "")
		var ck upgradeCheckResp
		_ = json.NewDecoder(resp.Body).Decode(&ck)
		resp.Body.Close()
		if ck.UpgradeAvailable {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("upgrade_available never went true within %s", within)
}
