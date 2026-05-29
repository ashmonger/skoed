# TODO

## Intent

Build dblock: a self-hosted DNS filtering and parental control solution with multi-node sync, web UI, and container-native deployment. Foundation artifacts are now validated; proceeding with BDD-First delivery starting at Milestone 1.

## Preconditions
- [x] AGENTS_MODE: standard (default — .env absent)
- [x] PROBLEM_STATEMENT.md validated by UoR
- [x] UBIQUITOUS_LANGUAGE.md validated by UoR
- [x] GLOBAL_TECHNICAL_ARCHITECTURE.md validated by UoR
- [x] ROADMAP.md validated by UoR
- [ ] IMPLEMENTATION_PLAN.md created

## Active feature

**Milestone 2 — Multi-Node Cluster**
Current phase: **Phase 1 — Functional Specifications**

## Current tasks

- [x] M1 — merged to master, all 58 acceptance tests green, demo validated — 2026-05-29
- [x] M2 — initial design questions answered (failover model, sync direction, Helm scope) — 2026-05-29
- [x] M2 — architecture pivot: hashicorp/raft + bbolt as source of truth; obsoletes SSE / manual+quorum failover — 2026-05-29
- [x] M2 — IMPLEMENTATION_PLAN.md updated — 2026-05-29
- [x] M2 — functional specs revised for Raft architecture (5 .feature files: node-enrollment, cluster-config-sync, leader-failover, cluster-status, query-log-aggregates) — 2026-05-29
- [x] M2 — technical specs written: OpenAPI extended with /cluster/* endpoints; raft-fsm.md, cluster-store.md, query-log-cluster.md added; config-schema.md flagged as import/export only — 2026-05-29
- [ ] M2 — Write acceptance tests
- [ ] M2 — Implementation
- [ ] M2 — Refactoring phase
- [ ] M2 — Demo: docker compose with primary + 2 replicas
- [ ] M2 — UoR validation and merge to master

## Blockers

None.

## Open questions

None for M2 design (see QUESTIONS_AND_ANSWERS.md for resolved M2 decisions).

## Resolved questions

- DNSSEC: transparent proxy — forward DNSSEC records (RRSIG, DNSKEY, DS, NSEC) as-is to clients that set the DO bit. dblock does not validate. — 2026-05-29
- Block policy: configurable per blocklist, with a global default. Supported values: NXDOMAIN, NULL (0.0.0.0 / ::), NODATA. — 2026-05-29
- IPv6: full dual-stack — DNS listener on IPv4 and IPv6, AAAA records in local DNS entries, IPv6 client identification in query log and profiles, NULL block returns both 0.0.0.0 and ::. — 2026-05-29
- Wildcard syntax: `*.example.com` matches the apex (`example.com`) and all subdomains at any depth (`sub.example.com`, `a.b.example.com`). Applies to both blocklists and allowlists. — 2026-05-29
- Client groups (M3): a client may belong to multiple groups; effective rules are the union of all group blocklists and all group allowlists. Ungrouped clients use a built-in default group. — 2026-05-29
- Client identification (M3): primary = DHCP API integration (Kea REST API, dnsmasq lease file, ISC DHCP lease file, generic HTTP API); fallback = IP-only identification when no DHCP integration is configured or IP is not in the lease table. — 2026-05-29

## Hypotheses

- H1: `miekg/dns` is sufficient for dblock's DNS engine needs (forwarding + root resolution). — **VALIDATED** at M1 implementation (2026-05-29).
- H2: Quorum-based primary step-down (last-seen + health checks) prevents split-brain in practice for home/lab scale (≤ 10 nodes). — **OBSOLETED 2026-05-29** by H4 (Raft architecture).
- H3: SSE over HTTP/1.1 is sufficient for config sync transport. — **OBSOLETED 2026-05-29** by H4 (Raft architecture).
- H4: hashicorp/raft + go.etcd.io/bbolt are operationally suitable for dblock's workload (≤10 nodes, ≤1 write/day per cluster, ~1–10 MB state). — open, validate throughout M2.

## Done when

- Milestone 1: single node serves DNS, blocks ads, serves local entries, supports config import/export. All acceptance tests green. — **DONE** 2026-05-29.
- Milestone 2: primary + 2 replicas brought up in `docker compose`; config change on primary visible on replicas within 10s; manual + opt-in auto failover work; cluster status dashboard surfaces node roles and last-seen.
