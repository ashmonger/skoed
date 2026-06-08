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

### Milestone 2.5 — Helm Chart (Kubernetes deployment)

**Outcome**: dblock deploys onto a Kubernetes cluster via a single `helm install`. Per-node DNS service is reachable on each Kubernetes node; the Raft cluster forms automatically.

**Capabilities:**
- Helm chart in `deploy/helm/dblock/` with `values.yaml` exposing image tag, replica count, resource requests/limits, persistent-volume size, upstream resolvers, and the bootstrap-token Secret
- `DaemonSet` topology (one dblock pod per node) with `hostPort: 53` for the DNS listener
- `Service` per pod for the management API (`ClusterIP` by default, optional `NodePort`)
- `PersistentVolumeClaim` per pod for the data directory (raft/, bbolt, shadow config.yaml)
- `Secret` for the join token used by replica pods on first start
- CI: `kind` or `k3s` smoke test installing the chart and running a subset of the M2 acceptance tests against the pods

**Non-goals:**
- Operator pattern / CRDs (manual `helm upgrade` is enough)
- ACME / cert-manager integration (defer to M4)

**Dependencies:** Milestone 2 complete and validated. Independent of the Web UI work in M2.6.

---

### Milestone 2.6 — Web UI

**Outcome**: A browser-based UI ships embedded in the dblock binary, served by the existing management API. Every admin task currently doable via `curl` is doable via point-and-click on every supported milestone-1/2 endpoint.

**Capabilities:**
- SPA (Vue 3 + Vite, or Svelte — TBD at design time) compiled and embedded via `//go:embed web/dist`; served from `/` when authenticated
- Login screen using the existing Basic Auth credentials; first-run setup flow
- **Read views:** cluster topology / health, blocklists (with domain counts and last-updated), allowlist, local DNS entries, settings (DNS upstreams, block policy, query log retention), query log (live tail + filters), cluster stats (per-node + cluster-wide totals)
- **Write views:** create/edit/delete blocklists (URL or manual entries), allowlist add/remove, local DNS CRUD, settings patch, password change, token generation for new cluster members
- Responsive layout (mobile-friendly) for at-a-glance home dashboards
- Build pipeline: `go generate ./...` triggers a Vite build and copies `dist/` into the embed directory; `make build` includes it
- Same binary size budget: aim for ≤ 25 MB final image after UI embed

**Non-goals:**
- Custom theming / branding
- Multi-language i18n (English only at first)
- Cluster management primitives beyond what M2 already exposes (no operator-style "drain node" buttons)

**Dependencies:** Milestone 2 complete (Web UI exposes M2 endpoints from the start, no API changes needed).

**Risks:**
- Build pipeline complexity: cross-platform `npm` invocation from `go generate` is fiddly — mitigation: document the build steps and ship a `Makefile` target.
- Binary size: bundle audit at design time; if > 25 MB, defer non-essential views.

---

### Milestone 3 — Parental Control + DoH/DoT Detection

**Outcome**: A parent can assign a child's device to a restricted profile with category-based blocking, an evening-tightening schedule, and forced SafeSearch — all managed from the M2.6 Web UI. Clients that try to bypass filtering by switching to public DoH/DoT resolvers are detected and blocked at the DNS hostname layer.

**Capabilities:**

*Parental control:*
- Per-client profiles: assign blocking rules and allowlists to specific IPs or IP ranges
- Category-based blocking: curated domain categories (adult content, gambling, social media, gaming, streaming) sourced from OISD and Steven Black lists
- Schedule-based rules: define block/allow windows by time of day and day of week per profile
- SafeSearch enforcement: DNS rewriting for Google, Bing, YouTube, DuckDuckGo
- Profile inheritance: a default profile applies to all unassigned clients

