# TODO

## Intent

Build skoed: a self-hosted DNS filtering and parental control solution with multi-node sync, web UI, and container-native deployment. Foundation artifacts are now validated; proceeding with BDD-First delivery starting at Milestone 1.

## Preconditions
- [x] AGENTS_MODE: standard (default — .env absent)
- [x] PROBLEM_STATEMENT.md validated by UoR
- [x] UBIQUITOUS_LANGUAGE.md validated by UoR
- [x] GLOBAL_TECHNICAL_ARCHITECTURE.md validated by UoR
- [x] ROADMAP.md validated by UoR
- [ ] IMPLEMENTATION_PLAN.md created

## Active features

**M13 — Temporary Filtering Pause** — branch `feature/m13-filtering-pause`
**M14 — Block Dynamic-Lease Clients** — branch `feature/m14-block-dynamic-clients`
**M15 — Test Suite Cleanup + keepalived Reference** — branch `feature/m15-test-suite-keepalived`

## Completed milestones
- [x] M1–M9 merged to master — 2026-06-09
- [x] M10 — Active-Active Cluster — merged to master — 2026-06-09
- [x] M11 — Distribution & Documentation — merged to master — 2026-06-10
- [x] M12 — Cluster Join via Web UI + Config Backup/Restore — merged to master — 2026-06-12

## M12 tasks

- [x] M12 — functional spec: `specs/functional/cluster-join-webui.feature` (5 FSIDs) — 2026-06-12
- [x] M12 — UoR validated functional spec — 2026-06-12
- [x] M12 — functional spec: `specs/functional/config-backup-webui.feature` (3 FSIDs) — 2026-06-12
- [x] M12 — UoR validated functional spec — 2026-06-12
- [x] M12 — technical spec: `POST /api/v1/node/join-cluster` added to `specs/technical/management-api.openapi.yaml` (TS-ClusterJoinWebUi) — 2026-06-12
- [x] M12 — technical spec: export/import endpoints updated with FS-ConfigBackupWebUi* links — 2026-06-12
- [x] M12 — acceptance tests: `tests/acceptance/cluster_join_webui_test.go` (5 tests) — 2026-06-12
- [x] M12 — acceptance tests: `tests/acceptance/config_test.go` extended (3 tests) — 2026-06-12
- [x] M12 — all 8 acceptance tests green — 2026-06-12
- [x] M12 — implementation: `POST /api/v1/node/join-cluster` handler, `Cluster.ResetRaftForJoin()` — 2026-06-12
- [x] M12 — implementation: `exportShape` struct (credentials stripped from backup) — 2026-06-12
- [x] M12 — implementation: `Cluster.vue` join panel (single-node mode only) — 2026-06-12
- [x] M12 — implementation: `Settings.vue` config backup section — 2026-06-12
- [x] M12 — committed: `99ed32a` on `feature/cluster-join-config-backup-ui` — 2026-06-12
- [x] M12 — refactoring phase — implementation is clean, no behavior changes needed — 2026-06-12
- [x] M12 — all 8 acceptance tests green in Docker — 2026-06-12
- [x] M12 — demo note: `demos/m12/DEMO_NOTE.md` — 2026-06-12
- [x] M12 — CI green — runs on master push — 2026-06-12
- [x] M12 — UoR demo validation — confirmed on Proxmox cluster — 2026-06-12
- [x] M12 — merged to master at 87bb02a — 2026-06-12

## Current tasks

- [x] M10 — functional spec written: `specs/functional/active-active-cluster.feature` (7 FSIDs) — 2026-06-09
- [x] M10 — UoR validated functional spec — 2026-06-09
- [x] M10 — technical spec written: `specs/technical/active-active-cluster.md` (TS-ActiveActiveCluster) — 2026-06-09
- [x] M10 — acceptance tests written: `tests/acceptance/active_active_cluster_test.go` (5 pass + 2 skip stubs) — 2026-06-09
- [x] M10 — implementation done: `WriteForwardMiddleware`, `NodeID()`, `CommitIndex()`, `IsLeader()`, handler redirects removed — 2026-06-09
- [x] M10 — refactoring phase: removed clusterWriteAdapter (~75 lines), extracted package-level forwardClient + hopByHopHeaders, fixed X-Served-By header dedup in middleware — 2026-06-09
- [x] M10 — demo script: `demos/m10/demo.sh` (3-node cluster, write forwarding verified live) — 2026-06-09
- [x] M10 — demo note written: `demos/m10/DEMO_NOTE.md` — 2026-06-09
- [x] M10 — merged to master — 2026-06-09

