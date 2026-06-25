// Acceptance tests for M31 Backup Hardening.
//
// FSIDs covered:
//   FS-BackupExportEncrypted, FS-BackupImportEncryptedSuccess,
//   FS-BackupImportEncryptedWrongPassphrase, FS-BackupImportPlaintext,
//   FS-BackupScheduleEnableAndList, FS-BackupScheduleRetainCount,
//   FS-BackupDownload, FS-BackupScheduleDisable,
//   FS-BackupScheduleSkipsUnchanged,
//   FS-BackupDiffDetectsChanges, FS-BackupDiffNoChanges,
//   FS-BackupDiffSettingsChange

package acceptance

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// exportConfigEncrypted posts to /api/v1/config/export with a passphrase.
func exportConfigEncrypted(t *testing.T, n *Node, passphrase string) []byte {
	t.Helper()
	body := mustJSON(t, map[string]string{"passphrase": passphrase})
	resp := n.apiDo(t, "POST", "/api/v1/config/export", body)
	assertStatus(t, resp, http.StatusOK)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read encrypted export body: %v", err)
	}
	return data
}

// importConfigWithPassphrase posts to /api/v1/config/import with a file and passphrase.
func importConfigWithPassphrase(t *testing.T, n *Node, archiveBytes []byte, passphrase string) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("archive", "backup.age")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(archiveBytes); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	if passphrase != "" {
		if err := mw.WriteField("passphrase", passphrase); err != nil {
			t.Fatalf("write passphrase field: %v", err)
		}
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
	return resp
}