*DoH/DoT detection and Layer-2 blocking:*
- Default-on `doh-resolvers` blocklist: ~50 known DoH/DoT hostnames (Cloudflare, Google, Quad9, Mullvad, AdGuard, NextDNS, ControlD, …) so clients can't resolve them
- Explicit handling of `use-application-dns.net` (Firefox canary): always NXDOMAIN, making Firefox auto-disable its DoH-by-default
- DDR (RFC 9462) probe handling: `_dns.resolver.arpa` queries logged and never served
- Query log tags entries hitting known DoH bootstrap hostnames with `category: doh-probe` so the eventual dashboard can surface "client tried DoH" events

**Non-goals for this milestone:**
- Content inspection beyond DNS (no HTTP filtering)
- Quota-based blocking (time budgets)
- Per-application blocking
- Blocking DoH clients pinned to hardcoded IPs (handled by M3.5 + operator firewall config)

**Dependencies:** Milestone 1 complete and validated. Milestone 2 recommended (profiles sync across nodes). Milestone 2.6 (Web UI) required — per-client profiles with schedules and category pickers are unmanageable via raw API calls.

**Risks:**
- Clients with hardcoded resolver IPs (Chrome configured with `1.1.1.1` directly) still bypass DNS-hostname blocking; M3 catches the ~70–80% that uses hostnames, M3.5 + firewall close the rest.

---

### Milestone 3.5 — Per-Client DoH Surfacing + Firewall Recipes

**Outcome**: An admin can see, per client, whether DoH/DoT use is suspected, and apply ready-made firewall snippets to close the hardcoded-IP gap.

**Capabilities:**
- `GET /api/v1/clients/{ip}/doh-status` returns `using_doh`, `last_doh_query`, `suspected_provider` derived from the query log
- Dashboard alert: "Client X attempted DoH N times in the last hour"
- Firewall-rule generator: templates for `iptables`, `nftables`, MikroTik RouterOS, OpnSense/pfSense, and UniFi controllers that block egress 853/tcp + egress 443 to the curated DoH-resolver-IP set, scoped to client subnets
- Resolver-IP database refresh: same cadence as blocklist refresh; pulled from a curated source
- Documentation: "Closing the DoH gap" guide that walks through firewall placement

**Non-goals:**
- dblock pushing rules into routers automatically (operator copy-paste only)
- SNI-based blocking (belongs at the firewall, not in dblock)

**Dependencies:** Milestone 3 complete.

---

### Milestone 3.6 — Read-Only DHCP Integration + Anti-Spoof Detection

**Outcome**: The query log and dashboards display **hostnames** and MAC addresses next to client IPs, sourced from the LAN's DHCP server. Profiles match clients by stable DHCP Client-ID (option 61), MAC, or hostname in addition to IP/CIDR. Lease changes are reflected on dblock within minutes. Spoofing attempts (a known hostname suddenly appearing with a new MAC, or vice versa) raise a dashboard alert.

**Capabilities:**
- Read-only **DHCP source connectors**, configurable per node:
  - Kea DHCP REST API (`lease4-get-all` via the control-agent)
  - dnsmasq lease file (`/var/lib/misc/dnsmasq.leases`)
  - Generic HTTP API returning JSON `[{ ip, mac, hostname, client_id, expires_at }, …]`
  - **ISC DHCP `dhcpd` is intentionally NOT supported** — ISC declared it end-of-life in 2022; operators on `dhcpd` should migrate to Kea
- **Canonical Lease** record: `{ ip, mac, hostname, client_id, source, expires_at, first_seen, last_seen }`
- **Lease cache in bbolt** (replicated via Raft so all cluster nodes see consistent hostnames). Configurable refresh interval, default 60 s. Never blocks DNS resolution.
- **Stable identity priority**: when matching a query's client IP to a lease, prefer Client-ID over MAC over hostname. Client-ID is the hardest to spoof.
- **Anti-spoof detection** (Layers 1 + 2; see DEMO_NOTE for design):
  - Lease history tracks `(client_id, mac, hostname)` tuples per device
  - Anomaly types flagged: MAC changed for known Client-ID; Client-ID changed for known MAC; brand-new MAC matching an existing hostname
  - Anomalies surface as a Dashboard warning card (similar to the M3.5 DoH alert) and are kept for 7 days
