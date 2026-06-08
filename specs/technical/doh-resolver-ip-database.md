---
x-tsid: TS-DohResolverDb
x-fsid-links:
  - FS-DohResolverDbListSnapshotShape
  - FS-DohResolverDbSnapshotJsonExport
  - FS-DohResolverDbAdminForceRefresh
  - FS-DohResolverDbRefreshRequiresAuth
  - FS-DohResolverDbScheduledDailyRefresh
  - FS-DohResolverDbLeaderOnlyScheduler
  - FS-DohResolverDbReplicatedAcrossNodes
  - FS-DohResolverDbStaleFlagAfterSevenDays
  - FS-DohResolverDbUpstreamFailureKeepsLastGoodSnapshot
  - FS-DohResolverDbRefreshRetriesWithBackoff
  - FS-DohResolverDbReadEndpointPublicOrAuthenticated
  - FS-DohResolverDbResolverEntryShape
  - FS-DohResolverDbMetricsCounters
---

# TS-DohResolverDb — curated DoH/DoT resolver IP database

## One snapshot, one source of truth

The database is a **single replicated document** named the *resolver
snapshot*. There is exactly one current snapshot per cluster; refreshes
overwrite it through Raft so every node converges to the same bytes
(FS-DohResolverDbReplicatedAcrossNodes). No history is retained — the
M6 firewall-rule generator only needs "what's the latest list of public
DoH/DoT resolver IPs".

The snapshot is the input to TS-FirewallRuleGenerator. Keeping it
behind its own endpoints (rather than smuggling it inside the existing
blocklist refresh loop) lets operators inspect it, refresh it on
demand, and reason about its freshness without touching their
filtering config.

## Endpoints

| Path                                        | Auth | Body | Returns                                            |
|---------------------------------------------|------|------|----------------------------------------------------|
| `GET  /api/v1/doh-resolvers`                | none | —    | snapshot summary (`snapshot_id`, `fetched_at`, `stale`, `resolvers[]`, `source_url`, `last_refresh_error`) |
| `GET  /api/v1/doh-resolvers/snapshot.json`  | none | —    | raw snapshot document (`Content-Type: application/json`) |
| `POST /api/v1/doh-resolvers/refresh`        | yes  | `{}` | 202 with `{"queued":true,"current_snapshot_id":"…"}` |

The two `GET` routes are intentionally public — the snapshot is a list
of well-known public IPs the operator could equally well scrape from
each provider's status page, and TS-FirewallRuleGenerator's web UI
needs to render it on the unauthenticated landing page when the
"closing the DoH gap" callout is shown (FS-DohResolverDbReadEndpointPublicOrAuthenticated).
The `POST /refresh` is admin-only because it triggers an outbound HTTP
call (FS-DohResolverDbRefreshRequiresAuth).

### `GET /api/v1/doh-resolvers` — response shape

```json
{
  "snapshot_id":        "2026-06-08T03:11:42Z-7af3",
  "source_url":         "https://example.org/curated-doh-feed.json",
  "fetched_at":         "2026-06-08T03:11:42Z",
  "stale":              false,
  "last_refresh_error": "",
  "resolvers": [
    {
      "id":         "cloudflare",
      "name":       "Cloudflare",
      "ipv4":       ["1.1.1.1", "1.0.0.1"],
      "ipv6":       ["2606:4700:4700::1111", "2606:4700:4700::1001"],
      "source_url": "https://developers.cloudflare.com/1.1.1.1/ip-addresses/"
    }
  ]
}
```

`stale` is `true` when `now - fetched_at > 7d`
(FS-DohResolverDbStaleFlagAfterSevenDays). The snapshot is still
served — TS-FirewallRuleGenerator falls back to the last good list so
operators never get an empty rule blob when the upstream feed has
been broken for a week.

`last_refresh_error` is the empty string after a successful refresh
and a short human-readable reason after a failure (e.g. `"upstream 503"`,
`"parse: missing 'resolvers' key"`). It is cleared on the next success
(FS-DohResolverDbRefreshRetriesWithBackoff).

### `GET /api/v1/doh-resolvers/snapshot.json`

Same body as `GET /api/v1/doh-resolvers` but explicitly served with
`Content-Type: application/json` and `Content-Disposition: inline; filename="doh-resolvers.json"`.
Intended for `curl | jq` pipelines and for the M6 web UI's "download
raw snapshot" link (FS-DohResolverDbSnapshotJsonExport).

### `POST /api/v1/doh-resolvers/refresh`

Queues an immediate refresh on the **leader**. If the caller hits a
follower, the standard `middleware.forward` redirects to the leader
(same pattern as every other mutating endpoint).

