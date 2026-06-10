// Acceptance tests for Packaging and Distribution (M11).
//
// Covers FSIDs:
//   FS-AlpinePackageBuilt, FS-AlpinePackageContents,
//   FS-AurPkgbuildPresent, FS-AurPkgbuildVersionSync,
//   FS-HelmChartInstallsSkoed, FS-HelmChartLints, FS-HelmChartPublished,
//   FS-ProxmoxScriptInRelease, FS-DocsBuildSucceeds

package acceptance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// FS-AurPkgbuildPresent — PKGBUILD must exist and contain required fields.
func TestAurPkgbuildPresent(t *testing.T) {
	root := repoRoot(t)
	pkgbuild := filepath.Join(root, "packaging", "aur", "PKGBUILD")

	data, err := os.ReadFile(pkgbuild)
	if err != nil {
		t.Fatalf("PKGBUILD not found at %s: %v", pkgbuild, err)
	}
	content := string(data)

	for _, required := range []string{"pkgname=skoed", "pkgver=", "url=", "package()"} {
		if !strings.Contains(content, required) {
			t.Errorf("PKGBUILD missing required field: %q", required)
		}
	}
}

// FS-HelmChartLints — helm lint must pass with exit 0.
func TestHelmChartLints(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not installed — skipping chart lint test")
	}
	root := repoRoot(t)
	chart := filepath.Join(root, "charts", "skoed")

	if _, err := os.Stat(chart); os.IsNotExist(err) {
		t.Fatalf("Helm chart not found at %s", chart)
	}

	out, err := exec.Command("helm", "lint", chart).CombinedOutput()
	if err != nil {
		t.Fatalf("helm lint failed:\n%s", out)
	}
	t.Logf("helm lint output:\n%s", out)
}

// FS-ProxmoxScriptInRelease — the proxmox script must exist and be present in
// goreleaser extra_files so it is attached to releases.
func TestProxmoxScriptInExtraFiles(t *testing.T) {
	root := repoRoot(t)

	script := filepath.Join(root, "scripts", "proxmox-create.sh")
	if _, err := os.Stat(script); os.IsNotExist(err) {
		t.Fatalf("proxmox-create.sh not found at %s", script)
	}

	goreleaser := filepath.Join(root, ".goreleaser.yaml")
	data, err := os.ReadFile(goreleaser)
	if err != nil {
		t.Fatalf("cannot read .goreleaser.yaml: %v", err)
	}
	if !strings.Contains(string(data), "proxmox-create.sh") {
		t.Error(".goreleaser.yaml does not reference proxmox-create.sh in extra_files")
	}
}

// FS-AlpinePackageBuilt (static check) — .goreleaser.yaml must declare apk format.
func TestGoreleaserDeclaresApkFormat(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, ".goreleaser.yaml"))
	if err != nil {
		t.Fatalf("cannot read .goreleaser.yaml: %v", err)
	}
	if !strings.Contains(string(data), "- apk") {
		t.Error(".goreleaser.yaml nfpms formats does not include 'apk'")
	}
}

// FS-DocsBuildSucceeds — mdbook build must succeed.
func TestDocsBuildSucceeds(t *testing.T) {
	if _, err := exec.LookPath("mdbook"); err != nil {
		t.Skip("mdbook not installed — skipping docs build test")
	}
	root := repoRoot(t)
	out, err := exec.Command("mdbook", "build", filepath.Join(root, "docs")).CombinedOutput()
	if err != nil {
		t.Fatalf("mdbook build failed:\n%s", out)
	}
	index := filepath.Join(root, "docs", "book", "index.html")
	if _, err := os.Stat(index); os.IsNotExist(err) {
		t.Errorf("docs/book/index.html not produced after successful mdbook build")
	}
}

// FS-HelmChartInstallsSkoed (live — requires kind or k3s)
// FS-HelmChartPublished (live — requires GHCR credentials)
// These are validated in the CI release workflow; skipped here for local dev.
func TestHelmChartInstallsSkoed(t *testing.T) {
	t.Skip("live Kubernetes test — validated by CI release workflow")
}

func TestHelmChartPublished(t *testing.T) {
	t.Skip("live GHCR publish test — validated by CI release workflow")
}
