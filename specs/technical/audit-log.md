---
x-tsid: TS-AuditLog
x-fsid-links:
  - FS-AuditWriteRecorded
  - FS-AuditFailedWriteRecorded
  - FS-AuditReadsNotRecorded
  - FS-AuditListEndpointShape
  - FS-AuditFilterByActor
  - FS-AuditFilterByAction
  - FS-AuditReplicatesAcrossNodes
  - FS-AuditRequiresAuth
  - FS-AuditMetricsCounter
---

# TS-AuditLog — Replicated audit log

## Storage

bbolt bucket `audit` inside the existing cluster store. Keys are
big-endian 8-byte sequence numbers issued by Raft monotonically; values
are JSON-encoded `audit.Entry` rows. Newest-first reads use a reverse
cursor; pagination uses key-after rather than offset for stability.

A bucket-local `_meta` key holds the next-sequence value so it survives
restarts and snapshots without a separate counter store.

## Replicated through Raft

A new FSM command type `audit.append` carries the `audit.Entry` payload.
Apply persists the row and bumps the bucket-meta counter. Replication
ensures every follower sees the same `(seq, entry)` pair.

Because every mutation already runs through `LeaderForward`, the audit
write happens on the leader's request path — the follower that *received*
the user's request adds nothing. This keeps `node_id` accurate: the
audit entry's `node_id` is the leader at apply time.

## Entry shape

```json
{
  "id":         "01J9F7TQXM6E4R7…",      // 26-char ULID, monotonic
  "seq":        17321,                     // bucket-issued sequence
  "timestamp":  "2026-06-08T11:42:03Z",
  "actor":      "user:admin",              // M7: "token:<short-id>"
  "action":     "blocklist.create",        // <resource>.<verb>
  "target":     "blocklist:house-block",
  "result":     "ok",                      // "ok" | "error"
  "error":      "",                        // populated when result=error
  "diff":       "name=House block, source=hosts:file",
  "node_id":    "node-1",                  // leader at apply time
  "request_id": "01J9F7TQXMK4N3…"          // matches X-Request-ID header
}
```

## API surface

| Path             | Method | Auth     | Behaviour                                       |
|------------------|--------|----------|-------------------------------------------------|
| `/api/v1/audit`  | GET    | required | List newest-first. Filters: `actor`, `action` (prefix), `result`. Pagination: `limit` (1–500, default 50), `offset` (default 0). |

Response:

```json
{
  "entries": [...],
  "total":   12345,
  "limit":   50,
  "offset":  0
}
```

## Middleware

`audit.Middleware` wraps the **authenticated mutating** routes. It:

1. Reads the actor from Basic Auth (M7: from token table).
2. Captures method + path + JSON body (size-capped at 8 KB).
3. Calls the inner handler; observes status code + response body.
4. On any status that isn't 2xx-shaped, marks `result=error` and stashes
   the rejected payload summary in `diff`.
5. Posts an `audit.append` Raft command via the existing
   `cluster.Apply(...)` path. Errors during the audit write are logged
   but do NOT fail the user's request — a successful blocklist create
   that we couldn't audit is still better than a failed create.

Read-only verbs (GET, HEAD, OPTIONS) and unauthenticated routes are
skipped — see `auditExempt()`.

## Action taxonomy

Format: `<resource>.<verb>`. Verbs are `create`, `update`, `delete`,
`refresh`, `enable`, `disable`, `acknowledge`. Resources mirror the API
top-level path segment.

| API call                                                    | action                |
|-------------------------------------------------------------|-----------------------|
| `POST /api/v1/blocklists`                                   | `blocklist.create`    |
| `PATCH /api/v1/blocklists/{id}`                             | `blocklist.update`    |
| `DELETE /api/v1/blocklists/{id}`                            | `blocklist.delete`    |
| `POST /api/v1/blocklists/{id}/refresh`                      | `blocklist.refresh`   |
| `POST /api/v1/allowlist`                                    | `allowlist.create`    |
| `DELETE /api/v1/allowlist/{domain}`                         | `allowlist.delete`    |
| `POST /api/v1/local-dns`                                    | `local_dns.create`    |
| `PUT /api/v1/local-dns/{id}`                                | `local_dns.update`    |
| `DELETE /api/v1/local-dns/{id}`                             | `local_dns.delete`    |
| `PATCH /api/v1/settings`                                    | `settings.update`     |
| `POST /api/v1/auth/setup`                                   | `auth.setup`          |
| `PUT /api/v1/auth/password`                                 | `auth.password`       |
| `POST /api/v1/profiles` / PATCH / DELETE                    | `profile.*`           |
| `POST /api/v1/schedules` (+ binding) / PATCH / DELETE       | `schedule.*`          |
| `PATCH /api/v1/categories/{name}` + enable/disable          | `category.*`          |
| `POST /api/v1/clients/anomalies/{id}/acknowledge`           | `anomaly.acknowledge` |
| `POST /api/v1/cluster/tokens`                               | `cluster.token`       |
| `POST /api/v1/cluster/leadership/transfer`                  | `cluster.leadership`  |
| `DELETE /api/v1/cluster/nodes/{node_id}`                    | `cluster.remove_node` |
| `POST /api/v1/config/import`                                | `config.import`       |

The catalogue is hard-coded; unrecognised mutating paths fall back to
`<segment>.<method-lower>` so nothing slips through silently.

## Retention

Trimmed lazily on each `audit.append` apply: rows with `timestamp` older
than 90 days are deleted in the same Raft commit. No background sweeper
goroutine needed.

## Metrics

`skoed_audit_events_total{action}` — counter, incremented on every
successful `audit.append` apply. Wired through the M5.1 metrics surface
so it lives next to the rest of skoed's observability story.

## Web UI

New page **Settings → Audit log** (route `/settings/audit`):

- Top filter bar: actor, action (prefix), result, date range.
- Newest-first paginated table: timestamp, actor, action, target, result.
- Click-row expands to show `diff`, `node_id`, `request_id`.

Linked from the Settings landing page as a row with the same "panel"
visual it uses for "DNS cache" / "Authentication". Sidebar does NOT get
a new entry — audit lives under Settings and stays out of the primary
nav (operators check it once a quarter, not daily).
