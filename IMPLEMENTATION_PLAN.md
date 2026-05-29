# Implementation Plan

## Context
- Planning scope: Milestone 1 — Single Node Foundation
- Roadmap links: ROADMAP.md § Milestone 1
- Planning horizon: Milestone 1 only; M2+ planned after M1 demo validation
- Scope summary: A single dblock node that serves DNS (forwarding + root resolution, dual-stack, DNSSEC transparent), filters domains via configurable blocklists and allowlists, serves local DNS entries, logs queries, exposes a web UI with basic auth, and supports config import/export. Released as a Linux binary and Docker image.
- Assumptions:
  - `miekg/dns` covers all DNS engine needs (H1 — validated at Slice 1 start).
  - No external database; all state in YAML files on disk.
  - Web UI is a compiled Vue.js SPA embedded in the binary via Go `embed`.

## Global feature sequencing

| Order | Feature | Outcome | Depends on | FSIDs | TSIDs | Acceptance tests | Status |
|-------|---------|---------|-----------|-------|-------|-----------------|--------|
| 1 | DNS Engine Core | Queries resolved via upstream or root DNS; dual-stack; DNSSEC transparent | — | FS-DnsQueryForwarding, FS-RootDnsResolution, FS-DualStackDns, FS-DnssecTransparentProxy | TS-DnsEngine | tests/acceptance/dns_engine_test.go | Planned |
| 2 | Filtering Engine | Blocked domains return configured response; allowlist overrides | DNS Engine Core | FS-DomainBlocking, FS-BlockPolicyConfiguration, FS-BlocklistManagement, FS-AllowlistManagement | TS-FilteringEngine, TS-BlocklistApi | tests/acceptance/filtering_test.go | Planned |
| 3 | Local DNS Entries | Admin-defined A/AAAA/CNAME records served to clients | DNS Engine Core | FS-LocalDnsEntryManagement | TS-LocalDnsApi | tests/acceptance/local_dns_test.go | Planned |
| 4 | Config Store | Full config readable/writable as YAML; import/export works | Filtering Engine, Local DNS | FS-ConfigImportExport | TS-ConfigApi | tests/acceptance/config_test.go | Planned |
| 5 | Query Log | Every query logged; admin can browse by client and outcome | DNS Engine Core | FS-QueryLog | TS-QueryLogApi | tests/acceptance/query_log_test.go | Planned |
| 6 | Management API + Auth | All management operations available over HTTP; unauthenticated requests rejected | Config Store, Query Log | FS-WebUiAuthentication | TS-ManagementApi | tests/acceptance/auth_test.go | Planned |
| 7 | Web UI | SPA embedded in binary; all management features accessible via browser | Management API | (covered by above FSIDs) | TS-WebUi | manual demo | Planned |
| 8 | Packaging | Linux binary (amd64, arm64) + Docker image (≤ 100 MB) | All slices | — | — | CI size gate | Planned |

## Cross-feature dependencies and blockers

| Dependency | Upstream | Downstream | Impact if late | Mitigation | Status |
|-----------|---------|-----------|---------------|-----------|--------|
| `miekg/dns` capability | DNS Engine Core | All | Blocked if library insufficient | Evaluate at Slice 1; fallback to coredns library | Open (H1) |
| Config store schema finalized | Config Store | Import/export, Web UI | Schema churn breaks tests | Finalize schema before writing acceptance tests | Open |
| Vue.js embed build pipeline | Web UI | Packaging | Binary build broken | Set up build pipeline (Vite + go:generate) at Slice 7 start | Open |

## Critical path and milestones

- Critical path: DNS Engine Core → Filtering Engine → Config Store → Management API → Web UI → Packaging
- Milestone 1:
  - Exit criteria:
    - All acceptance tests pass
    - `docker run` starts a functional node
    - Linux binary installs and serves DNS within 10 minutes on a fresh host
    - Config exported from one node and imported on another restores identical behavior
    - Web UI accessible; blocklist management, local DNS, and query log functional
    - CI green (tests + image size gate)

## Validation checkpoints

- [ ] Functional specs validated by UoR (all M1 .feature files)
- [ ] Technical specs validated by UoR (OpenAPI for management API + DNS engine notes)
- [ ] Acceptance tests validated by UoR
- [ ] Implementation done (all acceptance tests pass)
- [ ] CI green
- [ ] Refactoring validated (acceptance + full tests green after refactor)
- [ ] Demo prepared and validated by UoR

## Risks and trade-offs

- Risk: `miekg/dns` insufficient for root DNS or DNSSEC transparent proxy
  - Trigger: prototype DNS engine fails to handle root resolution or DNSSEC DO bit forwarding
  - Response: evaluate `coredns` DNS library as drop-in alternative; log decision in `decisions/`

- Risk: embedded Vue.js SPA inflates binary above 50 MB
  - Trigger: binary > 50 MB after Vite build + go:generate
  - Response: audit bundle size; defer non-essential UI features; use dynamic imports

- Risk: blocklist with > 1M domains causes excessive memory use
  - Trigger: idle RAM > 64 MB with a large blocklist loaded
  - Response: replace map-based lookup with radix trie or Bloom filter; measure at Slice 2

## Open questions

None. All M1 questions resolved (see QUESTIONS_AND_ANSWERS.md).

## Change log

- 2026-05-29: Initial plan created for Milestone 1.
