# Automated Blocklist Refresh

skoed automatically keeps blocklists up to date by periodically re-downloading and re-parsing each list URL. DNS filtering continues without interruption during a refresh.

## Default interval

The default refresh interval is **24 hours** from the last successful refresh. This applies to every blocklist unless overridden per list.

Configure the global default in `config.yaml`:

```yaml
blocklists:
  refresh_interval: "24h"
```

Accepted duration formats follow Go's `time.ParseDuration` syntax: `"24h"`, `"6h"`, `"30m"`, `"1h30m"`.

## Per-list override

Each blocklist can have its own refresh interval, set at creation time or updated later.

**At creation:**

```http
POST /api/v1/blocklists
Content-Type: application/json

{
  "name": "My fast-rotating list",
  "url": "https://example.com/blocklist.txt",
  "refresh_interval": "6h"
}
```

**After creation:**

```http
POST /api/v1/blocklists/<id>
Content-Type: application/json

{
  "refresh_interval": "30m"
}
```

A per-list `refresh_interval` takes precedence over the global default. Set it to `""` (empty string) to fall back to the global default.

## What happens during a refresh

1. skoed downloads the list from the configured URL.
2. The response body is parsed according to the list's format (hosts file, plain domain list, etc.).
3. The resulting domain set is atomically swapped into memory — the old set remains active until the new one is fully ready.
4. The blocklist's `last_refreshed_at` timestamp is updated.

There is no DNS service interruption. Queries that arrive during step 1–2 are evaluated against the previous domain set.

## Failure handling

If the download fails (network error, non-200 HTTP status) or the response cannot be parsed:

- The previous domain set stays active — no blocking rules are lost.
- The error message is recorded in the blocklist's `last_error` field, readable via `GET /api/v1/blocklists/<id>`.
- The next retry is scheduled after the normal `refresh_interval` — there is no immediate exponential back-off.

Example error response:

```json
{
  "id": "bl_abc123",
  "name": "My list",
  "last_refreshed_at": "2026-06-09T10:00:00Z",
  "last_error": "download failed: HTTP 503 from https://example.com/blocklist.txt",
  ...
}
```

## Manual refresh

To trigger an immediate refresh without waiting for the scheduled interval:

```http
POST /api/v1/blocklists/<id>/refresh
```

The endpoint returns `202 Accepted` immediately. The refresh runs asynchronously in the background.

Poll for completion:

```http
GET /api/v1/blocklists/<id>
```

The refresh is complete when `last_refreshed_at` advances past the timestamp recorded before you sent the manual refresh request. If it failed, `last_error` will be populated.

## Cluster behaviour

In a cluster, blocklist refresh is coordinated through the leader:

- The **leader** downloads the list URL and parses the domain set.
- The result is written to the replicated Raft log.
- **Followers** apply the log entry and update their in-memory domain sets.

Followers do **not** independently fetch the list URL. This avoids redundant external traffic and keeps all nodes consistent.

If the leader fails mid-refresh, the newly elected leader will schedule a fresh refresh at the next interval. No partial state is applied.
