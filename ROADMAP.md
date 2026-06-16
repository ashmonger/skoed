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

**Outcome**: An operator can suspend DNS filtering for a configurable window — either cluster-wide (all clients) or per-profile (only clients matched by that profile) — without touching config. Filtering resumes automatically when the timer expires.

**Capabilities:**

Global pause:
- `POST /api/v1/filter/pause` — body `{duration_seconds: N, reason?: string}` where N ≤ `filtering.pause_max_seconds`. Returns `{active: true, resumes_at: <ISO-8601>, reason}`. Cluster-wide: replicated through Raft so every node honours the same window simultaneously.
- `DELETE /api/v1/filter/pause` — cancels an active global pause early.
- `GET /api/v1/filter/pause` — returns current global pause state `{active, resumes_at?, reason?}`.
- Filter engine short-circuits all blocklist + profile rules for every client when a global pause is active; local DNS entries and DNSSEC posture are unchanged.
- Dashboard: countdown chip showing "Filtering paused — resumes in X:XX" + "Resume now" button. Chip disappears when the pause expires or is cancelled.

Per-profile pause:
- `POST /api/v1/profiles/{id}/pause` — body `{duration_seconds: N, reason?: string}`. Suspends blocklist rules only for clients matched by that profile.
- `DELETE /api/v1/profiles/{id}/pause` — cancels an active profile pause early.
- `GET /api/v1/profiles/{id}/pause` — returns current pause state for that profile.
- Profiles page: countdown badge + "Resume" button on paused profile cards.
- Multiple profiles can be paused simultaneously with independent timers.
- Global pause takes precedence: when a global pause is active, all clients see unfiltered DNS regardless of profile pause state.

Common:
- Query log entries during any pause carry `pause_active: true`.
- Pause state survives a node restart (stored in bbolt, replicated via Raft). Auto-expires correctly even across restarts.
- Settings: configurable hard ceiling `filtering.pause_max_seconds` (default 86400). Set to 0 to disable the feature entirely for all scopes.

**Non-goals:**
- Per-client pause granularity (pause applies to a whole profile or globally, not to individual IPs)
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

### Milestone 15 — Cluster Resilience: Test Suite Hardening + keepalived VIP

**Outcome**: The multi-node test suite runs reproducibly in Docker so port races and TIME_WAIT accumulation no longer cause flakes; a VRRP Virtual IP managed by keepalived keeps the cluster reachable through a leader re-election.

**Capabilities:**
- Docker-based acceptance harness: full test suite spins up in isolated containers, eliminating host kernel state pollution across runs.
- keepalived VRRP on all 3 Proxmox nodes: one VIP floats to the current primary; clients always hit the same address regardless of which node holds the Raft leader role.
- Proxmox provisioning scripts updated to auto-install keepalived and write `/etc/keepalived/keepalived.conf` on first deploy.
- Acceptance tests validate VIP failover: node eviction triggers leader election and VIP moves within the observable window.

**Non-goals:**
- Weighted VRRP priority based on Raft leader role (VIP always follows the provisioned priority order, not the dynamic leader)
- Automated keepalived config rotation when a node is permanently removed

**Dependencies:** M10 active-active cluster (Raft quorum); M14 test suite at 391 tests green.

---

### Milestone 16 — In-Place Upgrade: Binary Swap

**Outcome**: A running skoed node can fetch the latest release tarball from GitHub, validate it, and atomically replace its own binary — all via a single API call — without dropping DNS service.

**Capabilities:**
- `GET /api/v1/upgrade/latest` — queries GitHub Releases API, returns `{tag, url, current_version, upgrade_available}`.
- `POST /api/v1/upgrade/apply` — body `{url}`. Downloads tarball, extracts binary to a sibling path, fsyncs, renames over the running binary. Responds `{status: "applied", previous_version, new_version}`.
- `SKOED_TEST_SWAP_DEST` env var redirects the swap target so acceptance tests can exercise the full code path without overwriting the test binary.
- Three acceptance tests: upgrade available, upgrade not available (already latest), apply swap succeeds.
- Binary is statically linked (`CGO_ENABLED=0`) so the swapped file runs on musl/Alpine identically to the build host.

**Non-goals:**
- Rolling cluster-wide upgrade coordination (that is M18)
- Rollback to previous binary (operator responsibility; use PBS snapshot)
- Signature / checksum verification beyond tar extraction (add in M20 with token scope gate)

**Dependencies:** M10 cluster (API endpoint lives on each node); M7 API tokens (upgrade endpoint requires admin token).

---

### Milestone 17 — Schedule Bindings + Config Shadow Export

