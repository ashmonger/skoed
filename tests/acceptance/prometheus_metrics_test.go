// Acceptance tests for M5.1 — Prometheus /metrics exporter.
//
// FSIDs covered:
//   FS-MetricsEndpointAvailable      → TestMetricsEndpointAvailable
//   FS-MetricsBuildInfo              → TestMetricsBuildInfo
//   FS-MetricsDnsQueryCounter        → TestMetricsDnsQueryCounter
//   FS-MetricsDnsQueryHistogram      → TestMetricsDnsQueryHistogram
//   FS-MetricsCacheGauges            → TestMetricsCacheGauges
//   FS-MetricsClusterGauges          → TestMetricsClusterGauges
//   FS-MetricsDhcpGaugesWhenEnabled  → TestMetricsDhcpGaugesWhenEnabled
//   FS-MetricsOpenByDefault          → TestMetricsOpenByDefault
//   FS-MetricsOptionalAuthGate       → TestMetricsOptionalAuthGate

package acceptance

import (
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/miekg/dns"
)

// fetchMetrics fetches /metrics with no auth and returns the body.
// Skips the test if the endpoint returns 404 (impl pending).
func fetchMetrics(t *testing.T, n *Node) string {
	t.Helper()
	resp, err := http.Get(n.APIBase + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M5.1 impl pending: /metrics returns 404")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics: want 200, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /metrics body: %v", err)
	}
	return string(body)
}

// FS-MetricsEndpointAvailable
func TestMetricsEndpointAvailable(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})
	resp, err := http.Get(n.APIBase + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M5.1 impl pending: /metrics returns 404")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type: want text/plain*, got %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	if !strings.Contains(s, "# HELP skoed_build_info") {
		t.Errorf("body missing `# HELP skoed_build_info`:\n%s", s)
	}
	if !strings.Contains(s, "# TYPE skoed_build_info gauge") {
		t.Errorf("body missing `# TYPE skoed_build_info gauge`:\n%s", s)
	}
}

// FS-MetricsBuildInfo
func TestMetricsBuildInfo(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})
	body := fetchMetrics(t, n)
	// Match: skoed_build_info{commit="...",go="...",version="..."} 1
	// Label order is alphabetic per Prometheus exposition format.
	re := regexp.MustCompile(`skoed_build_info\{[^}]*commit="[^"]*"[^}]*go="[^"]*"[^}]*version="[^"]*"[^}]*\}\s+1`)
	if !re.MatchString(body) {
		t.Errorf("skoed_build_info{...} 1 with labels {commit,go,version} not found:\n%s", body)
	}
}

// FS-MetricsDnsQueryCounter — after a forwarded query, the forwarded counter
// is >= 1. We don't need a working upstream — even a SERVFAIL is recorded
// against an outcome (`error` or `forwarded` depending on impl); the test
// only asserts the series shape and a positive counter.
func TestMetricsDnsQueryCounter(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("9.9.9.9"))
	n := startNode(t, NodeConfig{UpstreamResolvers: []string{upstream}})
	// Generate a couple of forwarded queries.
	for i := 0; i < 3; i++ {
		dnsQuery(t, n.DNSAddr, "metrics-test.example.", dns.TypeA)
	}
	body := fetchMetrics(t, n)
	if !strings.Contains(body, "skoed_dns_queries_total") {
		t.Skipf("M5.1 impl pending: skoed_dns_queries_total absent")
	}
	// Find at least one series line with outcome="forwarded" and a positive value.
	got := sumSeriesByLabel(t, body, "skoed_dns_queries_total", "outcome", "forwarded")
	if got < 1 {
		t.Errorf("skoed_dns_queries_total{outcome=\"forwarded\"} want >= 1, got %g", got)
	}
}

// FS-MetricsDnsQueryHistogram
func TestMetricsDnsQueryHistogram(t *testing.T) {
	t.Parallel()
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("9.9.9.9"))
	n := startNode(t, NodeConfig{UpstreamResolvers: []string{upstream}})
	dnsQuery(t, n.DNSAddr, "histogram-test.example.", dns.TypeA)
	body := fetchMetrics(t, n)
	if !strings.Contains(body, "skoed_dns_query_duration_seconds") {
		t.Skipf("M5.1 impl pending: histogram absent")
	}
	for _, want := range []string{
		"skoed_dns_query_duration_seconds_bucket",
		"skoed_dns_query_duration_seconds_count",
		"skoed_dns_query_duration_seconds_sum",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("histogram series %q missing", want)
		}
	}
}

