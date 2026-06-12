# Roadmap

## Strategic direction

skoed is a self-hosted DNS filtering solution designed to replace Pi-Hole and AdGuard Home in home and lab environments. The strategic goal is to deliver the same core functionality in a single binary that natively supports multi-node clusters, per-client parental control, and container-native deployment — without requiring external databases or manual per-node configuration.

## Milestones

---

### Milestone 1 — Single Node Foundation

**Outcome**: A single skoed node fully replaces Pi-Hole or AdGuard Home on a home network. An administrator can be operational within 10 minutes on a fresh Linux host or via Docker.

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

**Outcome**: skoed deploys onto a Kubernetes cluster via a single `helm install`. Per-node DNS service is reachable on each Kubernetes node; the Raft cluster forms automatically.

**Capabilities:**
- Helm chart in `deploy/helm/skoed/` with `values.yaml` exposing image tag, replica count, resource requests/limits, persistent-volume size, upstream resolvers, and the bootstrap-token Secret
- `DaemonSet` topology (one skoed pod per node) with `hostPort: 53` for the DNS listener
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

**Outcome**: A browser-based UI ships embedded in the skoed binary, served by the existing management API. Every admin task currently doable via `curl` is doable via point-and-click on every supported milestone-1/2 endpoint.

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
- skoed pushing rules into routers automatically (operator copy-paste only)
- SNI-based blocking (belongs at the firewall, not in skoed)

**Dependencies:** Milestone 3 complete.

---

### Milestone 3.6 — Read-Only DHCP Integration + Anti-Spoof Detection

**Outcome**: The query log and dashboards display **hostnames** and MAC addresses next to client IPs, sourced from the LAN's DHCP server. Profiles match clients by stable DHCP Client-ID (option 61), MAC, or hostname in addition to IP/CIDR. Lease changes are reflected on skoed within minutes. Spoofing attempts (a known hostname suddenly appearing with a new MAC, or vice versa) raise a dashboard alert.

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
- **Reservation export**: `GET /api/v1/clients/export-reservations?format=dnsmasq|kea|json` emits operator-pasteable static-reservation syntax derived from the current lease snapshot. Lets the operator bootstrap their DHCP server's reservation table from devices skoed has already observed.

**Non-goals:**
- skoed writing leases (read-only)
- DHCPv6 lease parsing (defer; IPv4 first)
- ISC `dhcpd` lease file parser (deprecated upstream)
- Active probing — ARP/NDP cross-check is Layer 3 of anti-spoofing, deferred to backlog
- Automatic remediation (alert only; operator decides)
- Sub-second freshness — operator can ride DNS via the IP fallback while leases catch up

**Dependencies:** Milestone 3 complete (profile model). Helpful but not strictly required by M3.5 / M4.

---

### Milestone 4 — skoed as a DoH/DoT Server

**Outcome**: Devices that *want* encrypted DNS get it from skoed itself, with the same filtering applied. The "fight against DoH" turns into "we serve DoH, just point at us".

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

**Outcome**: The Web UI and management API are reachable over HTTPS using the same ACME-issued cert M4 already manages for DoH and DoT. Operators on a public-facing host stop needing a reverse proxy in front of skoed.

**Capabilities:**
- New `node.api.tls.enabled` toggle. When on, skoed binds an HTTPS listener on `api_address` and reuses the cert from M4 (`node.dns.tls.acme.*` or `node.dns.tls.cert_file`)
- Two listen modes (operator picks): **single-port swap** (plain HTTP returns 308 → HTTPS) or **dual-port** (HTTP on `api_address`, HTTPS on `api_tls_address`) for LAN-script compatibility
- HSTS header on the HTTPS listener (configurable, off by default for LAN deployments)
- Same Basic Auth applies (API tokens land later in M7)

**Non-goals:**
- A separate cert from DoH/DoT — one cert, one renewal
- mTLS / client certs

**Dependencies:** M4 ACME. Closes the open TODO entry from 2026-06-05.

---

### Milestone 4.7 — DNS Cache Controls

**Outcome**: Operators can purge the DNS cache on demand and see cache health from outside the process. Config edits stop bulldozing the entire cache as a side effect of handler rebuilds.

**Today's gap** (`apps/skoed/internal/dns/cache.go`):
- No `Clear()` / `Purge()` method on `Cache`; no API endpoint; no UI button.
- Every Raft apply (allowlist add, profile rename, even a settings tweak) wipes the cache as an unintended side effect, because `rebuildDNS` constructs a fresh handler-and-cache pair on every apply. Hot domains have to be re-fetched after any config change.
- No visibility — operators can't tell if the cache is hot, full, or even running.

