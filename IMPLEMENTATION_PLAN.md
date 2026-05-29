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
  - SSE over plain HTTP/1.1 is sufficient for sync transport (H3 — validate at Slice 2).
  - Quorum-based auto-failover with last-seen heartbeats prevents split-brain for ≤ 10 nodes in practice (H2 — validate at Slice 4).
  - Config state remains on disk as YAML (no external database).
  - Node-local state (DNS listen port, API port, role) is NOT synced; cluster-wide state (blocklists, allowlists, local DNS, settings, auth) IS synced.

## Global feature sequencing (Milestone 2)

| Order | Feature | Outcome | Depends on | FSIDs | TSIDs | Acceptance tests | Status |
|-------|---------|---------|-----------|-------|-------|-----------------|--------|
| 1 | Node Enrollment | Replica joins primary using a single-use join token; receives initial config | M1 Management API | FS-NodeEnrollment | TS-ClusterApi | tests/acceptance/enrollment_test.go | Planned |
| 2 | Config Sync (SSE) | Replicas open `/api/v1/sync/events`; receive `config-changed` events and pull new config within 10s | Node Enrollment | FS-ClusterConfigSync | TS-SyncProtocol | tests/acceptance/sync_test.go | Planned |
| 3 | Manual Failover | Admin promotes a replica via API; former primary demotes to replica on next contact | Config Sync | FS-ManualFailover | TS-ClusterApi | tests/acceptance/failover_test.go | Planned |
| 4 | Quorum Auto-Failover (opt-in) | When `cluster.auto_failover=true`, replicas detect primary loss via missed heartbeats and elect a new primary by majority vote | Manual Failover | FS-QuorumAutoFailover | TS-QuorumProtocol | tests/acceptance/quorum_test.go | Planned |
| 5 | Cluster Status | API exposes all nodes' roles, last-seen, sync state; primary surfaces its replicas, replicas surface their primary | Node Enrollment | FS-ClusterStatus | TS-ClusterApi | tests/acceptance/cluster_status_test.go | Planned |
| 6 | Sync-Aware Refactor | Existing M1 mutation endpoints reject writes on replicas (read-only); writes accepted only on primary | Config Sync, Cluster Status | (refines M1 FSIDs) | (refines M1 TSIDs) | tests/acceptance/sync_test.go | Planned |

## Cross-feature dependencies and blockers

| Dependency | Upstream | Downstream | Impact if late | Mitigation | Status |
|-----------|---------|-----------|---------------|-----------|--------|
| Config version counter | Node Enrollment | Config Sync, Manual Failover | Replicas can't detect new config | Add monotonic `config_version` to config root; increment on every mutation | Open |
| Heartbeat protocol | Cluster Status | Quorum Auto-Failover | Auto-failover impossible without liveness signal | Piggyback on SSE `keep-alive` event every 5s; missed 3× = node lost | Open |
| Join token lifecycle | Node Enrollment | All cluster ops | Replicas can't authenticate | Tokens are single-use, 15 min TTL, generated on demand by primary | Open |

## Critical path and milestones

- Critical path: Node Enrollment → Config Sync → Manual Failover → Cluster Status → Quorum Auto-Failover → Sync-Aware Refactor
- Milestone 2 exit criteria:
  - All M2 acceptance tests pass
  - A primary + 2 replicas can be brought up in `docker compose` in under 5 minutes
  - Config change on the primary visible on both replicas within 10 seconds
  - Manual promotion of a replica works; former primary demotes cleanly when reachable
  - With `auto_failover=true`, killing the primary causes a replica to take over within 30 seconds
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

- Risk: SSE over HTTP/1.1 doesn't survive transparent proxies / firewalls
  - Trigger: replica disconnects every N seconds without a clean reason
  - Response: add periodic reconnection with backoff; document supported network setups; consider WebSocket fallback in M2.5

- Risk: Quorum auto-failover causes split-brain in flaky networks
  - Trigger: two nodes both claim primary after a partition heals
  - Response: design coordinated step-down with config version comparison; document as a known limitation; auto-failover stays opt-in

- Risk: Join token theft allows unauthorized node enrollment
  - Trigger: token leaked from logs or terminal history
  - Response: short TTL (15 min), single-use, primary logs all enrollment attempts; rotate tokens never reused

## Open questions

None. M2 design questions resolved 2026-05-29 (see QUESTIONS_AND_ANSWERS.md).

## Hypotheses to validate in M2

- H2: Quorum-based primary step-down (last-seen + health checks) prevents split-brain for ≤ 10 nodes. **Validate at Slice 4.**
- H3: SSE over HTTP/1.1 is sufficient for config sync transport. **Validate at Slice 2 by measuring reconnection behavior.**

## Change log

- 2026-05-29: Initial plan created for Milestone 1.
- 2026-05-29: Milestone 1 complete; plan updated for Milestone 2.
