# Roadmap

## Strategic direction

dblock is a self-hosted DNS filtering solution designed to replace Pi-Hole and AdGuard Home in home and lab environments. The strategic goal is to deliver the same core functionality in a single binary that natively supports multi-node clusters, per-client parental control, and container-native deployment — without requiring external databases or manual per-node configuration.

## Milestones

---

### Milestone 1 — Single Node Foundation

**Outcome**: A single dblock node fully replaces Pi-Hole or AdGuard Home on a home network. An administrator can be operational within 10 minutes on a fresh Linux host or via Docker.

**Capabilities:**
- DNS forwarding with configurable upstream resolvers (DoH, DoT, plain UDP/TCP)
- Root DNS recursive resolution (air-gapped capable)
- Blocklist management: add/remove/update from URL or manual entry; supported formats: hosts, domain list, AdBlock/ABP
- Allowlist management: per-domain overrides
- Local DNS entries: A, AAAA, CNAME records for home/lab hostnames
- Query log: per-client history with outcome (blocked, allowed, forwarded, cached)
- Web UI: dashboard (stats, top blocked domains), blocklist management, local DNS, query log
- Config import/export: single YAML archive, importable on a fresh node
- Basic authentication for web UI and API
- Single binary Linux install (amd64, arm64) + Docker image

**Non-goals for this milestone:**
- Multi-node sync
- Per-client profiles or parental control
- Kubernetes deployment

**Dependencies:** None.

**Risks:**
- Blocklist scale: lists with millions of domains require efficient in-memory structure (radix trie or bloom filter).

---

### Milestone 2 — Multi-Node Cluster

**Outcome**: A second or third node can be added to an existing installation in ≤ 5 manual steps. All configuration changes made on the primary appear on all replicas within 10 seconds.

**Capabilities:**
- Node enrollment: primary generates a join token; replica uses it to enroll and receive initial config
- Primary/replica role management: clear promotion/demotion via UI
- Config push: every config change on the primary is immediately pushed to all enrolled replicas
- Split-brain detection: nodes track last-seen timestamps and run periodic health checks; primary steps down if quorum is lost
- Cluster status dashboard: shows all nodes, their roles, last-seen timestamps, and sync state
- Helm chart: Kubernetes DaemonSet deployment with per-node DNS service

**Non-goals for this milestone:**
- Per-client profiles or parental control
- Active-active (any-write) cluster mode
- Full consensus protocol (Raft)

**Dependencies:** Milestone 1 complete and validated.

**Risks:**
- Split-brain edge cases: quorum-based step-down mitigates but does not eliminate; documented as a known limitation until Raft is added.

---

### Milestone 3 — Parental Control

**Outcome**: A parent can assign a child's device to a restricted profile with category-based blocking, a schedule that tightens access in the evening, and SafeSearch forced on all search engines — all managed from the web UI.

**Capabilities:**
- Per-client profiles: assign blocking rules and allowlists to specific IPs or IP ranges
- Category-based blocking: curated domain categories (adult content, gambling, social media, gaming, streaming) sourced from OISD and Steven Black lists
- Schedule-based rules: define block/allow windows by time of day and day of week per profile
- SafeSearch enforcement: DNS rewriting for Google, Bing, YouTube, DuckDuckGo
- Profile inheritance: a default profile applies to all unassigned clients

**Non-goals for this milestone:**
- Content inspection beyond DNS (no HTTP filtering)
- Quota-based blocking (time budgets)
- Per-application blocking

**Dependencies:** Milestone 1 complete and validated. Milestone 2 recommended (profiles sync across nodes).

**Risks:**
- HTTPS bypass: clients using DNS-over-HTTPS directly bypass dblock; mitigated by documentation and optional firewall guidance.

---

### Milestone 4 — Production Hardening

**Outcome**: dblock is suitable for always-on lab and small-office use with monitoring, automation, and reliable upgrades.

**Capabilities:**
- Prometheus metrics endpoint at `/metrics`
- Audit log: records who changed what configuration and when
- Automated blocklist refresh: configurable interval (daily by default) with optional signature verification
- Multi-architecture release builds: amd64, arm64 (Linux)
- In-place upgrade: download and replace binary via UI or CLI without losing configuration
- Documentation site: install guide, configuration reference, cluster setup guide, troubleshooting

**Non-goals for this milestone:**
- GUI-driven OS updates
- HA active-active cluster with Raft consensus (deferred to post-Milestone 4)

**Dependencies:** Milestones 1–3 complete and validated.

---

## Post-Milestone 4 (backlog, not committed)

- Active-active cluster mode (any-node writes, Raft-based consensus)
- DNS-over-HTTPS (DoH) and DNS-over-TLS (DoT) server endpoints
- API token authentication (replace basic auth)
- IPv6-only and dual-stack network support validation
- Operator (Kubernetes) for lifecycle management

## Dependencies and risks (cross-milestone)

| Risk | Affected milestones | Mitigation |
|------|-------------------|-----------|
| `miekg/dns` library limitations | M1+ | Evaluate `coredns` as an alternative before M1 implementation |
| Blocklist size in memory | M1+ | Radix trie or bloom filter; measure at ≥ 1M domains |
| HTTPS bypass of DNS filtering | M3+ | Document; provide optional firewall rule templates |
| Split-brain in multi-node | M2+ | Quorum health checks; Raft deferred |
| Container image size creep | M1+ | CI gate: reject image builds > 100 MB |

## Non-goals (permanent)

- DHCP server
- VPN or proxy
- Deep packet inspection / HTTP filtering
- Mobile application
- Cloud-hosted SaaS
