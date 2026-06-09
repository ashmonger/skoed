// Acceptance tests for M4.5 — API Documentation Browser.
//
// FSIDs covered:
//   FS-ApiDocsServed              → TestApiDocsServed
//   FS-ApiDocsOpenApiYaml         → TestApiDocsOpenApiYamlServed
//   FS-ApiDocsAssetsServed        → TestApiDocsAssetsServed (covers CSS + JS)
//   FS-ApiDocsHonorsBrowserAuth   → (manual via demo recipe — Swagger UI is
//                                    a browser-side concern; an acceptance test
//                                    would essentially re-test net/http BasicAuth)
//   FS-ApiDocsDisabledByConfig    → TestApiDocsDisabledByConfig
//
// All tests self-skip when the server doesn't yet expose /api/docs (the
// pre-impl state).

package acceptance

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestApiDocsServed(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	resp, err := http.Get(n.APIBase + "/api/docs/")
	if err != nil {
		t.Fatalf("GET /api/docs/: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		t.Skipf("M4.5 impl pending: /api/docs/ returns 404")
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type %q, want text/html…", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(strings.ToLower(string(body)), "swagger-ui") {
		t.Errorf("body missing 'swagger-ui' marker; got %s", truncate(string(body), 200))
	}
}

func TestApiDocsOpenApiYamlServed(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	resp, err := http.Get(n.APIBase + "/api/openapi.yaml")
	if err != nil {
		t.Fatalf("GET /api/openapi.yaml: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		t.Skipf("M4.5 impl pending: /api/openapi.yaml returns 404")
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "yaml") {
		t.Errorf("Content-Type %q, want …yaml…", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	if !strings.HasPrefix(s, "openapi:") {
		t.Errorf("body does not start with 'openapi:'; got %s", truncate(s, 80))
	}
	if !strings.Contains(s, "x-tsid:") {
		t.Errorf("body missing x-tsid marker from the source spec; got %s", truncate(s, 200))
	}
}

func TestApiDocsAssetsServed(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node

	for _, asset := range []struct {
		path    string
		wantCT  string
	}{
		{"/api/docs/swagger-ui.css", "text/css"},
		{"/api/docs/swagger-ui-bundle.js", "javascript"},
	} {
		t.Run(asset.path, func(t *testing.T) {
			resp, err := http.Get(n.APIBase + asset.path)
			if err != nil {
				t.Fatalf("GET %s: %v", asset.path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode == 404 {
				t.Skipf("M4.5 impl pending: %s returns 404", asset.path)
			}
			if resp.StatusCode != 200 {
				t.Fatalf("%s: status %d", asset.path, resp.StatusCode)
			}
			ct := resp.Header.Get("Content-Type")
			if !strings.Contains(ct, asset.wantCT) {
				t.Errorf("%s: Content-Type %q does not contain %q", asset.path, ct, asset.wantCT)
			}
		})
	}
}

func TestApiDocsDisabledByConfig(t *testing.T) {
	t.Parallel()
	// Skip until we have a harness knob to write api.docs.enabled=false.
	// Will be wired in the impl commit when ApiSection.Docs lands in
	// cluster.NodeYAML.
	t.Skipf("M4.5 impl pending: harness does not yet write api.docs.enabled=false")
}

// truncate returns at most n chars of s, with an ellipsis if it was cut.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
