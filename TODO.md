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

## Milestone index

Legend: ✅ shipped · 🔄 active · ⬜ planned

| ID | Title | Status | Tag | Demo | Spec(s) | Tests |
|----|-------|--------|-----|------|---------|-------|
| M1 | Single Node Foundation | ✅ | — | [note](demos/m1/DEMO_NOTE.md) | [dns-fwd](specs/functional/dns-query-forwarding.feature) · [block](specs/functional/domain-blocking.feature) · [allowlist](specs/functional/allowlist-management.feature) · [local-dns](specs/functional/local-dns-entry-management.feature) · [qlog](specs/functional/query-log.feature) · [config](specs/functional/config-import-export.feature) · [auth](specs/functional/web-ui-authentication.feature) · [dnssec](specs/functional/dnssec-transparent-proxy.feature) · [dual-stack](specs/functional/dual-stack-dns.feature) · [root-dns](specs/functional/root-dns-resolution.feature) | [dns](tests/acceptance/dns_engine_test.go) · [filter](tests/acceptance/filtering_test.go) · [local](tests/acceptance/local_dns_test.go) · [qlog](tests/acceptance/query_log_test.go) · [config](tests/acceptance/config_test.go) · [auth](tests/acceptance/auth_test.go) |
| M2 | Multi-Node Cluster (Raft) | ✅ | — | [note](demos/m2/DEMO_NOTE.md) | [enroll](specs/functional/node-enrollment.feature) · [sync](specs/functional/cluster-config-sync.feature) · [failover](specs/functional/leader-failover.feature) · [status](specs/functional/cluster-status.feature) | [enroll](tests/acceptance/enrollment_test.go) · [sync](tests/acceptance/sync_test.go) · [failover](tests/acceptance/failover_test.go) · [status](tests/acceptance/cluster_status_test.go) |
| M2.5 | Helm Chart | ✅ | — | [note](demos/m2.5/DEMO_NOTE.md) | [helm](specs/functional/helm-chart-deployment.feature) | [helm](tests/acceptance/helm_test.go) |
| M2.6 | Web UI | ✅ | — | [note](demos/m2.6/DEMO_NOTE.md) | [ui](specs/functional/web-ui.feature) · [ui-m3](specs/functional/web-ui-m3.feature) | _(screenshot tests in web/)_ |
| M3 | Parental Control + DoH/DoT Detection | ✅ | — | [note](demos/m3/DEMO_NOTE.md) · [themes](demos/m3/themes-cluster.md) · [ui](demos/m3/web-ui.md) | [profiles](specs/functional/per-client-profiles.feature) · [categories](specs/functional/category-blocking.feature) · [schedules](specs/functional/schedule-rules.feature) · [safesearch](specs/functional/safesearch.feature) · [doh-detect](specs/functional/doh-detection.feature) | [profiles](tests/acceptance/profiles_test.go) · [categories](tests/acceptance/categories_test.go) · [safesearch](tests/acceptance/safesearch_test.go) |
| M3.5 | Per-Client DoH Surfacing + Firewall Recipes | ✅ | — | [note](demos/m3.5/DEMO_NOTE.md) | [doh-status](specs/functional/per-client-doh-status.feature) · [fw-gen](specs/functional/firewall-rule-generator.feature) · [fw-ui](specs/functional/firewall-rules-web-ui.feature) · [resolver-db](specs/functional/doh-resolver-ip-database.feature) | [doh-status](tests/acceptance/client_doh_status_test.go) · [fw-gen](tests/acceptance/firewall_rule_generator_test.go) · [fw-ui](tests/acceptance/firewall_rules_web_ui_test.go) · [resolver-db](tests/acceptance/doh_resolver_database_test.go) |
| M3.6 | DHCP Integration + Anti-Spoof Detection | ✅ | — | [note](demos/m3.6/DEMO_NOTE.md) | [connectors](specs/functional/dhcp-connectors.feature) · [identity](specs/functional/dhcp-client-identity.feature) · [spoof](specs/functional/dhcp-spoof-detection.feature) | [connectors](tests/acceptance/dhcp_connectors_test.go) · [identity](tests/acceptance/dhcp_client_identity_test.go) · [spoof](tests/acceptance/dhcp_spoof_detection_test.go) |
| M4 | DoH/DoT Server + ACME | ✅ | — | [note](demos/m4/DEMO_NOTE.md) · [acme](demos/m4/acme.md) | [doh-dot](specs/functional/doh-dot-server.feature) · [acme](specs/functional/acme-tls.feature) | [doh](tests/acceptance/doh_server_test.go) · [acme](tests/acceptance/acme_tls_test.go) |
| M4.5 | API Documentation Browser | ✅ | — | [note](demos/m4.5/DEMO_NOTE.md) | [api-docs](specs/functional/api-docs-browser.feature) | [api-docs](tests/acceptance/api_docs_test.go) |
| M4.6 | HTTPS Management API | ✅ | — | [note](demos/m4.6/DEMO_NOTE.md) | [mgmt-https](specs/functional/management-api-https.feature) | [mgmt-https](tests/acceptance/management_api_https_test.go) |
| M4.7 | DNS Cache Controls | ✅ | — | [note](demos/m4.7/DEMO_NOTE.md) | [cache](specs/functional/dns-cache-controls.feature) | [cache](tests/acceptance/dns_cache_controls_test.go) |
| M5.1 | Prometheus `/metrics` | ✅ | — | [note](demos/m5/m5.1.md) | [prom](specs/functional/prometheus-metrics.feature) | [prom](tests/acceptance/prometheus_metrics_test.go) |
| M5.2 | Audit Log | ✅ | — | [note](demos/m5/m5.2.md) | [audit](specs/functional/audit-log.feature) | [audit](tests/acceptance/audit_log_test.go) |
| M5.3 | Encrypted Cluster Mesh (mTLS) | ✅ | — | [note](demos/m5/m5.3.md) | [mesh](specs/functional/encrypted-cluster-mesh.feature) | [mesh](tests/acceptance/encrypted_mesh_test.go) |
| M5.4 | Automated Blocklist Refresh | ✅ | — | [note](demos/m5/m5.4.md) | [refresh](specs/functional/automated-blocklist-refresh.feature) | [refresh](tests/acceptance/blocklist_refresh_test.go) |
| M5.5 | Native Packaging (.deb + Proxmox) | ✅ | — | [note](demos/m5/m5.5.md) | [pkg](specs/functional/native-packaging.feature) | [pkg](tests/acceptance/packaging_test.go) |
| M5.6 | In-place Upgrade (check + banner) | ✅ | — | [note](demos/m5/m5.6.md) | [upgrade](specs/functional/in-place-upgrade.feature) | [upgrade](tests/acceptance/in_place_upgrade_test.go) |
| M5.7 | Multi-architecture Release Builds | ✅ | — | [note](demos/m5/m5.7.md) | [multi-arch](specs/functional/multi-arch-builds.feature) | [pkg](tests/acceptance/packaging_test.go) |
| M5.8 | Documentation Site | ✅ | — | [note](demos/m5/m5.8.md) | [docs](specs/functional/documentation-site.feature) | _(CI docs build)_ |
| M5.9.1 | CLI + TUI (charm-stack) | ✅ | — | [note](demos/m5/m5.9.1.md) | [cli](specs/functional/dblock-cli.feature) | [cli](tests/acceptance/cli_test.go) |
| M5.9.2 | `make dev` SPA hot-reload | ✅ | — | [note](demos/m5/m5.9.2.md) | [make-dev](specs/functional/make-dev.feature) | _(build target)_ |
| M5.9.3 | Docker test cache (go-mod volume) | ✅ | — | [note](demos/m5/m5.9.3.md) | [docker-cache](specs/functional/docker-test-cache.feature) | _(run-in-docker.sh)_ |
| M5.9.4 | Getting Started card + docs page | ✅ | — | [note](demos/m5/m5.9.4.md) | [onboard](specs/functional/getting-started.feature) | _(UI test)_ |
| M5.9.5 | URL tester (CLI + landing page) | ✅ | — | [note](demos/m5/m5.9.5.md) | [url-test](specs/functional/url-tester.feature) | [url](tests/acceptance/url_tester_test.go) |
| M5.9.7 | "Would this domain be blocked?" tester | ✅ | — | [note](demos/m5/m5.9.7.md) | [domain-test](specs/functional/domain-tester.feature) | [domain](tests/acceptance/test_domain_test.go) |
| M6 | Closing the DoH Gap (firewall rules) | ✅ | — | [note](demos/m6/DEMO_NOTE.md) | [fw-gen](specs/functional/firewall-rule-generator.feature) · [fw-ui](specs/functional/firewall-rules-web-ui.feature) · [resolver-db](specs/functional/doh-resolver-ip-database.feature) | [fw-gen](tests/acceptance/firewall_rule_generator_test.go) · [fw-ui](tests/acceptance/firewall_rules_web_ui_test.go) · [doh-db](tests/acceptance/doh_resolver_database_test.go) |
| M6.5 | DHCP Layer-3 Anti-Spoof + Replicated Leases | ✅ | — | [note](demos/m6.5/DEMO_NOTE.md) · [demo](demos/m6.5/demo.sh) | [arp](specs/functional/dhcp-arp-cross-check.feature) · [lease-rep](specs/functional/dhcp-lease-replication.feature) · [dhcpv6](specs/functional/dhcpv6-lease-parsing.feature) · [origin](specs/functional/lease-origin-tagging.feature) | [arp](tests/acceptance/dhcp_arp_cross_check_test.go) · [lease-rep](tests/acceptance/dhcp_lease_replication_test.go) · [dhcpv6](tests/acceptance/dhcpv6_lease_parsing_test.go) · [origin](tests/acceptance/lease_origin_tagging_test.go) |
| M7 | API Token Authentication | ✅ | — | [note](demos/m7/DEMO_NOTE.md) | [tokens](specs/functional/api-token-authentication.feature) | [tokens](tests/acceptance/api_token_test.go) |
| M8 | Encrypted DNS Expansion (DoH3 + DNSCrypt) | ✅ | — | [note](demos/m8/DEMO_NOTE.md) | [enc-dns](specs/functional/encrypted-dns-expansion.feature) | [enc-dns](tests/acceptance/encrypted_dns_expansion_test.go) |
| M9 | Kubernetes Operator | ✅ | — | [note](demos/m9/DEMO_NOTE.md) | [k8s-op](specs/functional/kubernetes-operator.feature) | [k8s-op](tests/acceptance/kubernetes_operator_test.go) |
| M10 | Active-Active Cluster (any-node writes) | ✅ | — | [note](demos/m10/DEMO_NOTE.md) · [demo](demos/m10/demo.sh) | [active-active](specs/functional/active-active-cluster.feature) | [active-active](tests/acceptance/active_active_cluster_test.go) |
| M11 | Distribution & Documentation | ✅ | — | [note](demos/m11/DEMO_NOTE.md) | [pkg-dist](specs/functional/packaging-and-distribution.feature) | [pkg](tests/acceptance/packaging_test.go) |
| M12 | Cluster Join via Web UI + Config Backup | ✅ | — | [note](demos/m12/DEMO_NOTE.md) | [join-ui](specs/functional/cluster-join-webui.feature) · [backup-ui](specs/functional/config-backup-webui.feature) | [join-ui](tests/acceptance/cluster_join_webui_test.go) · [config](tests/acceptance/config_test.go) |
| M13 | Temporary Filtering Pause (break-glass) | ✅ | — | [note](demos/m13/DEMO_NOTE.md) | [pause](specs/functional/filtering-pause.feature) | [pause](tests/acceptance/filtering_pause_test.go) |
| M14 | Block Dynamic-Lease Clients | ✅ | — | [note](demos/m14/DEMO_NOTE.md) · [report](demos/m14/test-report.html) | [block-dyn](specs/functional/profile-block-dynamic-clients.feature) | [block-dyn](tests/acceptance/profile_block_dynamic_test.go) |
| M15 | Test Suite Cleanup + keepalived Reference | ✅ | v0.1.3 | [note](demos/m15/DEMO_NOTE.md) · [report](demos/m15/test-report.html) | [enc-dns](specs/functional/encrypted-dns-expansion.feature) | [enc-dns](tests/acceptance/encrypted_dns_expansion_test.go) · [refresh](tests/acceptance/blocklist_refresh_test.go) |
| M16 | In-place Upgrade Binary Swap | ✅ | v0.1.4 | [note](demos/m16/DEMO_NOTE.md) · [report](demos/m16/test-report.html) | [upgrade](specs/functional/in-place-upgrade.feature) | [upgrade](tests/acceptance/in_place_upgrade_test.go) |
| M17 | Schedule Bindings + Config Export | ✅ | — | [note](demos/m17/DEMO_NOTE.md) | [schedules](specs/functional/schedules.feature) · [shadow](specs/functional/config-shadow-yaml.feature) | [schedules](tests/acceptance/schedules_test.go) · [shadow](tests/acceptance/shadow_yaml_test.go) |
| M18 | Rolling Cluster Upgrade | ✅ | v0.1.5 | [note](demos/m18/DEMO_NOTE.md) · [report](demos/m18/test-report.html) | [rolling-upgrade](specs/functional/rolling-upgrade.feature) | [rolling-upgrade](tests/acceptance/rolling_upgrade_test.go) |
| M19 | Query Log Aggregates + DoH3 test expansion | ✅ | — | [note](demos/m19/DEMO_NOTE.md) · [report](demos/m19/test-report.html) | [qlog-agg](specs/functional/query-log-aggregates.feature) | [qlog-agg](tests/acceptance/query_log_aggregates_test.go) |
| M20 | Cluster Security Hardening (token scoping + node cert rotation) | ✅ | — | [note](demos/m20/DEMO_NOTE.md) · [report](demos/m20/test-report.html) | [sec](specs/functional/cluster-security-hardening.feature) | [sec](tests/acceptance/cluster_security_hardening_test.go) |
| M21 | DNSSEC Validation Mode | ✅ | — | [note](demos/m21/DEMO_NOTE.md) · [report](demos/m21/test-report.html) | [dnssec-val](specs/functional/dnssec-validation-mode.feature) | [dnssec-val](tests/acceptance/dnssec_validation_test.go) |
| M22 | Webhook / Push Alerts | ✅ | — | [note](demos/m22/DEMO_NOTE.md) · [report](demos/m22/test-report.html) | [webhooks](specs/functional/webhooks.feature) | [webhooks](tests/acceptance/webhooks_test.go) |
| M22.5 | Browser Extension — Push Notification Bridge (Firefox + Chrome) | ⬜ | — | — | — | — |
| M23 | Skoed4Phone — DNS-over-VPN | ⬜ | — | — | — | — |
| M24 | Companion / Remote-Admin App | ⬜ | — | — | — | — |

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

