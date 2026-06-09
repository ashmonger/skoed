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

## Active feature

**Milestone 8 — Encrypted DNS Expansion (DoH3 + DNSCrypt v2)**
Current phase: **Phase 6 — Demo + UoR validation** (awaiting demo sign-off + merge)

## Completed milestones
- [x] M1–M7 merged to master; 350 acceptance tests green as of 2026-06-09

## Current tasks

- [x] M8 — functional spec written: `specs/functional/encrypted-dns-expansion.feature` (17 FSIDs) — 2026-06-09
- [x] M8 — technical spec written: `specs/technical/encrypted-dns-expansion.md` (TS-EncryptedDnsExpansion) — 2026-06-09
- [x] M8 — UoR validates spec + dependency approval: quic-go + ameshkov/dnscrypt (decision: `decisions/20260609-M8ExternalDependencies.md`) — 2026-06-09
- [x] M8 — acceptance tests written: `tests/acceptance/encrypted_dns_expansion_test.go` (9 FSIDs) — 2026-06-09
- [x] M8 — implementation done: DoH3 (quic-go/http3), DNSCrypt v2 (ameshkov/dnscrypt), Raft keypair rotation, settings stamp — 2026-06-09
- [ ] M8 — refactoring phase
- [ ] M8 — demo note written: `demos/m8/DEMO_NOTE.md` — awaiting UoR validation
- [ ] M8 — merge to master

## Blockers

None.

## Backlog (post-M4)

### Active

- "Block dynamic-lease clients" category (M3.7 candidate). Requires
  per-connector knowledge of static-vs-dynamic origin. dnsmasq lease
  file alone doesn't surface this — would need a separate `dhcp-host=`
  config parser, OR a skoed-owned static-pin list (duplicates state),
  OR a "known vs unknown" approximation. Defer until after M3.6 ships
  the export endpoint, which closes most of the bootstrapping pain
  this category was supposed to address. — added 2026-06-05.

- **Temporary "filtering pause" / break-glass mode** — M3.x or M6.x
  candidate. — added 2026-06-08.
  - Scope: cluster-wide, per-profile, or per-group.
  - Duration: 1m / 5m / 30m / 1h / custom, with a hard ceiling (e.g. 24h).
  - During the window: all DNS queries forwarded as if no blocklist
    / no profile rules matched. Local DNS entries + DNSSEC posture
    unchanged.
  - Surfaced on the Dashboard with a countdown chip + "Resume
    filtering" button; auto-resumes when the timer expires.
  - Replicated through Raft so every node honours the same window
    (no split-brain where one node still blocks).
  - Audited (M5.2): action = `filter.pause`, target = `cluster` |
    `profile:<id>`, diff carries the duration + reason text.
  - CLI: `skoed filter pause [--profile <id>] [--duration 30m]` +
    `skoed filter resume`.
  - Prometheus: `skoed_filter_pause_active{scope="…"} 1` + a
    seconds-remaining gauge.
  - Non-goal: scheduled recurring pauses (that's already M3 schedules).
  - Implementation cost is mostly UI + a few lines in the filter
    engine to short-circuit when pause.active; the Raft + audit +
    metrics hooks are reuse.

### Packaging + deployment

- **Proxmox deploy script for LXC containers.** Single-command bootstrap
  inside a Proxmox host (`pveam`, `pct create`, …) — installs skoed,
  writes a sane config, hands the operator a working node URL. Target
  the homelab Proxmox use case where the cluster topology fits in one
  hypervisor. — added 2026-06-05.
- **Debian packages (.deb).** Apt-installable build for amd64/arm64
  Debian / Ubuntu / Raspberry Pi OS. systemd unit, default config in
  `/etc/skoed/`, `skoed` user, `apt upgrade` path. Pairs with
  the M5 in-place upgrade work. — added 2026-06-05.

### Strategic

- **Find a better name.** "skoed" is functional but not memorable.
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

### Under reconsideration — needs UoR decision

These were previously listed as permanent non-goals and have since
moved to the "Non-goals under reconsideration" sections of
`PROBLEM_STATEMENT.md` and `ROADMAP.md`. Each needs a separate
decision to either (a) commit to a milestone or (b) push back to
permanent non-goal. — added 2026-06-05.

- **Transparent proxy mode.** Operate as a transparent L4 proxy so
  clients with hardcoded resolver IPs are redirected to skoed
  regardless of their DNS settings. Was "VPN or proxy" non-goal.
- **Deep-packet inspection / HTTP filtering.** Inspect cleartext HTTP
  to enforce content rules at a level DNS can't reach. Puts skoed
  into the same category as Squid / e2guardian.
- **Mobile application.** Native iOS / Android admin app — read-only
  dashboards at minimum, full management as a stretch. Probably needs
  the M5 audit log + API tokens first so revocation works cleanly.
- **Cloud-hosted SaaS.** Run skoed as a managed service (per-customer
  cluster, billing, multi-tenant isolation). Big strategic pivot;
  contradicts the self-hosted-first thesis but worth a thought.

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
