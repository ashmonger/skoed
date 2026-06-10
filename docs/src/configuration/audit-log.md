# Audit log

The audit log records all administrative write operations and authentication
events so you can answer "who changed what, and when?" across a multi-node
cluster.

---

## What is logged

Every write that flows through the management API is recorded:

- **Configuration changes:** blocklist create/update/delete, allowlist add/remove, profile changes, schedule changes, local DNS changes, settings updates.
- **Authentication events:** login, password change, API token creation and revocation.
- **Category operations:** enable/disable category for a profile.

Read-only requests (`GET`) are not logged.

---

## Storage and replication

Audit entries are stored in a bbolt table and replicated across all cluster
nodes via the Raft log. This means every node holds the same complete
history and a single node failure does not cause audit data loss.

Retention is a rolling **90-day window**. Entries older than 90 days are
purged lazily in the same Raft commit that appends a new entry — there is
no background sweep goroutine.

---

## Querying the audit log

```
GET /api/v1/audit
```

**Query parameters:**

| Parameter | Description |
|-----------|-------------|
| `actor` | Filter by exact actor name (usually the admin username or token ID) |
| `action` | Prefix match on action string (e.g. `blocklist` matches `blocklist.create`, `blocklist.delete`, etc.) |
| `result` | `ok` or `error` |
| `limit` | Number of results per page (default 50, max 500) |
| `offset` | Offset for pagination |

Results are returned newest-first.

**Example — list the last 20 blocklist operations:**

```bash
curl -s -u admin:password \
  "http://skoed:8080/api/v1/audit?action=blocklist&limit=20"
```

**Example — show only failed operations:**

```bash
curl -s -u admin:password \
  "http://skoed:8080/api/v1/audit?result=error&limit=50"
```

---

## Response schema

```json
{
  "entries": [
    {
      "id":         "01j2abc...",
      "seq":        42,
      "timestamp":  "2026-06-10T14:30:00Z",
      "actor":      "admin",
      "action":     "blocklist.create",
      "target":     "bl-oisd-small",
      "result":     "ok",
      "error":      "",
      "diff":       "{\"name\":\"OISD small\",\"enabled\":true}",
      "node_id":    "skoed-01",
      "request_id": "req-9f3a..."
    }
  ],
  "total":  156,
  "limit":   50,
  "offset":   0
}
```

**Field descriptions:**

| Field | Description |
|-------|-------------|
| `id` | Unique entry identifier (UUID) |
| `seq` | Monotonic sequence number (never reused across the cluster lifetime) |
| `timestamp` | RFC 3339 timestamp of the operation |
| `actor` | Username or token ID that performed the action |
| `action` | Dot-separated action string, e.g. `blocklist.create`, `auth.login`, `profile.delete` |
| `target` | ID of the affected object (empty for operations with no specific target) |
| `result` | `ok` or `error` |
| `error` | Error message when `result` is `error`; empty otherwise |
| `diff` | JSON-encoded summary of the change (new value for creates, changed fields for updates) |
| `node_id` | ID of the node that received the request |
| `request_id` | HTTP request ID (correlates with access logs) |

---

## Web UI

Navigate to **Settings → Audit Log** in the Web UI to browse audit entries
in a filterable table. Columns can be sorted; clicking an entry expands the
`diff` field inline.

---

## Export

Download the full audit log as newline-delimited JSON (one JSON object per
line):

```bash
curl -s -u admin:password \
  "http://skoed:8080/api/v1/audit/export" \
  -o audit.ndjson
```

The export is not paginated — it returns all rows matching the optional
query parameters in one stream. Use the same `actor`, `action`, and
`result` filters as the list endpoint.

**Example — export only authentication events:**

```bash
curl -s -u admin:password \
  "http://skoed:8080/api/v1/audit/export?action=auth" \
  -o auth-events.ndjson
```
