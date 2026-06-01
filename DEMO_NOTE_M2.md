# Milestone 2 Demo Note

**Date:** 2026-06-01
**Branch:** dblock-m2
**Acceptance tests:** 103/103 green (58 M1 + 45 M2)
**Demo image:** `dblock:m2` built from `apps/dblock/Dockerfile` (Alpine 3.20, CGO_ENABLED=0, multi-stage)

## Setup

A 3-node dblock cluster on a user-defined Docker network `dblock-m2`:

| Container | Role | Host ports |
|---|---|---|
| `dblock-1` | Bootstrap leader → demoted by manual transfer → re-elected after failover | 8081 → 8080 (API), 5301 → 53 (DNS) |
| `dblock-2` | Follower → promoted via transfer → removed → restarted | 8082, 5302 |
| `dblock-3` | Follower → removed from cluster | 8083, 5303 |

Two `alpine:3.20` clients ran `dig` against the cluster.

## Scenarios exercised (every one passed)

1. **Single-node bootstrap** — `dblock-1` came up alone; `/cluster/health` reported `mode=single-node, status=ok`.
2. **Join-token issuance** — `POST /cluster/tokens` returned a 64-char hex token with a ~15-minute expiry.
3. **Enrollment of `dblock-2`** — fresh node started with `bootstrap.token` in its config; auto-enrolled via `POST /cluster/join`; cluster became 2-node, both `in_sync`.
4. **Enrollment of `dblock-3`** — same flow with a fresh token; cluster became 3-node; `/cluster/health` flipped to `mode=cluster`.
5. **Leader write replication** — `POST /blocklists` (id=`badads`) on `dblock-1` → all 3 nodes returned `NXDOMAIN` for `tracker.bad.example.com` within 2s.
6. **Follower write forwarding** — `POST /blocklists` (id=`ads-org`) on `dblock-2` → silently forwarded to leader → all 3 nodes blocked `ads.example.org`.
7. **Two-client DNS check** — clients hitting `dblock-1` and `dblock-3` saw identical responses for both blocked and forwarded domains.
8. **`/cluster/health` from a follower** — returned the same view as from the leader; `reachable_members=3`.
9. **Cluster-wide stats** — generated 15 queries spread across nodes; after the organic 60s flush, `/cluster/stats` aggregated `total` and `top_domains` from every per-node bucket.
10. **Cluster query-log fan-out** — `/cluster/query-log` merged entries from every node; each entry tagged with its serving `node_id`; per-node section reported `status:ok, entry_count:N`.
11. **Leadership transfer** — `POST /cluster/leadership/transfer {target_node_id:"dblock-2"}` → 204; within 2s `dblock-2` was leader, term bumped from 2 → 3.
12. **Node removal** — `DELETE /cluster/nodes/dblock-3` → 204; cluster shrunk to 2 voters; a write attempted via `dblock-3` returned HTTP 503 (no leader from its standpoint).
13. **Minority partition** — killed `dblock-2` (current leader). `dblock-1` lost quorum; writes returned `{"error":"no leader", "leader_address":""}`; `/cluster/health` flipped to `status=degraded, has_leader=false, reachable_members=1`.
14. **Recovery** — restarted `dblock-2`; within 5s `dblock-1` became the new leader (term 4); `/cluster/health` returned to `status=ok`.
15. **Shadow YAML** — `cat /var/lib/dblock/config.yaml` on `dblock-1` showed the merged file: `node:` section with its identity, all 3 replicated blocklists, bcrypt admin hash. This is what PBS would back up.

## Scope implemented in M2

- [x] Raft + bbolt FSM replicated state machine (`hashicorp/raft`, `go.etcd.io/bbolt`)
- [x] Single-node bootstrap (zero-config; default to single-node Raft cluster)
- [x] Join token flow (single-use, sha256-hashed, 15-min TTL, leader-validated)
- [x] Cluster membership management (AddVoter on join, RemoveServer on remove)
- [x] Transparent follower → leader write forwarding (every mutating endpoint)
- [x] Cluster status with per-peer live probes (`/cluster/self` + `/cluster/status`)
- [x] Cluster-aware health endpoint (`/cluster/health`)
- [x] Automatic Raft leader election on leader loss
- [x] Manual leadership transfer
- [x] Cluster-wide hourly query-log aggregates (per-node aggregator + Raft commit)
- [x] Follower → leader aggregate forwarding (internal endpoint + shared cluster secret)
- [x] Live fan-out for raw query log (`/cluster/query-log`) with per-node status reporting
- [x] Shadow YAML write-through after each Raft commit (debounced, atomic-rename)
- [x] M1 backward compat (binary auto-synthesises `node:` section from M1 fields and migrates)

## Not implemented in M2 (deferred)

- Web UI for cluster topology (still API-only, same as M1)
- Helm chart / Kubernetes DaemonSet (deferred to M2.5)
- `node.advertise_address` separate from `node.api_address` — see Known Limitations
- Per-client filtering / parental control (M3)

## Known limitations surfaced by this demo

- **Advertise vs bind address** — `node.api_address` is used for BOTH binding and advertising to peers. Operators must set it to a hostname that other cluster members can reach (e.g. `dblock-1:8080`), not `0.0.0.0:8080`. Caught while running scenario 6 in the first attempt; documented here as a backlog item for M2.1 (split `api_address` from a new `api_advertise_address`).
- **Aggregate flush is per-hour boundary or 60s, whichever first** — in production this means the dashboard's cluster-wide stats lag by up to a minute on a low-traffic cluster. Acceptable per the spec; configurable via `DBLOCK_TEST_AGGREGATE_FLUSH_SECONDS` in tests, not yet exposed in production config.
- **Cluster secret is in bbolt** — replicated to every member via Raft; this is by design but means a compromised node yields the cluster's internal auth token. Production deployments should harden via mTLS at the network layer (Tailscale, WireGuard, etc.). M2.5+ work.
- **`/cluster/stats` `aggregate_retention_days` shows 0 in shadow YAML** — the production default (30 days) is only applied at startup via `Defaults()`; the bbolt-derived shadow snapshot reports the raw zero value. Cosmetic; the pruning still uses 30 days. Will be tidied via a settings.patch on first bootstrap in a future commit.

## Production readiness checklist

| Item | Status |
|---|---|
| Single static binary, runs on Alpine/musl | ✓ |
| Multi-stage Dockerfile (~12 MB final image) | ✓ |
| Single-node and cluster modes from same binary | ✓ |
| Acceptance tests cover both modes | ✓ (103 tests) |
| Shadow YAML compatible with PBS-style filesystem backups | ✓ |
| M1 → M2 in-place migration | ✓ |
| Leadership transfer + node removal | ✓ |
| Quorum-based write protection during partition | ✓ |

Ready for UoR validation and merge to main.
