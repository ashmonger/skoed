---
x-tsid: TS-ClusterStore
x-fsid-links:
  - FS-NodeEnrollmentSingleNodeBootstrap
  - FS-NodeEnrollmentM1ConfigMigration
  - FS-NodeEnrollmentGenerateToken
  - FS-NodeEnrollmentJoinTokenIsSingleUse
  - FS-NodeEnrollmentJoinTokenExpires
  - FS-NodeRemoval
  - FS-ClusterConfigSyncWriteToLeader
  - FS-ClusterConfigSyncBlocklistRemove
  - FS-ClusterConfigSyncAllowlist
  - FS-ClusterConfigSyncLocalDns
  - FS-QueryLogAggregatesPerNodePerHour
  - FS-QueryLogAggregatesRetention
  - FS-ConfigShadowYamlPresentOnDisk
  - FS-ConfigShadowYamlUpdatesAfterWrite
  - FS-ConfigShadowYamlAtomicWrite
  - FS-ConfigShadowYamlIgnoredOnRead
  - FS-ConfigShadowYamlExcludesNodeLocal
  - FS-ConfigShadowYamlRebuiltOnBoot
  - FS-ConfigShadowYamlRoundTrips
---

# TS-ClusterStore — On-disk State

## Files per node

```
/var/lib/dblock/
├── config.yaml           # SINGLE per-node file. Combines node-local
│                         # settings (id, raft_address, api_address, dns
│                         # listen) with a write-through shadow of the
│                         # replicated state (blocklists, allowlist, …).
│                         # On startup: node-local is read; cluster-replicated
│                         # is read only on first boot (migration into bbolt).
│                         # While running: bbolt is source of truth; the shadow
│                         # writer rewrites this file after every commit,
│                         # preserving the node section verbatim.
├── cluster.bbolt         # replicated state machine; runtime source of truth
├── querylog.bbolt        # per-node raw query log; NOT replicated
└── raft/
    ├── raft-log.bolt     # Raft log store
    ├── raft-stable.bolt  # Raft stable store (term, vote)
    └── snapshots/        # periodic Raft snapshots
```

## config.yaml — the single per-node file

```yaml
schema_version: 1

# Node-local. Never replicated; preserved verbatim across shadow rewrites.
node:
  id: "node-1"                 # stable identifier used in Raft membership
  raft_address: "0.0.0.0:7000" # bound for Raft RPC traffic
  api_address: "0.0.0.0:8080"  # bound for the HTTP management API
  dns:
    listen:
      port: 53
      ipv4: true
      ipv6: true
  data_dir: "/var/lib/dblock"

# Optional. Consumed exactly once on first boot when no cluster.bbolt exists;
# ignored on every subsequent boot. Tokens are single-use; the field is
# simply unread next time, so the same config.yaml can be re-deployed as-is.
bootstrap:
  leader_address: "http://192.168.1.10:8080"
  token: "..."

# Cluster-replicated state. On first boot this section seeds bbolt; on every
# subsequent boot bbolt is the source of truth and this section is rewritten
# by the shadow writer after every Raft commit.
dns:
  mode: forwarding
  upstream_resolvers: ["9.9.9.9:53"]
  upstream_timeout_seconds: 3
  cache: {enabled: true, max_entries: 1000}
filtering:
  block_policy: nxdomain
  blocklists: []
  allowlist: []
local_dns:
  entries: []
query_log:
  max_entries: 10000
auth:
  username: admin
  password_hash: "$2a$..."
```

The same admin can run `node-1` on port 5353 and `node-2` on port 53 in the
same cluster — `node.dns.listen.port` is per-host and never replicated.

The binary is invoked with `--config <path/to/config.yaml>`. The data
directory is `node.data_dir` (defaulting to `filepath.Dir(--config)`), and
all state files (`cluster.bbolt`, `querylog.bbolt`, `raft/`) live there.

### Backward compatibility with M1 config.yaml

M1 config files have no `node:` section. The M2 binary synthesises one when
it sees an M1 file: `node.id="node-1"`, listen ports taken from `dns.listen`
and `api.port`, `raft_address=127.0.0.1:0` (picked at start). The cluster-
replicated sections are migrated into bbolt as if they were the bootstrap
seed. Existing M1 deployments need no manual conversion.

### Test affordances

