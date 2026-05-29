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

**Milestone 1 — Single Node Foundation**
Current phase: **Phase 4 — Implementation (Milestone 1)**

## Current tasks

- [x] UoR validates the four foundation artifacts (PROBLEM_STATEMENT, UBIQUITOUS_LANGUAGE, GLOBAL_TECHNICAL_ARCHITECTURE, ROADMAP) — 2026-05-29
- [ ] Create IMPLEMENTATION_PLAN.md for Milestone 1
- [x] Write functional specs for Milestone 1 features (11 .feature files) — 2026-05-29
- [x] Write technical specs (3 artifacts: dns-engine.md, config-schema.md, management-api.openapi.yaml) — 2026-05-29
- [x] Write acceptance tests (6 files + harness in tests/acceptance/) — 2026-05-29
- [x] Implement Milestone 1 — all 58 acceptance tests green — 2026-05-29
- [x] Refactoring phase (no behavior change) — all 58 acceptance tests remain green — 2026-05-29
- [x] Demo: two-container Docker demo completed — 2026-05-29 (see DEMO_NOTE.md)
- [x] UoR validation — 2026-05-29
- [x] Merged to master, branch deleted — 2026-05-29

## Blockers

None.

## Open questions

- Multi-node sync model: will use primary+replica with last-seen timestamp quorum. Full consensus (Raft) deferred. — hypothesis, to be validated during M2 design.
- None.

## Resolved questions

- DNSSEC: transparent proxy — forward DNSSEC records (RRSIG, DNSKEY, DS, NSEC) as-is to clients that set the DO bit. dblock does not validate. — 2026-05-29
- Block policy: configurable per blocklist, with a global default. Supported values: NXDOMAIN, NULL (0.0.0.0 / ::), NODATA. — 2026-05-29
- IPv6: full dual-stack — DNS listener on IPv4 and IPv6, AAAA records in local DNS entries, IPv6 client identification in query log and profiles, NULL block returns both 0.0.0.0 and ::. — 2026-05-29
- Wildcard syntax: `*.example.com` matches the apex (`example.com`) and all subdomains at any depth (`sub.example.com`, `a.b.example.com`). Applies to both blocklists and allowlists. — 2026-05-29
- Client groups (M3): a client may belong to multiple groups; effective rules are the union of all group blocklists and all group allowlists. Ungrouped clients use a built-in default group. — 2026-05-29
- Client identification (M3): primary = DHCP API integration (Kea REST API, dnsmasq lease file, ISC DHCP lease file, generic HTTP API); fallback = IP-only identification when no DHCP integration is configured or IP is not in the lease table. — 2026-05-29

## Hypotheses

- H1: `miekg/dns` is sufficient for dblock's DNS engine needs (forwarding + root resolution). — **VALIDATED** at M1 implementation (2026-05-29).
- H2: Quorum-based primary step-down (last-seen + health checks) prevents split-brain in practice for home/lab scale (≤ 10 nodes). — open, validate at M2.

## Done when

- Milestone 1: single node serves DNS, blocks ads, serves local entries, has a working web UI, and config can be imported/exported. All acceptance tests green.