## M16 tasks — In-place Upgrade Binary Swap (branch: `feature/m16-upgrade-binary-swap`)

**Scope (UoR-approved 2026-06-16):** Make `POST /api/v1/upgrade/start` actually download
and swap the binary. No cosign (`require_signature: false`). No rolling cluster-aware
upgrade (non-goal for M16). Feed asset URLs use GitHub Releases (`ashmonger/skoed`).

### M16 specs
- [x] M16 — functional spec: add `FS-UpgradeBinarySwap` scenario to `specs/functional/in-place-upgrade.feature` — 2026-06-16
- [x] M16 — UoR validates functional spec — 2026-06-16
- [x] M16 — technical spec: update `specs/technical/in-place-upgrade.md` with swap flow + feed assets field + GitHub Releases URLs — 2026-06-16
- [x] M16 — UoR validates technical spec — 2026-06-16

### M16 tests
- [x] M16 — acceptance test: `TestUpgradeBinarySwap` added, all pending skips removed — 2026-06-16
- [x] M16 — UoR validates acceptance tests — 2026-06-16

### M16 implementation
- [x] M16 — add `Assets map[string]string` to `Feed` + `CheckResult` in `internal/upgrade/checker.go` — 2026-06-16
- [x] M16 — implement `internal/upgrade/swapper.go`: download tar.gz → extract binary → rename over exe — 2026-06-16
- [x] M16 — update `UpgradeStart` handler: full swap + `os.Exit(0)` (skipped in test mode) — 2026-06-16
- [x] M16 — all acceptance tests green (409/409 full suite) — 2026-06-16

