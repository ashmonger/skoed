---
x-tsid: TS-LeaseRepl
x-fsid-links:
  - FS-LeaseReplOnlyLeaderPolls
  - FS-LeaseReplFollowersServeReplicatedSnapshot
  - FS-LeaseReplLeasesEndpointExposesSnapshot
  - FS-LeaseReplSourceEndpointReportsLeader
  - FS-LeaseReplLeaderFailoverResumesPolling
---

# TS-LeaseRepl — Raft-replicated DHCP lease cache

## The shape of the change

M3.6 left the DHCP manager as a **node-local poller**: every skoed
node ran its own `dhcp.Manager`, woke the configured connector every
refresh interval, and held leases in `m.byIP` in memory. The recommended
deployment ("every node points at the same source") gave cluster-wide
consistency *by configuration discipline* — but the DHCP server was
woken N times per refresh.

M6.5 inverts this:

```
   leader                              followers
   ──────                              ─────────
   tick ──► connector.Fetch()          (idle)
              │
              ▼
        normalize → diff
              │
              ▼
      raft.Apply(CmdLeasesReplace)
              │
   ┌──────────┴───────────────────────────────┐
   ▼                                          ▼
 FSM.Apply on leader            FSM.Apply on follower
   │                                          │
   ▼                                          ▼
 bbolt: dhcp/snapshot           bbolt: dhcp/snapshot
 in-memory: dhcpmgr.byIP        in-memory: dhcpmgr.byIP
```

The DNS handler and the management API on every node now read from
the **replicated** snapshot. Only the leader's `dhcp.Manager` calls
`connector.Fetch()`; followers' managers are in a "consumer" mode —
they observe `FSM.Apply` and rebuild their in-memory state from it.

## Snapshot-replace vs delta-only — the decision

**Decision: snapshot-replace** at every successful leader poll. Each
poll produces exactly one `CmdLeasesReplace` Raft entry whose payload
is the full canonical lease set the leader just observed. Followers
overwrite their `dhcp/snapshot` bucket atomically.

Why not delta-only?

- **Determinism**: the canonical snapshot is the connector's view at
  poll instant T. A delta stream would need a sequence number and
  forces every follower to apply the same delta in the same order
  starting from the same baseline — exactly the problem Raft already
  solves for the *whole* snapshot, but at higher coordination cost.
- **Idempotence**: replays after restart Just Work. A delta replay
  on top of a stale baseline diverges silently.
- **Operator mental model**: "the leader's last poll is the
  cluster's lease state" is a one-liner. "The cluster's lease state
  is the leader's bootstrap snapshot + every delta since the last
  snapshot" requires explaining the FSM contract.

Cost: payload size scales with lease count, not lease *churn*.
The next section quantifies why that's fine.

## Raft churn budget — quantified

Worst-realistic deployment for M6.5 = a 3-node skoed cluster with
**100 leases** and a **1-hour DHCP lease TTL** (typical SOHO Kea
default). Refresh interval is the M3.6 default of 60 s.

| Quantity | Value |
|---|---|
| Polls per hour (leader only) | 60 |
| Raft entries per hour | 60 |
| Per-lease wire size (canonical JSON) | ~180 B |
| Per-snapshot payload (100 leases) | ~18 KB + ~200 B envelope ≈ 18.2 KB |
| Raft bytes appended per hour | 60 × 18.2 KB ≈ **1.1 MB/h** |
| Raft entries per hour from M3.6 sources combined (config, audit, stats) | ~50 |
| Lease-replication share of total log growth | ~55% by entries, dominant by bytes |

Snapshot cadence: Raft snapshots roll at 8192 entries or 24 h
(see TS-RaftFsm). 60 × 24 = 1440 lease-repl entries per day on this
cluster — well under the entry threshold. Snapshot bytes stay
bounded because each snapshot stores **one** `dhcp/snapshot` value
(the latest), not the cumulative history.

**Larger-cluster check** (1000 leases, 60 s refresh, 24 h):
- 60 entries/h × 24 = 1440 entries/day (still ≤ 8192)
- ~180 KB per snapshot × 60/h ≈ 11 MB/h Raft log throughput
- bbolt snapshot value: ~180 KB (one row replaced in place)