**Outcome**: Schedules can be associated with (profile, blocklist) pairs via the API so time-windowed filtering rules are fully describable through the API; the cluster shadow `config.yaml` written by the ShadowWriter includes schedules and bindings so PBS/restic filesystem backups capture a complete, human-readable replica of cluster state.

**Capabilities:**
- `GET /api/v1/schedules/{id}/bindings` — returns the list of `{schedule_id, profile_id, blocklist_id}` bindings for the given schedule. Returns 404 if the schedule does not exist and an empty array if it exists with no bindings.
- `ShadowWriter` extended: `clusterSections` now includes `schedules: []` and `schedule_bindings: []`; both are serialized into `config.yaml` after every FSM apply.
- Acceptance tests: bindings list populated, bindings list empty, schedule not found, config.yaml written with schedule data after cluster mutation.

**Non-goals:**
- Bulk-binding multiple profiles in one request
- Binding validation against schedule window overlap
- UI for schedule binding management

**Dependencies:** M3 schedule engine; M10 Raft FSM (bindings stored in bbolt `config_schedule_bindings` bucket).

---

### Milestone 18 — Active-Active Cluster Phase 2: Rolling Upgrade + Load Balancing

**Outcome**: Multi-node clusters can upgrade all nodes sequentially — one at a time, each completing before the next starts — without dropping DNS service or losing quorum; API read traffic distributes across all healthy followers so the leader is not the single point for read load.

**Capabilities:**

Rolling upgrade:
- `POST /api/v1/cluster/upgrade/apply` — body `{url}`. Orchestrates a sequential binary swap across all cluster members: drains one node (waits for it to be a follower, not the leader; if it is the leader, triggers a Raft leadership transfer first), upgrades it, confirms the node rejoined quorum, then moves to the next.
- Upgrade is aborted immediately if any node fails to rejoin within a configurable timeout (`upgrade_node_timeout_seconds`).
- Each node's upgrade uses the existing M16 `POST /api/v1/upgrade/apply` under the hood (no code duplication).
- `GET /api/v1/cluster/upgrade/status` — returns current upgrade state: `{in_progress, pending_nodes, completed_nodes, failed_node?}`.

Load balancing research:
- Document which request classes are safe to serve from followers (all `GET` requests, DNS queries) vs. which must go to the leader (all mutating API calls).
- Implement optional read-forwarding: followers respond directly to `GET` requests rather than proxying to leader. Mutating calls continue to forward to leader.
- Evaluate whether keepalived VIP should route based on leader role; document the trade-off (dynamic VIP adds VRRP preempt complexity vs. static VIP with client-side retry).

**Non-goals:**
- Canary-style partial rollout (upgrade all or none)
- Automated rollback on version mismatch (operator must intervene)
- Blue-green node replacement (add a new node then decommission the old one)

**Dependencies:** M15 keepalived VIP (cluster stays reachable during upgrade); M16 binary swap (single-node upgrade building block); M10 Raft leadership transfer.

---

### Milestone 19 — Query Log Aggregates + DoH3 Test Expansion

**Outcome**: Operators get cluster-wide query statistics (top blocked domains, unique client count, block rate) aggregated in a single API call from all nodes; DoH3/HTTP3 acceptance test coverage is extended to validate alt-svc advertisement, fallback, and concurrent query behavior.

**Capabilities:**

Query log aggregates:
- `GET /api/v1/query-log/aggregates` — fans out to all cluster members, aggregates: total queries, total blocked, block rate, top-10 blocked domains, top-10 querying clients, unique client count. Time range: last 1h / 24h / 7d via `?range=` param.
- Each node responds with its local stats; the requesting node merges and deduplicates before returning.
- Graceful degradation: if a node is unreachable, its stats are omitted and the response includes `{degraded: true, missing_nodes: [...]}`.
- Response time budget: 2 s timeout per node; aggregate response must return within 3 s.

DoH3 test expansion:
- Acceptance test: `alt-svc` header present on DoH `GET /dns-query` responses advertising `h3=":443"`.
- Acceptance test: concurrent DoH3 queries (10 parallel) all resolve correctly.
- Acceptance test: DoH3 fallback — client connecting on HTTP/2 still gets correct responses (no protocol lock-in).
- Acceptance test: DoH3 query with `OPT` records (EDNS0 extended payload) resolves without truncation.

**Non-goals:**
- Persistent aggregate storage (aggregates are computed on-demand from in-memory query logs)
- Per-profile aggregates (whole-cluster view only)
- Real-time streaming of aggregate stats (polling only)

**Dependencies:** M10 cluster fan-out infrastructure; M6 DoH3 implementation.

---

### Milestone 20 — Cluster Security Hardening