## M11 tasks

- [x] M11 — functional spec: `specs/functional/packaging-and-distribution.feature` (10 FSIDs) — 2026-06-10
- [x] M11 — technical spec: `specs/technical/packaging-and-distribution.md` (TS-PackagingAndDistribution) — 2026-06-10
- [x] M11 — acceptance tests: `tests/acceptance/packaging_test.go` (5 pass + 2 skip stubs) — 2026-06-10
- [x] M11 — Alpine APK: goreleaser apk format + `make apk`/`make apk-arm64` — 2026-06-10
- [x] M11 — AUR PKGBUILD: `packaging/aur/PKGBUILD` + `.SRCINFO` + CI sync step — 2026-06-10
- [x] M11 — Helm chart: `charts/skoed/` (Deployment/DaemonSet, PVC, Service, ConfigMap, SA) — 2026-06-10
- [x] M11 — Proxmox script in goreleaser extra_files — 2026-06-10
- [x] M11 — CI updated: `dblock-*` branches, helm-lint job, docs build job, apk packaging step — 2026-06-10
- [x] M11 — Release workflow: Helm OCI publish, AUR sync, docs dispatch — 2026-06-10
- [x] M11 — README.md rewrite (badges, quickstart, feature list, install matrix, cluster quickstart) — 2026-06-10
- [x] M11 — All 15 doc stubs filled: docker, kubernetes, cluster ops, all configuration pages, all operations pages, all reference pages — 2026-06-10
- [x] M11 — demo note: `demos/m11/DEMO_NOTE.md` — 2026-06-10
- [x] M11 — merged to master — 2026-06-10

- [x] M9 — functional spec written: `specs/functional/kubernetes-operator.feature` (9 FSIDs) — 2026-06-09
- [x] M9 — UoR validated functional spec — 2026-06-09
- [x] M9 — technical spec written: `specs/technical/kubernetes-operator.md` (TS-KubernetesOperator) — 2026-06-09
- [x] M9 — acceptance tests written: `tests/acceptance/kubernetes_operator_test.go` (7 FSIDs + 2 skip stubs) — 2026-06-09
- [x] M9 — implementation done: `apps/skoed-operator/` (controller-runtime v0.19.0, SkoedCluster + SkoedNode CRDs, Helm chart) — 2026-06-09
- [x] M9 — demo note written: `demos/m9/DEMO_NOTE.md` — 2026-06-09
- [x] M9 — merged to master (ab2bd92) — 2026-06-09
- [x] M9 — refactoring phase (removed unused resource import/hack, extracted applyDefaults, dropped unused callAPI body param, added status API log) — 2026-06-09

## Blockers

None.

## M13 tasks

- [x] M13 — ROADMAP.md entry — 2026-06-12
- [x] M13 — functional spec: `specs/functional/filtering-pause.feature` (16 FSIDs, global + per-profile, query log during pause) — 2026-06-12
- [x] M13 — UoR validated functional spec — 2026-06-12
- [x] M13 — technical spec: pause endpoints in `specs/technical/management-api.openapi.yaml` (TS-FilterPause, 6 endpoints, 2 schemas) — 2026-06-12
- [x] M13 — acceptance tests: `tests/acceptance/filtering_pause_test.go` (16 tests, compile green) — 2026-06-12
- [x] M13 — implementation: pause state in config, filter engine short-circuit, API handlers (16/16 acceptance tests green) — 2026-06-12
- [x] M13 — refactoring phase: removed `now2` shadow, clarified sentinel comment — 2026-06-12
- [x] M13 — demo note: `demos/m13/DEMO_NOTE.md` — 2026-06-12
- [x] M13 — UoR demo validation — 2026-06-12
- [x] M13 — merge to master — 2026-06-12

## M14 tasks