A future deployment hitting the 8192-entry threshold from lease churn
alone would need a 6 s refresh on 1000+ leases. That's a knob in
`node.dhcp.refresh_seconds`, not a fundamental limit of this design;
operators are explicitly steered toward a 60 s minimum in the spec.

**Coalescer (no-op on identical polls)**: the leader compares the
incoming `[]Lease` against the last-applied snapshot. If the
canonical (IP, MAC, ClientID, Hostname, ExpiresAt-rounded-to-seconds)
tuples are byte-identical, **no Raft entry is appended**. This is the
"5 leases changed out of 1000" case from
FS-LeaseReplChurnDoesNotAmplifyRaftLog: the leader still appends one
entry per poll where *anything* changed, but a steady-state cluster
where nothing changes for 10 polls produces zero entries.

## Raft command set additions

```go
const (
    CmdLeasesReplace      CommandKind = "leases.replace"          // M6.5
    CmdAnomalyAppend      CommandKind = "dhcp_anomaly.append"     // M6.5
    CmdAnomalyAcknowledge CommandKind = "dhcp_anomaly.acknowledge"// M6.5
    CmdAnomalySweep       CommandKind = "dhcp_anomaly.sweep"      // M6.5
)
```

| Kind | Payload | Effect on bbolt | Frequency |
|---|---|---|---|
| `leases.replace` | `LeasesReplacePayload{ LeaderNodeID, PollUnix, Leases []Lease }` | Rewrites `dhcp/snapshot` (single key) | ≤ 1 per refresh interval |
| `dhcp_anomaly.append` | `AnomalyAppendPayload{ Anomaly }` | Writes `dhcp/anomalies/<id>` | bursty, < 5/h steady-state |
| `dhcp_anomaly.acknowledge` | `AnomalyAckPayload{ ID, AcknowledgedUnix }` | Updates `dhcp/anomalies/<id>.acknowledged_at` | human-driven |
| `dhcp_anomaly.sweep` | `AnomalySweepPayload{ BeforeUnix }` | Deletes `dhcp/anomalies/*` where `detected_at < before` | once per hour |

Payload shapes (add to `apps/skoed/internal/cluster/commands.go`):

```go
type LeasesReplacePayload struct {
    LeaderNodeID string        `json:"leader_node_id"`
    PollUnix     int64         `json:"poll_unix"`
    Leases       []dhcp.Lease  `json:"leases"`
}

type AnomalyAppendPayload struct {
    Anomaly dhcp.Anomaly `json:"anomaly"`
}

type AnomalyAckPayload struct {
    ID               string `json:"id"`
    AcknowledgedUnix int64  `json:"acknowledged_unix"`
}

type AnomalySweepPayload struct {
    BeforeUnix int64 `json:"before_unix"`
}
```

**Determinism**: `FSM.Apply` for `leases.replace` rebuilds
`m.byIP` from the payload only; it does NOT call `time.Now()` and
does NOT compare against history (that's the leader's job —
followers see anomalies through `dhcp_anomaly.append`). This keeps
the FSM call deterministic per the TS-RaftFsm contract.

## bbolt key additions

New top-level bucket `dhcp/` with two sub-buckets:

| Key | Value | Written by |
|---|---|---|
| `dhcp/snapshot` | latest `LeasesReplacePayload` JSON | `CmdLeasesReplace` |
| `dhcp/anomalies/<id>` | `Anomaly` JSON | `CmdAnomalyAppend`, updated by `CmdAnomalyAcknowledge` |

**Cardinality bounds**:
- `dhcp/snapshot`: exactly **one** key. Value size grows with lease
  count (180 B/lease + envelope). 10K leases ≈ 1.8 MB.
- `dhcp/anomalies/<id>`: bounded by `AnomalyRetention` (7 days) +
  the per-incident dedup logic that already exists in M3.6
  `recordAnomaly`. Realistic upper bound: ~100 entries per cluster
  in a 7-day window. Hard cap (defensive, beyond which the sweep
  drops the oldest): 1000 entries.

Snapshot/restore semantics inherit from TS-RaftFsm — the
`dhcp/` bucket is part of the bbolt file copy. No special-casing.