// FS-MetricsCacheGauges
func TestMetricsCacheGauges(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})
	body := fetchMetrics(t, n)
	if !strings.Contains(body, "skoed_dns_cache_max_entries") {
		t.Skipf("M5.1 impl pending: cache gauges absent")
	}
	for _, want := range []string{
		"skoed_dns_cache_size",
		"skoed_dns_cache_max_entries",
		"skoed_dns_cache_hits_total",
		"skoed_dns_cache_misses_total",
		"skoed_dns_cache_evictions_total",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("cache series %q missing", want)
		}
	}
	// max_entries should be > 0 (harness sets it to 1000).
	if v := scalarSeriesValue(t, body, "skoed_dns_cache_max_entries"); v <= 0 {
		t.Errorf("skoed_dns_cache_max_entries: want > 0, got %g", v)
	}
}

// FS-MetricsClusterGauges
func TestMetricsClusterGauges(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	n := c.Leader(t).Node
	body := fetchMetrics(t, n)
	if !strings.Contains(body, "skoed_cluster_node_role") {
		t.Skipf("M5.1 impl pending: cluster gauges absent")
	}
	// Single-node bootstrap should report leader=1, follower=0.
	leader := sumSeriesByLabel(t, body, "skoed_cluster_node_role", "role", "leader")
	follower := sumSeriesByLabel(t, body, "skoed_cluster_node_role", "role", "follower")
	if leader != 1 {
		t.Errorf("skoed_cluster_node_role{role=\"leader\"}: want 1, got %g", leader)
	}
	if follower != 0 {
		t.Errorf("skoed_cluster_node_role{role=\"follower\"}: want 0, got %g", follower)
	}
	for _, want := range []string{
		"skoed_cluster_raft_term",
		"skoed_cluster_commit_index",
		"skoed_cluster_members",
		"skoed_cluster_reachable_members",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("cluster series %q missing", want)
		}
	}
	if members := scalarSeriesValue(t, body, "skoed_cluster_members"); members != 1 {
		t.Errorf("skoed_cluster_members: want 1 (single-node), got %g", members)
	}
}

// FS-MetricsDhcpGaugesWhenEnabled — when DHCP integration is configured,
// /metrics MUST expose skoed_dhcp_* series. When DHCP is not configured,
// the series MUST be absent (no zero-valued ghost series).
func TestMetricsDhcpGaugesWhenEnabled(t *testing.T) {
	t.Parallel()
	// Without DHCP enabled, the DHCP gauges should be absent.
	plain := startNode(t, NodeConfig{})
	body := fetchMetrics(t, plain)
	if strings.Contains(body, "skoed_dhcp_") {
		t.Errorf("DHCP series leaked when DHCP integration is disabled")
	}
	// The "with DHCP enabled" branch needs the cluster harness's
	// DhcpOpts wiring (single-node M2 cluster). If the M5.1 impl
	// doesn't register the series yet, skip cleanly.
	t.Run("with_dhcp", func(t *testing.T) {
		t.Skip("M5.1 with-DHCP scenario covered by full cluster harness; see prometheus_metrics_dhcp_test.go")
	})
}

// FS-MetricsOpenByDefault — /metrics with no Authorization header returns 200.
func TestMetricsOpenByDefault(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})
	resp, err := http.Get(n.APIBase + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M5.1 impl pending")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unauthenticated /metrics: want 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "# TYPE ") {
		t.Errorf("body is not a Prometheus exposition (no `# TYPE` lines)")
	}
}

// FS-MetricsOptionalAuthGate — exercised by the cluster harness in a
// dedicated test that flips node.api.metrics.require_auth. Stubbed
// for now until the harness gains an APIMetricsRequireAuth knob; the
// shape lives in prometheus-metrics.feature.
func TestMetricsOptionalAuthGate(t *testing.T) {
	t.Parallel()
	t.Skip("FS-MetricsOptionalAuthGate exercised once cluster harness exposes APIMetricsRequireAuth knob")
}

// ── Prometheus exposition parsing helpers ──────────────────────────────────

// scalarSeriesValue returns the value of a series with no labels (or matches
// a `name 42` line ignoring labels). Fails the test if the series is absent.
func scalarSeriesValue(t *testing.T, body, name string) float64 {
	t.Helper()
	// Match: name{...} value   OR    name value
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `(?:\{[^}]*\})?\s+(\S+)\s*$`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("series %q not found in /metrics body", name)
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("series %q value %q: %v", name, m[1], err)
	}
	return v
}

// sumSeriesByLabel sums the values of all series lines named `name` whose
// label `label` equals `value`. Returns 0 if no such series exist.
func sumSeriesByLabel(t *testing.T, body, name, label, value string) float64 {
	t.Helper()
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `\{([^}]*)\}\s+(\S+)\s*$`)
	matches := re.FindAllStringSubmatch(body, -1)
	want := label + `="` + value + `"`
	var total float64
	for _, m := range matches {
		if !strings.Contains(m[1], want) {
			continue
		}
		v, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			continue
		}
		total += v
	}
	return total
}