**Outcome**: API tokens carry named scopes that enforce least-privilege access; mTLS node certificates can be rotated across all cluster nodes without restarting skoed or losing quorum, closing the operational gap left by the initial M5.3 mTLS implementation.

**Capabilities:**

Token scoping:
- Tokens have a `scope` field: `read` (all `GET` endpoints), `write` (all mutating API calls except cluster and upgrade admin ops), `admin` (everything, including `/api/v1/cluster/*` and `/api/v1/upgrade/*`).
- Existing tokens created before this milestone default to `admin` scope (backward-compatible).
- `POST /api/v1/tokens` accepts `{name, scope}`. Scope defaults to `write` if omitted.
- `GET /api/v1/tokens/{id}` returns `{id, name, scope, created_at}`.
- Endpoints enforce scope: returning 403 with `{error: "insufficient scope", required: "<scope>"}` on mismatch.
- Token scope is replicated via Raft; scope enforcement is node-local.

Node certificate rotation:
- `POST /api/v1/cluster/certs/rotate` — triggers a rolling mTLS certificate renewal: generates a new CA + node certs, distributes them to all peers via the existing cluster replication channel, then reloads TLS listeners without dropping existing connections.
- Certificate rotation is serialized (one node at a time) to maintain quorum throughout.
- `GET /api/v1/cluster/certs/status` — returns `{ca_expires_at, nodes: [{id, cert_expires_at, rotation_pending}]}`.

**Non-goals:**
- External CA integration (cert rotation is self-signed only; external CA is a separate security review)
- Per-endpoint token scoping (coarser read/write/admin buckets are sufficient)
- Token expiry / TTL (tokens are permanent until explicitly revoked; TTL is a future concern)

**Dependencies:** M7 API tokens (base token infrastructure); M5.3 mTLS mesh (cert infrastructure); M10 Raft replication (used to distribute new certs).

---

### Milestone 21 — Skoed4Phone: DNS-over-VPN

**Outcome**: iOS and Android devices can use skoed as their DNS resolver regardless of network by running a lightweight local VPN tunnel that intercepts all DNS queries; when the device is on a LAN that already has a skoed cluster, the VPN defers to the cluster.

**Capabilities:**
- Local VPN profile (WireGuard or Android VPNService / iOS NEPacketTunnelProvider) establishes a loopback-like tunnel that captures UDP/53 and TCP/53 packets only — no traffic is rerouted to a remote server.
- Intercepted DNS queries are forwarded to a configured skoed node (LAN or external) using the DoH3 endpoint (`/dns-query`) with an API token for authentication.
- LAN detection: if the device connects to a known SSID or resolves a sentinel hostname that matches a configured skoed node, the VPN disables itself and lets the OS use the network's DNS directly.
- Single-node mode: if no external skoed node is configured, a bundled minimal skoed core (blocklist-only, no cluster) runs on-device; blocklists are downloaded once and cached.
- Battery / data budget: the on-device core uses a pre-compiled blocklist snapshot (no live Raft); blocklist updates are batched at configurable intervals (default: daily on Wi-Fi only).

**Non-goals:**
- Full skoed cluster participation on phone (phone is a leaf client, not a Raft peer)
- Traffic proxying beyond DNS (skoed4phone is DNS-only, not a full VPN)
- App Store / Play Store distribution from this repository (build pipeline is manual; distribution is out of scope for M21)

**Dependencies:** M7 API tokens (phone authenticates to cluster via read-scoped token); M20 token scoping (read-only token keeps cluster write-surface unexposed to the device); M6 DoH3 (phone uses HTTP3 transport to the cluster).

---

### Milestone 22 — Companion / Remote-Admin App

**Outcome**: Authorized operators can view the query log, browse aggregated stats, manage profiles, and toggle filtering pause from a mobile browser or native app when away from the LAN, using an API token for authentication.

**Capabilities:**
- Progressive Web App (PWA) hosted at `/app` on each skoed node: installable from browser on Android and iOS, works offline for last-fetched data.
- Query log viewer: paginated list with domain, client, outcome, timestamp; filter by outcome (blocked/forwarded/local) and time range; deep-link to blocklist detail for blocked entries.
- Cluster-wide aggregate stats card (reuses M19 `/api/v1/query-log/aggregates`): block rate, unique clients, top blocked domains.
- Profile list: show active schedules, pause status, blocklist count per profile; toggle per-profile pause (requires write-scoped token).
- Global filtering pause toggle (requires admin-scoped token); countdown chip with "Resume now" action.
- Authentication: Bearer token (API token from M7/M20); stored in browser credential store or OS keychain. No username/password on the app — token only.
- Remote access: app works over the internet when the operator exposes the skoed API port (or sets up a reverse proxy); no skoed-side relay or TURN server is added.

