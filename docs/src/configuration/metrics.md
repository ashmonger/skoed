# Prometheus metrics

skoed exposes a Prometheus-compatible `/metrics` endpoint. The endpoint is
served on the same port as the management API (default `8080`) and is
always registered — there is no separate `metrics.enabled` toggle.

Authentication for `/metrics` is independent of the management API auth and
is off by default, which suits most LAN deployments where the metrics port
is not exposed externally.

---

## Enabling metrics authentication

Set `node.api.metrics.require_auth: true` in `config.yaml` to gate the
endpoint behind HTTP Basic Auth using the management API credentials:

```yaml
node:
  api:
    metrics:
      require_auth: true
```

When `require_auth` is `false` (the default), any client that can reach
the API port can scrape `/metrics` without credentials.

---

## Metrics port

The metrics endpoint is served on `node.api_address` (default `:8080`) at
the path `/metrics`. There is no separate metrics port.

If you need to expose metrics on a different address from the management
API, run a reverse proxy in front of skoed.

---

## Key metrics

### DNS queries

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `skoed_dns_queries_total` | Counter | `outcome`, `transport` | Queries answered, by outcome and transport |
| `skoed_dns_query_duration_seconds` | Histogram | `outcome` | Wall-clock query latency |

`outcome` values: `forwarded`, `blocked`, `cached`, `local`

`transport` values: `udp` (plain DNS), `doh`, `dot`, `doh3`, `dnscrypt`

### DNS cache

| Metric | Type | Description |
|--------|------|-------------|
| `skoed_dns_cache_size` | Gauge | Current number of cached responses |
| `skoed_dns_cache_max_entries` | Gauge | Configured cache capacity |
| `skoed_dns_cache_hits_total` | Counter | Cumulative cache hits |
| `skoed_dns_cache_misses_total` | Counter | Cumulative cache misses |
| `skoed_dns_cache_evictions_total` | Counter | Cumulative LRU evictions |

### Cluster / Raft

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `skoed_cluster_node_role` | Gauge | `role` (`leader`, `follower`) | 1 if this node holds the role |
| `skoed_cluster_raft_term` | Gauge | — | Current Raft term |
| `skoed_cluster_commit_index` | Gauge | — | Latest committed log index |
| `skoed_cluster_members` | Gauge | — | Nodes in the Raft configuration |
| `skoed_cluster_reachable_members` | Gauge | — | Reachable members (always ≥ 1) |

### Blocklist refresh

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `skoed_blocklist_last_refresh_seconds` | Gauge | `id` | Unix timestamp of last refresh attempt |
| `skoed_blocklist_refresh_failures_total` | Counter | `id` | Cumulative refresh failures per blocklist |

### DHCP integration (when enabled)

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `skoed_dhcp_leases` | Gauge | `source`, `origin` | Current lease count by source and origin |
| `skoed_dhcp_anomalies_open` | Gauge | — | Unacknowledged anti-spoof anomalies |
| `skoed_dhcp_last_poll_age_seconds` | Gauge | `source` | Age of the last successful DHCP poll |
| `skoed_dhcp_poll_errors_total` | Counter | `source` | Cumulative poll failures |
| `skoed_dhcp_is_polling_leader` | Gauge | — | 1 on the active DHCP poller node |

### Audit log

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `skoed_audit_events_total` | Counter | `action` | Cumulative audit entries appended via Raft |

### Build info

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `skoed_build_info` | Gauge | `version`, `commit`, `go` | Always 1; labels carry build metadata |

---

## Grafana dashboard

A community Grafana dashboard is planned for publication at
[grafana.com/grafana/dashboards/](https://grafana.com/grafana/dashboards/)
(placeholder — not yet published). In the meantime, the metrics above map
directly to Prometheus queries you can use in your own dashboards.

---

## Example Prometheus scrape configuration

```yaml
scrape_configs:
  - job_name: skoed
    static_configs:
      - targets:
          - skoed-01:8080
          - skoed-02:8080
          - skoed-03:8080
    # Uncomment if require_auth is enabled:
    # basic_auth:
    #   username: admin
    #   password: your-password
```

For HTTPS deployments, add:

```yaml
    scheme: https
    tls_config:
      # ca_file: /etc/prometheus/skoed-ca.pem   # self-signed CA
      insecure_skip_verify: false
```
