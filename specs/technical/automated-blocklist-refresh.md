---
x-tsid: TS-AutomatedBlocklistRefresh
x-fsid-links:
  - FS-AutoRefreshLeaderOnly
  - FS-AutoRefreshUpdatesAllNodes
  - FS-AutoRefreshStatusFields
  - FS-AutoRefreshFailureRecorded
  - FS-AutoRefreshStaleAlert
  - FS-AutoRefreshDisabledWhenZero
  - FS-AutoRefreshMetrics
---

# TS-AutomatedBlocklistRefresh — leader-only worker

## Config

```yaml
node:
  blocklist_refresh:
    default_interval_seconds: 86400   # 24 h cluster-wide default
    max_concurrent: 4                 # cap on parallel HTTP fetches
```

Per-blocklist override lives in the existing `config.Blocklist`
struct as `refresh_interval_seconds`. Zero ⇒ "don't auto-refresh"
(manual `POST /refresh` still works). Unset ⇒ inherit cluster default.

## Worker

`internal/refresh/scheduler.go` — single goroutine started on every
node. Each tick (10 s) the scheduler:

1. Asks `cluster.IsLeader()`. If false → return immediately.
2. Walks the current blocklist snapshot.
3. For each blocklist with `Source.Type=="url"`, computes
   `next_refresh_at = last_refresh_at + interval`. If `now >= next`,
   queues the blocklist for refresh.
4. Up to `max_concurrent` blocklists fetch in parallel.

Per-blocklist refresh:
1. HTTP GET the URL with a 30 s timeout.
2. Parse via the existing format-router (`internal/filter/parsers/`).
3. Compare new domain set vs current. If equal → status = `unchanged`,
   only the `last_refresh_at` is updated.
4. Else apply a new `CmdBlocklistUpsert` carrying the updated
   `Blocklist.Domains` + status metadata.

Failure path: HTTP error / parse error / timeout → record
`last_refresh_status=error`, `last_refresh_error=<short reason>`. The
prior `Domains` slice is preserved (we only Upsert on success).

## State on the blocklist

`config.Blocklist` gains three operator-facing fields:

```go
RefreshIntervalSeconds int    `yaml:"refresh_interval_seconds,omitempty"`
LastRefreshAt          string `yaml:"last_refresh_at,omitempty"`         // RFC3339
LastRefreshStatus      string `yaml:"last_refresh_status,omitempty"`     // ok|error|unchanged
LastRefreshError       string `yaml:"last_refresh_error,omitempty"`
```

These flow through the existing `CmdBlocklistUpsert` Raft command, so
every node sees the same fields without a new FSM verb.

## API

`GET /api/v1/blocklists` and `GET /api/v1/blocklists/{id}` already
return the full blocklist row — the new fields surface for free.

## Web UI

- **Blocklists table** gains a "Last refresh" column:
  `<status chip> <relative time>`. Error chip is danger-toned.
- **Edit modal** gains a "Refresh interval" field (seconds; 0 = manual
  only).
- **Dashboard** alert card: any blocklist with `LastRefreshAt` older
  than `2× refresh_interval_seconds` is listed.

## Metrics

```
skoed_blocklist_last_refresh_seconds{id="…"}   gauge (epoch seconds)
skoed_blocklist_refresh_failures_total{id="…"} counter
```

Wired through the existing `internal/metrics` package as a custom
Collector reading directly from the cluster store snapshot.
