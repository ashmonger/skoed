# M32 — Per-Domain Upstream Routing

## Implemented

### Route Configuration
- `PATCH /api/v1/settings` accepts `dns.upstream_routes: [{match, resolvers}]` alongside existing `upstream_resolvers`
- Routes apply only when `dns.mode = "forwarding"`; ignored in recursive mode
- Route list is ordered: first match wins (top-down priority)
- Empty `upstream_routes` array or omission clears all routes

### Match Semantics
- `*.suffix` — matches any subdomain at any depth (e.g. `*.corp.internal` matches `api.corp.internal`, `db.prod.corp.internal`)
- Exact string — matches only that domain (e.g. `api.example.com`)
- Bare `*` is rejected (400) — would silently route everything, almost certainly a misconfiguration

### DNS Resolution
- `ForwardWithRoutes` checks routes top-down before falling back to global `upstream_resolvers`
- Each route gets its own `Forwarder` built from its resolver list
- No match → falls through to global upstream list unchanged

### Cluster Replication
- Routes replicated via Raft as part of `dns.upstream_routes` in the cluster config snapshot
- Shadow YAML (`config.yaml`) includes `upstream_routes` so routes survive node restart and cold start

### Upstream Discovery
- `POST /api/v1/settings/discover-upstreams` — reads `/etc/resolv.conf` nameservers, returns `{"suggested_resolvers": ["x.x.x.x:53", ...]}`
- Nothing applied automatically; operator uses suggestions to populate resolver fields

### Web UI
- Settings page: forwarding mode shows "Per-domain routes" section with Add/Remove buttons
- Each route: pattern input + resolvers textarea (one per line)
- Routes saved alongside other DNS settings on "Save DNS settings"

## Not Implemented / Limitations

- No CIDR/IP-range match (match is domain pattern only, not client IP)
- No per-profile routes (routes apply globally across all profiles)
- No UI for upstream discovery (button not exposed in UI; API-only)
- Route ordering is defined by list position; no drag-to-reorder in UI
- Proxmox/enterprise validation pending UoR review

## Acceptance Tests
14/14 pass (local): `TestM32Route*`, `TestM32UpstreamDiscovery`
