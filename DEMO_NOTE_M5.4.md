# DEMO NOTE — M5.4 Automated Blocklist Refresh

## Scope

URL-source blocklists now refresh on a schedule. The leader runs the
worker; results replicate via the existing `CmdBlocklistUpsert` Raft
command so every node sees the same domain set + freshness metadata.

### Implemented

- **`internal/refresh/scheduler.go`** — single goroutine started on
  every node. Each tick (10 s prod, 1 s in tests via `DBLOCK_TEST_MODE`):
  - Asks `cluster.IsLeader()` — followers return immediately.
  - Walks the blocklist snapshot; for each URL-source blocklist whose
    `last_refresh_at + interval` has passed, queues it.
  - Up to `max_concurrent=4` blocklists fetch in parallel.
- **Per-blocklist refresh state** added to `config.Blocklist`:
  `RefreshIntervalSeconds`, `LastRefreshAt`, `LastRefreshStatus` (ok /
  error / unchanged), `LastRefreshError`. All flow through the
  existing FSM command — no new Raft verb.
- **Failure preservation**: on HTTP/parse error the prior `Domains`
  slice survives; only the `LastRefresh*` fields update. Operators
  never lose blocking while a feed is down.
- **`unchanged` short-circuit**: if the fetched set equals the prior
  set, status = `unchanged` and `Domains`/`LastUpdated` are NOT
  rewritten. Saves a Raft round-trip on every quiet poll.
- **interval=0** explicitly disables auto-refresh; manual
  `POST /api/v1/blocklists/{id}/refresh` still works.

### API

- `POST /api/v1/blocklists` accepts a new `refresh_interval_seconds`
  field.
- `GET /api/v1/blocklists` and `GET /api/v1/blocklists/{id}` surface
  the four new fields.

### Metrics

```
dblock_blocklist_last_refresh_seconds{id="…"}   gauge
dblock_blocklist_refresh_failures_total{id="…"} counter
```

Custom Collector reads the live snapshot at scrape time. Failure
counter is held in the scheduler's in-process map (cumulative since
process start; resets only on restart).

### Web UI

- **Blocklists table** gains an "Auto-refresh" column. Each row
  shows a status chip (`ok` / `unchanged` / `error`) + the interval
  (`every 24h`, `every 5s`) + last refresh relative timestamp.
  Inline blocklists show "—"; URL blocklists with interval=0 show
  "manual".
- **Create-blocklist modal** gains a "Auto-refresh interval
  (seconds)" numeric field, visible only for URL-source blocklists.
  Help text: "0 = manual only. Typical: 86400 (24 h). Leader-only
  fetches."
- **Dashboard alert card** ("Stale blocklists") lists any
  URL+interval>0 blocklist whose `last_refresh_at` is older than
  2× the interval. Polls every 60 s; warning-toned.

### Acceptance tests

6 acceptance tests in `tests/acceptance/blocklist_refresh_test.go`:

| FSID                              | Test                                | Topology |
|-----------------------------------|-------------------------------------|----------|
| FS-AutoRefreshLeaderOnly          | **TestAutoRefreshLeaderOnly**       | **3 nodes** |
| FS-AutoRefreshUpdatesAllNodes     | **TestAutoRefreshUpdatesAllNodes**  | **3 nodes** |
| FS-AutoRefreshStatusFields        | TestAutoRefreshStatusFields         | 1 node   |
| FS-AutoRefreshFailureRecorded     | TestAutoRefreshFailureRecorded      | 1 node   |
| FS-AutoRefreshDisabledWhenZero    | TestAutoRefreshDisabledWhenZero     | 1 node   |
| FS-AutoRefreshMetrics             | TestAutoRefreshMetrics              | 1 node   |

The leader-only test uses an `httptest.Server` + atomic hit counter:
in a 6-second window with a 2-second refresh interval, the expected
hit count is ~3 (leader only). The test asserts `hits ≤ 8` so any
follower also fetching would push the number well past the bound.

All 6 PASS. Full M1→M5.4 suite green in Docker (~599 s).

FS-AutoRefreshStaleAlert is a UI scenario — covered by visual
inspection via the `m5.4-dashboard-stale-alert.png` screenshot.

### Screenshots

- `docs/screenshots/m5.4-blocklists-table.png` — table with
  Auto-refresh column populated (3 URL blocklists with different
  intervals).
- `docs/screenshots/m5.4-create-modal.png` — create-blocklist modal
  with the new "Auto-refresh interval" field visible.
- `docs/screenshots/m5.4-dashboard-stale-alert.png` — Dashboard with
  the stale-blocklist warning card.

### Not implemented (deferred / non-goals)

- Per-rule deltas (UI shows count delta only)
- Push-style refresh hooks
- Multi-source merge (one URL per blocklist)
- GPG signature verification — M5.4.1
- Backoff on consecutive failures — constant interval for v1

## Demo

```bash
# Boot a fresh single-node cluster
./dblock --config /tmp/m5.4/config.yaml &
curl -fsS -X POST http://127.0.0.1:8080/api/v1/auth/setup \
  -H 'content-type: application/json' \
  -d '{"username":"admin","password":"demopass123"}'

# Create a URL blocklist with a 5-minute refresh interval.
curl -u admin:demopass123 -X POST http://127.0.0.1:8080/api/v1/blocklists \
  -H 'content-type: application/json' \
  -d '{
    "id": "hagezi-pro",
    "name": "Hagezi Pro",
    "source": {"type":"url","url":"https://raw.githubusercontent.com/hagezi/dns-blocklists/main/hosts/pro.txt","format":"hosts"},
    "refresh_interval_seconds": 300
  }'

# Inspect the refresh state.
curl -fsS -u admin:demopass123 http://127.0.0.1:8080/api/v1/blocklists/hagezi-pro | jq
# { ..., "last_refresh_at":"2026-06-08T12:34:56Z",
#        "last_refresh_status":"ok",
#        "refresh_interval_seconds":300 }

# Metrics.
curl -fsS http://127.0.0.1:8080/metrics | grep blocklist_
# dblock_blocklist_last_refresh_seconds{id="hagezi-pro"} 1.7822728e+09
# dblock_blocklist_refresh_failures_total{id="hagezi-pro"} 0
```

## Next

M5.5 — Native packaging (.deb + Proxmox LXC).
