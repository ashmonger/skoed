Feature: Prometheus Metrics Exporter
  As an operator running dblock as the household DNS server
  I want a /metrics endpoint that any Prometheus scraper understands
  So I can graph DNS throughput, cache health, cluster state and DHCP
  freshness from outside the process — without parsing logs.

  Background:
    Given dblock is running with the management API enabled

  @fsid:FS-MetricsEndpointAvailable
  Scenario: /metrics serves Prometheus text format
    When an HTTP GET hits /metrics
    Then the response is 200
    And the Content-Type starts with "text/plain"
    And the body contains the line "# HELP dblock_build_info"
    And the body contains the line "# TYPE dblock_build_info gauge"

  @fsid:FS-MetricsBuildInfo
  Scenario: Build info exposes version, commit, and go version
    When an HTTP GET hits /metrics
    Then the body contains a series named `dblock_build_info`
    And that series has labels `version`, `commit`, and `go`
    And its value is 1

  @fsid:FS-MetricsDnsQueryCounter
  Scenario: DNS query counters increment per outcome
    Given the cache is empty
    When the resolver answers 3 forwarded queries AND 2 blocked queries
    Then /metrics contains `dblock_dns_queries_total{outcome="forwarded"} 3`
    And /metrics contains `dblock_dns_queries_total{outcome="blocked"} 2`

  @fsid:FS-MetricsDnsQueryHistogram
  Scenario: DNS query duration histogram is populated
    When the resolver answers at least one query
    Then /metrics contains a series named `dblock_dns_query_duration_seconds_bucket`
    And /metrics contains `dblock_dns_query_duration_seconds_count`
    And /metrics contains `dblock_dns_query_duration_seconds_sum`

  @fsid:FS-MetricsCacheGauges
  Scenario: Cache gauges and counters reflect the existing M4.7 cache state
    Given the DNS cache is enabled with max_entries = 1024
    And 2 unique domains have been cached after a miss-then-hit
    When an HTTP GET hits /metrics
    Then /metrics contains `dblock_dns_cache_max_entries 1024`
    And /metrics contains `dblock_dns_cache_size 2`
    And /metrics contains `dblock_dns_cache_hits_total` with a value >= 2
    And /metrics contains `dblock_dns_cache_misses_total` with a value >= 2

  @fsid:FS-MetricsClusterGauges
  Scenario: Cluster role and Raft state are exposed as gauges
    Given a single-node dblock is running (it is its own leader)
    When an HTTP GET hits /metrics
    Then /metrics contains `dblock_cluster_node_role{role="leader"} 1`
    And /metrics contains `dblock_cluster_node_role{role="follower"} 0`
    And /metrics contains a series named `dblock_cluster_raft_term`
    And /metrics contains a series named `dblock_cluster_commit_index`
    And /metrics contains `dblock_cluster_members 1`
    And /metrics contains `dblock_cluster_reachable_members 1`

  @fsid:FS-MetricsDhcpGaugesWhenEnabled
  Scenario: DHCP gauges appear when the DHCP integration is enabled
    Given the DHCP integration is configured with at least one connector
    And the connector reports 5 active leases
    When an HTTP GET hits /metrics
    Then /metrics contains `dblock_dhcp_leases 5`
    And /metrics contains a series named `dblock_dhcp_last_poll_age_seconds`
    And /metrics contains a series named `dblock_dhcp_anomalies_open`

  @fsid:FS-MetricsOpenByDefault
  Scenario: /metrics requires no authentication by default
    Given the operator has not enabled node.api.metrics.require_auth
    When an HTTP GET hits /metrics with no Authorization header
    Then the response is 200
    And the body is a valid Prometheus exposition

  @fsid:FS-MetricsOptionalAuthGate
  Scenario: Operators can opt in to authenticated /metrics
    Given node.api.metrics.require_auth is set to true
    When an HTTP GET hits /metrics with no Authorization header
    Then the response is 401
    And the same request with valid Basic credentials returns 200

  Non-goals:
    - OpenTelemetry / OTLP support (Prometheus exposition only for M5.1).
    - Per-route HTTP-handler timings (scrape-interval averages are enough).
    - Recording rules / alerting rules shipped with dblock (operator's job).
    - Push-mode (Pushgateway) — pull-mode scraping only.
    - Cardinality-exploding labels (no per-client, per-domain labels).