- `GET /api/v1/clients/{ip}` returns the enriched record + recent-anomaly list
- Query log entries gain optional `client_hostname`, `client_mac`, `client_id` fields (omitted when no lease match)
- Profile-binding rules accept `client_macs`, `client_hostnames`, `client_ids` in addition to `client_ips` / `client_cidrs`. Match priority: Client-ID > MAC > hostname > IP/CIDR.
- Web UI: client list (sortable by hostname / last-seen), per-client drill-down, spoof-anomaly inbox
- Settings page: per-connector form (URL, file path, refresh interval, credentials)
- **Reservation export**: `GET /api/v1/clients/export-reservations?format=dnsmasq|kea|json` emits operator-pasteable static-reservation syntax derived from the current lease snapshot. Lets the operator bootstrap their DHCP server's reservation table from devices dblock has already observed.

**Non-goals:**
- dblock writing leases (read-only)
- DHCPv6 lease parsing (defer; IPv4 first)
- ISC `dhcpd` lease file parser (deprecated upstream)
- Active probing — ARP/NDP cross-check is Layer 3 of anti-spoofing, deferred to backlog
- Automatic remediation (alert only; operator decides)
- Sub-second freshness — operator can ride DNS via the IP fallback while leases catch up

**Dependencies:** Milestone 3 complete (profile model). Helpful but not strictly required by M3.5 / M4.

---

### Milestone 4 — dblock as a DoH/DoT Server

**Outcome**: Devices that *want* encrypted DNS get it from dblock itself, with the same filtering applied. The "fight against DoH" turns into "we serve DoH, just point at us".