**Capabilities:**
- **Explicit purge API**: `POST /api/v1/dns/cache/purge` (full purge) and `POST /api/v1/dns/cache/purge?domain=<fqdn>` (targeted purge for one name across all qtypes). Audit-logged in M5.
- **Web UI button**: "Clear DNS cache" on Settings → DNS, with a confirmation modal showing the current cache size.
- **Targeted invalidation on config change**: rebuildDNS preserves the existing `*Cache` (atomic swap of the cache pointer instead of recreating); allowlist / blocklist mutations trigger surgical purges of the affected names only. Local-DNS / profile-match changes still invalidate matching keys, not the whole cache.
- **Cache visibility**: `GET /api/v1/dns/cache/stats` returns `{ size, max_entries, hits_24h, misses_24h, evictions_24h, oldest_expiry, newest_expiry }`. Stats page gets a "DNS cache" card next to "DoH attempts today".
- **Metrics hook**: counters (hits/misses/evictions/size) wired so the M5 Prometheus exporter surfaces them at `skoed_dns_cache_*`.

**Non-goals:**
- Persistent cache across restarts (the cache is by design ephemeral)
- Per-client cache namespaces (M3.6 profile matching could deviate by profile; defer until anyone asks)
- Negative-result cache (NXDOMAIN caching) — separate scope decision

**Dependencies:** None hard. Cache rework lives entirely inside `internal/dns/`. Metrics hook anticipates M5's Prometheus endpoint.

---

### Milestone 5 — Production Hardening (umbrella)

**Outcome**: skoed is suitable for always-on lab and small-office use with monitoring, automation, and reliable upgrades.

M5 is an umbrella for six independent capabilities, each with its own
sub-milestone below. Plus M5.3 (Encrypted Cluster Mesh) and M5.5
(Native Packaging) which already have their own entries elsewhere in
this file.

| Sub | Title                                  | Status     |
|-----|----------------------------------------|------------|
| 5.1 | Prometheus `/metrics` exporter         | in flight  |
| 5.2 | Audit log                              | next       |
| 5.3 | Encrypted Cluster Mesh                 | see below  |
| 5.4 | Automated blocklist refresh            |            |
| 5.5 | Native Packaging (.deb + Proxmox LXC)  | see below  |
| 5.6 | In-place upgrade                       |            |
| 5.7 | Multi-architecture release builds      |            |
| 5.8 | Documentation site                     |            |

**Non-goals for the M5 umbrella:**
- GUI-driven OS updates
- HA active-active cluster with Raft consensus (deferred to M10)

**Dependencies:** Milestones 1–4 complete and validated.

---

### Milestone 5.1 — Prometheus `/metrics` Exporter

**Outcome**: skoed exposes a Prometheus-format metrics endpoint so operators can graph DNS throughput, cache health, cluster state, and DHCP-cache freshness from outside the process. Standard `prometheus.io/scrape: "true"` annotations work out of the box.

**Capabilities:**
- `GET /metrics` returns Prometheus text format. Unauthenticated by default (the metrics are operator-internal, expose-them-only-on-the-LAN), with an opt-in `node.api.metrics.require_auth` toggle for paranoid deployments.
- DNS engine counters:
  - `skoed_dns_queries_total{outcome="..."}` — blocked / forwarded / cached / local, per-transport (+ -doh / -dot suffixes)
  - `skoed_dns_query_duration_seconds` histogram (5 buckets: 1ms, 10ms, 100ms, 1s, 5s)
- Cache (wires the existing M4.7 counters):
  - `skoed_dns_cache_size`, `_max_entries`, `_hits_total`, `_misses_total`, `_evictions_total`
- Cluster (gauges, refreshed per scrape):
  - `skoed_cluster_node_role{role="leader|follower"}` (1 / 0)
  - `skoed_cluster_raft_term`, `_commit_index`, `_members`, `_reachable_members`
- DHCP (when M3.6 integration is enabled):
  - `skoed_dhcp_leases`, `_anomalies_open`, `_last_poll_age_seconds`, `_poll_errors_total`
- Build info gauge `skoed_build_info{version="...",commit="...",go="..."}` set to 1

**Non-goals:**
- OpenTelemetry support (Prometheus only for M5; OTel later if asked)
- Per-route HTTP-handler timings (overkill; scrape interval averages are enough)
- Custom histograms / summaries (operator brings their own recording rules)
- Authenticated `/metrics` by default (the metrics are not secrets; operators on shared networks set `require_auth` themselves)

**Dependencies:** None hard. M4.7 already exposed the cache counters in Go; this milestone wires them through `prometheus/client_golang`.

---

### Milestone 5.2 — Audit Log

**Outcome**: Every state-changing API call (auth, blocklist edits, profile changes, schedule mutations, settings) is recorded with timestamp, actor, target, and diff so operators can answer "who turned cat:doh off at 2 AM?". Surface via Web UI table + Prometheus counters + a `GET /api/v1/audit` endpoint.

