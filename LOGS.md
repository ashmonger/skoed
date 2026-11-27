# Logs

## Decisions log

- **2026-11-27** — Under 400–3,000 qps in the DNS overload acceptance tests (branch `work/2/overload-tests`), node-2 exited with `fatal error: concurrent map iteration and map write` in the stats aggregator (`apps/skoed/internal/log/aggregates.go`, `tryFlush` → `buildAggregate` → `topN`): the flush iterates the hour bucket's maps without the lock while every query's `Observe` writes them. Present since v0.1.0. The UoR approved a direct fix without a functional specification (detach the bucket under the lock, merge it back on commit failure) and a hotfix release v0.5.2; see `decisions/20261127-DirectFixAggregatorMapRace.md`.
- **2026-11-27** — skoed exited on `bind: cannot assign requested address` after a reboot of a Proxmox LXC container, started by systemd before the container's IP was set: once in the 5-node performance run (`demos/perf-5node-lxc/REPORT.md` T8, `tls listen 10.10.10.14:7000`) and twice in the journal of CT 221 during the AdGuard Home comparison runs (`start raft: raft transport: listen tcp 10.10.10.21:7000`; results on the perf host in `/root/perf/results/adguard-compare/`). The UoR approved a direct fix, with an acceptance test in place of a functional specification: skoed waits up to 60 s for a listen address that is not assigned yet; see `decisions/20261127-WaitForUnassignedListenAddress.md`.
- **2026-11-26** — The UoR removed three planned milestones from the roadmap: M40 Companion / Remote-Admin App, M48 Skoed4Phone (DNS over VPN) and M49 Browser Extension Distribution. None had specs, tests or code; their sections are gone from `ROADMAP.md` and their rows from the `TODO.md` milestone index.
- **2026-09-17** — v0.4.3 was validated on a 3-node Proxmox cluster built from the published release. DNS forwarding, blocklist replication and leader failover passed, and the `/var/lib/skoed/bin` relocation was confirmed in the `.deb`. Two defects were found that no specification covers: an enabled blocklist with zero domains is accepted and silently filters nothing, and the Alpine provisioning script creates a `skoed` user and then runs the daemon as root. The UoR approved fixing both directly, with acceptance tests in place of a functional specification; see `decisions/20260917-DirectFixForValidationDefects.md`. The audit findings M-15, H-5 and M-5 were also reproduced live on the cluster.
- **2026-06-30** — M34.5 infrastructure fix: `raft.Config.SnapshotThreshold` lowered from 8192 → 64 and `SnapshotInterval` set to 30 s (`internal/cluster/raft.go`). Root cause: hashicorp/raft v1.7.3 does not persist `lastApplied`; with the default threshold and ~663 log entries no snapshot was ever taken, causing a ~30-second full log replay on every restart. API requests served during replay returned stale mid-replay values. The fix is transparent to operators — no migration required. Validated: all 3 Proxmox nodes restart in <2 s with correct state after the first snapshot is taken.
- **2026-06-10** — M11 spec-lint updated to accept `*_test.go` acceptance tests (Go) alongside `*.test.ts` (TypeScript). The template default checks for TypeScript; this project uses Go throughout.
- **2026-06-10** — M11 Rule 6 documentation exemption granted for `README.md` and all `docs/src/**/*.md` pages (see Exception pointers below).

## Outcomes log

- **Date**: 2026-07-17
  **Artifact**: M35 — Filtering Pause Enhancements
  **Outcome**: Shipped (released in v0.4.0). Delivered per-client pause (`client_ips` on `POST /api/v1/profiles/{id}/pause`), pause history (`GET .../pause/history`, 50-entry cap), `filter.pause_expired` webhook event, and the new-dynamic-client dashboard alert (`GET/POST /api/v1/clients/new-dynamic`), all Raft-replicated. 10/10 acceptance tests; Proxmox 3-node validation 23/23 (14 functional + 9 replication). A whole-codebase security audit surfaced four M35 defects, all fixed with regression tests and released across v0.4.0–v0.4.3: C-1 (FSM `closePauseHistoryEntry` used `time.Now()` inside Apply → cluster divergence, live-verified 54 ms spread; now uses payload `EndedAt`), H-1 (per-client pause failed open — unparseable IPs paused the whole profile; now rejected at the API and fail-closed in the engine), H-4 (dismiss was a Raft delete resurrected by per-query local tracking; now a replicated tombstone), M-14 (pause-expiry watcher gained panic recovery, context cancellation, leader-only guard). UoR sign-off 2026-07-17. Web UI pause-management controls (duration picker, countdown badge) remain a UI follow-up. Demo: `demos/m35/`.

- **Date**: 2026-07-17
  **Artifact**: M50 — Hardening & Technical Debt (roadmap milestone created)
  **Outcome**: New milestone added to `ROADMAP.md` and the `TODO.md` index to consolidate the deferred findings from the 2026-07 whole-codebase security audit (`SECURITY_AUDIT.md`: H-5 node-local sessions, H-10 recursor bailiwick, H-14 DHCP hardening, Mediums M-1..M-17, Lows) and engineering debt surfaced during the v0.4.x upgrade incident. Also captures a CI gap discovered this session: `ci.yml` runs `go vet` + the acceptance suite but no `go test ./...` for `apps/skoed` internal packages, so unit tests (version-compare, upgrade-skip) are never executed in CI. The audit's Criticals, most Highs, and the upgrade robustness fixes already shipped in v0.4.x. M50 is roadmap-only pending UoR validation; no specs/tests/code written yet (BDD-First).

