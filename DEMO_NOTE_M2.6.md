# Milestone 2.6 Demo Note — Web UI

**Date:** 2026-06-04
**Branch:** dblock-m2.6
**Image:** `dblock:m2.6` (multi-stage build from `apps/dblock/Dockerfile`,
~11 MB final, ~400 KB growth over M2 from the embedded SPA)

## Setup

```sh
docker run -d --name dblock-ui-demo \
  -v /tmp/dblock-ui-demo:/var/lib/dblock \
  -p 8090:8080 -p 5390:53/udp \
  dblock:m2.6 --config /var/lib/dblock/config.yaml

# First-run admin setup
curl -X POST http://localhost:8090/api/v1/auth/setup \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"demo1234"}'

# Browser
open http://localhost:8090/
```

Seeded with two blocklists, two allowlist entries, two local DNS records,
and 10 DNS queries to give the dashboard something to render.

## Screenshots captured (Chromium headless via Playwright)

Captured at 1400×900 in both Monokai-Solarized (light) and Monokai (dark).
All 10 routes rendered with realistic data — found in
`docs/screenshots/`:

| Route | Light | Dark |
|---|---|---|
| `/login` | `login-light.png` | `login-dark.png` |
| `/` (Dashboard) | `dashboard-light.png` | `dashboard-dark.png` |
| `/blocklists` | `blocklists-light.png` | `blocklists-dark.png` |
| `/allowlist` | `allowlist-light.png` | `allowlist-dark.png` |
| `/local-dns` | `local-dns-light.png` | `local-dns-dark.png` |
| `/query-log` | `querylog-light.png` | `querylog-dark.png` |
| `/stats` | `stats-light.png` | `stats-dark.png` |
| `/cluster` | `cluster-light.png` | `cluster-dark.png` |
| `/settings` | `settings-light.png` | `settings-dark.png` |
| `/account` | `account-light.png` | `account-dark.png` |

## Verified end-to-end

- ✅ `GET /` returns 200 + the SPA shell (`<div id="app">`).
- ✅ `GET /assets/*` serves the bundled JS / CSS / icons.
- ✅ `GET /api/v1/health` still answers (API path takes precedence over the
  static fallback handler).
- ✅ Dashboard shows 4 stat tiles (status=ok, mode=single-node, members=1/1,
  total queries=10), a query breakdown (5 blocked / 5 local), top blocked
  domains, and the cluster nodes table.
- ✅ Blocklists view lists the seeded entries with an inline enable toggle.
- ✅ Cluster view shows the leader node, raft address, API address, commit
  index, and a "Generate token" CTA.
- ✅ Query log table renders timestamps, client IPs, domains, type, outcome
  badges (blocked/local), and blocklist id where applicable.
- ✅ Both palettes render correctly; dark mode preserves contrast on every
  view; theme persists across reloads via localStorage.
- ✅ All 103 M1+M2 acceptance tests stay green after the SPA was embedded
  (one auth test updated to assert SPA-serves-200 rather than 401 catch-all
  semantics; that's the right new contract).

## Bundle stats

```
dist/assets/style.css      24 KB │ gzip   4.5 KB
dist/assets/app.js        145 KB │ gzip  56 KB
dist/assets/<10 view chunks>     │ 1-4 KB each gzipped
Total embedded              260 KB │ gzip   90 KB
```

Binary: 10.5 MB → 11.0 MB. Well inside the 25 MB roadmap budget.

## Limitations surfaced

- The screenshot pipeline runs against a single-node cluster. Multi-node UI
  validation (failover, leadership transfer flow from the Cluster view)
  needs to be redone once M3 wiring is in place since the UI ships before
  the M3 features that exercise more of it.
- The malware-URL blocklist seeded in the demo failed to populate domains
  (the URL was a fake example.org one). Not a UI bug — the Blocklist view
  correctly shows the entry with 0 domains.
- Live tail on `/query-log` polls every 2 s rather than using SSE. SSE
  becomes worthwhile in M3 once we're streaming per-client events.

Ready for UoR validation and merge.