- [x] M14 — ROADMAP.md entry — 2026-06-12
- [x] M14 — functional spec: `specs/functional/profile-block-dynamic-clients.feature` (10 FSIDs) — pre-existing
- [x] M14 — acceptance tests: `tests/acceptance/profile_block_dynamic_test.go` (10 tests, skip-stubbed) — pre-existing
- [x] M14 — implementation: block_dynamic_clients field, filter engine wiring, validation, clients API origin/profile_ids
- [x] M14 — all 10 acceptance tests green (no skip stubs)
- [x] M14 — refactoring phase — implementation clean, no changes needed
- [x] M14 — demo note: `demos/m14/DEMO_NOTE.md`
- [x] M14 — UoR demo validation — 2026-06-16
- [ ] M14 — merge to master

## M15 tasks

### M15-A — Test suite cleanup + Alt-Svc
- [x] M15 — functional spec: `FS-Doh3AltSvcAdvertised`, `FS-Doh3AltSvcAbsentWhenDisabled` added to `specs/functional/encrypted-dns-expansion.feature` — 2026-06-16
- [x] M15 — UoR validated functional spec — 2026-06-16 (inline, scope approved)
- [x] M15 — technical spec: Alt-Svc header behavior added to `specs/technical/encrypted-dns-expansion.md` (section 10) — 2026-06-16
- [x] M15 — acceptance tests: `TestDoh3AltSvcAdvertised`, `TestDoh3AltSvcAbsentWhenDisabled` in `tests/acceptance/encrypted_dns_expansion_test.go` — 2026-06-16
- [x] M15 — test fix: `tests/acceptance/blocklist_refresh_test.go` — `SKOED_TEST_MODE=1` + accept "unchanged" on initial refresh — 2026-06-16
- [x] M15 — test fix: `tests/acceptance/doh_resolver_database_test.go` — `SKOED_TEST_MODE=1` on timing-sensitive tests — 2026-06-16
- [x] M15 — implementation: Alt-Svc header in `apps/skoed/internal/dns/encrypted.go:handleDoH` — 2026-06-16
- [x] M15 — all altered acceptance tests green (6/6 refresh, 2/2 Alt-Svc) — 2026-06-16

### M15-C — keepalived reference (BDD-exempt, logged in LOGS.md)
- [x] M15 — keepalived: `deploy/keepalived/keepalived.conf.template` + `deploy/keepalived/skoed-health.sh` — 2026-06-16
- [x] M15 — keepalived: `docs/src/cluster/keepalived.md` page + SUMMARY.md entry — 2026-06-16
- [x] M15 — keepalived: real-env deployment on CT301/302/303 — VIP 10.0.0.10 — failover PASS — 2026-06-16
- [x] M15 — keepalived: fixed 5 provisioning bugs (pre-destroy, Bearer auth, health field, curl missing, api_address) — 2026-06-16
- [x] M15 — keepalived: `docs/src/cluster/keepalived.md` corrected (eth0, curl prereq, api_address req) — 2026-06-16

### M15 completion
- [x] M15 — refactoring phase — no refactoring needed (changes are minimal) — 2026-06-16
- [x] M15 — demo note: `demos/m15/DEMO_NOTE.md` updated with real-env keepalived results — 2026-06-16
- [x] M15 — test report: `demos/m15/test-report.html` + `docs/src/releases/m15-test-report.html` — 2026-06-16
- [x] M15 — UoR demo validation — 2026-06-16
- [x] M15 — merge to master — 2026-06-16 (merged at 299b341)
- [x] M15 — tag v0.1.3 pushed — GitHub Release pending (gh CLI unavailable, create via web UI)

## Backlog

### Packaging + deployment (both shipped in M11 — strike)
- ~~Proxmox LXC script~~ — shipped in M11 (`scripts/proxmox-cluster.sh`)
- ~~Debian .deb packages~~ — shipped in M11 (goreleaser nfpm + `packaging/`)

### Strategic

- ~~**Find a better name.**~~ Done — name is **skoed**. — closed 2026-06-09

### From the ROADMAP post-M5 backlog (now mirrored here for tracking)

- **Active-active cluster** — any-node writes via Raft. Today the
  leader serializes writes; this would let multi-DC deployments
  accept writes locally. Major architecture change.
- **DoH3 / HTTP/3 endpoint.** Client adoption is still slow but
  growing; defer until usage warrants the QUIC dep.
- **DNSCrypt server endpoint.** Rare in modern clients; lowest
  priority of the encrypted-DNS family.
