# M34.5 — Configurable Session Timeout

## Implemented

### Session Timeout Setting
- `PATCH /api/v1/settings` — `auth.session_timeout_seconds` field accepted cluster-wide via Raft
  - Valid range: 1–604800 s (1 second to 7 days)
  - Preset UI options: 30 min / 1 h / 4 h / 8 h / 24 h / 7 d
  - Default: 28800 s (8 hours) — returned when stored value is 0 (i.e. on a fresh cluster)
- `GET /api/v1/settings` — `auth.session_timeout_seconds` returned in `auth` sub-object

### Session TTL Enforcement
- New sessions (POST /api/v1/auth/login) use the configured TTL at creation time
- `CreateSession` reads live cluster config at login; tokens created before a timeout change retain their original TTL
- Tokens expire in-memory on the node they were issued on (node-local session store — same as pre-M34.5)

### Web UI
- Settings page "Auth" section: session timeout dropdown (6 preset durations)
- Change is saved immediately via PATCH; current value reflected on reload

### Infrastructure Fix (SnapshotThreshold)
- `SnapshotThreshold` lowered from 8192 → 64; `SnapshotInterval` set to 30 s in `cluster/raft.go`
- Root cause: hashicorp/raft v1.7.3 does not persist `lastApplied`. With the old 8192 threshold and ~663 log entries, no snapshot was ever taken. Every restart replayed all 663 entries (~30 s). The API returned stale mid-replay values when polled during replay. With threshold=64, restarts replay ≤64 entries (near-instant).

## Not Implemented / Limitations

- No per-user session timeout (single cluster-wide value only)
- Tokens do not gain a new TTL when the timeout is changed; only new logins reflect the updated value
- Session store is node-local in-memory; a token issued on node A is not valid on node B without re-login (pre-existing limitation, not introduced by M34.5)
- No UI feedback showing when an existing session will expire

## Acceptance Tests

19/19 pass: full Proxmox 3-node validation (CHK-01 through CHK-09c)
- Default value, PATCH, validation boundaries, expiry enforcement, replication, follower forwarding, restart persistence (all 3 nodes)
