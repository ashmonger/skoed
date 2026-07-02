# M39 — Prometheus Histograms + Grafana Dashboard

## Implemented

- **`skoed_dns_query_duration_seconds`** — histogram of end-to-end DNS query latency (client receives response) with standard buckets: 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.0 seconds. Labels: `result` (allowed / blocked / nxdomain).
- **`skoed_dns_upstream_duration_seconds`** — histogram of upstream forwarder round-trip latency. Label: `upstream` (the upstream address, sanitized — no credentials, no query strings). Only fires in forwarding mode; not emitted in recursive mode.
- **`skoed_dhcp_lease_duration_seconds`** — histogram of offered DHCP lease durations. Buckets: 300, 3600, 14400, 43200, 86400, 172800 seconds.
- **Existing counters preserved** — all previously shipped counters (`skoed_dns_queries_total`, `skoed_dhcp_leases_total`, etc.) remain unchanged.
- **Grafana dashboard** — `packaging/grafana/skoed-dashboard.json`; importable as a standard Grafana dashboard JSON. Panels: DNS QPS by result, DNS query latency p50/p95/p99, upstream latency by server, DHCP lease duration distribution, active leases gauge, blocked domains counter.
- **Acceptance tests** — `TestDnsQueryDurationHistogramBuckets`, `TestDnsUpstreamDurationHistogram`, `TestGrafanaDashboardFile` (all in `prometheus_histograms_test.go`).

## Not Implemented

- **Prometheus scrape config** — no bundled `prometheus.yml`; operators configure scrape targets manually.
- **Grafana provisioning file** — dashboard must be imported manually via the Grafana UI; no auto-provisioning YAML is shipped.
- **Per-profile histogram labels** — latency histograms are global; no per-profile breakdown.
- **Alert rules** — no bundled Prometheus alerting rules for latency SLOs.

## Limitations

- `skoed_dns_upstream_duration_seconds` is only emitted when skoed runs in forwarding mode (`upstream_dns` configured). On the reference Proxmox cluster (recursive mode), this metric will be absent from `/metrics`; this is expected behavior and is covered by acceptance tests that spin up a forwarding-mode node.
- The Grafana dashboard JSON targets a Prometheus datasource named `"Prometheus"` (the default). Installations with a different datasource name must update the `datasource` field after import.
- Histogram bucket boundaries are fixed at build time; they cannot be adjusted via configuration.
