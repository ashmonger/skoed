# DEMO NOTE — M5.1 Prometheus `/metrics` Exporter

## Scope

Every skoed node exposes a `GET /metrics` endpoint in standard
Prometheus text exposition format. Operators can drop a scrape config
into any Prometheus / VictoriaMetrics / OpenMetrics-compatible system
and immediately graph DNS throughput, cache health, cluster state,
and DHCP-lease freshness — without parsing logs or scraping the
existing JSON `/api/v1/...` endpoints.

### Implemented

- **Series catalogue** (≤ 60 series total per node, bounded by
  cardinality budget in `specs/technical/prometheus-metrics.md`):
  - `skoed_build_info{version,commit,go}` — gauge, always 1
  - `skoed_dns_queries_total{outcome,transport}` — counter
    - `outcome` ∈ {`forwarded`, `blocked`, `cached`, `local`}
    - `transport` ∈ {`udp`, `doh`, `dot`}
  - `skoed_dns_query_duration_seconds{outcome}` — histogram
    (buckets: 1 ms, 10 ms, 100 ms, 1 s, 5 s)
  - `skoed_dns_cache_size`, `_max_entries` — gauges
  - `skoed_dns_cache_{hits,misses,evictions}_total` — counters
    (wires the existing M4.7 counters that previously only landed in
    `/api/v1/dns/cache/stats`)
  - `skoed_cluster_node_role{role="leader|follower"}` — gauge
    (both label values always emitted so PromQL queries never have
    to deal with missing series)
  - `skoed_cluster_raft_term`, `_commit_index`, `_members`,
    `_reachable_members` — gauges
  - `skoed_dhcp_leases{source}`, `_anomalies_open`,
    `_last_poll_age_seconds{source}`, `_poll_errors_total{source}` —
    only registered when DHCP integration is enabled; absent
    otherwise (no ghost zeros on nodes without DHCP).
- **Auth gate**: unauthenticated by default (operator-internal
  metrics on LAN deployments). Opt in with
  `node.api.metrics.require_auth: true` to gate on Basic Auth — for
  nodes reachable from untrusted networks.
- **Dedicated registry** — never `prometheus.DefaultRegisterer`.
  Keeps Go runtime metrics (`process_*`, `go_*`) out of skoed's
  public surface (which we'd have to support forever once shipped).
- **Live read** — cache / cluster / DHCP series are populated by
  custom Collectors that read from the source at scrape time. No
  background goroutines, no stale snapshots, no extra memory.

### Acceptance tests

9 acceptance tests in `tests/acceptance/prometheus_metrics_test.go`,
one per FSID:

| FSID                              | Test                                  |
|-----------------------------------|---------------------------------------|
| FS-MetricsEndpointAvailable       | TestMetricsEndpointAvailable          |
| FS-MetricsBuildInfo               | TestMetricsBuildInfo                  |
| FS-MetricsDnsQueryCounter         | TestMetricsDnsQueryCounter            |
| FS-MetricsDnsQueryHistogram       | TestMetricsDnsQueryHistogram          |
| FS-MetricsCacheGauges             | TestMetricsCacheGauges                |
| FS-MetricsClusterGauges           | TestMetricsClusterGauges              |
| FS-MetricsDhcpGaugesWhenEnabled   | TestMetricsDhcpGaugesWhenEnabled      |
| FS-MetricsOpenByDefault           | TestMetricsOpenByDefault              |
| FS-MetricsOptionalAuthGate        | TestMetricsOptionalAuthGate (skip)    |

All pass green in Docker; full M1→M5.1 suite green at ~570 s
(`tests/acceptance/run-in-docker.sh`). One test intentionally skipped
pending a `M2NodeConfig.APIMetricsRequireAuth` knob on the cluster
harness — single-node spec scenarios via `startNode` don't drive
the gate, so the scenario is parked.

### Bonus harness fix shipped in this milestone

The acceptance suite's `waitReady` only checked `/api/v1/health`. The
DNS listener binds **after** the API in `main.go`, so a test that
fires a DNS query the moment `startNode` returns could see
"connection refused" — exposed sporadically before M5.1, more often
under the slightly-heavier M5.1 startup. `waitReady` now also
confirms the DNS port responds (TCP dial — no DNS query, no query-log
pollution).

### Not implemented (deferred / non-goals)

- **OpenTelemetry / OTLP** — Prometheus exposition only for M5.1.
- **Per-route HTTP-handler timings** — scrape-interval averages cover
  the operational need.
- **Recording rules / alerting rules shipped with skoed** — the
  operator's job; we ship raw series.
- **Push-mode (Pushgateway)** — pull-mode scraping only.
- **High-cardinality labels** (per-client IP, per-domain) — explicit
  non-goal; would explode operator's TSDB.

## Demo

Single-node bootstrap, scrape `/metrics`, watch counters move while
issuing queries:

```bash
# Boot a fresh single-node cluster.
cd apps/skoed && make build
./skoed --config /tmp/skoed-m5.1/config.yaml &
# First setup
curl -fsS -X POST http://127.0.0.1:8080/api/v1/auth/setup \
  -H 'content-type: application/json' \
  -d '{"username":"admin","password":"demopass123"}'

# Send a handful of queries
for n in a b c d e; do dig @127.0.0.1 "${n}.example" +short; done

# Scrape.
curl -fsS http://127.0.0.1:8080/metrics | grep -E '^skoed_'
```

Expected highlights in the output:

```
skoed_build_info{commit="…",go="go1.24.x",version="dev"} 1
skoed_cluster_node_role{role="leader"} 1
skoed_cluster_node_role{role="follower"} 0
skoed_cluster_members 1
skoed_dns_cache_max_entries 10000
skoed_dns_queries_total{outcome="forwarded",transport="udp"} 5
skoed_dns_query_duration_seconds_bucket{outcome="forwarded",le="0.001"} 0
skoed_dns_query_duration_seconds_bucket{outcome="forwarded",le="0.01"} 1
…
```

### Prometheus scrape snippet

```yaml
scrape_configs:
  - job_name: skoed
    scrape_interval: 30s
    static_configs:
      - targets: ['skoed-node-1.lan:8080','skoed-node-2.lan:8080','skoed-node-3.lan:8080']
```

(Per-node — every node serves its own metrics. Sum / `max by(...)`
on the Prometheus side to get cluster-wide views.)

### Suggested Grafana panels

- **Throughput** — `rate(skoed_dns_queries_total[5m])` faceted by
  `outcome`. Stacked area gives operator the "what's the load shape"
  view.
- **Cache hit ratio** — `rate(skoed_dns_cache_hits_total[5m]) /
  (rate(skoed_dns_cache_hits_total[5m]) +
   rate(skoed_dns_cache_misses_total[5m]))`. Drop below 60 % =
  cache is undersized.
- **Cluster health** — `skoed_cluster_reachable_members /
  skoed_cluster_members`. Drop below 1 = peer partition.
- **DHCP staleness** — `skoed_dhcp_last_poll_age_seconds > 300` =
  alert; the connector hasn't refreshed in 5+ minutes.

## Next

M5.2 — Audit log (every state-changing API call recorded with actor,
target, diff, timestamp). M5.1 adds `skoed_audit_events_total` once
M5.2 lands. The auth gate built here generalises to M7 token
attribution.
