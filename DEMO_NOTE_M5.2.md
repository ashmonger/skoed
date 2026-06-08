# DEMO NOTE — M5.2 Audit Log

## Scope

Every state-changing API call against the management surface is now
recorded as one row in a Raft-replicated audit log. Operators can answer
"who turned cat:doh off at 2 AM?" by hitting `GET /api/v1/audit` from
any node, or by opening **Settings → Audit log** in the Web UI.

### Implemented

- **bbolt-replicated `audit` bucket.** Keys are big-endian 8-byte
  sequence numbers issued by Raft monotonically; values are the full
  `AuditRow` JSON. Newest-first reads use a reverse cursor.
- **New FSM command `audit.append`.** Carries actor / action / target /
  result / error / diff / node_id / request_id; assigns the next
  sequence at apply time so every node records the same `(seq, entry)`
  for the same Raft log entry.
- **`audit.Middleware` on the authenticated mutating group.** Captures
  the request body (8 KB cap), runs the inner handler, observes the
  status code, then posts an `audit.append` command. Audit-write
  failures are logged but never block the user — a successful blocklist
  create that we couldn't audit is still better than a failed one.
- **Action catalogue** mapping `<method> <chi-pattern>` → action string
  for every mutating route. Fallback for unrecognised routes:
  `<segment>.<lowercase-verb>` so nothing slips through silently.
- **Target derivation** — pulls `{id}`/`{name}`/`{domain}`/`{node_id}`
  from chi URL params first, then falls back to the request body's
  `id`/`name`/`domain`/`hostname`/`username` field. Yields targets like
  `blocklists:screenshot-bl-1` or `local-dns:nas.lab`.
- **Diff summary** — one-line `key=value` digest of the request body
  for human-readable scanning. Truncated at 256 chars.
- **Lazy 90-day retention.** Trim runs inside the same Raft commit as
  the append — no background sweeper goroutine.
- **`GET /api/v1/audit`** — paginated (default 50, max 500), newest-first,
  filters: `actor`, `action` (prefix), `result`. Auth-gated.
- **`dblock_audit_events_total{action}` Prometheus counter** — bumped
  on every successful append; lives next to the M5.1 series.
- **Web UI page** `/settings/audit` — filter bar (actor / action /
  result / page size), table with click-to-expand row showing `id`,
  `seq`, `diff`, `error`, `request_id`. Danger-toned left border on
  error rows. Linked from Settings → "Audit log" card.

### Acceptance tests

9 acceptance tests in `tests/acceptance/audit_log_test.go`, one per
FSID. **The replication scenario uses a real 3-node cluster** and
verifies every node sees the same entry within 2 s of the POST:

| FSID                              | Test                                    | Topology |
|-----------------------------------|-----------------------------------------|----------|
| FS-AuditWriteRecorded             | TestAuditWriteRecorded                  | 1 node   |
| FS-AuditFailedWriteRecorded       | TestAuditFailedWriteRecorded            | 1 node   |
| FS-AuditReadsNotRecorded          | TestAuditReadsNotRecorded               | 1 node   |
| FS-AuditListEndpointShape         | TestAuditListEndpointShape              | 1 node   |
| FS-AuditFilterByActor             | TestAuditFilterByActor (skip; needs M7) | —        |
| FS-AuditFilterByAction            | TestAuditFilterByAction                 | 1 node   |
| FS-AuditReplicatesAcrossNodes     | **TestAuditReplicatesAcrossNodes**      | **3 nodes** |
| FS-AuditRequiresAuth              | TestAuditRequiresAuth                   | 1 node   |
| FS-AuditMetricsCounter            | TestAuditMetricsCounter                 | 1 node   |

8/8 PASS + 1 intentional SKIP in Docker; full M1→M5.2 suite green
(~560 s).

### Screenshots

- `docs/screenshots/m5.2-audit-log.png` — populated audit log,
  filter bar, mixed OK/error rows
- `docs/screenshots/m5.2-audit-log-expanded.png` — first row expanded
  showing `id`, `seq`, `diff`, `request_id`
- `docs/screenshots/m5.2-settings-audit-card.png` — Settings landing
  with the new "Audit log" link card

### Not implemented (deferred / non-goals)

- Tamper-evident hash chain — Raft replication already provides
  per-entry consensus; tampering one node breaks Raft.
- Forwarding to external SIEM — operator pipes the API.
- Audit of read operations — writes only.
- Per-field JSON-patch diffs — `diff` is a human string, not RFC 6902.
- Configurable retention UI — 90-day default trim is non-configurable
  for M5.2.
- Sidebar nav entry — audit lives under Settings; operators check
  it once a quarter, not daily.

## Demo

```bash
# Boot a fresh single-node cluster
cd apps/dblock && make build
./dblock --config /tmp/m5.2/config.yaml &
curl -fsS -X POST http://127.0.0.1:8080/api/v1/auth/setup \
  -H 'content-type: application/json' \
  -d '{"username":"admin","password":"demopass123"}'

# Issue a couple of mutations
curl -u admin:demopass123 -X POST http://127.0.0.1:8080/api/v1/blocklists \
  -H 'content-type: application/json' \
  -d '{"id":"house-block","name":"House block","source":{"type":"manual"}}'
curl -u admin:demopass123 -X POST http://127.0.0.1:8080/api/v1/allowlist \
  -H 'content-type: application/json' \
  -d '{"domain":"example.com"}'

# Read the audit log
curl -u admin:demopass123 http://127.0.0.1:8080/api/v1/audit?limit=10 | jq

# Per-action Prometheus counter
curl -fsS http://127.0.0.1:8080/metrics | grep dblock_audit_events_total
# dblock_audit_events_total{action="allowlist.create"} 1
# dblock_audit_events_total{action="blocklist.create"} 1
```

## Next

M5.3 — Encrypted cluster mesh (mTLS for Raft peers + internal API).
