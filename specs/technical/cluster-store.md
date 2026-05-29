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
---

# TS-ClusterStore — On-disk State

## Files per node

```
/var/lib/dblock/
├── node.yaml             # node-local, NOT replicated
├── cluster.bbolt         # replicated state machine; source of truth
├── querylog.bbolt        # per-node raw query log; NOT replicated
├── raft/
│   ├── raft-log.bolt     # Raft log store
│   ├── raft-stable.bolt  # Raft stable store (term, vote)
│   └── snapshots/        # periodic Raft snapshots
└── config.yaml           # legacy M1 file; preserved for export only after migration
```

## node.yaml (node-local, not replicated)

Keys that depend on the host and must not be overwritten by Raft:

```yaml
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
```

The same admin can run `node-1` on port 5353 and `node-2` on port 53 in the
same cluster — DNS listen ports are not portable between nodes.

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

Two supported paths:

- **Export YAML** — `GET /api/v1/config/export` dumps cluster state from
  bbolt into the M1 YAML format. Suitable for human review and migration.
  Does NOT include `cluster/members`, `cluster/tokens`, or `stats/*`.
- **Snapshot copy** — stop the node, copy `cluster.bbolt` and `raft/` to
  a backup destination. Restores must go onto the same `node.yaml.node.id`
  (Raft identity matters). For disaster recovery across nodes, use the
  YAML export + a fresh single-node bootstrap.

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
