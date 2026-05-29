# Questions and Answers

## Open questions

None.

## Confirmed answers

- **Q**: What technology stack?
  **A**: Go 1.22+. DNS: `miekg/dns`. Web UI: Vue.js compiled and embedded. Config: YAML. — 2026-05-29

- **Q**: Project name?
  **A**: dblock. — 2026-05-29

- **Q**: Multi-node sync model?
  **A**: Primary + replicas (push). Hybrid approach: split-brain avoidance via last-seen timestamps and health-check quorum. Full consensus (Raft) deferred to post-M2. — 2026-05-29

- **Q**: M2 — How does a replica become the new primary when the current primary fails?
  **A**: ~~Hybrid. Manual default, opt-in quorum auto-failover.~~ **SUPERSEDED 2026-05-29** by the Raft decision below — Raft handles leader election automatically; manual promotion and the opt-in quorum protocol are no longer needed.

- **Q**: M2 — How do config changes propagate from primary to replicas?
  **A**: ~~Replica pulls from primary via SSE.~~ **SUPERSEDED 2026-05-29** by the Raft decision below — config replication is now via Raft log; writes accepted on any node and forwarded to the leader; SSE is not used.

- **Q**: M2 — Is the Helm chart / Kubernetes DaemonSet part of M2 scope?
  **A**: Deferred to a later milestone (M2.5). M2 focuses on the cluster core (Raft replication, enrollment, cluster status). Plain `kubectl apply` of a Deployment remains supported. — 2026-05-29

- **Q**: M2 — What is the replication core for cluster config?
  **A**: hashicorp/raft + go.etcd.io/bbolt. Pure Go (no CGO), proven in production by Consul / Nomad / Vault / k3s. Any node accepts writes; non-leader nodes forward to the leader transparently. Raft handles leader election, split-brain prevention, log replication, and snapshots. Hypothesis H2 (manual quorum protocol) is replaced by H4 (hashicorp/raft is operationally suitable for ≤10 nodes on home/lab networks). — 2026-05-29

- **Q**: M2 — What is the on-disk source of truth for cluster config?
  **A**: bbolt is the source of truth. The YAML file from M1 becomes an import/export artifact only — useful for backup, migration, and human review, but never the live state. Editing config.yaml on disk after boot has no effect. Node-local settings (DNS listen port, API port) remain in a small `node.yaml` file. — 2026-05-29

- **Q**: M2 — How does the cluster expose query log data for the future dashboard?
  **A**: Hybrid. Raw entries stay per-node (volume too high to replicate). Each node writes hourly aggregate counters (totals, top-N domains, top-N clients) into a bbolt bucket that Raft replicates to all nodes — so cluster-wide stats are available from any node with zero extra protocol code. Individual-entry searches use an on-demand fan-out endpoint that queries every node in parallel. — 2026-05-29

- **Q**: M2 — Should the YAML config still be written to disk for filesystem-level backup tools (Proxmox Backup Server, restic, borg)?
  **A**: Yes. A write-through shadow YAML at `<data_dir>/config.yaml` is updated after every Raft commit (debounced ~1 s, atomic rename). It is the M1 export format byte-for-byte. It is NEVER read by a running node — bbolt remains source of truth — but PBS-style FS snapshots capture it automatically. On restore to a fresh node, the M1→M2 migration imports it. — 2026-05-29

- **Q**: Parental control scope?
  **A**: All four: category-based blocking, schedule-based blocking, per-device/client profiles, SafeSearch enforcement. — 2026-05-29

- **Q**: Primary deployment target for v1?
  **A**: Both simultaneously — Linux bare-metal/LXC binary + Docker image from Milestone 1. Helm chart in Milestone 2. — 2026-05-29

- **Q**: DNSSEC stance for Milestone 1?
  **A**: Transparent proxy — forward DNSSEC records (RRSIG, DNSKEY, DS, NSEC) as-is to clients that set the DO bit. dblock does not validate signatures. Zero complexity added; DNSSEC-validating clients handle validation themselves. — 2026-05-29

- **Q**: Block policy for blocked domains?
  **A**: Configurable per blocklist, with a global default. Supported response types: NXDOMAIN, NULL (A=0.0.0.0 / AAAA=::), NODATA (NOERROR with empty answer). — 2026-05-29

- **Q**: IPv6 support scope for Milestone 1?
  **A**: Full dual-stack — DNS listener on both IPv4 and IPv6 (port 53), AAAA records in local DNS entries, IPv6 client identification in query log and client profiles, NULL block returns 0.0.0.0 for A queries and :: for AAAA queries. — 2026-05-29

- **Q**: Wildcard domain syntax and matching semantics for blocklists and allowlists?
  **A**: `*.example.com` matches the apex domain (`example.com`) and all subdomains at any depth (`sub.example.com`, `a.b.example.com`). Applies to both blocklist entries and allowlist entries. — 2026-05-29

- **Q**: Client group membership model (M3)?
  **A**: A client may belong to multiple groups. Effective rules are the union of all group blocklists and the union of all group allowlists. Ungrouped clients fall into a built-in default group. — 2026-05-29

- **Q**: Client identification by MAC address — how to resolve IP → MAC (M3)?
  **A**: Primary = DHCP integration (supported sources: Kea DHCP REST API, dnsmasq lease file, ISC DHCP lease file, generic configurable HTTP API returning leases as JSON). Fallback = IP-only identification for clients not in the DHCP lease table or when no DHCP source is configured. — 2026-05-29
