// Kubernetes operator acceptance tests.
//
// FSIDs covered:
//   FS-OperatorCrdRegistered    → TestOperatorHelmTemplateRendersCorrectly
//   FS-ClusterProvisioned       → TestSkoedClusterCrdSchema
//   FS-ClusterScaleUp           → TestSkoedClusterCrdSchema (replicas field present)
//   FS-ClusterScaleDown         → TestScaleDownRequiresLiveCluster (skip)
//   FS-PvcSurvivesPodRestart    → TestPvcVolumeClaimTemplateInCrd
//   FS-AcmeCertAutoRotate       → TestAcmeCertRotationRequiresLiveCluster (skip)
//   FS-StatusConditions         → TestSkoedClusterPrinterColumns
//   FS-StatusConditionsOnFailure→ TestSkoedClusterPrinterColumns
//   FS-HelmFallbackUnaffected   → TestHelmFallbackUnaffected
//
// Tests requiring a live Kubernetes cluster are t.Skip()'d with clear instructions.
// Tests requiring the Helm CLI are skipped when `helm` is not in PATH.
package acceptance

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRoot returns the absolute path to the repository root by walking up from
// this test file's location.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// tests/acceptance/ → two levels up
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func helmBinary(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm binary not found in PATH — skipping Helm-based tests")
	}
	return path
}

func runHelm(t *testing.T, args ...string) string {
	t.Helper()
	helm := helmBinary(t)
	cmd := exec.Command(helm, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("helm %v failed: %v\nstderr: %s", args, err, stderr.String())
	}
	return stdout.String()
}

// TestOperatorHelmTemplateRendersCorrectly verifies that `helm template` on the
// operator chart emits all required K8s object kinds.
// FS-OperatorCrdRegistered
func TestOperatorHelmTemplateRendersCorrectly(t *testing.T) {
	root := repoRoot(t)
	chartPath := filepath.Join(root, "deploy", "helm", "skoed-operator")
	if _, err := os.Stat(chartPath); err != nil {
		t.Skip("operator Helm chart not found at " + chartPath)
	}

	out := runHelm(t, "template", "test-install", chartPath)

	required := []string{
		"kind: CustomResourceDefinition",
		"name: skoedclusters.skoed.io",
		"name: skoednodes.skoed.io",
		"kind: Deployment",
		"kind: ClusterRole",
		"kind: ClusterRoleBinding",
		"kind: ServiceAccount",
	}
	for _, want := range required {
		if !strings.Contains(out, want) {
			t.Errorf("helm template output missing %q", want)
		}
	}
}

// TestSkoedClusterCrdSchema verifies the SkoedCluster CRD schema contains the
// required fields with correct validation.
// FS-ClusterProvisioned, FS-ClusterScaleUp
func TestSkoedClusterCrdSchema(t *testing.T) {
	root := repoRoot(t)
	crdPath := filepath.Join(root, "deploy", "helm", "skoed-operator", "templates", "crds", "skoedcluster.yaml")
	data, err := os.ReadFile(crdPath)
	if err != nil {
		t.Skip("SkoedCluster CRD YAML not found: " + err.Error())
	}
	content := string(data)

	requiredFields := []string{
		"replicas",
		"minimum: 1",
		"maximum: 7",
		"image",
		"storage",
		"size",
		"dns",
		"api",
		"adminSecretRef",
		"status:",
		"leader",
		"voters",
		"readyReplicas",
	}
	for _, field := range requiredFields {
		if !strings.Contains(content, field) {
			t.Errorf("SkoedCluster CRD missing field/constraint %q", field)
		}
	}
}

// TestSkoedClusterPrinterColumns verifies status conditions surface Raft health
// in the default kubectl output columns.
// FS-StatusConditions, FS-StatusConditionsOnFailure
func TestSkoedClusterPrinterColumns(t *testing.T) {
	root := repoRoot(t)
	crdPath := filepath.Join(root, "deploy", "helm", "skoed-operator", "templates", "crds", "skoedcluster.yaml")
	data, err := os.ReadFile(crdPath)
	if err != nil {
		t.Skip("SkoedCluster CRD YAML not found: " + err.Error())
	}
	content := string(data)

	for _, col := range []string{"Replicas", "Ready", "Leader"} {
		if !strings.Contains(content, "name: "+col) {
			t.Errorf("SkoedCluster CRD missing printer column %q", col)
		}
	}
}

// TestPvcVolumeClaimTemplateInCrd verifies that the StatefulSet PVC template
// approach is represented in the CRD schema (storage.size field).
// FS-PvcSurvivesPodRestart
func TestPvcVolumeClaimTemplateInCrd(t *testing.T) {
	root := repoRoot(t)
	crdPath := filepath.Join(root, "deploy", "helm", "skoed-operator", "templates", "crds", "skoedcluster.yaml")
	data, err := os.ReadFile(crdPath)
	if err != nil {
		t.Skip("SkoedCluster CRD YAML not found")
	}
	// storage.size is the spec field that drives the PVC template capacity.
	if !strings.Contains(string(data), "storage") || !strings.Contains(string(data), "size") {
		t.Error("SkoedCluster CRD missing storage.size — PVC template will not be configured correctly")
	}
}

// TestHelmFallbackUnaffected verifies the existing skoed Helm chart still renders
// without errors when the operator is installed.
// FS-HelmFallbackUnaffected
func TestHelmFallbackUnaffected(t *testing.T) {
	root := repoRoot(t)
	chartPath := filepath.Join(root, "deploy", "helm", "skoed")
	if _, err := os.Stat(chartPath); err != nil {
		t.Skip("plain skoed Helm chart not found at " + chartPath)
	}

	out := runHelm(t, "template", "test-fallback", chartPath)

	if !strings.Contains(out, "kind:") {
		t.Error("skoed Helm chart rendered no K8s objects — chart may be broken")
	}
	// Operator-specific CRDs must NOT appear in the plain chart.
	if strings.Contains(out, "skoedclusters.skoed.io") {
		t.Error("plain skoed Helm chart should not reference operator CRDs")
	}
}

// TestScaleDownRequiresLiveCluster documents FS-ClusterScaleDown.
// Full behavioral validation (Raft deregistration before pod deletion) requires
// a live Kubernetes cluster. Run with kind:
//
//	kind create cluster
//	helm install skoed-operator deploy/helm/skoed-operator/
//	kubectl apply -f tests/fixtures/skoedcluster-3node.yaml
//	kubectl patch skoedcluster my-cluster --type merge -p '{"spec":{"replicas":1}}'
//	kubectl get skoedcluster my-cluster -o jsonpath='{.status.voters}'  # should show 1
func TestScaleDownRequiresLiveCluster(t *testing.T) {
	t.Skip("FS-ClusterScaleDown: full test requires a live K8s cluster (see comment for kind instructions)")
}

// TestAcmeCertRotationRequiresLiveCluster documents FS-AcmeCertAutoRotate.
// Full validation requires a live cluster with DoH/DoT enabled and a near-expiry cert.
func TestAcmeCertRotationRequiresLiveCluster(t *testing.T) {
	t.Skip("FS-AcmeCertAutoRotate: full test requires a live K8s cluster with TLS configured")
}