## Leader polling, follower consumption

Manager API additions in `apps/skoed/internal/dhcp/manager.go`:

```go
// SetClusterRole switches the manager between "leader" (active poller)
// and "follower" (passive consumer of replicated state). Called by the
// cluster package on leader-change events.
func (m *Manager) SetClusterRole(role ClusterRole)

type ClusterRole int
const (
    RoleUnknown ClusterRole = iota
    RoleLeader
    RoleFollower
)

// ApplyReplicatedSnapshot replaces the in-memory snapshot from a
// FSM-applied LeasesReplacePayload. Idempotent. No anomaly detection
// runs here — anomalies arrive via ApplyReplicatedAnomaly.
func (m *Manager) ApplyReplicatedSnapshot(p LeasesReplacePayload)

// ApplyReplicatedAnomaly inserts or updates an anomaly from a
// FSM-applied AnomalyAppend or AnomalyAcknowledge.
func (m *Manager) ApplyReplicatedAnomaly(a Anomaly)
```

The existing `Start()`/`Shutdown()` API is unchanged. On
`SetClusterRole(RoleFollower)` the poll loop's ticker stops calling
`connector.Fetch()` (but the goroutine stays alive — when the node
gets elected leader, the ticker resumes immediately, no goroutine
restart). On `SetClusterRole(RoleLeader)` the first action is an
**immediate poll** so the new leader's first replicated snapshot
happens within seconds of election, not after a full refresh interval
— this is the FS-LeaseReplLeaderFailoverResumesPolling contract.

**Anti-double-poll guard** (FS-LeaseReplNoDoublePollDuringTransition):
before each `connector.Fetch()`, the manager re-checks
`raft.State() == Leader`. If a leadership flap happened mid-tick, the
poll is skipped. This is the same defensive check used by the stats
committer in M5.0.

## Empty-cluster (no leader yet) handling

`GET /api/v1/leases` and `GET /api/v1/clients` check Raft state on
entry:

```
if raft.Leader() == "" || time-since-leader-known > 5s:
    return 503 with { error: "no leader", retry_after_seconds: 5 }
    Retry-After: 5
```

This is the FS-LeaseReplEmptyClusterReturns503 contract. The 5 s
window exists so that a *transient* leader gap (mid-election) doesn't
flap the API into 503 on every request — but a genuinely
not-yet-bootstrapped cluster always returns 503 (never the misleading
`200 {"leases":[]}`).

`GET /api/v1/clients/{ip}` follows the same rule; the M3.6 endpoint
that returned 404 for "ip not in cache" now returns 503 if the cache
is empty *and* no leader exists.

## HTTP surface

```yaml
# Excerpt — full document lives in specs/technical/management-api.openapi.yaml

paths:
  /api/v1/leases:
    get:
      summary: Replicated DHCP lease snapshot
      x-tsid: TS-LeaseRepl
      x-fsid-links: [FS-LeaseReplLeasesEndpointExposesSnapshot,
                     FS-LeaseReplFollowersServeReplicatedSnapshot,
                     FS-LeaseReplEmptyClusterReturns503]
      security: [{ bearerAuth: [] }]
      responses:
        "200":
          description: Current replicated snapshot
          headers:
            x-leader-node-id:
              schema: { type: string }
              description: Raft leader node id at response time
          content:
            application/json:
              schema:
                type: object
                required: [leases, source]
                properties:
                  leases:
                    type: array
                    items: { $ref: '#/components/schemas/Lease' }
                  source:
                    $ref: '#/components/schemas/LeasesSource'
        "503":
          description: No leader elected yet
          headers:
            Retry-After:
              schema: { type: integer }
          content:
            application/json:
              schema:
                type: object
                required: [error, retry_after_seconds]
                properties:
                  error: { type: string, enum: ["no leader"] }
                  retry_after_seconds: { type: integer }

  /api/v1/leases/source:
    get:
      summary: Which node polls, and when last
      x-tsid: TS-LeaseRepl
      x-fsid-links: [FS-LeaseReplSourceEndpointReportsLeader,
                     FS-LeaseReplLastPollUnixAdvances,
                     FS-LeaseReplSourceUnreachableKeepsLastGood]
      security: [{ bearerAuth: [] }]
      responses:
        "200":
          content:
            application/json:
              schema: { $ref: '#/components/schemas/LeasesSource' }
        "503":
          description: No leader elected yet

components:
  schemas:
    Lease:
      type: object
      required: [ip, mac, source, expires_at]
      properties:
        ip:         { type: string }
        mac:        { type: string }
        hostname:   { type: string }
        client_id:  { type: string }
        source:     { type: string }
        expires_at: { type: string, format: date-time }
    LeasesSource:
      type: object
      required: [connector_kind, last_poll_unix, leader_node_id]
      properties:
        connector_kind: { type: string, enum: [kea, dnsmasq, http_json] }
        last_poll_unix: { type: integer, format: int64 }
        source_url:     { type: string }
        leader_node_id: { type: string }
```

