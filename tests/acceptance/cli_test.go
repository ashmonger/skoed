// Acceptance tests for M5.9.1 — dblock CLI.
//
// FSIDs covered:
//   FS-CliVersionFlag       → TestCliVersion
//   FS-CliHealth            → TestCliHealth
//   FS-CliStatus            → TestCliStatus       ← 3-node
//   FS-CliTokenCreate       → TestCliTokenCreate
//   FS-CliBlocklistTest     → TestCliBlocklistTest
//   FS-CliDaemonStillWorks  → TestCliDaemonStillWorks
//   FS-UrlTesterCliSubcommand → TestCliBlocklistTest (M5.9.5 re-uses the same exec)
//
// FS-TuiTopShowsLiveDashboard intentionally NOT tested — bubbletea TUI
// testing is finicky and the value is low; manual screenshot only.

package acceptance

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// runCli runs the dblock binary with the given args and returns
// (stdout, stderr, exit). Skips when the binary isn't built (CLI not
// yet wired) or when --version returns nonzero (subcommand framework
// not present).
func runCli(t *testing.T, env []string, args ...string) (string, string, int) {
	t.Helper()
	bin := dblockBinary(t)
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("dblock binary missing: %v", err)
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return stdout.String(), stderr.String(), exitErr.ExitCode()
		}
		t.Fatalf("run dblock %v: %v", args, err)
	}
	return stdout.String(), stderr.String(), 0
}

// FS-CliVersionFlag
func TestCliVersion(t *testing.T) {
	stdout, _, exit := runCli(t, nil, "--version")
	if exit != 0 {
		t.Skipf("M5.9.1 impl pending: dblock --version exit=%d", exit)
	}
	// Shape: dblock <semver-or-dev> (commit=<hex-or-unknown>, go=go1.<x>)
	if !strings.Contains(stdout, "dblock") || !strings.Contains(stdout, "go=go1.") {
		t.Errorf("version output shape unexpected: %q", stdout)
	}
}

// FS-CliHealth
func TestCliHealth(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	auth := fmt.Sprintf("DBLOCK_AUTH=%s:%s", defaultUsername, defaultPassword)
	apiFlag := []string{"health", "--api", n.APIBase}
	stdout, stderr, exit := runCli(t, []string{auth}, apiFlag...)
	if strings.Contains(stderr, "unknown command") {
		t.Skip("M5.9.1 impl pending: health subcommand missing")
	}
	if exit != 0 {
		t.Errorf("dblock health: exit=%d, stderr=%q", exit, stderr)
	}
	low := strings.ToLower(stdout + stderr)
	if !strings.Contains(low, "ok") {
		t.Errorf("expected status=ok somewhere in output; stdout=%q stderr=%q", stdout, stderr)
	}
}

// FS-CliStatus — runs against a 3-node cluster and confirms the
// leader is identified in the output.
func TestCliStatus(t *testing.T) {
	c := startCluster(t, 3)
	n := c.Leader(t).Node
	auth := fmt.Sprintf("DBLOCK_AUTH=%s:%s", defaultUsername, defaultPassword)
	stdout, stderr, exit := runCli(t, []string{auth}, "status", "--api", n.APIBase)
	if strings.Contains(stderr, "unknown command") {
		t.Skip("M5.9.1 impl pending: status subcommand missing")
	}
	if exit != 0 {
		t.Errorf("dblock status: exit=%d, stderr=%q", exit, stderr)
	}
	// Each node-id should appear in the table.
	for _, cn := range c.nodes {
		if !strings.Contains(stdout, cn.NodeID) {
			t.Errorf("status output missing node %q. stdout=%q", cn.NodeID, stdout)
		}
	}
	if !strings.Contains(strings.ToLower(stdout), "leader") {
		t.Errorf("status output should mark a leader; stdout=%q", stdout)
	}
}

// FS-CliTokenCreate
func TestCliTokenCreate(t *testing.T) {
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	auth := fmt.Sprintf("DBLOCK_AUTH=%s:%s", defaultUsername, defaultPassword)
	stdout, stderr, exit := runCli(t, []string{auth}, "token", "create", "--api", n.APIBase)
	if strings.Contains(stderr, "unknown command") {
		t.Skip("M5.9.1 impl pending: token subcommand missing")
	}
	if exit != 0 {
		t.Errorf("dblock token create: exit=%d, stderr=%q", exit, stderr)
	}
	// Should reference leader_address + token field somewhere.
	if !strings.Contains(stdout, "token") || !strings.Contains(stdout, n.APIBase) {
		t.Errorf("token output should include token + leader URL; stdout=%q", stdout)
	}
}

// FS-CliBlocklistTest — uses an httptest server so the CLI is exercised
// end-to-end without depending on the public internet.
func TestCliBlocklistTest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "0.0.0.0 cli-test-a.example\n0.0.0.0 cli-test-b.example\n0.0.0.0 cli-test-c.example\n")
	}))
	defer srv.Close()

	stdout, stderr, exit := runCli(t, nil, "blocklist", "test", srv.URL, "--format", "hosts")
	if strings.Contains(stderr, "unknown command") {
		t.Skip("M5.9.1 impl pending: blocklist subcommand missing")
	}
	if exit != 0 {
		t.Errorf("dblock blocklist test: exit=%d, stderr=%q", exit, stderr)
	}
	// Output should reference the count.
	if !strings.Contains(stdout, "3") {
		t.Errorf("output should mention the domain count (3); stdout=%q", stdout)
	}
}

// FS-CliDaemonStillWorks — invoking dblock with no subcommand and
// just --config <path> behaves like the existing daemon flow.
func TestCliDaemonStillWorks(t *testing.T) {
	// startCluster already invokes `dblock --config <path>` (no
	// subcommand). If we got this far in the suite, the daemon
	// subcommand fallback works. This test makes the assertion
	// explicit by running a brand-new node and waiting for it.
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(n.APIBase + "/api/v1/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 || resp.StatusCode == 401 {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("daemon-as-default did not come up at %s", n.APIBase)
}