- **API token authentication.** Replace HTTP Basic Auth with revocable
  tokens (scopes, per-token rate limits, audit log integration).
- ~~**IPv6-only / dual-stack validation.**~~ Closed — dual-stack (IPv4+IPv6 simultaneous) is the shipped mode; IPv6-only standalone deploy is low-value. — 2026-06-09
- ~~**Kubernetes operator.**~~ Done — M9 shipped (SkoedCluster + SkoedNode CRDs, controller-runtime v0.19.0). — 2026-06-09
- **Firewall-rule generators** (from M3.5 carve-out). Templates for
  iptables / nftables / MikroTik RouterOS / OpnSense / pfSense / UniFi
  to close the hardcoded-resolver-IP DoH bypass.
- **Curated DoH-resolver-IP database** + auto-refresh (companion to
  the firewall generators).
- **"Closing the DoH gap" guide** (companion docs).
- **ARP/NDP cross-check** (M3.6 Layer-3 anti-spoof). Query the local
  ARP cache via netlink; flag when DHCP-reported MAC ≠ ARP-reported
  MAC. Requires CAP_NET_ADMIN.
- **Raft-replicated lease cache** (M3.6 follow-on). Today each node
  polls its own DHCP source; replicating leases would give true
  cluster-wide identity consistency at the cost of leader-only-polls.
- **DHCPv6 lease parsing** (M3.6 follow-on).

### Under reconsideration — needs UoR decision

- **Mobile application.** Deferred until there is confirmed evidence
  that "official" DNS (ISP resolver, DoH in browser/OS) is bypassed
  by skoed in the target deploy topology. Needs M7 API tokens first.
  — still open 2026-06-09

*(Transparent proxy, DPI/HTTP filtering, and cloud SaaS moved to permanent
non-goals — 2026-06-09)*

## Open questions

None for M2 design (see QUESTIONS_AND_ANSWERS.md for resolved M2 decisions).

## Resolved questions

- DNSSEC: transparent proxy — forward DNSSEC records (RRSIG, DNSKEY, DS, NSEC) as-is to clients that set the DO bit. skoed does not validate. — 2026-05-29
- Block policy: configurable per blocklist, with a global default. Supported values: NXDOMAIN, NULL (0.0.0.0 / ::), NODATA. — 2026-05-29
- IPv6: full dual-stack — DNS listener on IPv4 and IPv6, AAAA records in local DNS entries, IPv6 client identification in query log and profiles, NULL block returns both 0.0.0.0 and ::. — 2026-05-29
- Wildcard syntax: `*.example.com` matches the apex (`example.com`) and all subdomains at any depth (`sub.example.com`, `a.b.example.com`). Applies to both blocklists and allowlists. — 2026-05-29
- Client groups (M3): a client may belong to multiple groups; effective rules are the union of all group blocklists and all group allowlists. Ungrouped clients use a built-in default group. — 2026-05-29
- Client identification (M3): primary = DHCP API integration (Kea REST API, dnsmasq lease file, ISC DHCP lease file, generic HTTP API); fallback = IP-only identification when no DHCP integration is configured or IP is not in the lease table. — 2026-05-29

## Hypotheses

- H1: `miekg/dns` is sufficient for skoed's DNS engine needs (forwarding + root resolution). — **VALIDATED** at M1 implementation (2026-05-29).
- H2: Quorum-based primary step-down (last-seen + health checks) prevents split-brain in practice for home/lab scale (≤ 10 nodes). — **OBSOLETED 2026-05-29** by H4 (Raft architecture).
- H3: SSE over HTTP/1.1 is sufficient for config sync transport. — **OBSOLETED 2026-05-29** by H4 (Raft architecture).
- H4: hashicorp/raft + go.etcd.io/bbolt are operationally suitable for skoed's workload (≤10 nodes, ≤1 write/day per cluster, ~1–10 MB state). — open, validate throughout M2.

## Done when

- Milestone 1: single node serves DNS, blocks ads, serves local entries, supports config import/export. All acceptance tests green. — **DONE** 2026-05-29.
- Milestone 2: primary + 2 replicas brought up in `docker compose`; config change on primary visible on replicas within 10s; manual + opt-in auto failover work; cluster status dashboard surfaces node roles and last-seen.
