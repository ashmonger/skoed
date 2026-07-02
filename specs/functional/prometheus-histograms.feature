Feature: Prometheus Histograms and Grafana Dashboard
  As an operator
  I want p50/p95/p99 DNS latency percentiles and per-upstream breakdown
  So that I can diagnose slow resolvers and import a ready-made Grafana dashboard

  Non-goals:
  - Built-in Grafana or Prometheus server (operator provides their own)
  - Pushing metrics (pull-only Prometheus scrape)
  - Per-client or per-domain label cardinality (unbounded label sets)

  Background:
    Given skoed is running with Prometheus metrics enabled
    And a Prometheus instance is scraping GET /api/v1/metrics

  @fsid:FS-DnsQueryDurationHistogram
  Scenario: dns_query_duration_seconds histogram has fine-grained buckets
    When DNS queries are answered
    Then the metric skoed_dns_query_duration_seconds{outcome="forwarded"} is a histogram
    And it has buckets at 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2 seconds
    And the outcome label covers: forwarded, blocked, cached, local

  @fsid:FS-DnsUpstreamDurationHistogram
  Scenario: Per-upstream resolver latency is tracked
    When a DNS query is forwarded to an upstream resolver
    Then skoed_dns_upstream_duration_seconds{upstream="<host>"} is incremented
    And the upstream label contains only scheme and host (no credentials or query strings)

  @fsid:FS-DhcpLeaseDurationHistogram
  Scenario: DHCP lease duration is tracked
    When a DHCP lease is granted or renewed
    Then skoed_dhcp_lease_duration_seconds{origin="dynamic"} is incremented
    And the bucket covering the lease duration is incremented

  @fsid:FS-GrafanaDashboardFile
  Scenario: A Grafana dashboard JSON is bundled with the release
    Given the skoed release is built
    Then the file packaging/grafana/skoed-dashboard.json exists
    And it is valid JSON importable into Grafana against a Prometheus datasource
    And it contains panels for: QPS, p95 latency, block rate, upstream latency
