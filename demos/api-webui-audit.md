# API ↔ Web UI Coverage Audit

Generated: 2026-06-18 · skoed master

## Summary

| Status | Count |
|--------|-------|
| Full UI coverage | ~70 endpoints |
| Partial / indirect | 3 |
| No UI (by design — admin/internal/backend) | ~15 |
| No UI (missing — TODO) | 5 |

---

## Full Audit

| Method + Path | Category | Web UI Panel | Has UI | Reason if no/partial |
|---|---|---|---|---|
| GET /api/v1/health | Health | — | no | Unauthenticated internal health check; not admin-facing |
| POST /api/v1/auth/setup | Auth | Setup | yes | |
| POST /api/v1/auth/login | Auth | Login | yes | |
| DELETE /api/v1/auth/session | Auth | Shell (account menu) | yes | |
| PUT /api/v1/auth/password | Auth | Account | yes | |
| GET /api/v1/audit | Audit | Audit | yes | |
| GET /api/v1/blocklists | Blocklists | Blocklists | yes | |
| POST /api/v1/blocklists | Blocklists | Blocklists | yes | |
| GET /api/v1/blocklists/{id} | Blocklists | Blocklists | yes | |
| PATCH /api/v1/blocklists/{id} | Blocklists | Blocklists | yes | |
| DELETE /api/v1/blocklists/{id} | Blocklists | Blocklists | yes | |
| POST /api/v1/blocklists/{id}/refresh | Blocklists | Blocklists | yes | |
| GET /api/v1/allowlist | Allowlist | Allowlist | yes | |
| POST /api/v1/allowlist | Allowlist | Allowlist | yes | |
| DELETE /api/v1/allowlist/{domain} | Allowlist | Allowlist | yes | |
| GET /api/v1/profiles/{id}/allowlist | Allowlist | Profiles modal | yes | |
| POST /api/v1/profiles/{id}/allowlist | Allowlist | Profiles modal | yes | |
| DELETE /api/v1/profiles/{id}/allowlist/{domain} | Allowlist | Profiles modal | yes | |
| GET /api/v1/local-dns | Local DNS | LocalDNS | yes | |
| POST /api/v1/local-dns | Local DNS | LocalDNS | yes | |
| PUT /api/v1/local-dns/{id} | Local DNS | LocalDNS | yes | |
| DELETE /api/v1/local-dns/{id} | Local DNS | LocalDNS | yes | |
| GET /api/v1/settings | Settings | Settings | yes | |
| PATCH /api/v1/settings | Settings | Settings | yes | |
| GET /api/v1/query-log | Query Log | QueryLog | yes | |
| GET /api/v1/cluster/query-log | Query Log | QueryLog / Stats | yes | |
| GET /api/v1/profiles | Profiles | Profiles | yes | |
| POST /api/v1/profiles | Profiles | Profiles | yes | |
| GET /api/v1/profiles/{id} | Profiles | Profiles modal | yes | |
| PATCH /api/v1/profiles/{id} | Profiles | Profiles modal | yes | |
| DELETE /api/v1/profiles/{id} | Profiles | Profiles | yes | |
| GET /api/v1/profiles/{id}/pause | Profiles | Profiles modal | yes | |
| POST /api/v1/profiles/{id}/pause | Profiles | Profiles modal | yes | |
| DELETE /api/v1/profiles/{id}/pause | Profiles | Profiles modal | yes | |
| GET /api/v1/schedules | Schedules | Schedules | yes | |
| POST /api/v1/schedules | Schedules | Schedules | yes | |
| GET /api/v1/schedules/{id} | Schedules | Schedules modal | yes | |
| PATCH /api/v1/schedules/{id} | Schedules | Schedules modal | yes | |
| DELETE /api/v1/schedules/{id} | Schedules | Schedules | yes | |
| GET /api/v1/schedules/{id}/bindings | Schedules | Schedules modal | yes | |
| POST /api/v1/schedules/{id}/bindings | Schedules | Schedules modal | yes | |
| DELETE /api/v1/schedules/{id}/bindings/{profile}/{blocklist} | Schedules | Schedules modal | yes | |
| GET /api/v1/categories | Categories | Categories | yes | |
| GET /api/v1/categories/{name} | Categories | Categories modal | yes | |
| PATCH /api/v1/categories/{name} | Categories | Categories modal | yes | |
| POST /api/v1/categories/{name}/enable | Categories | Categories modal | yes | |
| POST /api/v1/categories/{name}/disable | Categories | Categories modal | yes | |
| GET /api/v1/filtering/pause | Filtering Pause | Blocklists (banner) | yes | |
| POST /api/v1/filtering/pause | Filtering Pause | Blocklists (banner) | yes | |
| DELETE /api/v1/filtering/pause | Filtering Pause | Blocklists (banner) | yes | |
| GET /api/v1/clients | Clients | Clients | partial | Endpoint exists; UI uses /leases for the main list |
| GET /api/v1/clients/{ip} | Clients | — | **no** | **TODO: per-client detail panel** |
| GET /api/v1/clients/{ip}/doh-status | Clients | — | **no** | **TODO: per-client DoH status in detail panel** |
| GET /api/v1/clients/{ip}/arp-state | Clients | — | no | ARP/NDP state; operator debug use only |
| GET /api/v1/clients/_leases | Clients | Clients | yes | Lease snapshot table |
| GET /api/v1/clients/anomalies | Clients | Clients (anomalies) | yes | |
| POST /api/v1/clients/anomalies/{id}/acknowledge | Clients | Clients (anomalies) | yes | |
| GET /api/v1/clients/export-reservations | Clients | Clients (export) | yes | |
| GET /api/v1/leases | Clients | Clients | yes | |
| GET /api/v1/leases/source | Clients | — | no | Lease source URL; operator debug only |
| GET /api/v1/dns/cache/stats | DNS | Settings (DNS Cache) | yes | |
| POST /api/v1/dns/cache/purge | DNS | Settings (DNS Cache) | yes | |
| GET /api/v1/upgrade/check | Upgrade | Stats (banner) | yes | |
| POST /api/v1/upgrade/start | Upgrade | Stats (banner) | yes | |
| POST /api/v1/cluster/upgrade/apply | Upgrade | Cluster (rolling upgrade) | yes | |
| GET /api/v1/cluster/upgrade/status | Upgrade | Cluster (rolling upgrade) | yes | |
| POST /api/v1/test-domain | Testing | TestDomain | yes | |
| POST /api/v1/_public/test-blocklist | Testing | Landing (public) | yes | |
| POST /api/v1/_public/test-domain | Testing | Landing (public) | yes | |
| GET /api/v1/firewall-rules | Firewall | Clients/Profiles (DoH-gap) | yes | |
| POST /api/v1/doh-resolvers/refresh | DoH Resolvers | — | no | Leader-only operator action; no UI needed |
| GET /api/v1/doh-resolvers | DoH Resolvers | — | no | Backend data used by firewall rule generator |
| GET /api/v1/doh-resolvers/snapshot.json | DoH Resolvers | — | no | Backend JSON snapshot; not admin-facing |
| GET /api/v1/webhooks | Webhooks | — | **no** | **TODO: webhook management UI panel (M22 follow-up)** |
| POST /api/v1/webhooks | Webhooks | — | **no** | **TODO: webhook management UI panel** |
| DELETE /api/v1/webhooks/{id} | Webhooks | — | **no** | **TODO: webhook management UI panel** |
| POST /api/v1/webhooks/{id}/test | Webhooks | — | **no** | **TODO: webhook management UI panel** |
| GET /api/v1/events | SSE | — | no | Browser extension backend; not admin UI |
| GET /api/v1/tokens | API Tokens | — | no | Admin CLI/API only; security-sensitive; UI would require careful scoping |
| POST /api/v1/tokens | API Tokens | — | no | Same — no UI by design for now |
| DELETE /api/v1/tokens/{id} | API Tokens | — | no | Same |
| PATCH /api/v1/tokens/{id} | API Tokens | — | no | Same |
| GET /api/v1/cluster/certs/status | mTLS Certs | — | no | Admin operator only; no UI by design |
| POST /api/v1/cluster/certs/rotate | mTLS Certs | — | no | Admin operator only |
| POST /api/v1/cluster/tokens | Cluster | Cluster (join token) | yes | |
| GET /api/v1/cluster/status | Cluster | Cluster | yes | |
| GET /api/v1/cluster/self | Cluster | Cluster | yes | |
| GET /api/v1/cluster/health | Cluster | Shell (sidebar) | yes | |
| GET /api/v1/cluster/stats | Cluster | Cluster / Stats | yes | |
| POST /api/v1/cluster/leadership/transfer | Cluster | Cluster (nodes table) | yes | |
| DELETE /api/v1/cluster/nodes/{node_id} | Cluster | Cluster (nodes table) | yes | |
| POST /api/v1/cluster/join | Cluster | — | no | Internal cluster protocol; not admin-facing |
| POST /api/v1/node/join-cluster | Cluster | Cluster (join existing) | yes | |
| POST /api/v1/cluster/mtls-bootstrap | Cluster | — | no | Pre-Raft internal bootstrap; not admin-facing |
| POST /api/v1/cluster/_internal/aggregates | Cluster | — | no | Follower→leader internal channel; not admin-facing |
| POST /api/v1/upgrade/node-start | Upgrade | — | no | Internal rolling upgrade protocol; not admin-facing |
| GET /api/v1/config/export | Config | Settings (export) | yes | |
| POST /api/v1/config/import | Config | Settings (import) | yes | |
| GET /metrics | Metrics | — | no | Prometheus scrape endpoint; operator/monitoring |
| GET /api/docs | API Docs | Shell (sidebar) | yes | Swagger UI |

---

## Missing UI — Prioritised TODO

| Priority | Endpoint(s) | Suggested UI location |
|---|---|---|
| High | GET/POST /api/v1/webhooks, DELETE /api/v1/webhooks/{id}, POST /api/v1/webhooks/{id}/test | New "Webhooks" settings panel |
| Medium | GET /api/v1/clients/{ip}, GET /api/v1/clients/{ip}/doh-status | Per-client detail drawer/modal in Clients view |
| Low | GET /api/v1/tokens, POST, DELETE, PATCH | API token manager in Account/Security settings |
