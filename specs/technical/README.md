# Technical Specifications

This directory contains technical specifications that describe HOW functional requirements are implemented.

## Rules (from AGENTS.md)

- HTTP contracts MUST be specified in OpenAPI
- Async contracts MUST be specified in AsyncAPI
- Every technical artifact MUST include:
  - `x-tsid: TS-<TitleCamelCase>`
  - `x-fsid-links: [FS-...]`
- TSIDs MUST be unique and map to at least one FSID
- Technical specs MUST be user-validated before moving to tests
- Technical spec MAY need `<TitleCamelCase>.md` files for sequence diagrams, flowcharts, DMN, SLO

## Index

### Milestone 1
- `management-api.openapi.yaml` (TS-ManagementApi) — REST contract: blocklists, allowlist, local DNS, settings, config, auth
- `dns-engine.md` (TS-DnsEngine) — query pipeline, forwarder, recursor, cache, dual-stack
- `config-schema.md` (TS-ConfigSchema) — YAML config schema, field types, defaults, validation

### Milestone 2
- `management-api.openapi.yaml` (TS-ManagementApi) — extended with `/api/v1/cluster/*` endpoints
- `raft-fsm.md` (TS-RaftFsm) — FSM command set, snapshot, apply/restore semantics
- `cluster-store.md` (TS-ClusterStore) — on-disk layout: node.yaml, cluster.bbolt, querylog.bbolt, raft/
- `query-log-cluster.md` (TS-QueryLogCluster) — hourly aggregates via Raft, cross-node fan-out
- `helm-chart.md` (TS-HelmChart) — Helm chart layout, values, rendered manifests
- `web-ui.md` (TS-WebUi) — SPA architecture (Vue 3 + Vite), routing, component model

### Milestone 3
- `profiles-and-schedules.md` (TS-ProfilesAndSchedules) — per-client profiles, schedule rules, binding model
- `multi-arch-builds.md` (TS-MultiArchBuilds) — goreleaser config, buildx cross-compilation, amd64/arm64
- `native-packaging.md` (TS-NativePackaging) — .deb packaging, systemd unit, Proxmox LXC template

### Milestone 3.6 (DHCP anti-spoof)
- `dhcp-arp-cross-check.md` (TS-ArpCheck) — layer-3 ARP/NDP cross-check, anomaly detection, bbolt cache
- `profile-block-dynamic-clients.md` (TS-BlockDyn) — block-dynamic-clients profile rule, lease origin lookup

### Milestone 5
- `automated-blocklist-refresh.md` (TS-AutomatedBlocklistRefresh) — leader-only refresh worker, failure tracking
- `audit-log.md` (TS-AuditLog) — replicated audit rows via Raft, 90-day trim, query API
- `prometheus-metrics.md` (TS-PrometheusMetrics) — `/metrics` exporter, gauge/counter inventory
- `encrypted-cluster-mesh.md` (TS-EncryptedClusterMesh) — mTLS for Raft + internal API, CA bootstrap
- `dblock-cli.md` (TS-DblockCli) — charm-stack CLI + TUI (cobra, bubbletea, lipgloss)
- `in-place-upgrade.md` (TS-InPlaceUpgrade) — release feed, version comparison, UI banner
- `domain-tester.md` (TS-DomainTester) — verdict + rationale endpoints, public test-blocklist
- `url-tester.md` (TS-UrlTester) — URL tester (CLI entry + public landing page)
- `getting-started.md` (TS-GettingStarted) — dashboard onboarding card, docs chapter
- `docker-test-cache.md` (TS-DockerTestCache) — persistent go-mod + go-build cache for acceptance runs

### Milestone 6
- `categories-safesearch-doh.md` (TS-CategoriesSafeSearchDoh) — category catalog, SafeSearch enforcement, DoH/DoT client detection
- `doh-resolver-ip-database.md` (TS-DohResolverDb) — curated DoH/DoT resolver IP snapshot, leader-only refresh
- `firewall-rule-generator.md` (TS-FwRule) — paste-ready DoH/DoT block rules per platform (iptables, nftables, macOS pf)
- `firewall-rules-web-ui.md` (TS-FwRuleUi) — web UI surfaces for the firewall-rule generator (copy buttons, platform tabs)
- `make-dev.md` (TS-MakeDev) — `make dev` SPA hot-reload, Vite proxy config

### Milestone 6.5 (DHCP lease replication)
- `dhcp-lease-replication.md` (TS-LeaseRepl) — Raft-replicated DHCP lease cache, leader poll + follower sync
- `dhcpv6-lease-parsing.md` (TS-Dhcpv6Lease) — DHCPv6 lease parsing for Kea and dnsmasq connectors
- `lease-origin-tagging.md` (TS-LeaseOrigin) — per-connector static-vs-dynamic origin tagging

### Milestone 7
- `api-token-authentication.md` (TS-ApiToken) — revocable scoped Bearer tokens, hash storage, in-memory cache

### Milestone 8
- `encrypted-dns-expansion.md` (TS-EncryptedDnsExpansion) — DoH3 (HTTP/3 over QUIC) + DNSCrypt v2 server, Raft keypair replication

### Documentation / Tooling
- `documentation-site.md` (TS-DocumentationSite) — mdBook + Pagefind + GitHub Pages deployment
