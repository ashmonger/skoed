---
x-tsid: TS-PrometheusMetrics
x-fsid-links:
  - FS-MetricsEndpointAvailable
  - FS-MetricsBuildInfo
  - FS-MetricsDnsQueryCounter
  - FS-MetricsDnsQueryHistogram
  - FS-MetricsCacheGauges
  - FS-MetricsClusterGauges
  - FS-MetricsDhcpGaugesWhenEnabled
  - FS-MetricsOpenByDefault
  - FS-MetricsOptionalAuthGate
---

# TS-PrometheusMetrics — `/metrics` exporter

## Library

`github.com/prometheus/client_golang/prometheus` + `promhttp`. Both
are widely used, vendor-free, and already in the broader Go ecosystem
the project depends on (no new transitive concerns).

## Route

| Path        | Method | Auth                                                                 |
|-------------|--------|----------------------------------------------------------------------|
| `/metrics`  | GET    | none by default. Gated by Basic auth if `node.api.metrics.require_auth=true`. |

The route is registered on the **management** HTTP server (same listener
as `/api/v1/...` and `/api/docs/`), not on the DNS listener. When HTTPS
is enabled (M4.6), `/metrics` is reachable over the same scheme.

## Series catalogue

| Series                                           | Type      | Labels                  |
|--------------------------------------------------|-----------|-------------------------|
| `skoed_build_info`                              | gauge     | version, commit, go     |
| `skoed_dns_queries_total`                       | counter   | outcome, transport      |
| `skoed_dns_query_duration_seconds`              | histogram | outcome                 |
| `skoed_dns_cache_size`                          | gauge     | —                       |
| `skoed_dns_cache_max_entries`                   | gauge     | —                       |
| `skoed_dns_cache_hits_total`                    | counter   | —                       |
| `skoed_dns_cache_misses_total`                  | counter   | —                       |
| `skoed_dns_cache_evictions_total`               | counter   | —                       |
| `skoed_cluster_node_role`                       | gauge     | role                    |
| `skoed_cluster_raft_term`                       | gauge     | —                       |
| `skoed_cluster_commit_index`                    | gauge     | —                       |
| `skoed_cluster_members`                         | gauge     | —                       |
| `skoed_cluster_reachable_members`               | gauge     | —                       |
| `skoed_dhcp_leases`                             | gauge     | source                  |
| `skoed_dhcp_anomalies_open`                     | gauge     | —                       |
| `skoed_dhcp_last_poll_age_seconds`              | gauge     | source                  |
| `skoed_dhcp_poll_errors_total`                  | counter   | source                  |

### Label values

- `outcome` ∈ {`forwarded`, `blocked`, `cached`, `local`, `error`}
- `transport` ∈ {`udp`, `tcp`, `doh`, `dot`}
- `role` ∈ {`leader`, `follower`} — both series always present so
  PromQL doesn't have to deal with missing series.
- `source` ∈ {`kea`, `dnsmasq`, `http_json`}

### Cardinality budget

The total static cardinality is bounded by the cross-product of the
label sets above, ≤ 60 series per node. No high-cardinality labels
(client IP, domain, query name) are ever attached. M5.2 audit log
will reuse the same discipline.

## Histogram buckets

`skoed_dns_query_duration_seconds`: `0.001, 0.01, 0.1, 1, 5`. Five
buckets is enough to distinguish cache (`<1ms`), local (`<10ms`),
forwarder hit (`<100ms`), forwarder cold (`<1s`), and timeout-ish
(`<5s`).

## Config keys

```yaml
node:
  api:
    metrics:
      require_auth: false   # default; set true to gate /metrics on Basic auth
```

If absent, the default is `false` (open). The setting is local to the
node (not Raft-replicated) — operators may close `/metrics` on
internet-facing nodes and leave it open on LAN-only nodes.

## Implementation notes

- Use a **dedicated** `*prometheus.Registry`, not `prometheus.DefaultRegisterer`.
  Avoids accidentally exporting Go runtime metrics we don't want, and
  makes parallel tests deterministic.
- The DHCP series are only registered when the DHCP manager is
  instantiated (i.e. `node.dhcp.enabled=true`). Otherwise they are
  absent from the exposition — keep no-op gauges out of the catalogue
  for nodes that don't run DHCP integration.
- The cluster gauges are populated by a `prometheus.CollectorFunc`-style
  shim that pulls the latest snapshot from the cluster manager at
  collection time. No background goroutine.
- The DNS counters are incremented in the existing query handler at the
  same place the query-log row is written, so the two never diverge.
