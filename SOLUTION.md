# Solution: skoed

## Problem Statement

Home network administrators and parents face uncontrolled ad traffic, trackers, and unrestricted content access across all devices on their network. Existing solutions (AdGuard Home, Pi-Hole) require per-node configuration, lack built-in multi-node sync, or have complex deployment requirements. This causes privacy risks, slower browsing, bandwidth waste, and exposure of children to inappropriate content — without a maintainable, scalable, self-hosted solution.

## Ubiquitous Language

### Actors
- **Network Administrator**: A person who installs, configures, and operates skoed on a home or lab network.
- **Client**: A network device (laptop, phone, IoT device) identified by IP address that sends DNS queries to skoed.

### Core Domain Terms
- **Node**: A running skoed instance on a host that serves DNS queries and enforces filtering rules.
- **Primary node**: The single authoritative configuration source in a skoed cluster; all config changes originate here.
- **Replica node**: A skoed instance that mirrors configuration from the primary node and serves DNS independently.
- **Cluster**: A set of skoed nodes (1 primary + 0..N replicas) sharing the same configuration.
- **Blocklist**: A named collection of domain rules sourced from a provider URL or defined manually.
- **Allowlist**: A named collection of domains explicitly permitted, overriding all blocklist matches.
- **Client profile**: A named set of rules (active blocklists, allowlists, schedules) applied to one or more clients.
- **Local DNS entry**: A manually configured A, AAAA, or CNAME record served for home or lab hostnames.
- **Upstream resolver**: An external DNS server used to forward non-blocked queries. skoed defaults to Quad9 (9.9.9.9 / 149.112.112.112) — Swiss-based, no personal-data logging, blocks malicious domains. Google DNS is intentionally not a default.
- **Root DNS resolution**: Recursive DNS resolution starting from IANA root nameservers; no third-party upstream required.
- **SafeSearch rewrite**: A DNS response override that redirects a search engine or video platform to its safe-search endpoint.
- **Config sync**: The mechanism by which the primary node propagates configuration changes to all replica nodes.
- **Split-brain**: A state where two nodes simultaneously believe they are primary, risking configuration divergence.
- **Enrollment**: The process by which a new node joins an existing cluster and receives its initial configuration.

### Forbidden/Ambiguous Terms
- **Fast**: Use explicit latency thresholds (e.g., "< 5ms for cached responses").
- **Easy**: Replace with measurable outcomes (e.g., "a node is enrolled in fewer than 5 manual steps").
- **Secure**: Specify trust boundaries and mechanisms (e.g., "sync API requires mutual TLS").
- **Robust**: Replace with concrete failure handling (e.g., "replicas continue serving DNS if the primary is unreachable").

## Global Technical Architecture

### System Boundaries
- skoed is a single Go binary per node.
- Each binary embeds: DNS server, HTTP management API, web UI (compiled SPA), and sync engine.
- Clients configure skoed node IP(s) as their DNS resolver via DHCP or static assignment.
- External DNS (upstream resolvers or root nameservers) is accessed outbound for query forwarding.
- Node-to-node sync uses a dedicated internal HTTPS REST API.

### Components

| Component | Responsibility |
|-----------|----------------|
| DNS engine | Receives DNS queries, applies filters, resolves via upstream or root DNS, returns responses |
| Filtering engine | Evaluates blocklists, allowlists, client profiles, schedule rules, SafeSearch rewrites |
| Sync engine | Manages cluster roles (primary/replica), detects split-brain, pushes config to replicas |
| HTTP API | RESTful management API for all configuration operations |
| Web UI | SPA embedded in binary via Go `embed`; full management interface |
| Config store | YAML-based configuration; supports full import/export as a single archive |
| Query log | Stores recent DNS query history per client |

### Technology Stack
- Language: Go 1.22+
- DNS library: `miekg/dns`
- Web UI: Vue.js (compiled, embedded via `embed`)
- Config format: YAML
- Node sync: HTTPS REST (JSON) with shared secret or mutual TLS
- Container: Multi-stage Docker build, Alpine final image
- Kubernetes: Helm chart (DaemonSet per node or Deployment)
- Blocklist formats: hosts file, domain list, AdBlock/ABP syntax

### Non-Functional Requirements
- Binary size: ≤ 50 MB
- Container image size: ≤ 100 MB
- DNS query latency (blocked/cached): ≤ 5 ms
- Config sync propagation: ≤ 10 s under normal network conditions
- Node enrollment: completable in ≤ 5 manual steps from a fresh install
- Operates air-gapped when root DNS resolution is enabled
- Supported architectures: amd64, arm64

### Top Risks and Trade-offs
- **Split-brain**: mitigated by last-seen timestamps and health-check quorum; full consensus protocol deferred.
- **Embedded UI increases binary size**: mitigated by keeping UI minimal and using efficient bundling.
- **DNS amplification**: mitigated by restricting recursive resolution to configured client subnets only.

## Roadmap

### Milestone 1 — Single Node Foundation
Goal: a single skoed node can replace Pi-Hole or AdGuard Home on a home network.
- DNS forwarding (configurable upstream resolvers)
- Root DNS recursive resolution
- Blocklist management: add/remove/update from URL or manual entry (hosts, domain list, AdBlock formats)
- Allowlist management
- Local DNS entries (A, AAAA, CNAME)
- Query log with per-client breakdown
- Web UI: dashboard, blocklist management, local DNS, query log
- Config import/export (single YAML archive)
- Single binary install + Docker image
- Basic authentication for web UI

### Milestone 2 — Multi-Node Cluster
Goal: install a second or third node and have configuration automatically replicate.
- Node enrollment via UI (generate join token on primary, enter on replica)
- Primary/replica role management
- Config push from primary to all replicas on every change
- Split-brain detection: last-seen timestamps, periodic health checks
- Cluster status dashboard in web UI
- Helm chart for Kubernetes (DaemonSet)

### Milestone 3 — Parental Control
Goal: parents can apply different access rules per device with time-based controls.
- Per-client profiles (assign rules to specific IPs or IP ranges)
- Category-based blocking (OISD, Steven Black curated lists)
- Schedule-based rules: block/allow windows by time of day and day of week
- SafeSearch enforcement: DNS rewriting for Google, Bing, YouTube, DuckDuckGo

### Milestone 4 — Production Hardening
Goal: skoed is ready for lab/production use with observability and automation.
- Prometheus metrics endpoint
- Audit log (who changed what, when)
- Automated blocklist refresh (configurable interval)
- Multi-architecture builds (amd64, arm64)
- Upgrade mechanism (in-place binary replacement)
- Documentation site