Response: `202 Accepted` with
`{"queued":true,"current_snapshot_id":"<snapshot_id at queue time>"}`.
The body documents which snapshot was current when the refresh was
queued, so polling callers can detect the new snapshot by comparing
`snapshot_id` (FS-DohResolverDbAdminForceRefresh).

If a refresh is already in flight, the duplicate request is collapsed
silently — same 202 body, no second outbound HTTP call.

### Error responses

| Status | When                                                      | Body                                       |
|--------|-----------------------------------------------------------|--------------------------------------------|
| `401`  | `POST /refresh` without valid Basic Auth                  | `{"error":"unauthorized"}`                 |
| `404`  | `GET /api/v1/doh-resolvers*` before the **first** refresh has completed and no seeded fallback is present | `{"error":"no snapshot yet"}` |
| `409`  | `POST /refresh` called on a non-leader that cannot resolve a leader (split brain) | `{"error":"no leader"}` |
| `500`  | bbolt read error / Raft apply error on the leader path    | `{"error":"<short reason>"}`               |
| `503`  | `POST /refresh` while shutting down                       | `{"error":"shutting down"}`                |

The "no snapshot yet" 404 only fires in the cold-boot window before
the first refresh has succeeded; once any snapshot has been written
(seeded or fetched), subsequent failures preserve the prior snapshot
and the read endpoints keep returning 200
(FS-DohResolverDbUpstreamFailureKeepsLastGoodSnapshot).

## Config

```yaml
node:
  doh_resolver_db:
    upstream_url:          "https://raw.githubusercontent.com/<curated>/doh-resolvers.json"
    refresh_interval_seconds: 86400   # 24 h, leader-only
    request_timeout_seconds:  20
    stale_after_seconds:      604800  # 7 d → GET marks stale=true
    seed_path:                "/usr/share/skoed/doh-resolvers.seed.json"  # optional
```

`upstream_url` is the single tracked feed (the curated list lives
upstream so skoed isn't in the business of maintaining the IP catalog
itself; see Non-goals in the functional spec). `seed_path`, when set
and present, is loaded once on first boot to populate the snapshot
before the first network refresh — guarantees the firewall-rule
generator works in air-gapped labs from the very first request.

`refresh_interval_seconds` is intentionally not per-snapshot — the
list is cluster-global, there are no per-blocklist semantics to
borrow.

## Worker — `internal/dohresolvers/scheduler.go`

A single goroutine per node started by the api.App boot path. Each
tick (10 s, same cadence as TS-AutomatedBlocklistRefresh):

1. `cluster.IsLeader()` → if false, return immediately
   (FS-DohResolverDbLeaderOnlyScheduler).
2. Read `meta.last_refresh_attempt_at` and `meta.last_refresh_success_at`
   from the in-memory snapshot.
3. If `now - last_refresh_attempt_at >= refresh_interval_seconds`
   (or this is the first tick after boot) → enqueue a refresh.
4. The refresh runs in a dedicated goroutine guarded by a `sync.Mutex`
   so concurrent ticks collapse to a single in-flight fetch.

### Refresh cycle

1. HTTP `GET upstream_url` with `request_timeout_seconds`.
2. On non-2xx or transport error → enter backoff: 30 s, 2 min, 10 min
   (three attempts total within the refresh window before giving up
   until the next scheduled tick) (FS-DohResolverDbRefreshRetriesWithBackoff).
3. Decode the body as a JSON list of resolver entries. Reject
   (parse-error path) if any entry violates the schema:
   - `id` matches `^[a-z0-9][a-z0-9-]{0,31}$`
   - `name` non-empty, ≤ 64 chars
   - each `ipv4` parses via `netip.ParseAddr` and `Is4()`
   - each `ipv6` parses via `netip.ParseAddr` and `Is6()`
   - **at least one** of `ipv4`/`ipv6` is non-empty
     (FS-DohResolverDbResolverEntryShape)
   - duplicate `id`s rejected
4. Build the new snapshot:
   - `snapshot_id = <fetched_at RFC3339>-<sha256(body)[:8]>`
   - `fetched_at  = time.Now().UTC().Format(RFC3339)`
   - `source_url  = upstream_url`
   - `last_refresh_error = ""`
5. Apply `CmdDohResolverSnapshotReplace` through Raft. Followers
   commit the same bytes (FS-DohResolverDbReplicatedAcrossNodes,
   FS-DohResolverDbScheduledDailyRefresh).
6. On total failure (all 3 attempts exhausted) → apply
   `CmdDohResolverRefreshFailure` carrying only
   `{last_refresh_attempt_at, last_refresh_error}`. The snapshot
   payload is **not** touched (FS-DohResolverDbUpstreamFailureKeepsLastGoodSnapshot).

### Why a dedicated Raft command (not `ImportFromM1`)

