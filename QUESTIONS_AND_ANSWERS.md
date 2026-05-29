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
