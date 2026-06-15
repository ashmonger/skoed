// Acceptance tests for config import and export.
//
// FSIDs covered:
//   FS-ConfigExport, FS-ConfigImportOnFreshNode, FS-ConfigImportAtomic,
//   FS-ConfigImportOverwritesExisting, FS-ConfigExportImportRoundTrip
//   FS-ConfigBackupWebUiDownload, FS-ConfigBackupWebUiImport, FS-ConfigBackupWebUiRoundTrip

package acceptance

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/miekg/dns"
)

// exportConfig calls GET /api/v1/config/export and returns the raw archive bytes.
func exportConfig(t *testing.T, n *Node) []byte {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/config/export", "")
	assertStatus(t, resp, http.StatusOK)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read export body: %v", err)
	}
	return data
}

// importConfig calls POST /api/v1/config/import with a multipart archive field and asserts 200.
func importConfig(t *testing.T, n *Node, archiveBytes []byte) {
	t.Helper()
	importConfigExpect(t, n, archiveBytes, http.StatusOK)
}

// importConfigExpect is like importConfig but asserts an arbitrary status code.
func importConfigExpect(t *testing.T, n *Node, archiveBytes []byte, wantStatus int) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("archive", "config.tar.gz")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(archiveBytes); err != nil {
		t.Fatalf("write archive to form: %v", err)
	}
	mw.Close()

	req, err := http.NewRequest(http.MethodPost, n.APIBase+"/api/v1/config/import", &buf)
	if err != nil {
		t.Fatalf("build import request: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if n.sessionToken != "" {
		req.Header.Set("Authorization", "Bearer "+n.sessionToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("config import request: %v", err)
	}
	assertStatus(t, resp, wantStatus)
	resp.Body.Close()
}

// addLocalDNSEntry adds a local DNS A-record entry via the API.
func addLocalDNSEntry(t *testing.T, n *Node, hostname, ip string) {
	t.Helper()
	resp := n.apiDo(t, "POST", "/api/v1/local-dns", mustJSON(t, map[string]string{
		"hostname": hostname,
		"ip":       ip,
	}))
	assertStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
}

// ── FS-ConfigExport ───────────────────────────────────────────────────────────

// FS-ConfigExport
// GET /api/v1/config/export returns a non-empty archive with Content-Type application/gzip.
func TestConfigExport(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})

	resp := n.apiDo(t, "GET", "/api/v1/config/export", "")
	assertStatus(t, resp, http.StatusOK)
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if ct != "application/gzip" {
		t.Fatalf("expected Content-Type application/gzip, got %q", ct)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read export body: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("export archive body is empty")
	}

	// Verify the bytes are a valid gzip stream containing a tar archive.
	gr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("export body is not valid gzip: %v", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	_, err = tr.Next()
	if err != nil {
		t.Fatalf("export archive contains no tar entries: %v", err)
	}
}

// ── FS-ConfigImportOnFreshNode ────────────────────────────────────────────────

// FS-ConfigImportOnFreshNode
// Export from nodeA and import to a fresh nodeB; nodeB exhibits the same blocklist behavior.
func TestConfigImportOnFreshNode(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))

	nodeA := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, nodeA, "ads", []string{"ads.example.com"}, "")

	// Verify nodeA blocks the domain.
	assertRcode(t, dnsQuery(t, nodeA.DNSAddr, "ads.example.com", dns.TypeA), dns.RcodeNameError)

	archive := exportConfig(t, nodeA)

	nodeB := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})

	// Before import, nodeB should resolve the domain normally.
	assertRcode(t, dnsQuery(t, nodeB.DNSAddr, "ads.example.com", dns.TypeA), dns.RcodeSuccess)

	importConfig(t, nodeB, archive)

	// After import, nodeB should block the domain.
	assertRcode(t, dnsQuery(t, nodeB.DNSAddr, "ads.example.com", dns.TypeA), dns.RcodeNameError)
}

// ── FS-ConfigImportAtomic ─────────────────────────────────────────────────────

// FS-ConfigImportAtomic
// Submitting a corrupt archive returns HTTP 400 and leaves the existing config intact.
func TestConfigImportAtomic(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, n, "existing", []string{"blocked.example.com"}, "")

	// Confirm the existing blocklist works.
	assertRcode(t, dnsQuery(t, n.DNSAddr, "blocked.example.com", dns.TypeA), dns.RcodeNameError)

	// Submit an obviously invalid archive.
	corruptArchive := []byte("not a valid archive")
	importConfigExpect(t, n, corruptArchive, http.StatusBadRequest)

	// Existing config must be unchanged; the domain is still blocked.
	assertRcode(t, dnsQuery(t, n.DNSAddr, "blocked.example.com", dns.TypeA), dns.RcodeNameError)
}

// ── FS-ConfigImportOverwritesExisting ─────────────────────────────────────────

