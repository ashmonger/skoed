# M13 Demo Note — Temporary Filtering Pause

## Implemented scope

### Global pause

Pauses filtering for **all clients** cluster-wide for a bounded duration.

```bash
# Pause for 5 minutes (300s)
curl -s -X POST http://localhost:8080/api/v1/filtering/pause \
  -H 'Content-Type: application/json' \
  -d '{"duration_seconds": 300, "reason": "software update"}'
# → {"active":true,"resumes_at":"...","reason":"software update"}

# Check status
curl -s http://localhost:8080/api/v1/filtering/pause

# Cancel early
curl -s -X DELETE http://localhost:8080/api/v1/filtering/pause
```

While paused, blocked domains resolve normally. DNS queries are still logged with `"pause_active": true` in the query log.

### Per-profile pause

Pauses filtering for clients matched by a specific profile, while other profiles remain fully enforced.

```bash
# Pause the "kids" profile for 1 hour
curl -s -X POST http://localhost:8080/api/v1/profiles/kids/pause \
  -H 'Content-Type: application/json' \
  -d '{"duration_seconds": 3600}'

# Cancel early
curl -s -X DELETE http://localhost:8080/api/v1/profiles/kids/pause
```

### Ceiling enforcement

The maximum pause duration is configurable via settings (default: 86400 s = 24 h). Requests exceeding the ceiling are rejected with 400:

```bash
# Set a 10-minute ceiling
curl -s -X PATCH http://localhost:8080/api/v1/settings \
  -H 'Content-Type: application/json' \
  -d '{"filtering": {"pause_max_seconds": 600}}'

# Disable the feature entirely (ceiling=0)
curl -s -X PATCH http://localhost:8080/api/v1/settings \
  -H 'Content-Type: application/json' \
  -d '{"filtering": {"pause_max_seconds": 0}}'
```

### Cluster behaviour

- Pause state is replicated via Raft; all nodes honour the same deadline.
- Pauses survive a restart: `ResumesAt` is persisted in bbolt and reloaded on startup.
- Only the leader accepts write requests; followers forward to the leader.

### Query log

Every DNS query during a pause is logged with `pause_active: true`:

```json
{
  "domain": "ads.example.com",
  "outcome": "forwarded",
  "pause_active": true
}
```

## Not implemented scope

- Per-client granularity (pause applies to the whole profile, not individual IPs).
- Scheduled/recurring pauses (always a one-shot deadline).
- Notifications when a pause starts or expires.
- UI controls (pause management is API-only; web dashboard is read-only).

## Limitations

- `pause_max_seconds = 0` disables the feature for all scopes (no per-profile opt-in when globally disabled).
- Clock skew between cluster nodes can cause a sub-second discrepancy in when the pause is enforced on each node.

## Acceptance tests

All 16 acceptance tests green: `tests/acceptance/filtering_pause_test.go`

Coverage: global pause (set/expire/cancel/idempotent/ceiling), per-profile pause (set/expire/cancel/multi-profile/interaction with global), restart survival, idempotent replace, ceiling enforcement, feature-disabled-when-ceiling-zero, query log marking.
