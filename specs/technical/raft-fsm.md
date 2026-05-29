---
x-tsid: TS-RaftFsm
x-fsid-links:
  - FS-ClusterConfigSyncWriteToLeader
  - FS-ClusterConfigSyncWriteToFollowerIsForwarded
  - FS-ClusterConfigSyncBlocklistRemove
  - FS-ClusterConfigSyncAllowlist
  - FS-ClusterConfigSyncLocalDns
  - FS-ClusterConfigSyncSurvivesFollowerDisconnect
  - FS-ClusterConfigSyncMinorityPartitionRefusesWrites
  - FS-ClusterConfigSyncMajorityPartitionContinues
  - FS-LeaderFailoverAutomaticElection
  - FS-LeaderFailoverNoSplitBrainAcrossPartition
  - FS-LeaderFailoverFormerLeaderRejoinsAsFollower
  - FS-LeaderFailoverWritesDuringTransition
  - FS-NodeEnrollmentSingleNodeBootstrap
  - FS-NodeEnrollmentM1ConfigMigration
  - FS-NodeEnrollmentJoinWithValidToken
  - FS-NodeRemoval
---

# TS-RaftFsm — Raft Finite State Machine

This document defines dblock's Raft FSM: the command set, snapshot format,
and apply / snapshot / restore semantics. Implementation library:
[`github.com/hashicorp/raft`](https://github.com/hashicorp/raft) with
[`github.com/hashicorp/raft-boltdb/v2`](https://github.com/hashicorp/raft-boltdb)
for the log and stable stores.

## Command set

Every mutation that must be replicated cluster-wide is encoded as a single
FSM command. Commands are serialised as JSON (one per Raft log entry) with
the shape:

```json
{
  "kind": "<command kind>",
  "v": 1,
  "payload": { ... }
}
```

| Kind | Payload | Effect on bbolt |
|---|---|---|
| `blocklist.upsert` | `{ id, name, enabled, source, block_policy, domains[] }` | Writes `config/blocklists/<id>` |
| `blocklist.delete` | `{ id }` | Deletes `config/blocklists/<id>` |
| `blocklist.set_enabled` | `{ id, enabled }` | Updates `config/blocklists/<id>.enabled` |
| `allowlist.add` | `{ domain }` | Adds entry to `config/allowlist` set |
| `allowlist.remove` | `{ domain }` | Removes entry from `config/allowlist` set |
| `local_dns.upsert` | `{ id, hostname, type, value, ttl }` | Writes `config/local_dns/<id>` |
| `local_dns.delete` | `{ id }` | Deletes `config/local_dns/<id>` |
| `settings.patch` | partial settings object | Merges into `config/settings` |
| `auth.set_credentials` | `{ username, password_hash }` | Writes `config/auth` |
| `token.create` | `{ token_hash, expires_at }` | Writes `cluster/tokens/<hash>` |
| `token.consume` | `{ token_hash }` | Deletes `cluster/tokens/<hash>` |
| `stats.commit_hour` | `HourlyAggregate` | Writes `stats/<node_id>/<hour_unix>` |
| `stats.prune` | `{ before }` | Deletes `stats/*/<hour_unix>` where `hour_unix < before` |
| `config.import` | full M1 config snapshot | Replays into bbolt as a single atomic command |

**Rules:**
- Every command is idempotent at the bbolt level — applying it twice yields
  the same final state (required because Raft may replay log on restart).
- Tokens are stored as a hash, never as plaintext. The leader returns the
  plaintext token to the caller exactly once.
- `stats.commit_hour` is the only high-frequency command — at most one per
  node per hour, so cluster-wide throughput is `(node_count / 3600) Hz` —
  negligible for Raft.

## Apply semantics

```
admin --HTTP write-->  any node
                         |
                         ├── if follower: forward to leader's API
                         └── if leader:
                               1. encode command
                               2. raft.Apply(command, timeout)
                               3. raft commits to quorum
                               4. FSM.Apply runs on every node:
                                    bbolt.Update(tx -> apply command)
                                    notify in-process subscribers:
                                       - filter engine rebuild
                                       - local DNS resolver rebuild
                                       - shadow YAML writer (debounced)
                               5. response returned to admin
```

- `raft.Apply` returns only after the entry is committed to a quorum.
- `FSM.Apply` MUST be deterministic: no clocks, no random numbers, no I/O
  beyond bbolt. The Raft library serialises all FSM calls.
- Side effects (filter rebuild, DNS handler swap, shadow YAML write) happen
  via an in-process pub/sub fed by `FSM.Apply` — never directly from HTTP
  handlers. Each subscriber owns its own goroutine; FSM.Apply is not
  blocked on subscriber latency.
- The shadow YAML write is debounced ~1 s and is a strict side effect: a
  failed YAML write is logged but never fails the FSM apply. See
  `cluster-store.md` § Shadow YAML for the writer protocol.

## Snapshot format

Snapshots are taken when the Raft log grows past 8192 entries (the hashicorp
default) or every 24 hours, whichever comes first. The snapshot is a single
bbolt file copy: while the FSM holds a read transaction, the file is streamed
to the sink. No transformation, no key rewriting.

```
snapshot = bbolt.Tx (read-only) → write each bucket → SnapshotSink
restore  = SnapshotSink → write to temp file → atomic rename → reopen bbolt
```

The first snapshot after a fresh bootstrap is small (≤ 100 KB). The largest
realistic snapshot is dominated by `config/blocklists/*` — for a 1M-domain
blocklist, ~30 MB.

## Restore semantics

On restart, a node:
1. Opens the bbolt file (read-only mode initially).
2. Opens the Raft log + snapshot store from `raft/`.
3. The Raft library calls `FSM.Restore(snapshot)` if a snapshot is newer than
   the on-disk bbolt — the FSM replaces the bbolt file atomically.
4. Raft then replays log entries after the snapshot. Each one goes through
   the same `FSM.Apply` path.
5. Once `LastIndex == CommitIndex`, the node starts serving HTTP and DNS.

If `FSM.Apply` returns an error during restore (e.g. unknown command kind
from a forward-incompatible version), the node refuses to start and emits a
clear error pointing at the offending command index.

## Bootstrap modes

| Scenario | Behavior |
|---|---|
| Fresh data dir, no peers configured | Initialise single-node Raft cluster; this node is leader |
| Fresh data dir, peers configured | Refuse to start; admin must use the join token flow |
| Existing data dir, no peers | Restart from local Raft state; election proceeds normally |
| Existing data dir, peers configured | Same as above; peer list is informational only (Raft membership lives in the log) |
| M1 config.yaml present, no bbolt | Migrate: read YAML, bootstrap single-node Raft, then `raft.Apply(config.import)` with the full YAML as payload |

## Write forwarding

Followers do not write to bbolt directly. The HTTP middleware checks
`raft.State()`:

- `Leader` → encode command, `raft.Apply(...)`, return result
- `Follower` → look up `raft.Leader()`'s API address from
  `cluster/members/<id>`; reverse-proxy the HTTP request to that address
- `Candidate` (mid-election) → block for up to 5 seconds waiting for the
  state to settle; if still candidate, return HTTP 503

## Failure modes

| Failure | Effect | Recovery |
|---|---|---|
| Leader crash | Followers detect via heartbeat timeout (default 1s); election within ~3s | Automatic; new leader continues from same committed log |
| Quorum loss (minority partition) | Stuck side returns 503 on all writes; reads from local bbolt still served | Heal partition; minority catches up via Raft log replication |
| FSM divergence (a bug) | Apply error logged; node refuses further commands; should never happen if FSM is deterministic | Manual: remove node, rebuild from snapshot, re-add |
| bbolt corruption | Open fails on startup | Manual: delete bbolt + raft/snapshots/, re-join via token; new snapshot streamed from another node |
| Raft log corruption | Open fails on startup | Same as bbolt corruption |

## Non-goals

- Read-your-writes for follower reads (write returns when committed; followers
  may briefly lag — readers tolerate this).
- Linearisable reads (a follower may serve stale reads up to one heartbeat
  interval old; acceptable for an admin UI).
- Multi-Raft groups or sharding — one global FSM per cluster.
- Witness / non-voting members in M2 (Raft `Learner` mode is supported by
  the library but not exposed via the API in M2).