// enableBackupSchedule calls PUT /api/v1/settings/backup.
func enableBackupSchedule(t *testing.T, n *Node, intervalHours, retainCount int) {
	t.Helper()
	body := mustJSON(t, map[string]interface{}{
		"enabled":        true,
		"interval_hours": intervalHours,
		"retain_count":   retainCount,
	})
	resp := n.apiDo(t, "PUT", "/api/v1/settings/backup", body)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

// disableBackupSchedule calls PUT /api/v1/settings/backup with enabled=false.
func disableBackupSchedule(t *testing.T, n *Node) {
	t.Helper()
	body := mustJSON(t, map[string]interface{}{
		"enabled":        false,
		"interval_hours": 1,
		"retain_count":   10,
	})
	resp := n.apiDo(t, "PUT", "/api/v1/settings/backup", body)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

// triggerBackup calls POST /api/v1/config/backups/trigger and returns whether
// a new backup was created.
func triggerBackup(t *testing.T, n *Node) bool {
	t.Helper()
	resp := n.apiDo(t, "POST", "/api/v1/config/backups/trigger", "")
	assertStatus(t, resp, http.StatusOK)
	defer resp.Body.Close()
	var result struct {
		Created bool `json:"created"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode trigger response: %v", err)
	}
	return result.Created
}

// listBackups calls GET /api/v1/config/backups and returns the entries.
func listBackups(t *testing.T, n *Node) []map[string]interface{} {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/config/backups", "")
	assertStatus(t, resp, http.StatusOK)
	defer resp.Body.Close()
	var result struct {
		Backups []map[string]interface{} `json:"backups"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode list backups response: %v", err)
	}
	if result.Backups == nil {
		return []map[string]interface{}{}
	}
	return result.Backups
}

// diffArchives sends two archive bytes to POST /api/v1/config/diff.
func diffArchives(t *testing.T, n *Node, archiveA, archiveB []byte) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fwA, err := mw.CreateFormFile("archive_a", "a.tar.gz")
	if err != nil {
		t.Fatalf("create archive_a form file: %v", err)
	}
	if _, err := fwA.Write(archiveA); err != nil {
		t.Fatalf("write archive_a: %v", err)
	}
	fwB, err := mw.CreateFormFile("archive_b", "b.tar.gz")
	if err != nil {
		t.Fatalf("create archive_b form file: %v", err)
	}
	if _, err := fwB.Write(archiveB); err != nil {
		t.Fatalf("write archive_b: %v", err)
	}
	mw.Close()

	req, err := http.NewRequest(http.MethodPost, n.APIBase+"/api/v1/config/diff", &buf)
	if err != nil {
		t.Fatalf("build diff request: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if n.sessionToken != "" {
		req.Header.Set("Authorization", "Bearer "+n.sessionToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("diff request: %v", err)
	}
	return resp
}

// ─── FS-BackupExportEncrypted ─────────────────────────────────────────────────

// TestBackupExportEncrypted verifies that POST /api/v1/config/export with a
// passphrase returns an age-encrypted archive beginning with "age-encryption.org/v1".
// FSID: FS-BackupExportEncrypted
func TestBackupExportEncrypted(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{Mode: "forwarding"})

	data := exportConfigEncrypted(t, n, "s3cr3t")
	if len(data) == 0 {
		t.Fatal("encrypted export body is empty")
	}
	const ageHeader = "age-encryption.org/v1"
	if !strings.HasPrefix(string(data[:min(len(data), 25)]), ageHeader) {
		t.Fatalf("expected age header %q, got %q", ageHeader, string(data[:min(len(data), 25)]))
	}
}

// ─── FS-BackupImportEncryptedSuccess ─────────────────────────────────────────

// TestBackupImportEncryptedSuccess verifies that importing an encrypted archive
// with the correct passphrase returns 200.
// FSID: FS-BackupImportEncryptedSuccess
func TestBackupImportEncryptedSuccess(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{Mode: "forwarding", UpstreamResolvers: []string{upstream}})

	archive := exportConfigEncrypted(t, n, "s3cr3t")
	resp := importConfigWithPassphrase(t, n, archive, "s3cr3t")
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

// ─── FS-BackupImportEncryptedWrongPassphrase ──────────────────────────────────

// TestBackupImportEncryptedWrongPassphrase verifies that importing with a wrong
// passphrase returns 422 with the expected error message.
// FSID: FS-BackupImportEncryptedWrongPassphrase
func TestBackupImportEncryptedWrongPassphrase(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{Mode: "forwarding"})

	archive := exportConfigEncrypted(t, n, "s3cr3t")
	resp := importConfigWithPassphrase(t, n, archive, "wrong")
	assertStatus(t, resp, http.StatusUnprocessableEntity)
	body := readBody(t, resp)
	if !strings.Contains(body, "invalid passphrase") && !strings.Contains(body, "corrupted archive") {
		t.Fatalf("expected passphrase error, got: %s", body)
	}
}

// ─── FS-BackupImportPlaintext ─────────────────────────────────────────────────

// TestBackupImportPlaintext verifies that a plaintext archive can still be
// imported via the updated POST /api/v1/config/import endpoint.
// FSID: FS-BackupImportPlaintext
func TestBackupImportPlaintext(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{Mode: "forwarding", UpstreamResolvers: []string{upstream}})

	// Use the existing GET export (plaintext tar.gz).
	archive := exportConfig(t, n)
	// Import with no passphrase via the new endpoint.
	resp := importConfigWithPassphrase(t, n, archive, "")
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

// ─── FS-BackupScheduleEnableAndList ──────────────────────────────────────────

// TestBackupScheduleEnableAndList enables scheduled backups, triggers one cycle,
// then verifies at least one entry appears in the backup list.
// FSID: FS-BackupScheduleEnableAndList
func TestBackupScheduleEnableAndList(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{Mode: "forwarding"})

	enableBackupSchedule(t, n, 1, 3)
	created := triggerBackup(t, n)
	if !created {
		t.Fatal("expected backup to be created on first trigger")
	}

	entries := listBackups(t, n)
	if len(entries) == 0 {
		t.Fatal("expected at least one backup entry after trigger")
	}
	// Verify required fields.
	entry := entries[0]
	for _, field := range []string{"id", "created_at", "size_bytes"} {
		if entry[field] == nil {
			t.Fatalf("backup entry missing field %q", field)
		}
	}
}

// ─── FS-BackupScheduleRetainCount ────────────────────────────────────────────