### M16 completion
- [x] M16 — refactoring phase: updated package comment in checker.go, simplified zero-value CheckResult — 2026-06-16
- [x] M16 — demo note: `demos/m16/DEMO_NOTE.md` — 2026-06-16
- [x] M16 — test report: `demos/m16/test-report.html` — 2026-06-16
- [x] M16 — UoR demo validation — 2026-06-16
- [x] M16 — merge to master + tag v0.1.4 — 2026-06-16

---

## M17 tasks — Schedule Bindings + Config Export (branch: `feature/m19-schedule-bindings`)

**Scope (UoR-approved 2026-06-16):**
- `GET /api/v1/schedules/{id}/bindings` — list profiles/clients currently bound to a schedule
- Write schedules to `config.yaml` via `ShadowWriter` (currently schedules are bbolt-only)

### M17 specs
- [x] M17 — functional spec: 4 FSIDs in `specs/functional/schedules.feature` — 2026-06-16
- [x] M17 — UoR validates functional spec — 2026-06-16
- [x] M17 — technical spec: GET /bindings + schedule YAML schema in `specs/technical/profiles-and-schedules.md` — 2026-06-16
- [x] M17 — UoR validates technical spec — 2026-06-16

### M17 tests
- [x] M17 — acceptance tests: `TestScheduleBindingsList`, `TestScheduleBindingsListEmpty`, `TestScheduleBindingsListNotFound`, `TestScheduleWrittenToConfigYaml` — 2026-06-16
- [x] M17 — UoR validates acceptance tests — 2026-06-16