**Non-goals:**
- Full configuration management (blocklist add/delete, local DNS entry management, cluster ops) — those remain in the existing web admin at `/`
- Push notifications for pause expiry or new device detection
- Self-hosted relay / zero-config remote access (operator is responsible for port exposure or VPN)

**Dependencies:** M7 API tokens; M19 query log aggregates; M20 token scoping (companion uses read or write token, never admin); M21 Skoed4Phone (shares PWA infrastructure with the companion app).

---

### Milestone 23 — DNSSEC Validation Mode

**Outcome**: Operators can switch skoed from transparent DNSSEC proxy mode (M1 default: forwards DO bit without validating) to full DNSSEC validation mode: unsigned or BOGUS responses return SERVFAIL instead of the forged answer, giving privacy-conscious users cryptographic assurance that DNS answers have not been tampered with.

**Capabilities:**
- `dns.dnssec_mode` config field: `"transparent"` (default, current behavior) or `"validate"` (new). Replicated via Raft.
- In `validate` mode: the resolver performs full DNSSEC chain validation using a built-in DNSKEY root trust anchor (RFC 7958 format, auto-updated from IANA).
  - BOGUS records (validation fails) → SERVFAIL returned to client.
  - INSECURE records (no DNSSEC chain) → returned as-is (only signed domains are protected).
  - NXDOMAIN with NSEC proof → returned as-is.
- `GET /api/v1/settings` returns `dnssec_mode` in the `dns` section.
- `PATCH /api/v1/settings` accepts `dns.dnssec_mode`.
- Dashboard settings panel: DNSSEC mode toggle (Transparent / Validate) with a warning callout: "Validate mode will SERVFAIL for misconfigured signed domains."
- Query log entries: `dnssec_status` field added (`ok`, `bogus`, `insecure`, `indeterminate`).

**Non-goals:**
- DNSSEC signing of skoed-served local DNS entries (skoed is a resolver, not an authoritative server)
- Per-profile DNSSEC policy (one cluster-wide mode only)
- DNSSEC-aware caching (cache behavior in validate mode is unchanged)
- Trust anchor auto-rollover via RFC 5011 (manual root trust anchor update only)

**Dependencies:** M1 DNS engine (`miekg/dns` validation support); M10 Raft replication (mode change propagates to all nodes simultaneously).

---

### Milestone 24 — Webhook / Push Alerts

**Outcome**: Operators configure HTTP webhook endpoints to receive push notifications for cluster events (new unknown device detected, blocklist download failure, cluster node down, filtering pause expiry), eliminating the need to poll Prometheus or the API for operational awareness.

**Capabilities:**
- `webhooks` config section, replicated via Raft: list of webhook endpoints, each with `{url, secret, events: []}`.
- Supported event types:
  - `device.new` — first DNS query from an IP not matched by any existing client/lease record.
  - `blocklist.download_failed` — a scheduled blocklist refresh fails (network error, HTTP 4xx/5xx, parse error).
  - `cluster.node_down` — a peer node becomes unreachable (Raft heartbeat timeout).
  - `cluster.node_rejoined` — a previously-down node rejoins the cluster.
  - `filter.pause_started` and `filter.pause_expired` — global or per-profile pause state changes.
- Webhook payload: JSON `{event, timestamp, node_id, data: {...}}` where `data` contains event-specific fields.
- HMAC-SHA256 signature header (`X-Skoed-Signature`) for payload verification using the configured secret.
- Delivery: at-least-once, best-effort. Failed deliveries are retried 3 times with exponential backoff (1 s, 4 s, 16 s). Failures are logged in the audit log; they never block cluster operation.
- `POST /api/v1/webhooks` — create a webhook endpoint.
- `GET /api/v1/webhooks` — list configured webhooks.
- `DELETE /api/v1/webhooks/{id}` — remove a webhook.
- `POST /api/v1/webhooks/{id}/test` — send a test event to verify connectivity.
- Dashboard: Webhooks page with endpoint list, last-delivery status per endpoint, and test-fire button.

**Non-goals:**
- Email or SMS delivery (webhook only; operators connect their own email-via-webhook service like Mailgun or ntfy.sh)
- Per-client device alerts beyond `device.new`
- Webhook delivery guarantees / durable queue (best-effort with 3 retries)
- Fan-out deduplication across cluster nodes (each node fires independently; operators should expect duplicate events during leader re-elections)

**Dependencies:** M10 cluster health heartbeat (used to detect `cluster.node_down`); M5.2 audit log (webhook delivery failures are recorded there); M7 API tokens (webhook management API requires admin token); M22 Companion App (can subscribe to webhooks for push notification delivery to the app).

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