// TestBackupScheduleRetainCount triggers 4 backups with retain_count=3 and
// verifies the list contains exactly 3 entries.
// FSID: FS-BackupScheduleRetainCount
func TestBackupScheduleRetainCount(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{Mode: "forwarding", UpstreamResolvers: []string{upstream}})

	enableBackupSchedule(t, n, 1, 3)

	// We need to change config between triggers so the raft index advances
	// and dedup doesn't skip the backup. Add a blocklist between triggers.
	triggerBackup(t, n)
	addInlineBlocklist(t, n, "bl1", []string{"a.example.com"}, "")
	triggerBackup(t, n)
	addInlineBlocklist(t, n, "bl2", []string{"b.example.com"}, "")
	triggerBackup(t, n)
	addInlineBlocklist(t, n, "bl3", []string{"c.example.com"}, "")
	triggerBackup(t, n)

	entries := listBackups(t, n)
	if len(entries) != 3 {
		t.Fatalf("expected exactly 3 backups (retain_count=3), got %d", len(entries))
	}
}

// ─── FS-BackupDownload ────────────────────────────────────────────────────────

// TestBackupDownload verifies that a stored backup can be downloaded by ID.
// FSID: FS-BackupDownload
func TestBackupDownload(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{Mode: "forwarding"})

	enableBackupSchedule(t, n, 1, 10)
	created := triggerBackup(t, n)
	if !created {
		t.Skip("backup was deduped — no entry to download")
	}

	entries := listBackups(t, n)
	if len(entries) == 0 {
		t.Fatal("no backups listed after trigger")
	}
	id, ok := entries[0]["id"].(string)
	if !ok || id == "" {
		t.Fatalf("backup entry has no valid id: %v", entries[0])
	}

	resp := n.apiDo(t, "GET", "/api/v1/config/backups/"+id+"/download", "")
	assertStatus(t, resp, http.StatusOK)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read download body: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("downloaded backup is empty")
	}
}

// ─── FS-BackupScheduleDisable ─────────────────────────────────────────────────

// TestBackupScheduleDisable verifies that disabling the scheduler stops new
// backups from being created when triggered.
// FSID: FS-BackupScheduleDisable
func TestBackupScheduleDisable(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{Mode: "forwarding"})

	enableBackupSchedule(t, n, 1, 10)
	triggerBackup(t, n) // may or may not create — doesn't matter
	countBefore := len(listBackups(t, n))

	disableBackupSchedule(t, n)

	// After disabling, trigger should return created=false.
	created := triggerBackup(t, n)
	if created {
		t.Fatal("expected no backup after disabling the scheduler")
	}
	countAfter := len(listBackups(t, n))
	if countAfter != countBefore {
		t.Fatalf("backup count changed after disable: before=%d after=%d", countBefore, countAfter)
	}
}

// ─── FS-BackupScheduleSkipsUnchanged ─────────────────────────────────────────

// TestBackupScheduleSkipsUnchanged verifies that triggering twice without
// any config changes between them results in only one backup.
// FSID: FS-BackupScheduleSkipsUnchanged
func TestBackupScheduleSkipsUnchanged(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{Mode: "forwarding"})

	enableBackupSchedule(t, n, 1, 10)

	first := triggerBackup(t, n)
	if !first {
		t.Fatal("expected first trigger to create a backup")
	}
	// Trigger again without any config change — should be deduped.
	second := triggerBackup(t, n)
	if second {
		t.Fatal("expected second trigger to be deduped (no config change)")
	}

	entries := listBackups(t, n)
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 backup after two identical triggers, got %d", len(entries))
	}
}

// ─── FS-BackupDiffDetectsChanges ─────────────────────────────────────────────