**Capabilities:**
- bbolt-replicated audit-log table (cluster-wide; viewable from any node)
- Per-entry fields: `id`, `timestamp`, `actor` (`user:<name>` or `token:<id>` once M7 lands), `action` (e.g. `blocklist.create`), `target`, `node_id`, `diff_summary` (JSON patch-style or human string)
- Configurable retention (default 90 days); compacted into Raft snapshot
- Web UI: Audit page under Settings, with filters by actor / action / date range
- API: `GET /api/v1/audit?limit=N&offset=M&actor=…&action=…`
- Prometheus: `skoed_audit_events_total{action="..."}`

**Non-goals:**
- Forwarding to external SIEM (operator pipes the API)
- Tamper-evident hash chain (every entry is Raft-replicated; tampering = breaking Raft)
- Audit for read operations (writes only)

**Dependencies:** M5.1 (to wire the counter). M7 (token auth) will refine `actor=token:<id>` attribution.

---

### Milestone 5.4 — Automated Blocklist Refresh

**Outcome**: URL-source blocklists refresh on a schedule without operator intervention. Stale lists raise a Dashboard alert.

**Capabilities:**
- Per-blocklist `refresh_interval` field (default: cluster setting; default cluster setting: 24h)
- Refresh worker runs only on the leader; results replicate via Raft so all nodes see the same list version
- Per-blocklist `last_refresh_at`, `last_refresh_status` (`ok` / `error` / `unchanged`), `last_refresh_error`
- Optional GPG signature verification for sources that publish signed lists (e.g. Hagezi)
- Dashboard warning card when any blocklist hasn't refreshed in 2× its interval
- Prometheus: `skoed_blocklist_last_refresh_seconds{id="..."}`, `_refresh_failures_total{id="..."}`

**Non-goals:**
- Per-rule deltas (UI shows count delta only; full diff would explode UI)
- Push-style refresh hooks
- Multi-source merge (one URL per blocklist)

**Dependencies:** M2 cluster (so refresh ownership is leader-only). M5.1 for metrics.

---

### Milestone 5.6 — In-place Upgrade

**Outcome**: Operators upgrade skoed without losing config or interrupting the cluster — UI button or CLI command downloads the new binary, verifies its signature, and rolls the cluster one node at a time.

**Capabilities:**
- `GET /api/v1/upgrade/check` queries the release feed and returns `{current, available, release_notes_url}`
- `POST /api/v1/upgrade/start` downloads + verifies + swaps the binary, then exits 0; supervisor (systemd / pct) restarts. State on disk survives.
- CLI: `skoed upgrade [--version vX.Y.Z]` runs the same flow without the API
- Cluster-aware: only one node upgrades at a time; M2 leader election handles the brief gap
- Web UI: "Upgrade available" banner on the Dashboard with one-click trigger
- Cosign-signed releases; verification fails the upgrade if signature invalid

**Non-goals:**
- Rollback to prior version (operator keeps the prior binary if they want it)
- Zero-downtime upgrade on single-node deployments (a single-node restart IS the downtime)
- OS-level updates (M5.5 packages handle that via apt)

**Dependencies:** M5.5 native packaging (apt path) AND M5.7 multi-arch builds. M5.2 audit log (record who triggered the upgrade).

---

### Milestone 5.7 — Multi-architecture Release Builds

**Outcome**: Every skoed release ships both `linux/amd64` and `linux/arm64` binaries + matching Docker images, so Raspberry Pi / arm64 servers / Apple Silicon Linux VMs install the same release.

**Capabilities:**
- `goreleaser.yaml` builds both arches in one CI run
- Docker multi-arch manifest via `docker buildx --platform linux/amd64,linux/arm64`
- M5.5 `.deb` packages built for both arches
- CI gate: image size ≤ 100 MB per arch (existing M1 risk row)
- Release notes auto-include arch-specific checksums + cosign signatures

**Non-goals:**
- `linux/arm` (32-bit) — defer; almost no demand
- macOS / Windows builds — skoed is a Linux daemon
- FreeBSD / OpenBSD ports — community-driven