The following environment variables are honoured **only when**
`DBLOCK_TEST_MODE=1` is also set. Production builds and operational deployments
that omit `DBLOCK_TEST_MODE` ignore them, so they cannot weaken security or
durability outside the test harness.

| Variable | Effect |
|---|---|
| `DBLOCK_TEST_TOKEN_TTL_SECONDS` | Overrides the 15-minute join-token TTL with this many seconds (≥ 1). Used by acceptance tests to verify expired-token rejection without sleeping for 15 min. |
| `DBLOCK_TEST_AGGREGATE_FLUSH_SECONDS` | Overrides the 60-second aggregate-flush interval (≥ 1). Used by acceptance tests to make cluster stats observable in seconds rather than waiting for an hour boundary. |

These exist because their corresponding behaviours are time-driven and would
otherwise force test suites to either sleep for production-realistic durations
or invent test-only HTTP endpoints — both worse trade-offs than a documented,
mode-gated env-var knob.

## cluster.bbolt — replicated buckets

The FSM keeps the entire replicated state in a single bbolt database. Schema
version is held in `meta/schema_version`. Buckets:

| Bucket | Key | Value | Notes |
|---|---|---|---|
| `meta` | `schema_version` | uint32 | Bumped on incompatible schema changes |
| `meta` | `cluster_id` | string (UUID) | Generated on first bootstrap |
| `cluster/members` | `<node_id>` | `{api_address, raft_address, joined_at}` | Updated on every Raft membership change |
| `cluster/tokens` | `<sha256(token)>` | `{expires_at, created_by, used_at}` | `used_at` set on consume; pruned when expired |
| `config/blocklists` | `<id>` | full Blocklist JSON | See M1 config-schema.md |
| `config/allowlist` | `<domain>` | empty | Set semantics |
| `config/local_dns` | `<id>` | LocalDNSEntry JSON | |
| `config/settings` | `dns`, `filtering`, `query_log` | partial config | Single key per top-level section |
| `config/auth` | `credentials` | `{username, password_hash, version}` | Password hash is bcrypt; `version` bumps on each change |
| `stats/<node_id>` | `<hour_unix>` | HourlyAggregate JSON | See TS-QueryLogCluster |

**Indices:** none. The whole replicated state fits comfortably in RAM
(<100 MB even with a 1M-domain blocklist).

**Atomic writes:** each FSM command is one bbolt transaction.

## querylog.bbolt — per-node, not replicated

Bounded ring buffer matching M1 semantics. Stays per-node because:
- Volume is too high for Raft replication (every DNS query × every node).
- Privacy: a node sees only the clients on its own network segment.
- Failure of one node does not erase queries served by others.

| Bucket | Key | Value |
|---|---|---|
| `entries` | `<timestamp_ns>:<random>` | full Entry JSON |
| `meta` | `max_entries` | uint32 |

Eviction policy: drop oldest entry when `count >= max_entries`. Same as M1.

## Schema versioning

Single `meta/schema_version` (uint32) drives forward compatibility:

- An older binary reading a newer bbolt: refuses to start.
- A newer binary reading an older bbolt: runs an in-place migration as a
  single FSM command (so the migration is itself replicated), then bumps
  the version.

Migrations are written as one-shot Go functions registered in
`internal/cluster/store/migrations.go`. They MUST be deterministic.

## Shadow YAML (write-through mirror)

bbolt is the source of truth at runtime, but a YAML projection is kept on
disk at `<data_dir>/config.yaml` so that filesystem-level backup tools
(Proxmox Backup Server, restic, borg, `tar`-the-container) capture the
config without needing to call the HTTP export endpoint.

### Writer

A single goroutine per node owns the YAML file:

```
                     debounce 1s
FSM.Apply ─signal─►  ┌─────────┐
                     │ writer  │── render YAML from bbolt
                     │ goroutine│── write to config.yaml.tmp
                     │         │── fsync + atomic rename → config.yaml
                     └─────────┘
                          │
                          └─ records last-rendered commit_index
```

- Triggered by:
  - every successful `FSM.Apply` on the local node, AND
  - process startup (always re-renders once to catch any stale-YAML from a
    previous crash).
- Debounced: if more signals arrive within the 1-second window, only one
  write happens, reflecting the latest commit.
