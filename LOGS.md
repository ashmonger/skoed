# Logs

## Decisions log

- **2026-06-30** — M34.5 infrastructure fix: `raft.Config.SnapshotThreshold` lowered from 8192 → 64 and `SnapshotInterval` set to 30 s (`internal/cluster/raft.go`). Root cause: hashicorp/raft v1.7.3 does not persist `lastApplied`; with the default threshold and ~663 log entries no snapshot was ever taken, causing a ~30-second full log replay on every restart. API requests served during replay returned stale mid-replay values. The fix is transparent to operators — no migration required. Validated: all 3 Proxmox nodes restart in <2 s with correct state after the first snapshot is taken.
- **2026-06-10** — M11 spec-lint updated to accept `*_test.go` acceptance tests (Go) alongside `*.test.ts` (TypeScript). The template default checks for TypeScript; this project uses Go throughout.
- **2026-06-10** — M11 Rule 6 documentation exemption granted for `README.md` and all `docs/src/**/*.md` pages (see Exception pointers below).

## Outcomes log

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
