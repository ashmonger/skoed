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
- [x] M2 — shadow YAML on disk for PBS / filesystem-level backups: spec'd (config-shadow-yaml.feature) and documented in cluster-store.md / raft-fsm.md — 2026-05-29
- [ ] M2 — Write acceptance tests
- [ ] M2 — Implementation
- [ ] M2 — Refactoring phase
- [ ] M2 — Demo: docker compose with primary + 2 replicas
- [ ] M2 — UoR validation and merge to master

## Blockers

None.

## Backlog (post-M4)

### Active

- HTTPS for the management API / Web UI. M4 ACME currently only covers
  DoH and DoT; `api_address` is still plain HTTP. Pending design call:
  single-port swap (HTTP → 308 → HTTPS) vs dual-port (keep HTTP on LAN,
  add HTTPS for public). — added 2026-06-05.
- "Block dynamic-lease clients" category (M3.7 candidate). Requires
  per-connector knowledge of static-vs-dynamic origin. dnsmasq lease
  file alone doesn't surface this — would need a separate `dhcp-host=`
  config parser, OR a dblock-owned static-pin list (duplicates state),
  OR a "known vs unknown" approximation. Defer until after M3.6 ships
  the export endpoint, which closes most of the bootstrapping pain
  this category was supposed to address. — added 2026-06-05.

### Packaging + deployment

- **Proxmox deploy script for LXC containers.** Single-command bootstrap
  inside a Proxmox host (`pveam`, `pct create`, …) — installs dblock,
  writes a sane config, hands the operator a working node URL. Target
  the homelab Proxmox use case where the cluster topology fits in one
  hypervisor. — added 2026-06-05.
- **Debian packages (.deb).** Apt-installable build for amd64/arm64
  Debian / Ubuntu / Raspberry Pi OS. systemd unit, default config in
  `/etc/dblock/`, `dblock` user, `apt upgrade` path. Pairs with
  the M5 in-place upgrade work. — added 2026-06-05.

### Strategic

- **Find a better name.** "dblock" is functional but not memorable.
  Audit existing trademarks, check DNS/GitHub/crates.io availability,
  reserve a domain. Defer rename until at least M5 ships so we don't
  rebrand during active growth. — added 2026-06-05.

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
- **IPv6-only / dual-stack network support validation.** Already-coded
  features (DNS, DoH, DoT, ACME) need real-world IPv6-only deploy
  validation.
- **Kubernetes operator** for lifecycle management. Would supersede
  the M2.5 Helm chart for serious K8s users — handles cluster scaling,
  cert rotation, lease-data PVCs.
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

### Conflicts with current non-goals — needs UoR decision

These contradict the **permanent non-goals** list in
`PROBLEM_STATEMENT.md` and `ROADMAP.md`. Listed here because the UoR
asked for them; need a separate decision to either (a) revise the
non-goals or (b) keep them parked indefinitely as "wishful but out
of scope". — added 2026-06-05.

- **Transparent proxy mode.** Operate as a transparent L4 proxy so
  clients with hardcoded resolver IPs are redirected to dblock
  regardless of their DNS settings. Today's roadmap lists VPN/proxy
  as a permanent non-goal — this would partially overlap.
- **Deep-packet inspection / HTTP filtering.** Inspect cleartext HTTP
  to enforce content rules at a level DNS can't reach. Explicit
  permanent non-goal today; rethinking would be a major scope pivot
  (puts dblock into the same category as Squid / e2guardian).

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