- **Date**: 2026-07-01
  **Artifact**: M35.5 — Named Device Registry
  **Outcome**: Shipped. `Device` entity (name, profile_id, MACs, IPs, hostnames, client_ids) persisted in bbolt `config_devices` bucket and replicated cluster-wide via Raft. REST CRUD at `/api/v1/devices`. DNS filter engine gains Tier 0 device-registry match (beats CIDR Tier 1 and default profile); MAC extracted from EDNS0 option 65501. Query log enriched with `device_name`, `device_id`, `match_source`. Devices.vue replaces Clients page with register/edit side panel and real-time search. Critical fix: `fsm.Restore()` calls `store.init()` after snapshot restore — prevents nil panic on rolling upgrade from a pre-M35.5 snapshot. 9/9 acceptance tests green (Docker). 17/17 Proxmox 3-node cluster validation checks pass. Demo: `demos/m355/`.

- **Date**: 2026-06-30
  **Artifact**: M34.5 — Configurable Session Timeout
  **Outcome**: Shipped. `session_timeout_seconds` added to `AuthConfig` and persisted cluster-wide via Raft. Settings page gains a 6-preset session timeout selector (30 min / 1 h / 4 h / 8 h / 24 h / 7 d). Expiry enforced on every authenticated request; expired tokens return HTTP 401 and the browser redirects to login. Infrastructure fix applied: `SnapshotThreshold=64` eliminates the 30-second Raft log replay on restart. 19/19 Proxmox 3-node validation checks pass. Demo: `demos/m345/`.

- **Date**: 2026-05-29
  **Artifact**: Foundation artifacts (SOLUTION.md, PROBLEM_STATEMENT.md, UBIQUITOUS_LANGUAGE.md, GLOBAL_TECHNICAL_ARCHITECTURE.md, ROADMAP.md, TODO.md, QUESTIONS_AND_ANSWERS.md)
  **Outcome**: Generated from UoR free-text description via bootstrap path (c). Awaiting UoR validation.

## Hypotheses log

- **Date**: 2026-05-29
  **Hypothesis**: H1 — `miekg/dns` is sufficient for skoed's DNS engine (forwarding + root resolution + custom records).
  **Validation plan**: Prototype DNS engine at M1 implementation start; evaluate alternatives (`coredns`) if blocked.
  **Status**: Open

- **Date**: 2026-05-29
  **Hypothesis**: H2 — Quorum-based primary step-down (last-seen timestamps + health checks) is sufficient split-brain prevention at home/lab scale (≤ 10 nodes).
  **Validation plan**: Validate during M2 design; if not sufficient, evaluate Raft.
  **Status**: Open

## Visibility pointers (optional)
- Canonical decisions and exceptions are recorded in `decisions/YYYYMMDD-<CamelCaseName>.md`.

## Exception pointers (optional)

- **2026-11-27 — Direct fix for the stats aggregator map race** (Rule 5)
  - Scope: `tryFlush` and the merge on commit failure in `apps/skoed/internal/log/aggregates.go`; see `decisions/20261127-DirectFixAggregatorMapRace.md`.
  - Rationale: an internal defect with a known cause that makes a node exit under DNS load; the counting behavior is already specified by `FS-QueryLogAggregatesPerNodePerHour`, which the crash and the dropped queries break, so a new functional spec would not settle anything in question.
  - Mitigation: the DNS overload acceptance tests found it (two crashes on 2026-11-27) and are expected to run without the crash after the fix; no retroactive functional scenario is owed.
  - UoR approval: inline (UoR approved the fix directly on 2026-11-27 and asked for a hotfix release v0.5.2).

- **2026-11-27 — Wait for an unassigned listen address** (Rule 5)
  - Scope: the bounded bind retry for every listener bound to a specific IP (Raft plain and mTLS, peer API, main API, dual-port HTTPS) and `tests/acceptance/startup_unassigned_address_test.go`; see `decisions/20261127-WaitForUnassignedListenAddress.md`.
  - Rationale: the defect is reproduced three times on Proxmox LXC and the expected behavior was stated by the UoR, so a functional spec would not settle anything in question.
  - Mitigation: an acceptance test carries the contract and was seen to fail before the fix; a retroactive functional scenario with an `@fsid` is owed (TODO.md).
  - UoR approval: inline (UoR asked for the fix directly on 2026-11-27).

- **2026-06-16 — M15 keepalived Reference Exemption** (Rule 6)
  - Scope: `deploy/keepalived/keepalived.conf.template`, `deploy/keepalived/skoed-health.sh`, `docs/src/cluster/keepalived.md`.
  - Rationale: pure deployment-configuration templates and documentation. No skoed binary code changes; no effect on acceptance tests.
  - Mitigation: templates manually validated against a 3-node Proxmox LXC cluster.
  - UoR approval: inline (UoR requested M15-C explicitly).

- **2026-06-10 — M11 Documentation Exemption** (Rule 6)
  - Scope: `README.md` rewrite and all `docs/src/**/*.md` page content.
  - Rationale: these deliverables are documentation only — they describe observable product behavior but do not themselves implement it. Changes have no effect on test outcomes.
  - Mitigation: documentation accuracy is validated against the already-shipped implementation and FSID catalog; any claim in the docs maps to a spec or demo output.
  - UoR approval: inline (UoR requested M11 explicitly including "full wiki" and "nice README.md").

- **2026-11-27 — Fourth review cycle for the DNS overload and startup specs** (Rule 14): see `decisions/20261127-FourthSpecReviewCycle.md`.