// TestBackupDiffDetectsChanges diffs archive A (with blocklist "list-a") against
// archive B (with blocklists "list-a" and "list-b") and verifies "list-b" appears
// under added.blocklists.
// FSID: FS-BackupDiffDetectsChanges
func TestBackupDiffDetectsChanges(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{Mode: "forwarding", UpstreamResolvers: []string{upstream}})

	// Archive A: has "list-a".
	addInlineBlocklist(t, n, "list-a", []string{"a.example.com"}, "")
	archiveA := exportConfig(t, n)

	// Archive B: also has "list-b".
	addInlineBlocklist(t, n, "list-b", []string{"b.example.com"}, "")
	archiveB := exportConfig(t, n)

	resp := diffArchives(t, n, archiveA, archiveB)
	assertStatus(t, resp, http.StatusOK)
	defer resp.Body.Close()

	var diff struct {
		Added struct {
			Blocklists []string `json:"blocklists"`
		} `json:"added"`
		Removed struct {
			Blocklists []string `json:"blocklists"`
		} `json:"removed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&diff); err != nil {
		t.Fatalf("decode diff response: %v", err)
	}
	found := false
	for _, name := range diff.Added.Blocklists {
		if name == "list-b" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'list-b' in added.blocklists, got %v", diff.Added.Blocklists)
	}
	if len(diff.Removed.Blocklists) != 0 {
		t.Fatalf("expected empty removed.blocklists, got %v", diff.Removed.Blocklists)
	}
}

// ─── FS-BackupDiffNoChanges ───────────────────────────────────────────────────

// TestBackupDiffNoChanges verifies that diffing two identical archives returns
// all empty diff sections.
// FSID: FS-BackupDiffNoChanges
func TestBackupDiffNoChanges(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{Mode: "forwarding", UpstreamResolvers: []string{upstream}})

	archive := exportConfig(t, n)

	resp := diffArchives(t, n, archive, archive)
	assertStatus(t, resp, http.StatusOK)
	defer resp.Body.Close()

	var diff struct {
		Added   struct{ Blocklists []string `json:"blocklists"`; Allowlist []string `json:"allowlist"`; LocalDNS []string `json:"local_dns"`; Settings map[string]string `json:"settings"` } `json:"added"`
		Removed struct{ Blocklists []string `json:"blocklists"`; Allowlist []string `json:"allowlist"`; LocalDNS []string `json:"local_dns"`; Settings map[string]string `json:"settings"` } `json:"removed"`
		Changed struct{ Settings map[string]string `json:"settings"` } `json:"changed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&diff); err != nil {
		t.Fatalf("decode diff response: %v", err)
	}
	if len(diff.Added.Blocklists)+len(diff.Added.Allowlist)+len(diff.Added.LocalDNS) > 0 {
		t.Fatalf("expected empty added sections, got added=%+v", diff.Added)
	}
	if len(diff.Removed.Blocklists)+len(diff.Removed.Allowlist)+len(diff.Removed.LocalDNS) > 0 {
		t.Fatalf("expected empty removed sections, got removed=%+v", diff.Removed)
	}
	if len(diff.Changed.Settings) > 0 {
		t.Fatalf("expected empty changed.settings, got %v", diff.Changed.Settings)
	}
}

// ─── FS-BackupDiffSettingsChange ──────────────────────────────────────────────

// TestBackupDiffSettingsChange verifies that a changed upstream resolver
// appears in changed.settings.
// FSID: FS-BackupDiffSettingsChange
func TestBackupDiffSettingsChange(t *testing.T) {
	t.Parallel()
	upstreamA := startFakeUpstream(t, fakeUpstreamReturnsA("1.1.1.1"))
	upstreamB := startFakeUpstream(t, fakeUpstreamReturnsA("8.8.8.8"))
	n := startNode(t, NodeConfig{Mode: "forwarding", UpstreamResolvers: []string{upstreamA}})

	// Archive A: uses upstreamA.
	archiveA := exportConfig(t, n)

	// Change upstream resolver and export archive B.
	resp := n.apiDo(t, "PATCH", "/api/v1/settings", mustJSON(t, map[string]interface{}{
		"dns": map[string]interface{}{
			"upstream_resolvers": []string{upstreamB},
		},
	}))
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	archiveB := exportConfig(t, n)

	diffResp := diffArchives(t, n, archiveA, archiveB)
	assertStatus(t, diffResp, http.StatusOK)
	defer diffResp.Body.Close()

	var diff struct {
		Changed struct {
			Settings map[string]string `json:"settings"`
		} `json:"changed"`
	}
	if err := json.NewDecoder(diffResp.Body).Decode(&diff); err != nil {
		t.Fatalf("decode diff response: %v", err)
	}
	if diff.Changed.Settings["upstream_resolvers"] == "" {
		t.Fatalf("expected upstream_resolvers in changed.settings, got %v", diff.Changed.Settings)
	}
}

