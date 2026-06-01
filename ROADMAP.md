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

## Post-Milestone 5 (backlog, not committed)

- Active-active cluster mode (any-node writes, Raft-based consensus)
- DoH3 / HTTP/3 server endpoint
- DNSCrypt server endpoint
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
