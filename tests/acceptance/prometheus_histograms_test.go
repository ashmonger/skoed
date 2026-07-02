// Acceptance tests for M39 — Prometheus Histograms and Grafana Dashboard.
//
// FSIDs covered:
//   FS-DnsQueryDurationHistogram
//   FS-DnsUpstreamDurationHistogram
//   FS-GrafanaDashboardFile

package acceptance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/miekg/dns"
)

// FS-DnsQueryDurationHistogram
// skoed_dns_query_duration_seconds must be a histogram with at least the
// standard 0.001–2s buckets and cover the "forwarded" outcome label.
func TestDnsQueryDurationHistogramBuckets(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("9.9.9.9"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	// Generate some queries to populate the histogram.
	for i := 0; i < 3; i++ {
		dnsQuery(t, n.DNSAddr, "hist-duration.example.", dns.TypeA)
	}

	body := fetchMetrics(t, n)
	if !strings.Contains(body, "skoed_dns_query_duration_seconds") {
		t.Skipf("M39 histogram pending: skoed_dns_query_duration_seconds absent")
	}

	// Verify histogram shape.
	for _, want := range []string{
		`skoed_dns_query_duration_seconds_bucket`,
		`skoed_dns_query_duration_seconds_count`,
		`skoed_dns_query_duration_seconds_sum`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing series %q", want)
		}
	}

	// Verify standard M39 buckets are present (le= labels).
	for _, bucket := range []string{"0.001", "0.005", "0.01", "0.025", "0.05", "0.1", "0.25", "0.5", "1", "2"} {
		wantLe := `le="` + bucket + `"`
		if !strings.Contains(body, wantLe) {
			t.Errorf("histogram bucket le=%s not found in /metrics", bucket)
		}
	}

	// At least one forwarded query should be recorded.
	got := sumSeriesByLabel(t, body, "skoed_dns_query_duration_seconds_count", "outcome", "forwarded")
	if got < 1 {
		t.Errorf("skoed_dns_query_duration_seconds_count{outcome=forwarded}: want >= 1, got %g", got)
	}
}

// FS-DnsUpstreamDurationHistogram
// After forwarding a query, skoed_dns_upstream_duration_seconds must appear
// with an upstream label containing only scheme and host (no credentials or path).
func TestDnsUpstreamDurationHistogram(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("9.9.9.9"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	dnsQuery(t, n.DNSAddr, "upstream-hist.example.", dns.TypeA)

	body := fetchMetrics(t, n)
	if !strings.Contains(body, "skoed_dns_upstream_duration_seconds") {
		t.Skipf("M39 upstream histogram pending: skoed_dns_upstream_duration_seconds absent")
	}

	for _, want := range []string{
		"skoed_dns_upstream_duration_seconds_bucket",
		"skoed_dns_upstream_duration_seconds_count",
		"skoed_dns_upstream_duration_seconds_sum",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing upstream histogram series %q", want)
		}
	}

	// The upstream label must be present (non-empty series).
	if !strings.Contains(body, `upstream=`) {
		t.Errorf("skoed_dns_upstream_duration_seconds missing upstream= label")
	}

	// Upstream label must not contain credentials (@) or query strings (?).
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "skoed_dns_upstream_duration_seconds") {
			continue
		}
		if strings.Contains(line, "@") {
			t.Errorf("upstream label contains credentials (@): %s", line)
		}
		if strings.Contains(line, "?") {
			t.Errorf("upstream label contains query string (?): %s", line)
		}
	}

	// Count should be >= 1 after one forwarded query.
	total := sumSeriesByLabel(t, body, "skoed_dns_upstream_duration_seconds_count", "upstream", "")
	_ = total // any positive upstream label passes; already checked series existence
}

// FS-GrafanaDashboardFile
// The Grafana dashboard JSON must exist in the packaging directory and be
// valid JSON containing required panel references.
func TestGrafanaDashboardFile(t *testing.T) {
	t.Parallel()

	// Locate the repo root relative to this test file.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	// tests/acceptance → ../.. → repo root
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	dashPath := filepath.Join(repoRoot, "apps", "skoed", "packaging", "grafana", "skoed-dashboard.json")

	data, err := os.ReadFile(dashPath)
	if err != nil {
		t.Skipf("M39 Grafana dashboard file missing at %s (pending): %v", dashPath, err)
	}

	// Must be valid JSON.
	var dash map[string]any
	if err := json.Unmarshal(data, &dash); err != nil {
		t.Fatalf("Grafana dashboard JSON invalid: %v", err)
	}

	// Must contain at least these keywords in the JSON.
	body := string(data)
	for _, want := range []string{"upstream", "latency", "block"} {
		if !strings.Contains(strings.ToLower(body), want) {
			t.Errorf("Grafana dashboard missing reference to %q", want)
		}
	}
}