The existing `WithWriteLock`/`ImportFromM1` path replicates the entire
config blob per mutation. The resolver snapshot can reach a few
kilobytes per refresh; folding it into the full config snapshot would
balloon every unrelated config change and skew the bbolt → shadow YAML
mirror. Two narrow commands keep the resolver state out of
`config.yaml` entirely.

## Raft commands

| CommandKind                       | Payload                                                  | Effect |
|-----------------------------------|----------------------------------------------------------|--------|
| `CmdDohResolverSnapshotReplace`   | `{snapshot_id, fetched_at, source_url, resolvers[]}`     | Overwrites the entire snapshot; clears `last_refresh_error`; sets `last_refresh_attempt_at = last_refresh_success_at = fetched_at` |
| `CmdDohResolverRefreshFailure`    | `{attempted_at, error}`                                  | Updates only `last_refresh_attempt_at` + `last_refresh_error`; leaves the snapshot intact |

Both commands fire api.App's existing `onApply` callback so the
in-memory cache is refreshed atomically. The snapshot is a small,
self-contained document — there's no need for delta encoding.

## bbolt layout

New bucket added to `cluster.bbolt` (TS-ClusterStore §"replicated buckets"):

| Bucket                  | Key                       | Value                                | Notes |
|-------------------------|---------------------------|--------------------------------------|-------|
| `doh_resolvers`         | `snapshot`                | full snapshot JSON                   | One key — always overwritten |
| `doh_resolvers`         | `last_refresh_attempt_at` | RFC3339 string                       | Updated even on failure |
| `doh_resolvers`         | `last_refresh_error`      | short string (empty on success)      | Surfaced via API |

`meta/schema_version` is bumped by 1; the migration is a no-op (just
creates the new bucket). The shadow YAML writer (TS-ClusterStore
§"Shadow YAML") **excludes** `doh_resolvers/*` from the projection —
the snapshot is operational data, not user config, and including it
would balloon `config.yaml` every refresh.

## Stale fallback semantics

| State                                        | `GET /api/v1/doh-resolvers` returns               |
|----------------------------------------------|---------------------------------------------------|
| No snapshot, no seed                         | `404 {"error":"no snapshot yet"}`                 |
| Seed loaded, no successful network refresh   | `200 stale=false` (seed is treated as fresh)      |
| Snapshot < 7 d old                           | `200 stale=false`                                 |
| Snapshot ≥ 7 d old                           | `200 stale=true` (FS-DohResolverDbStaleFlagAfterSevenDays) |
| Snapshot exists, last attempt failed         | `200 stale=… last_refresh_error="…"` (snapshot preserved) |

## Read path

Handlers read directly from the api.App in-memory cache (refreshed by
`onApply`). The `resolvers[]` slice is large enough that re-decoding
JSON per request would be wasteful — the cache holds the parsed
`[]Resolver` and the handlers marshal it once per request. Same
pattern api.App already uses for the blocklist snapshot.

## Metrics

```
skoed_doh_resolver_refresh_total{outcome="success|failure"}  counter
skoed_doh_resolver_count                                     gauge
skoed_doh_resolver_last_refresh_timestamp_seconds            gauge (epoch seconds)
```

- Three series total (2 for the counter + 1 gauge each).
- `outcome` is the only label and is bounded to two values
  (FS-DohResolverDbMetricsCounters).
- The gauges are read directly from the in-memory snapshot by a
  custom Collector (same pattern as
  `skoed_blocklist_last_refresh_seconds` in
  TS-AutomatedBlocklistRefresh).
- Wired through `internal/metrics/metrics.go` with
  `ObserveDohResolverRefresh(outcome)`.

## Web UI

The snapshot itself is not the focus of an operator page — it's
plumbing for the M6 firewall-rule generator. The minimum surfacing is:

- **Dashboard alert card** (existing layout): if `stale=true` OR
  `last_refresh_error != ""`, surface a one-line warning with a
  "Refresh now" button that fires `POST /api/v1/doh-resolvers/refresh`.
- **Stats page** "Closing the DoH gap" callout (per the M6 UI task):
  shows `resolvers.length` and `fetched_at` for context next to the
  "Copy rules" generator.

A dedicated `/dashboard/doh-resolvers` page is intentionally NOT
shipped in M6 — operators interact with the snapshot indirectly via
the firewall-rule generator, and adding a table view risks implying
that the list is editable (which it is not; see Non-goals in the
functional spec).

## CLI

```
skoed doh-resolvers list                # GET /api/v1/doh-resolvers, table render
skoed doh-resolvers refresh             # POST /api/v1/doh-resolvers/refresh
skoed doh-resolvers export [--out F]    # GET /snapshot.json, prints or writes file
```