- Atomic: writes to a temp file in the same directory, `fsync`s, then
  `rename(2)`s over the target. Readers (backup tools) always see a
  complete file.
- Records `last_rendered_commit_index` in memory only — recomputed at boot.

### What's in it

The YAML is exactly the M1 export format (see `config-schema.md`):

```yaml
schema_version: 1
dns:
  mode: forwarding
  upstream_resolvers: [...]
  cache: {...}
filtering:
  block_policy: nxdomain
  allowlist: [...]
  blocklists: [...]
local_dns:
  entries: [...]
query_log:
  max_entries: 10000
auth:
  username: admin
  password_hash: $2a$...
```

### What's NOT in it

- `cluster/members` — Raft membership; valid only for the running cluster
  topology, would harm a PBS restore to a different host.
- `cluster/tokens` — short-lived secrets; restoring expired tokens is
  pointless and writing them to disk widens the secret blast radius.
- `stats/*` — hourly aggregates; operational data, not user config.
- `node.id`, `node.raft_address`, `node.api_address`, DNS listen ports —
  these live in `node.yaml` and are intentionally per-host.

### Behaviour during failure

| Failure | Effect | Recovery |
|---|---|---|
| Process killed mid-rename | Either old or new YAML on disk; both are complete | None needed; atomic rename guarantees consistency |
| Disk full | Rename fails; bbolt write already succeeded | Writer logs error, retries on next signal; alert via metrics |
| `data_dir` read-only at boot | Writer errors; node continues serving | Admin fixes permissions; YAML re-rendered on next FSM apply |
| YAML present but corrupted from external tampering | Not a problem on a running node (the writer overwrites it) | Edits are lost on next commit; not honoured (by design) |

### Why not honour manual edits?

Two writers (admin editor + Raft FSM) into the same file is unsafe —
last-writer-wins races, partial reads, ambiguous source-of-truth. The
restore path covers the intended use case: admins who want to bulk-edit
config can export, edit offline, then re-import via the API, which goes
through Raft like any other write.

## M1 → M2 migration

On first M2 startup with an M1 layout present:

1. Detect: `config.yaml` exists, `cluster.bbolt` does not.
2. Read the YAML, validate against the M1 schema.
3. Initialise bbolt and `raft/`; bootstrap as single-node cluster.
4. Apply one `config.import` FSM command containing the full M1 config.
5. Rename `config.yaml` → `config.yaml.imported` (preserved, no longer the
   source of truth). New exports overwrite a fresh `config.yaml`.

If any step fails, the node refuses to start and leaves all M1 files intact.

## Backup / restore

Three supported paths, in increasing order of fidelity:

- **YAML-only (recommended for off-host / cross-cluster restore)** — back
  up `<data_dir>/config.yaml`. The shadow writer keeps it current. On
  restore to a fresh node, the M1→M2 migration imports it on first boot.
  Loses runtime state (Raft log, query log, aggregates, tokens) but
  reproduces all user config. This is what Proxmox Backup Server captures
  when snapshotting the container filesystem; the writer guarantees the
  YAML on disk is at most ~1 s stale relative to the latest commit.
- **HTTP export** — `GET /api/v1/config/export`. Identical content to the
  shadow YAML; useful from outside the host.
- **Full filesystem snapshot** — back up the entire `<data_dir>`. Restores
  cluster.bbolt, raft/, querylog.bbolt, and config.yaml together. Must be
  restored onto a node with the same `node.id` (Raft identity matters).
  Suitable for in-place disaster recovery of a single node. For
  multi-node restore, restore one node this way and have the others
  re-join via the token flow.

## Disk-size envelope

| Component | Realistic max |
|---|---|
| `cluster.bbolt` | 50 MB (large blocklist) |
| `raft/raft-log.bolt` | 8192 entries × ~10 KB = ~80 MB before snapshot truncation |
| `raft/snapshots/` | 2× cluster.bbolt = 100 MB |
| `querylog.bbolt` | bounded by `max_entries`; default 10k entries ≈ 5 MB |

Total per node: ~250 MB worst case. Acceptable for home/lab.

## Non-goals

- Hot backup of bbolt during writes (snapshot copies require node stop).
- Encryption at rest in M2 (filesystem-level encryption is the documented answer).
- Sharded buckets per blocklist — single key per blocklist is sufficient at
  this scale.