The existing `/api/v1/clients` and `/api/v1/clients/{ip}/anomalies`
endpoints keep their M3.6 wire shape; they're now backed by the
replicated `dhcp/snapshot` bucket instead of the node-local
`m.byIP`. **No client-visible breaking change** — operators who
upgrade get cluster-consistent responses for free.

Write forwarding: `POST /api/v1/clients/anomalies/{id}/acknowledge`
on a follower is reverse-proxied to the leader (same M2 middleware
used for blocklist writes — no new code, just route registration).
This is FS-LeaseReplFollowerWriteForwarded.

## Scheduler jobs

| Job | Owner | Cadence | Effect |
|---|---|---|---|
| `dhcp.poll` | leader only | `node.dhcp.refresh_seconds` (default 60) | `Fetch()` → diff → `CmdLeasesReplace` (skipped if identical) |
| `dhcp.anomaly_sweep` | leader only | every 1 h | `CmdAnomalySweep{ BeforeUnix: now - 7d }` |

`dhcp.poll` is a single ticker per node (the existing M3.6 ticker,
gated by the leader-check). `dhcp.anomaly_sweep` is a new ticker, also
leader-only. Both stop firing the moment `raft.State() != Leader` and
resume on the next leadership-acquired event. Total scheduler
overhead per cluster: 2 goroutines on the leader, 0 on followers
(the M3.6 manager goroutine on followers exists but blocks on a
channel, no work).

## Metrics

Existing M5.1 series (unchanged label sets):

| Series | Type | Labels | Cardinality |
|---|---|---|---|
| `skoed_dhcp_leases` | gauge | `source` | ≤ 3 (one per connector kind) |
| `skoed_dhcp_anomalies_open` | gauge | — | 1 |
| `skoed_dhcp_last_poll_age_seconds` | gauge | `source` | ≤ 3 |
| `skoed_dhcp_poll_errors_total` | counter | `source` | ≤ 3 |

M6.5 additions:

| Series | Type | Labels | Cardinality | Meaning |
|---|---|---|---|---|
| `skoed_dhcp_lease_repl_applies_total` | counter | `result` | 2 (`ok`, `error`) | FSM applies of `CmdLeasesReplace` on this node |
| `skoed_dhcp_lease_repl_skipped_total` | counter | — | 1 | leader polls that produced no Raft entry (identical snapshot) |
| `skoed_dhcp_lease_repl_payload_bytes` | gauge | — | 1 | size of the last applied snapshot payload |
| `skoed_dhcp_is_polling_leader` | gauge | — | 1 | 1 if this node is the active poller, 0 otherwise |

**Total new series, worst case**: 5. No high-cardinality labels —
per-lease metrics are explicitly excluded (`ip`, `mac`, `client_id`
would each push series count into the thousands).

The existing `skoed_dhcp_last_poll_age_seconds` on followers now
reports the **replicated** `PollUnix` from the snapshot, not the
follower's own (zero) `lastPollAt` — operators alerting on
"DHCP poll is stale" continue to work on follower nodes.

## Posture

### Authn / Authz
- `GET /api/v1/leases` and `/api/v1/leases/source` require bearer
  auth (same middleware as `/api/v1/clients`).
