# Global Technical Architecture

## System Boundaries

- **dblock** is a single Go binary per node.
- Each binary embeds: DNS server, HTTP management API, web UI (compiled SPA), and sync engine.
- Clients configure one or more dblock node IPs as their DNS resolver (via DHCP or static assignment).
- External DNS (upstream resolvers or IANA root nameservers) is accessed outbound for non-blocked query resolution.
- Node-to-node cluster sync uses a dedicated internal HTTPS REST API, separate from the management API.
- No external database or message broker is required; all state is stored in local YAML files.

## External Actors

| Actor | Interaction |
|-------|-------------|
| DNS client | Sends UDP/TCP DNS queries to port 53 on a node |
| Network Administrator | Uses the web UI or HTTP API (port 80/443) to manage configuration |
| Upstream DNS resolver | Receives forwarded DNS queries from the DNS engine |
| IANA root nameservers | Contacted by the DNS engine when root resolution is enabled |
| Other dblock nodes | Exchange configuration over the sync API (HTTPS, configurable port) |

## Component Architecture

```
┌─────────────────────────────────────────────────────┐
│                    dblock binary                    │
│                                                     │
│  ┌─────────────┐  ┌──────────────────────────────┐  │
│  │  DNS engine  │  │       Filtering engine       │  │
│  │  (port 53)   │◄─│  blocklists / allowlists     │  │
│  │  UDP + TCP   │  │  client profiles / schedules  │  │
│  └──────┬───────┘  │  SafeSearch rewrites          │  │
│         │          └──────────────────────────────┘  │
│         ▼                                            │
│  ┌─────────────┐  ┌──────────────────────────────┐  │
│  │ Config store │  │        Sync engine           │  │
│  │  (YAML)      │◄─│  primary / replica roles     │  │
│  │  import/exp  │  │  split-brain detection       │  │
│  └─────────────┘  │  config push / enrollment    │  │
│                   └──────────────────────────────┘  │
│  ┌─────────────────────────────────────────────────┐ │
│  │           HTTP API (port 80/443)                │ │
│  │  management REST API + web UI (embedded SPA)    │ │
│  └─────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────┘
```

## Component Responsibilities

| Component | Responsibility | Isolation boundary |
|-----------|---------------|-------------------|
| DNS engine | Receive queries; consult filtering engine; forward or recursively resolve; return response | Only the filtering engine may alter DNS outcomes |
| Filtering engine | Evaluate rules (blocklists, allowlists, client profile, schedule, SafeSearch) and return a disposition (block, rewrite, forward) | Pure logic; no I/O; reads config store |
| Config store | Read/write YAML configuration; support atomic import/export | All state mutations go through this component |
| Sync engine | Manage cluster role; push config changes to replicas; detect split-brain via health checks | Only the primary role may initiate a config push |
| HTTP API | Expose management endpoints; validate auth; delegate to config store or sync engine | No business logic; thin orchestration layer |
| Web UI | SPA compiled and embedded in binary via `embed`; communicates only with HTTP API | No direct access to DNS engine or config store |
| Query log | Append DNS query events; expose recent history via API | Write-once append; no blocking of DNS path |

## Interface Contracts

- **DNS**: UDP/TCP port 53, standard DNS wire format (RFC 1035).
- **HTTP API**: REST, JSON, TLS optional (required in cluster mode). Documented in OpenAPI.
- **Sync API**: HTTPS REST (JSON), authenticated via shared secret or mutual TLS. Documented in OpenAPI.
- **Metrics**: Prometheus exposition format at `/metrics` (Milestone 4).

## Technology Stack

| Concern | Choice | Rationale |
|---------|--------|-----------|
| Language | Go 1.22+ | Single binary, small footprint, strong concurrency, used by AdGuard Home |
| DNS library | `miekg/dns` | De facto standard Go DNS library |
| Web UI | Vue.js (compiled, embedded via `embed`) | Lightweight SPA; no separate server needed |
| Config format | YAML | Human-readable, import/export friendly |
| Node sync | HTTPS REST (JSON) | Simple, debuggable, no additional runtime dependency |
| Container base | Alpine (multi-stage build) | Small image; Go binary has minimal OS dependencies |
| Kubernetes | Helm chart | Standard packaging for Kubernetes |
| Blocklist parsers | hosts, domain list, AdBlock/ABP | Covers 99% of public blocklist sources |

## Non-Functional Requirements

| Requirement | Target |
|-------------|--------|
| Binary size | ≤ 50 MB |
| Container image size | ≤ 100 MB |
| Idle memory per node | ≤ 64 MB |
| DNS response latency (blocked/cached) | ≤ 5 ms |
| DNS response latency (forwarded) | upstream RTT + ≤ 2 ms overhead |
| Config sync propagation | ≤ 10 s under normal network conditions |
| Node enrollment | ≤ 5 manual steps from a fresh install |
| Supported architectures | amd64, arm64 |
| Air-gapped operation | Supported when root DNS resolution is enabled |

## Security Boundaries

- The management API requires authentication (basic auth in Milestone 1; token-based auth in later milestones).
- The sync API requires a valid join token or mutual TLS; unauthenticated sync requests are rejected.
- Root DNS recursive resolution is restricted to clients within configured trusted subnets (prevents DNS amplification).
- Configuration files are local; no data is sent to external services.
- Secrets (join tokens, API credentials) are never logged or exposed in query logs.

## Deployment Topologies

### Single node
```
[Clients] ──DNS──► [dblock node (primary)]
```

### Multi-node cluster
```
[Clients] ──DNS──► [dblock node (primary)]
                        │ config push (HTTPS)
                        ├──► [dblock node (replica 1)]
                        └──► [dblock node (replica 2)]
```

### Kubernetes (DaemonSet)
- One dblock pod per node in the cluster.
- Primary elected from running pods.
- Shared PersistentVolume or ConfigMap for initial config distribution.

## Top Risks and Trade-offs

| Risk | Mitigation | Deferred |
|------|-----------|---------|
| Split-brain (two primaries) | Last-seen timestamps + health-check quorum; primary steps down if it cannot reach majority | Full consensus (Raft) deferred to post-Milestone 2 |
| Embedded UI inflates binary | Minimal UI, efficient bundling (Vite), tree-shaking | — |
| DNS amplification via root resolver | Restrict recursive resolution to trusted client subnets | — |
| Blocklist scale (millions of domains) | In-memory radix trie or bloom filter for O(1) lookup | Evaluated at Milestone 1 implementation |
| Single point of failure if primary down | Replicas continue serving DNS independently; write operations blocked until primary recovers | Active-active (any-write) deferred |