### M17 implementation
- [x] M17 — `ListScheduleBindings` handler in `handlers/schedules.go` — 2026-06-16
- [x] M17 — GET route wired in `api/app.go` — 2026-06-16
- [x] M17 — `ShadowWriter.clusterSections` extended with `Schedules` + `ScheduleBindings` — 2026-06-16
- [x] M17 — 4 M17 tests green; full suite (413 tests) green — 2026-06-16

### M17 completion
- [x] M17 — refactoring phase (implementation was already minimal and clean; no changes needed) — 2026-06-16
- [x] M17 — demo note: `demos/m17/DEMO_NOTE.md` — 2026-06-16
- [x] M17 — real-condition test on Proxmox 3-node cluster: 7/7 tests pass (CT 200/201/202, Raft term 5, commit index 26) — 2026-06-16
- [x] M17 — proof captured: all 7 tests pass with actual API responses in test-report.html — 2026-06-16
- [x] M17 — HTML test report: `demos/m17/test-report.html` — 2026-06-16
- [x] M17 — add GET/POST/DELETE /api/v1/schedules/{id}/bindings + Schedule/TimeWindow/ScheduleBinding schemas to management-api.openapi.yaml — 2026-06-17
- [x] M17 — UoR demo validation — 2026-06-17 (18/18 Proxmox tests pass, config export fixed)
- [x] M17 — merge to master — 2026-06-17 (commit 24127e1, CI #31 green)

---

## M18 tasks — Rolling Cluster Upgrade (branch: `feature/m18-rolling-upgrade`)

**Scope (UoR-approved):** Zero-downtime rolling upgrade for multi-node clusters via single API call.
Sequential node upgrade preserving Raft quorum. Adblock format fix.

### M18 specs
- [x] M18 — functional spec: `specs/functional/rolling-upgrade.feature` (6 FSIDs) — 2026-06-18
- [x] M18 — UoR validates functional spec — 2026-06-18
- [x] M18 — technical spec: upgrade endpoints in `specs/technical/rolling-upgrade.md` (TS-RollingUpgrade) — 2026-06-18
- [x] M18 — UoR validates technical spec — 2026-06-18

### M18 tests
- [x] M18 — acceptance tests: `tests/acceptance/rolling_upgrade_test.go` (6 tests) — 2026-06-18
- [x] M18 — UoR validates acceptance tests — 2026-06-18

### M18 implementation
- [x] M18 — `POST /api/v1/upgrade/node-start` handler (`handlers/upgrade.go`) — cluster-internal, bypasses WriteForwardMiddleware — 2026-06-18
- [x] M18 — `POST /api/v1/cluster/upgrade/apply` handler (`handlers/cluster_upgrade.go`) — rolling goroutine — 2026-06-18
- [x] M18 — `GET /api/v1/cluster/upgrade/status` handler — in_progress, pending_nodes, completed_nodes, failed_node — 2026-06-18
- [x] M18 — routes wired in `api/app.go` — 2026-06-18
- [x] M18 — adblock format fix: `filter/blocklist.go` parseByFormat maps "adblock" → ParseAskoed — 2026-06-18
- [x] M18 — OpenAPI: upgrade endpoints in `specs/technical/management-api.openapi.yaml` — 2026-06-18
- [x] M18 — 6/6 acceptance tests green — 2026-06-18

### M18 real-environment validation (Proxmox 3-node cluster)
- [x] M18 — Proxmox: Alpine CT 201 init script updated to supervisor="supervise-daemon" — 2026-06-18
- [x] M18 — Proxmox: Debian CT 200/202 binary moved to /var/lib/skoed/bin/ with symlink, Restart=always — 2026-06-18
- [x] M18 — Proxmox: rolling upgrade completed (15s, skoed-1→skoed-3→skoed-2) — 2026-06-18
- [x] M18 — Proxmox: DNS filtering verified on kids/adults/IoT profiles post-upgrade — 2026-06-18

### M18 completion
- [x] M18 — refactoring phase: removed debug log.Printf statements from cluster_upgrade.go — 2026-06-18
- [x] M18 — demo note: `demos/m18/DEMO_NOTE.md` — 2026-06-18
- [x] M18 — test report: `demos/m18/test-report.html` — 2026-06-18
- [x] M18 — 7 screenshots in `demos/m18/` — 2026-06-18
- [x] M18 — UoR demo validation — 2026-06-18
- [x] M18 — merge to master + tag v0.1.5 — 2026-06-18

---

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