- Anomaly acknowledge is write-forwarded to the leader; the leader's
  M5.2 audit middleware records the action with `actor`, `target=ANOM-…`,
  `result=ok|error`.
- No new permission scopes; the existing "admin" scope covers leases
  end-to-end. Read-only operators can be granted `GET /api/v1/leases`
  via the M5.2 RBAC overlay if/when M7 lands it — out of scope here.

### Audit
- Anomaly acknowledge is audited (M5.2 `CmdAuditAppend`,
  action `"dhcp_anomaly.acknowledge"`).
- `CmdLeasesReplace` is NOT audited. The lease set changes ~60 times
  per hour per cluster — auditing every replace would dominate the
  90-day audit retention envelope and would surface no operator-
  actionable signal. Per-anomaly audit and `dhcp_poll_failed`
  structured logs cover the human-relevant events.
- Structured log `dhcp_poll_failed` fires on the leader (not
  followers — they didn't poll) when `connector.Fetch()` returns an
  error. Fields: `connector_kind`, `source_url`, `error`, `node_id`.

### PII / data minimization
- Leases already carry MAC + hostname + client-id, all considered
  operator-visible by M3.6. No new fields land here (DHCPv6 fields
  are documented in a separate spec).
- `source_url` in `LeasesSource` is the configured connector URL —
  same value already exposed via `GET /api/v1/config/dhcp` to admins
  in M3.6. No new exposure.

### SSRF / cardinality abuse
- No new outbound HTTP from this milestone. The leader still polls
  the same configured `node.dhcp.source_url`; the URL is locked at
  config time and not influenced by request inputs.
- No user-supplied identifiers are reflected into metric labels.
  All new series have label cardinality ≤ 2.

### Netlink capability
Not applicable to this spec — netlink (ARP/NDP cross-check) is
covered by the sibling `dhcp-arp-cross-check` TSID. Lease
replication runs entirely in user space against bbolt + Raft + the
existing HTTP connectors.

### Failure modes
| Failure | Effect | Behaviour |
|---|---|---|
| Connector unreachable on leader | last good snapshot stays in `dhcp/snapshot`; `last_poll_unix` does not advance; `dhcp_poll_failed` logged | FS-LeaseReplSourceUnreachableKeepsLastGood |
| Leader crash mid-`raft.Apply` | entry either committed (followers see it) or not (new leader re-polls) — Raft handles both | FS-LeaseReplLeaderFailoverResumesPolling |
| Follower partitioned 5+ min | catches up via Raft log on reconnect; one `FSM.Apply` per missed `CmdLeasesReplace`; bounded by Raft snapshot if log was rolled | FS-LeaseReplStaleFollowerCatchesUp |
| Connector schema drift (extra fields) | unknown fields ignored at JSON unmarshal; canonical lease shape preserved | M3.6 invariant, unchanged |
| `dhcp/snapshot` corrupt on disk | bbolt open fails → standard TS-RaftFsm recovery (rejoin via token, snapshot streamed from peer) | inherited |

## Implementation map

```
apps/skoed/internal/dhcp/
  manager.go              (extend: SetClusterRole, ApplyReplicatedSnapshot,
                                   ApplyReplicatedAnomaly, leader-gated tick,
                                   identical-snapshot coalescer)
  replication.go          (new: payload diff helper + canonicalisation)
apps/skoed/internal/cluster/
  commands.go             (extend: CmdLeasesReplace, CmdAnomalyAppend,
                                   CmdAnomalyAcknowledge, CmdAnomalySweep
                                   + payload structs)
  fsm.go                  (extend: Apply cases → dhcp.Manager hooks,
                                   bbolt dhcp/ bucket)
  node.go                 (extend: emit leader-change events to dhcp.Manager)
apps/skoed/internal/api/handlers/
  leases.go               (new: GET /api/v1/leases, GET /api/v1/leases/source)
  clients.go              (extend: 503-on-no-leader guard)
apps/skoed/internal/metrics/
  metrics.go              (extend: 4 new series, ObserveLeaseRepl helpers)
specs/technical/
  management-api.openapi.yaml (extend: 2 new paths, LeasesSource schema)
tests/acceptance/
  dhcp_lease_replication_test.go (all 13 FSIDs)
```