`list` reads the public endpoint (no auth needed if reachable);
`refresh` requires credentials. Same smart-routing convention as
`skoed domain test`.

## Implementation map

```
apps/skoed/internal/api/handlers/
  doh_resolvers.go               (new: ListSnapshot + SnapshotJSON + ForceRefresh)
apps/skoed/internal/api/
  app.go                         (extend: wire scheduler boot + cache field)
  audit_middleware.go            (extend: exempt GETs from audit; POST /refresh stays audited)
apps/skoed/internal/cluster/
  commands.go                    (add CmdDohResolverSnapshotReplace + CmdDohResolverRefreshFailure)
  fsm.go                         (apply handlers + onApply hook)
apps/skoed/internal/cluster/store/
  doh_resolvers.go               (new: bbolt read/write helpers)
  migrations.go                  (add bucket-create migration; bump schema_version)
apps/skoed/internal/dohresolvers/
  scheduler.go                   (new: leader-only refresh loop)
  fetch.go                       (new: HTTP GET + parse + validate + backoff)
  snapshot.go                    (new: Snapshot / Resolver types + JSON round-trip)
  seed.go                        (new: read seed_path on cold boot)
apps/skoed/internal/config/
  schema.go                      (extend: node.doh_resolver_db block)
apps/skoed/internal/metrics/
  metrics.go                     (ObserveDohResolverRefresh + Collector for gauges)
apps/skoed/internal/cli/
  cmd_doh_resolvers.go           (new: list / refresh / export verbs)
web/src/views/
  Dashboard.vue                  (extend: stale/error alert card)
  Stats.vue                      (extend: "Closing the DoH gap" callout footer)
tests/acceptance/
  doh_resolver_db_test.go        (all FSIDs)
```

## Posture

**Auth gating.**
- `GET /api/v1/doh-resolvers` and `GET /api/v1/doh-resolvers/snapshot.json`
  are **public** — registered outside `r.Group(a.BasicAuth, …)`. The
  bytes returned are public IPs of public DoH/DoT services that
  anyone could equally well retrieve from each provider's status
  page. Exposing them on an auth-required endpoint would force the
  M6 firewall-rule generator's landing-page surface to either
  duplicate auth or carry a stale local copy.
- `POST /api/v1/doh-resolvers/refresh` is **admin-only** (inside the
  `r.Group(a.BasicAuth, a.auditMiddleware)` group). It triggers an
  outbound HTTP call, so it's both a state-changing and a
  resource-consuming operation.
- Followers redirect mutating calls to the leader via the existing
  `middleware.forward` (same pattern as every other Raft-backed write).

**Audit behaviour.**
- The two `GET` endpoints are added to the `auditExempt` prefix list
  (same exemption used for `/api/v1/test-domain`): they're read-only
  and would otherwise flood the audit log with every dashboard
  refresh.
- `POST /refresh` IS audited. The audit entry records actor,
  source IP, and the `current_snapshot_id` at queue time so a chain
  of operator-initiated refreshes is reconstructible after the fact.

**Metric series introduced (cardinality bound).**
- `skoed_doh_resolver_refresh_total{outcome}` — 2 series
  (success, failure).
- `skoed_doh_resolver_count` — 1 series (no labels).
- `skoed_doh_resolver_last_refresh_timestamp_seconds` — 1 series.
- Total new series: **4**. Bounded; no per-resolver or per-provider
  label that could explode cardinality.

**SSRF / network-egress concern.**
- The scheduler issues a single outbound HTTP `GET` to
  `node.doh_resolver_db.upstream_url`. The URL is configured by the
  operator at deployment time, **not** taken from any API request
  body. There is no "set the upstream URL" mutation API in M6 — the
  only way to change `upstream_url` is to edit `config.yaml` and
  restart, which is an admin-on-the-box operation already trusted
  to do anything.
- The `POST /refresh` body is `{}`; there is no operator-provided URL
  to follow. `POST /refresh` is therefore **not** an SSRF amplifier:
  it only re-triggers a fetch the scheduler would have made on its
  own within 24 h.
- The fetch enforces `request_timeout_seconds` (20 s default), a
  bounded retry budget (3 attempts), and rejects bodies that don't
  parse against the strict resolver schema. The parsed snapshot is
  capped implicitly by the upstream feed shape; for safety the
  decoder uses a 1 MiB `io.LimitReader` to refuse pathological
  payloads.

**PII concern.**
- The snapshot contains only public resolver IPs and provider names —
  no client IPs, no query content, no operator identifiers. Both the
  bbolt bucket and the public read endpoints are PII-free by
  construction.
- The audit entry for `POST /refresh` carries the admin username and
  source IP (standard audit shape; same as every other
  admin-initiated mutation).