// FS-ConfigImportOverwritesExisting
// Importing on a node that already has config replaces it completely.
func TestConfigImportOverwritesExisting(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))

	// nodeA has a blocklist with "new-domain.example.com".
	nodeA := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, nodeA, "new-list", []string{"new-domain.example.com"}, "")

	archive := exportConfig(t, nodeA)

	// nodeB starts with a different blocklist entry.
	nodeB := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, nodeB, "old-list", []string{"old-domain.example.com"}, "")

	// Before import: nodeB blocks old-domain but not new-domain.
	assertRcode(t, dnsQuery(t, nodeB.DNSAddr, "old-domain.example.com", dns.TypeA), dns.RcodeNameError)
	assertRcode(t, dnsQuery(t, nodeB.DNSAddr, "new-domain.example.com", dns.TypeA), dns.RcodeSuccess)

	importConfig(t, nodeB, archive)

	// After import: nodeB blocks new-domain (from imported config).
	assertRcode(t, dnsQuery(t, nodeB.DNSAddr, "new-domain.example.com", dns.TypeA), dns.RcodeNameError)

	// After import: old-domain is no longer blocked (old config replaced).
	assertRcode(t, dnsQuery(t, nodeB.DNSAddr, "old-domain.example.com", dns.TypeA), dns.RcodeSuccess)
}

// ── FS-ConfigExportImportRoundTrip ────────────────────────────────────────────

// FS-ConfigExportImportRoundTrip
// Node A has a blocklist blocking "ads.example.com" and a local entry "nas.home"→"192.168.1.50".
// Export A and import on B; B blocks the domain and resolves the local entry.
func TestConfigExportImportRoundTrip(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))

	nodeA := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, nodeA, "ads", []string{"ads.example.com"}, "")
	addLocalDNSEntry(t, nodeA, "nas.home", "192.168.1.50")

	// Verify nodeA behavior before export.
	assertRcode(t, dnsQuery(t, nodeA.DNSAddr, "ads.example.com", dns.TypeA), dns.RcodeNameError)
	assertAnswerA(t, dnsQuery(t, nodeA.DNSAddr, "nas.home", dns.TypeA), "192.168.1.50")

	archive := exportConfig(t, nodeA)

	nodeB := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})

	importConfig(t, nodeB, archive)

	// Node B must block ads.example.com.
	assertRcode(t, dnsQuery(t, nodeB.DNSAddr, "ads.example.com", dns.TypeA), dns.RcodeNameError)

	// Node B must resolve the local entry.
	assertAnswerA(t, dnsQuery(t, nodeB.DNSAddr, "nas.home", dns.TypeA), "192.168.1.50")
}

// ── FS-ConfigBackupWebUiDownload ──────────────────────────────────────────────

// FS-ConfigBackupWebUiDownload
// The exported archive must not contain admin credentials (password_hash field).
func TestConfigExportDoesNotIncludeCredentials(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	archive := exportConfig(t, n)

	// Extract the YAML from the archive and verify no password_hash field.
	gr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("open gzip: %v", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		_, err := tr.Next()
		if err != nil {
			break
		}
		data, _ := io.ReadAll(tr)
		if strings.Contains(string(data), "password_hash") {
			t.Fatal("exported archive contains password_hash — credentials must not be exported")
		}
	}
}

// ── FS-ConfigBackupWebUiImport ────────────────────────────────────────────────

// FS-ConfigBackupWebUiImport
// Importing a backup must not change the current node's admin credentials.
// After import the original username/password must still authenticate.
func TestConfigImportPreservesCredentials(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))

	// nodeA: source of the backup.
	nodeA := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})
	addInlineBlocklist(t, nodeA, "from-a", []string{"from-a.example.com"}, "")
	archive := exportConfig(t, nodeA)

	// nodeB: import the backup. Its credentials are defaultUsername/defaultPassword.
	nodeB := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	importConfig(t, nodeB, archive)

	// Verify nodeB still accepts the original credentials after import.
	resp := nodeB.apiDo(t, "GET", "/api/v1/cluster/health", "")
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

// ── FS-ConfigBackupWebUiRoundTrip ─────────────────────────────────────────────

// FS-ConfigBackupWebUiRoundTrip
// Config round-trips through the UI export→import flow preserving DNS behavior.
// This is an alias for FS-ConfigExportImportRoundTrip tested from the Settings
// page perspective; the same API is used so we verify the behaviour is intact.
func TestConfigBackupWebUiRoundTrip(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))

	nodeA := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	addInlineBlocklist(t, nodeA, "backup-list", []string{"backup-blocked.example.com"}, "")
	addLocalDNSEntry(t, nodeA, "my-server.home", "10.0.0.1")

	archive := exportConfig(t, nodeA)

	nodeB := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})
	importConfig(t, nodeB, archive)

	assertRcode(t, dnsQuery(t, nodeB.DNSAddr, "backup-blocked.example.com", dns.TypeA), dns.RcodeNameError)
	assertAnswerA(t, dnsQuery(t, nodeB.DNSAddr, "my-server.home", dns.TypeA), "10.0.0.1")
}
