# Implementation Plan

## Context
- Planning scope: Milestone 2 — Multi-Node Cluster
- Roadmap links: ROADMAP.md § Milestone 2
- Planning horizon: Milestone 2 only; Helm chart (M2.5) and M3+ planned separately
- Scope summary: A second or third dblock node joins an existing installation via a join token issued by the primary. All config changes on the primary propagate to enrolled replicas within seconds via SSE. Manual failover is the default; opt-in quorum-based auto-failover is available. A cluster status dashboard shows node roles, last-seen timestamps, and sync state. Helm chart is deferred to M2.5.

---

## Previous milestone — Milestone 1 (DONE)
- Planning scope: Milestone 1 — Single Node Foundation
- Roadmap links: ROADMAP.md § Milestone 1
- Scope summary: A single dblock node that serves DNS (forwarding + root resolution, dual-stack, DNSSEC transparent), filters domains via configurable blocklists and allowlists, serves local DNS entries, logs queries, exposes a management API with basic auth, and supports config import/export. Released as a Linux binary and Docker image. **Status: merged to master 2026-05-29, 58/58 acceptance tests green.**
- Assumptions:
  - hashicorp/raft + go.etcd.io/bbolt operates correctly for clusters of ≤10 nodes on home/lab networks (H4 — validate throughout M2).
  - bbolt is the source of truth; YAML becomes import/export only; node-local settings remain in a separate `node.yaml`.
  - Cluster-wide state (blocklists, allowlists, local DNS, settings, auth) goes through Raft; node-local state (DNS listen port, API port) and high-volume raw query log entries do NOT.
  - Query log aggregates (hourly buckets, top-N) are committed through Raft and replicated to every node; raw entries stay per-node and are accessed via fan-out when needed.

## Global feature sequencing (Milestone 2)

| Order | Feature | Outcome | Depends on | FSIDs | TSIDs | Acceptance tests | Status |
|-------|---------|---------|-----------|-------|-------|-----------------|--------|
| 1 | Raft Bootstrap & M1 Migration | First-run node initialises a single-node Raft cluster; existing M1 `config.yaml` is imported into bbolt | M1 (config + API) | FS-NodeEnrollmentSingleNodeBootstrap, FS-NodeEnrollmentM1ConfigMigration | TS-ClusterStore | tests/acceptance/bootstrap_test.go | Planned |
| 2 | Node Enrollment | A fresh node joins the cluster using a single-use join token; Raft membership change adds it as a voter; node-local settings are preserved | Slice 1 | FS-NodeEnrollment*, FS-NodeRemoval | TS-ClusterApi | tests/acceptance/enrollment_test.go | Planned |
| 3 | Config Sync via Raft | Writes to any node are forwarded to the leader and replicated to all followers; minority partition refuses writes | Slice 2 | FS-ClusterConfigSync* | TS-RaftLog | tests/acceptance/sync_test.go | Planned |
| 4 | Leader Failover | Automatic Raft election on leader loss; former leader rejoins as follower; manual leadership transfer API | Slice 3 | FS-LeaderFailover* | TS-RaftFsm | tests/acceptance/failover_test.go | Planned |
| 5 | Cluster Status | API exposes Raft role, last-contact, commit index, term; same view from any node | Slice 2 | FS-ClusterStatus* | TS-ClusterApi | tests/acceptance/cluster_status_test.go | Planned |
| 6 | Query Log Aggregates | Hourly aggregates committed through Raft; cluster stats available from any node; fan-out endpoint for raw entries | Slice 3 | FS-QueryLogAggregates* | TS-QueryLogCluster | tests/acceptance/query_log_aggregates_test.go | Planned |
| 7 | Shadow YAML | After every FSM apply, write-through `<data_dir>/config.yaml` (debounced, atomic rename) for filesystem-level backup tools (PBS, restic) | Slice 3 | FS-ConfigShadowYaml* | TS-ClusterStore | tests/acceptance/shadow_yaml_test.go | Planned |

## Cross-feature dependencies and blockers