**Dependencies:** M5.5 packaging (the .deb needs an arch). M5.6 upgrade (the upgrade verifier picks the right arch's binary).

---

### Milestone 5.8 — Documentation Site

**Outcome**: A `docs.skoed.io`-style static site with install guide, configuration reference, cluster setup, troubleshooting, and how-tos for common deployments (Proxmox, Pi-hole migration, K8s).

**Capabilities:**
- Static site generator (mdBook, Hugo, or VitePress — TBD; lean toward mdBook for low maintenance)
- Per-milestone docs synced from the in-repo `specs/` and `DEMO_NOTE_*.md` files
- Search via Pagefind (no JS framework dep)
- Versioned docs (latest + previous N major versions)
- Hosted via GitHub Pages (free, no infra to run)

**Non-goals:**
- Translated docs (English only)
- API reference auto-gen (M4.5 Swagger already does that)
- Comment threads / forums

**Dependencies:** None hard. Naturally pairs with the rest of M5 (install guide references M5.5 packages, cluster guide references M5.3 encrypted mesh, etc.).

---

### Milestone 5.3 — Encrypted Cluster Mesh

**Outcome**: All inter-node traffic is encrypted and mutually authenticated. Operators stop needing to run skoed inside a private overlay network to keep replicated state (blocklists, profiles, password hashes, query-log aggregates) off the wire.

**Today's gap** (`internal/cluster/raft.go:76`, `cluster.go:321`):
- Raft uses `raft.NewTCPTransport` — plain TCP for AppendEntries, voting, snapshots
- Follower → leader API forwarding uses plain HTTP, authenticated by a shared `X-Cluster-Secret` header
- Join flow uses plain HTTP with a single-use token

**Capabilities:**
- **mTLS for Raft** via hashicorp/raft's `StreamLayer` interface — replace the TCP transport with a TLS-wrapped one. Each node holds a cluster CA + per-node leaf cert; the CA is generated at bootstrap and replicated through the join flow (chicken-and-egg solved: bootstrap token carries the CA fingerprint)
- **HTTPS for cluster-internal API** — the M4.6 management-API HTTPS work also covers `/_internal/aggregates` and the forwarder. Configurable to either (a) reuse the M4 ACME cert, (b) use the cluster-CA-issued cert, or (c) accept self-signed peers via fingerprint pinning
- **Cluster-CA rotation** — operator can rotate the CA on a rolling restart; old CA stays valid for an overlap window
- **Auto-pinning on join** — joining nodes record the leader's cert fingerprint at enrolment time and refuse to talk to peers that don't match
- **Verify or pin, never trust-on-first-use silently** — joining without a fingerprint OR a known-good CA fails loudly

**Non-goals:**
- Per-tenant key segmentation (single CA per cluster)
- HSM / TPM integration
- Per-message AEAD on top of TLS (TLS is already AEAD)

**Dependencies:** M2 Raft, M4 ACME (reused tooling), M5 hardening track (rolling-restart story).

---

### Milestone 5.5 — Native Packaging

**Outcome**: skoed installs on Debian/Ubuntu/Raspberry Pi OS via `apt`, and on Proxmox via a one-shot LXC bootstrap script. The single-binary release stays available, but most operators move to the OS-managed install path.

**Capabilities:**
- `.deb` packages for amd64 and arm64 (`skoed`, `skoed-cluster` — the latter pulls in the cluster bootstrap helpers)
- systemd unit file; default config at `/etc/skoed/config.yaml`; data at `/var/lib/skoed`; `skoed` system user
- apt repo hosted alongside GitHub releases
- Proxmox LXC bootstrap script (`pveam` + `pct create` + first-run config wizard)
- All packages reuse the M5 in-place upgrade path on `apt upgrade` / `pct exec`

**Non-goals:**
- RPM / Arch / Alpine native packages (community-driven; the static binary stays available for those)
- A homebrew formula (macOS isn't a target host)

**Dependencies:** M5 (in-place upgrade hook).

---

### Milestone 5.9 — Operator QoL (umbrella)

**Outcome**: skoed feels nice to install, configure, and live with. Less curl, more `skoed <verb>`; faster dev loop; less surprise on first boot; safer URL-tester ergonomics.

Umbrella for several small landings — each lands as a separate PR but they're cheap enough to ship in a single session.

| Sub | Title                                                | Status |
|-----|------------------------------------------------------|--------|
| 5.9.1 | `skoed` CLI + TUI (charm-stack, full color)       | shipped |
| 5.9.2 | `make dev` — Vite hot-reload for the SPA            | shipped |
| 5.9.3 | Docker test cache (go-mod volume)                   | shipped |
| 5.9.4 | Getting Started card + docs page                    | shipped |
| 5.9.5 | URL tester (CLI + public landing page)              | shipped |
| 5.9.6 | Rename dblock → skoed + About page                   | shipped |
| 5.9.7 | "Would this domain be blocked?" tester              | shipped |

**Non-goals for the M5.9 umbrella:**
- Replacing the existing Web UI Vue stack
- Server-side rendering / no-JS support
- Public/SaaS posture (skoed stays a private-network admin tool)

**Dependencies:** Each sub-milestone is independent; pick any order.

---

### Milestone 5.9.1 — `skoed` CLI + TUI (charm-stack)

**Outcome**: Operators run `skoed <verb>` for everything they used to `curl -u admin:pwd …`; live cluster overview without leaving the terminal.

**Capabilities:**
- CLI verbs via [cobra](https://github.com/spf13/cobra) styled with [lipgloss](https://github.com/charmbracelet/lipgloss) (matches the SPA Lipgloss palette — same hexes):
  - `skoed version` (also: `skoed --version`)
  - `skoed health` (alias: `skoed ping`)
  - `skoed status` (cluster nodes + roles + commit index, color-coded)
  - `skoed token create` (returns the M5.3 join bundle)
  - `skoed blocklist test <url>` (M5.9.5 hooks here)
  - `skoed daemon` (current behaviour; default when no subcommand given so existing `skoed --config …` keeps working)
- TUI dashboard via [bubbletea](https://github.com/charmbracelet/bubbletea):
  - `skoed top` — live cluster + DNS rate + top blocked domains + audit-log tail. Hot-keys: `q` quit, `r` force refresh, `f` filter.
- Auth via the same `auth/setup`-set credentials; read from `~/.skoed/credentials` (mode 0600) or `--auth user:pass`. Talks to the management API at `--api http://localhost:8080` (or `SKOED_API` env).

**Non-goals:**
- A full curses TUI for editing config (operators edit YAML or use the Web UI)
- Shell completion (M5.9.1.1 follow-up; cobra has it built-in, just needs `make completions`)

**Dependencies:** None; everything talks to the existing management API.

---

### Milestone 5.9.2 — `make dev` (SPA hot-reload)

**Outcome**: UI iteration is instant. Edit a `.vue` file → see it in the browser without rebuilding the Go binary.

**Capabilities:**
- `make dev` starts:
  - A skoed daemon on a known port (e.g. 18099)
  - `vite dev` on 5173, proxying `/api/*` and `/metrics` to the daemon
- Vite HMR (already in the existing config) handles per-file reload
- `make dev-cluster` (stretch) spins a 3-node cluster + Vite proxy for testing leader-forward UX

**Non-goals:**
- Replacing the embedded-binary production model (Vite dev is for dev only)
- Auto-rebuilding the Go binary on `*.go` change (use `air` or `entr` if needed; not bundling another tool)

**Dependencies:** None.

---

### Milestone 5.9.3 — Docker test cache (go-mod volume)

**Outcome**: `make acceptance` runs in ~1 min on warm cache instead of ~10 min.

**Capabilities:**
- `tests/acceptance/run-in-docker.sh` mounts a persistent named volume at `/go/pkg/mod` and `/root/.cache/go-build`
- First run downloads + compiles; subsequent runs reuse
- `make acceptance-clean` clears the volume when a clean run is needed
- README note about the cache + how to wipe it

**Non-goals:**
- Caching the test binary itself (changes every commit anyway)
- Production-image impact (the container image used by run-in-docker.sh is dev-only; production .deb/Docker image are untouched)

**Dependencies:** None.

---

### Milestone 5.9.4 — Getting Started card + docs page

**Outcome**: A new operator who just set the admin password sees a clear "here's what to do next" affordance instead of an empty Dashboard.

**Capabilities:**
- Dashboard "Getting Started" card, visible iff `blocklists.length === 0 && profiles.length === 0`:
  - 3-step checklist: 1) Add a blocklist 2) Bootstrap a cluster (optional) 3) Point a client at skoed
  - Each step is a click that takes operator to the right page
  - Auto-hides once cluster has any blocklist (no dismiss needed)
  - Operator-dismissible via `[x]` (stored in `localStorage`); doesn't reappear
- New docs chapter `first-run/getting-started.md` covering the same flow with copy-pasteable bash

**Non-goals:**
- A wizard / multi-step modal (operators dislike modals; the dashboard card is enough)
- Pop-up toasts (zero pop-ups added)

**Dependencies:** M5.8 docs site (so the docs link goes somewhere).

---

### Milestone 5.9.5 — URL tester (CLI + public landing page)

**Outcome**: Operators can sanity-check a blocklist URL **before** they install skoed or set up auth — via CLI or via a public landing page on a running skoed that doesn't require login. skoed stays a private-network admin tool; the landing page is the *one* unauthenticated surface and is SSRF-guarded.

**Capabilities:**
- **CLI**: `skoed blocklist test <url> [--format hosts|domainlist|askoed|auto]`
  - Fetches in-process with 30 s timeout; parses; prints a styled summary:
    `✓  https://… → 12,453 domains (hosts format, 6 skipped)`
  - No daemon, no auth, no SSRF risk (operator's own process, operator's own network)
- **Public landing page** at `/`:
  - Replaces the current "redirect to /login if unauthenticated"
  - URL tester form + small "Login" button top-right → existing admin UI
  - Submitted URL is fetched by the skoed daemon; SSRF-guarded by an allow-list (public hosts only — refuses RFC1918 / localhost / link-local / metadata IPs)
  - Rate-limited (60 req/h per source IP) so a hostile internet visitor can't turn skoed into a port-scan amp
- Operator can disable the public landing page entirely with `node.api.public_landing.enabled = false` (config default: `true`; M5.9.5.1 may flip the default once we learn whether anyone leaves skoed internet-facing)

**Non-goals:**
- Authenticated tester (admin already has the Create modal; the public version is for evaluation-before-install)
- General-purpose admin features without auth (only the tester is unauthenticated)
- SaaS / multi-tenant posture (skoed stays single-org)

**Dependencies:** M5.9.1 (CLI subcommand framework).

---

### Milestone 5.9.7 — "Would this domain be blocked?" tester

**Outcome**: Operators and curious household members can ask skoed *"would `example.com` be blocked from this network?"* and get a clear answer with a rationale. Two surfaces with different depth:

- **Guest** (no auth, public landing card): yes/no for the default profile. Useful for "is the router actually using skoed?", "is my kid's school site blocked by mistake?", first-install sanity check.
- **Authenticated** (admin UI + CLI): full verdict with the reasoning chain — which client matched which profile, which blocklist hit, which schedule was active, what block policy would apply, would a local DNS entry / SafeSearch rewrite intervene first.

**Capabilities:**

*Backend:*
- `POST /api/v1/_public/test-domain` — body `{domain}`. Returns `{would_block: bool, reason: string}` where `reason ∈ {"blocklist","allowlist","local-dns","forwarded"}`. Evaluated against the default profile only. Rate-limited 60/h per source IP (reuse M5.9.5's token bucket) + the same SSRF-style allow-list (the *domain* doesn't need to resolve, but we refuse `.invalid` / `.local` / IP-literal inputs so the endpoint can't be used as a DNS-server discovery probe). Operator-disable via the same `node.api.public_landing.enabled=false` flag.
- `POST /api/v1/test-domain` (auth-gated) — body `{domain, client_ip?, profile_id?}`. Returns the full chain:
  ```json
  {
    "would_block": true,
    "reason": "blocklist",
    "matched_profile":   {"id":"kids","name":"Kids","matched_by":"client_ip"},
    "matched_blocklist": {"id":"hagezi-pro","name":"Hagezi Pro"},
    "matched_schedule":  {"id":"bedtime","name":"Kids bedtime","window_active":true},
    "block_policy":      "nxdomain",
    "local_dns_answer":  null,
    "safesearch_rewrite": null
  }
  ```
  Re-runs the existing `filter.Engine.EvaluateForClientID` path so the answer is identical to what a real query would get — no second source of truth.

*Web UI:*
- **Landing card** (next to the M5.9.5 blocklist tester): "Test a domain" — input + button → ✓ blocked / ✗ allowed strip with the reason chip.
- **`/dashboard/tools/test-domain`** (auth): full form with client-IP picker + profile dropdown, renders the rationale as a styled tree.

*CLI:*
- `skoed domain test <domain> [--client 10.42.10.50] [--profile kids]` — Lipgloss-styled verdict + chain. Hits the auth endpoint; falls back to the public one when called without credentials.

*Metrics:*
- `skoed_test_domain_requests_total{surface="guest|auth", verdict="block|allow"}` counter. Surfaces abuse + operator-curiosity patterns.

**Non-goals for this milestone:**
- Returning DNS-level RRs (no `dig`-style answer composition; just the verdict + rationale)
- Per-rule diff explainer ("this domain matched rule #42 on line 8 of the hagezi-pro source") — verdict + matched blocklist id is enough
- Recursive what-if ("what if I added this to the allowlist?") — operator can add it temporarily; revisit if asked

**Dependencies:** M5.9.1 (CLI framework), M5.9.5 (landing page + rate-limit + opt-out pattern), the existing filter engine + profile-match priority from M3.6.

---

### Milestone 6 — Closing the DoH Gap

**Outcome**: Operators can block hardcoded-resolver-IP DoH/DoT bypasses at their firewall, using skoed-generated rule snippets. Closes the last bypass route M3 + M3.5 leave open.

**Capabilities:**
- Firewall-rule generators for `iptables`, `nftables`, MikroTik RouterOS, OpnSense / pfSense, UniFi controllers
- Curated database of public DoH/DoT resolver IPs, refreshed daily from a tracked upstream
- Web UI: per-platform "Copy rules" button on the Clients / Stats pages, scoped to client subnets
- Documentation: "Closing the DoH gap" guide covering placement, monitoring, and false-positive recovery

**Non-goals:**
- skoed pushing rules into routers automatically (operator copy-paste only)
- SNI-based blocking (belongs at the firewall, not in skoed)

**Dependencies:** M3.5 detection track.

---

### Milestone 6.5 — DHCP Layer-3 Anti-Spoof + Replicated Leases

**Outcome**: The M3.6 anti-spoof detector gains a third layer (ARP/NDP cross-check), the lease cache replicates across the cluster, and skoed can finally name a "dynamic vs static" lease.

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

**Outcome**: Non-interactive callers (CLI scripts, CI jobs, Home Assistant, monitoring agents, the Kubernetes operator, etc.) authenticate to the management API with revocable, scoped tokens. The Web UI keeps using username + password so humans don't need to manage tokens to log in.

**Two-mode auth:**
- **Web UI / browser sessions** → username + password (unchanged from M1). The login flow stays the same; admins set a password during first-run setup.
- **Programmatic API access** → bearer tokens. `curl`, scripts, the operator, and any other non-browser caller MUST use a token (Basic Auth via `-u admin:pass` stays accepted as a deprecated transition path for two minor releases, then removed).

**Capabilities:**
- Token store in bbolt (replicated): `(id, hash, label, scopes, created_at, last_used, expires_at)`
- New endpoints: `POST /api/v1/tokens` (mint), `GET /api/v1/tokens` (list, no hash), `DELETE /api/v1/tokens/{id}` (revoke), `PATCH /api/v1/tokens/{id}` (relabel, change expiry)
- `Authorization: Bearer …` honored on every authenticated route
- Scopes: `read`, `write`, `cluster:admin` (mints tokens, transfers leadership). Default scope = `read+write` for ease of migration
- Per-token audit-log entries (pairs with M5 audit log) — every API call records `actor=token:<id>` or `actor=user:<username>`
- Web UI: token-management page under Account → "API Access". Token value shown ONCE on creation; afterwards only the label, scopes, last-used time, and revoke button
- Migration guide: how to flip CLI / Ansible / Home Assistant integrations from Basic Auth to Bearer

**Non-goals:**
- OAuth2 / OIDC integration (overkill for self-hosted)
- LDAP / SAML federation
- Token-for-Web-UI-login (sessions keep using password — tokens are for scripts)
- Per-IP / per-CIDR token binding (defer)

**Dependencies:** M5 audit log.

---

### Milestone 8 — Encrypted DNS Expansion (DoH3 + DNSCrypt)

**Outcome**: skoed serves the two remaining encrypted-DNS dialects so clients that prefer them get filtered DNS too.

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

**Outcome**: A native operator manages skoed clusters on Kubernetes via CRDs — supersedes the M2.5 Helm chart for serious K8s users.

**Capabilities:**
- CRDs: `DblockCluster`, `DblockNode`
- Automatic cluster scaling, ACME cert rotation, lease-data PVC management
- Helm chart kept as a thin wrapper for non-operator deployments
- Status conditions surface Raft health to `kubectl get skoedcluster`

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

### Milestone 11 — Distribution & Documentation

**Outcome**: skoed is a first-class citizen on every mainstream Linux packaging channel. An operator can install it from their native package manager, the docs site answers every "how do I…" question without opening the source tree, and the GitHub README gives a first-time visitor all the context needed to try skoed in under 5 minutes.

**Capabilities:**
- Alpine Linux `.apk` package built and attached to every GitHub Release (amd64 + arm64)
- AUR PKGBUILD (`packaging/aur/PKGBUILD`) kept in sync with releases; automated CI push to AUR
- Helm chart (`charts/skoed/`) — deploys skoed as a DaemonSet or Deployment on any CNCF-conformant Kubernetes cluster; published as an OCI chart to `ghcr.io/ashmonger/charts/skoed`
- Proxmox LXC bootstrap script attached to every GitHub Release as `proxmox-create.sh`
- CI release workflow publishes all of the above atomically on `v*` tags
- Documentation site: all stub pages replaced with real content — install (Docker, Kubernetes), all configuration options, cluster operations, reference (YAML schema, CLI, API)
- `README.md` rewritten as a product README: badges, 30-second quickstart, feature summary, install matrix, screenshots

**Non-goals:**
- Publishing to official Debian/Ubuntu PPA (requires Debian Developer sponsorship; out of scope)
- Publishing to Alpine's official `edge` repository (requires Alpine maintainer; out of scope)
- Homebrew formula (macOS install; skoed runs on Linux only)
- Automatic documentation translation

**Dependencies:** M1–M10 complete.

---

### Milestone 12 — Cluster Join via Web UI + Config Backup/Restore

**Outcome**: An operator can expand a single-node installation into a multi-node cluster entirely from the browser — no SSH, no CLI — and can download or upload the full configuration from the Settings page for safe migration and disaster recovery.

**Capabilities:**

*Cluster join (web UI):*
- Leader's Cluster page shows a "Generate join token" button; clicking it calls `POST /api/v1/cluster/tokens` and displays the resulting payload block (token + leader_address + expires_at) ready to copy
- Follower's Cluster page (when in `single-node` mode) shows a "Join an existing cluster" panel; operator pastes the payload and clicks Join
- New follower-side endpoint `POST /api/v1/node/join-cluster`: validates the pasted payload, resets local Raft state (`ResetRaftForJoin`), then calls the leader's `POST /api/v1/cluster/join`; returns 409 if the node is already a cluster member; forwards the leader's 403 if the token has been consumed
- Join panel auto-hides once the node's cluster health endpoint reports `mode: cluster`

*Config backup/restore (Settings page):*
- "Configuration backup" section with a "Download backup" link that calls `GET /api/v1/config/export` — produces a tar.gz archive with `config.yaml` stripped of admin credentials
- File picker + "Restore" button that `POST /api/v1/config/import` with the archive; guarded by a confirmation modal
- Export explicitly excludes `password_hash` and `auth.*` fields — credentials are a per-node secret that must not travel in portable backups; import preserves the current node's credentials unchanged

**Non-goals:**
- Cluster leave / node removal via UI (use API directly)
- Scheduled or automatic backups (manual download only)
- Backup encryption (operator is responsible for storage security)
- Merge / diff between two backup archives

**Dependencies:** M10 (active-active cluster, `cluster/tokens` endpoint, `cluster/join` endpoint already exist).

---

### Milestone 13 — Temporary Filtering Pause (Break-Glass Mode)

**Outcome**: An operator can suspend all DNS filtering for a configurable window without touching config — useful for debugging, guest access, or a parent granting a temporary exception. Filtering resumes automatically when the timer expires.

**Capabilities:**
- `POST /api/v1/filter/pause` — body `{duration_seconds: N, reason?: string}` where N ≤ 86400 (24 h ceiling). Returns `{active: true, resumes_at: <ISO-8601>, reason}`. Cluster-wide: replicated through Raft so every node honours the same window simultaneously.
- `DELETE /api/v1/filter/pause` — cancels an active pause early.
- `GET /api/v1/filter/pause` — returns current pause state `{active, resumes_at?, reason?}`.
- Filter engine short-circuits on every DNS query when a pause is active: blocklist + profile rules are skipped; local DNS entries and DNSSEC posture are unchanged. Query log entries during the window carry `outcome: forwarded` and `pause_active: true`.
- Dashboard: countdown chip showing "Filtering paused — resumes in X:XX" + "Resume now" button. Chip disappears when the pause expires or is cancelled.
- Pause state survives a node restart (stored in bbolt, replicated via Raft). Auto-expires correctly even across restarts.
- Settings: configurable hard ceiling `filtering.pause_max_seconds` (default 86400). Operator can set it to 0 to disable the feature entirely.

**Non-goals:**
- Per-profile or per-group scope (cluster-wide only; combine with profile rules for finer control)
- Scheduled recurring pauses (that is already M3 schedule-rules)
- Notifications / push alerts when pause starts or expires

**Dependencies:** M3 (filter engine + profiles). M5.2 audit log wires `action: filter.pause` / `filter.resume` automatically — implement if audit log is present, skip gracefully if not.

---

### Milestone 14 — Block Dynamic-Lease Clients (Profile Rule Completion)

**Outcome**: An operator can create an "untrusted" profile that automatically catches any device receiving a DHCP dynamic lease — guest phones, unregistered IoT gadgets — without listing every device individually.

**Capabilities:**
- `block_dynamic_clients: true` field on any non-default profile: when set, the profile matches every DNS client whose lease `origin` is `"dhcp_dynamic"` (in addition to any explicit `client_ips` / `client_macs` / `client_cidrs` — OR semantics).
- Only `"dhcp_dynamic"` origin triggers the rule. `"dhcp_static"`, `"router_advertised"`, `"manual_admin"`, empty/unknown origins are not matched (conservative default).
- `block_dynamic_clients: true` is rejected on the `default` profile (400) — operator must create a dedicated profile.
- Client-ID / MAC / hostname / IP match tiers still outrank `block_dynamic_clients` when a higher-priority profile also matches.
- `GET /api/v1/clients/{ip}` — `profile_ids` list includes the dynamic-matched profile when applicable; `origin` field reflects the lease origin.
- Replicates cluster-wide via Raft (profile field is already in the replicated config).

**Non-goals:**
- Auto-creating an "untrusted" profile on first boot
- Per-blocklist application (all profile blocklists apply uniformly)
- `block_static_clients` inverse rule
- DHCPv6 DUID as a matcher (observational only at this milestone)
- Time-bounded variants (use schedule-rules)

**Dependencies:** M3.6 DHCP connectors (lease `Origin` field) — already shipped. Functional spec and acceptance tests already written; this milestone completes the implementation.

---

## Pre-1.0 release tasks (no milestone number)

- ~~**Find a better name.**~~ **Done** — name is **skoed**.

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
- Transparent proxy mode (L4 transparent proxy to redirect hardcoded-resolver clients)
- Deep packet inspection / HTTP filtering (out of scope; puts skoed in Squid/e2guardian territory)
- Cloud-hosted SaaS (contradicts self-hosted-first thesis)

## Non-goals under reconsideration

- **Mobile application** — native iOS/Android admin app. Deferred until there is confirmed evidence that "official" DNS (ISP resolver, DoH built into browsers/OS) is bypassed by skoed in the target deploy topology. Without that, a mobile app would give operators false confidence. Needs M7 API tokens first for safe credential handling.