**Capabilities:**
- DoH server on `/dns-query` (RFC 8484) with auto-generated or operator-supplied TLS cert
- DoT server on port 853 with the same cert
- Same filter, local-DNS, and query-log pipeline as plain DNS — `outcome` field gains values like `forwarded-doh`, `blocked-doh` for analytics
- Optional ACME (Let's Encrypt) integration for the TLS cert
- Per-listen-protocol enable/disable in `node.dns.listen` (`port`, `doh_port`, `dot_port`)
- Cluster: every node serves DoH/DoT on its own listen address; clients can target any node

**Non-goals:**
- DNSCrypt (rare in modern clients)
- HTTP/3 (DoH3) — defer; clients are slow to adopt

**Dependencies:** Milestones 1–3 complete. M2 cluster mode for multi-node DoH endpoints.

**Risks:**
- Cert lifecycle in air-gapped deployments — manual cert provisioning supported as fallback.

---

### Milestone 4.5 — API Documentation Browser

**Outcome**: An operator can open `<host>:8080/api/docs` and see the entire management API as an interactive reference — every endpoint, every request/response shape, every status code — sourced from the existing `specs/technical/management-api.openapi.yaml`. "Try it out" buttons hit the live node using the operator's already-authenticated browser session.

**Capabilities:**
- Swagger UI 5 bundled via `go:embed` (~1.4 MB, ~400 KB gzipped) and mounted under `/api/docs`
- `/api/openapi.yaml` serves the live spec from the embedded `specs/technical/` snapshot (CI-validated against the actual routes via the existing `tools/traceability/`)
- Sidebar nav entry "API" linking to `/api/docs`
- "Try it out" honors the operator's existing Basic Auth — no separate API key
- Bundle is gated behind a config flag (`api.docs.enabled`, default true) so security-conscious operators can strip it

**Non-goals:**
- Hosting the docs publicly (the API is on a private interface; docs ride along)
- Generated client libraries (operators run `openapi-generator` themselves if needed)
- Redoc / Stoplight alternates — Swagger UI is the chosen renderer

**Dependencies:** None hard; the OpenAPI doc has existed since M1. Naturally pairs with M5's "documentation site" capability.

---

### Milestone 4.6 — HTTPS for the Management API

**Outcome**: The Web UI and management API are reachable over HTTPS using the same ACME-issued cert M4 already manages for DoH and DoT. Operators on a public-facing host stop needing a reverse proxy in front of dblock.

**Capabilities:**
- New `node.api.tls.enabled` toggle. When on, dblock binds an HTTPS listener on `api_address` and reuses the cert from M4 (`node.dns.tls.acme.*` or `node.dns.tls.cert_file`)
- Two listen modes (operator picks): **single-port swap** (plain HTTP returns 308 → HTTPS) or **dual-port** (HTTP on `api_address`, HTTPS on `api_tls_address`) for LAN-script compatibility
- HSTS header on the HTTPS listener (configurable, off by default for LAN deployments)
- Same Basic Auth applies (API tokens land later in M7)

**Non-goals:**
- A separate cert from DoH/DoT — one cert, one renewal
- mTLS / client certs

**Dependencies:** M4 ACME. Closes the open TODO entry from 2026-06-05.

---

### Milestone 5 — Production Hardening

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
- HA active-active cluster with Raft consensus (deferred to post-Milestone 5)

**Dependencies:** Milestones 1–4 complete and validated.

---

### Milestone 5.5 — Native Packaging

**Outcome**: dblock installs on Debian/Ubuntu/Raspberry Pi OS via `apt`, and on Proxmox via a one-shot LXC bootstrap script. The single-binary release stays available, but most operators move to the OS-managed install path.

**Capabilities:**
- `.deb` packages for amd64 and arm64 (`dblock`, `dblock-cluster` — the latter pulls in the cluster bootstrap helpers)
- systemd unit file; default config at `/etc/dblock/config.yaml`; data at `/var/lib/dblock`; `dblock` system user
- apt repo hosted alongside GitHub releases
- Proxmox LXC bootstrap script (`pveam` + `pct create` + first-run config wizard)
- All packages reuse the M5 in-place upgrade path on `apt upgrade` / `pct exec`

**Non-goals:**
- RPM / Arch / Alpine native packages (community-driven; the static binary stays available for those)
- A homebrew formula (macOS isn't a target host)

**Dependencies:** M5 (in-place upgrade hook).

---

### Milestone 6 — Closing the DoH Gap

**Outcome**: Operators can block hardcoded-resolver-IP DoH/DoT bypasses at their firewall, using dblock-generated rule snippets. Closes the last bypass route M3 + M3.5 leave open.

**Capabilities:**
- Firewall-rule generators for `iptables`, `nftables`, MikroTik RouterOS, OpnSense / pfSense, UniFi controllers
- Curated database of public DoH/DoT resolver IPs, refreshed daily from a tracked upstream
- Web UI: per-platform "Copy rules" button on the Clients / Stats pages, scoped to client subnets
- Documentation: "Closing the DoH gap" guide covering placement, monitoring, and false-positive recovery

**Non-goals:**
- dblock pushing rules into routers automatically (operator copy-paste only)
- SNI-based blocking (belongs at the firewall, not in dblock)

**Dependencies:** M3.5 detection track.

---

### Milestone 6.5 — DHCP Layer-3 Anti-Spoof + Replicated Leases

**Outcome**: The M3.6 anti-spoof detector gains a third layer (ARP/NDP cross-check), the lease cache replicates across the cluster, and dblock can finally name a "dynamic vs static" lease.

**Capabilities:**
- ARP/NDP cross-check via netlink: flag when DHCP's view of `(IP → MAC)` disagrees with the kernel's ARP table on this node
- Raft-replicated lease cache: only the leader polls; followers see leases via FSM replication
- DHCPv6 lease parsing (Kea + dnsmasq paths)
- Per-connector static-vs-dynamic origin tagging on `Lease`
- New "block-dynamic-clients" rule on profiles, gated on `Lease.IsStatic == false`

**Non-goals:**
- Active mitigation (still detect-only; operator decides)
- DHCP failover protocol awareness

**Dependencies:** M3.6.

---

### Milestone 7 — API Token Authentication

**Outcome**: Operators authenticate to the management API with revocable, scoped tokens. HTTP Basic Auth stays available as a migration path but is no longer the recommended default.

**Capabilities:**
- Token store in bbolt (replicated): `(id, scopes, created_at, last_used, expires_at)`
- New endpoints: `POST /api/v1/tokens`, `GET /api/v1/tokens`, `DELETE /api/v1/tokens/{id}`
- `Authorization: Bearer …` honored alongside `Authorization: Basic …`
- Per-token audit-log entries (pairs with M5 audit log)
- Web UI: token management page under Account
- Migration guide: how to flip a deployment from Basic Auth to tokens

**Non-goals:**
- OAuth2 / OIDC integration (overkill for self-hosted)
- LDAP / SAML federation

**Dependencies:** M5 audit log.

---

### Milestone 8 — Encrypted DNS Expansion (DoH3 + DNSCrypt)

**Outcome**: dblock serves the two remaining encrypted-DNS dialects so clients that prefer them get filtered DNS too.

**Capabilities:**
- **DoH3** on a configurable UDP/QUIC port (HTTP/3 transport, RFC 9230)
- **DNSCrypt v2** server with per-cluster certificate rotation
- Both reuse the same filter + query-log + cert pipeline as M4 DoH/DoT
- Per-listen-protocol enable/disable

**Non-goals:**
- ODoH (Oblivious DoH) — niche; defer
- Anonymized DNSCrypt relays

**Dependencies:** M4 DoH/DoT.

---

### Milestone 9 — Kubernetes Operator

**Outcome**: A native operator manages dblock clusters on Kubernetes via CRDs — supersedes the M2.5 Helm chart for serious K8s users.

**Capabilities:**
- CRDs: `DblockCluster`, `DblockNode`
- Automatic cluster scaling, ACME cert rotation, lease-data PVC management
- Helm chart kept as a thin wrapper for non-operator deployments
- Status conditions surface Raft health to `kubectl get dblockcluster`

**Non-goals:**
- Multi-cluster / federation
- Custom CNI integration

**Dependencies:** M2.5 Helm chart (lessons learned), M5 hardening.

---

### Milestone 10 — Active-Active Cluster

**Outcome**: Any node accepts writes; Raft handles consensus transparently. Multi-DC deployments stop pinning writes to the leader.

**Capabilities:**
- Multi-leader writes via Raft pre-vote + log shipping
- Per-namespace write sharding (auth state replicates everywhere; per-node telemetry stays local)
- Conflict-free state types where possible (counters, log appends); last-writer-wins with explicit metadata where not
- API responses surface "served by" + "committed at" so clients can reason about staleness

**Non-goals:**
- Geo-distributed write tolerance (assumes ≤ 50 ms RTT between voters)
- Eventual-consistency mode

**Dependencies:** M2 Raft + M5 hardening + significant testing.

---

## Pre-1.0 release tasks (no milestone number)

- **Find a better name.** Trademark / GitHub / crates.io search; secure a domain. Probably happens between M5 and M5.5.
- **IPv6-only / dual-stack validation.** Already-coded features need real-world IPv6-only deploy sign-off. Lightweight; rides alongside any milestone.

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

## Non-goals under reconsideration

These items were previously listed as permanent non-goals; they're being
re-evaluated. Listed here so the open status stays visible until a UoR
decision lands.

- Transparent proxy mode (was: "VPN or proxy")
- Deep packet inspection / HTTP filtering
- Mobile application
- Cloud-hosted SaaS