| Dependency | Upstream | Downstream | Impact if late | Mitigation | Status |
|-----------|---------|-----------|---------------|-----------|--------|
| Raft FSM (apply / snapshot / restore) | Slice 1 | All cluster ops | No replication possible | Define FSM commands first; cover with unit tests before integration tests | Open |
| bbolt schema for replicated state | Slice 1 | All cluster ops | Schema churn invalidates Raft snapshots | Finalise buckets (`config/*`, `cluster/*`, `stats/{node}/{hour}`) before Slice 3 | Open |
| Join token lifecycle | Slice 2 | All cluster ops | Nodes can't authenticate to join | Tokens are single-use, 15-min TTL, stored in bbolt (replicated), validated by the leader | Open |
| Library: `hashicorp/raft` vs `etcd/raft` | Slice 1 | All cluster ops | Wrong choice = rework | Default: `hashicorp/raft` — more library-shaped, used by k3s/Consul/Nomad; revisit if it doesn't fit | Decision needed |

## Critical path and milestones

- Critical path: Raft Bootstrap → Node Enrollment → Config Sync → Leader Failover → Query Log Aggregates → Cluster Status
- Milestone 2 exit criteria:
  - All M2 acceptance tests pass
  - A 3-node cluster can be brought up in `docker compose` in under 5 minutes
  - Config write on any node is committed across the cluster within 5 seconds
  - Killing the leader causes a follower to take over within 10 seconds (Raft default heartbeat tuning)
  - Minority partition refuses writes; majority partition continues; partition heal reconciles cleanly
  - Cluster-wide stats endpoint returns the same answer from any node
  - All M1 acceptance tests remain green (no regressions)

## Validation checkpoints

- [ ] Functional specs validated by UoR (all M2 .feature files)
- [ ] Technical specs validated by UoR (OpenAPI updates + sync-protocol.md + quorum-protocol.md)
- [ ] Acceptance tests validated by UoR
- [ ] Implementation done (all M2 acceptance tests pass, no M1 regressions)
- [ ] CI green
- [ ] Refactoring validated (acceptance + full tests green after refactor)
- [ ] Demo prepared and validated by UoR (multi-container Docker compose)

## Risks and trade-offs

- Risk: hashicorp/raft snapshotting interacts badly with large blocklists
  - Trigger: snapshot size > 10 MB causes long pauses on follower catch-up
  - Response: measure at Slice 3; if needed, use streaming snapshot (Raft library supports it) and skip blocklist domains larger than a threshold from the snapshot, refetching them from URL on restore

- Risk: bbolt corruption on power loss
  - Trigger: hard kill mid-write; bbolt database becomes unreadable
  - Response: write-ahead via Raft log gives recovery; document backup as periodic Raft snapshot export; bbolt's own write semantics already use mmap + fsync

- Risk: Join token theft allows unauthorized node enrollment
  - Trigger: token leaked from logs or terminal history
  - Response: short TTL (15 min), single-use, leader logs all enrollment attempts, tokens never reused

- Risk: Raft library API changes between minor versions
  - Trigger: dependency update breaks compilation
  - Response: pin `hashicorp/raft` to a tested major version in go.mod; track upstream releases

## Open questions

- Raft library: `hashicorp/raft` (recommended) vs `etcd/raft`. Default to `hashicorp/raft` unless UoR overrides. — open at Slice 1 start

## Hypotheses to validate in M2

- H4: `hashicorp/raft` + `go.etcd.io/bbolt` are operationally suitable for dblock's workload (≤10 nodes, ≤1 write/day per cluster, ~1–10 MB state) on home/lab networks. **Validate throughout M2.** Replaces H2 (manual quorum) and H3 (SSE).

## Change log

- 2026-05-29: Initial plan created for Milestone 1.
- 2026-05-29: Milestone 1 complete; plan updated for Milestone 2.
- 2026-05-29: Replication core pivoted from SSE-pull to hashicorp/raft + bbolt; source of truth moved from YAML to bbolt; query log aggregates added via Raft; manual + quorum failover obsoleted.
