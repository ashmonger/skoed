# Milestone 1 Demo Note

**Date:** 2026-05-29  
**Branch:** skoed-m1  
**Acceptance tests:** 58/58 green

## Setup

Two Docker containers on a shared bridge network (`skoed-demo`):

| Container | Image | Role |
|-----------|-------|------|
| `skoed-demo` | `skoed:demo` (Alpine 3.20, ~12 MB) | skoed server — DNS :53, API :8080 |
| one-shot client | `alpine:3.20` | DNS client — `dig` queries |

Multi-stage Dockerfile: builder uses `golang:1.24-alpine`, final image is `alpine:3.20` with the static binary copied in. No libc dependency.

## What was demonstrated

1. **DNS forwarding** — `example.com A` resolved via upstream, returned two IPs.
2. **Blocklist enforcement** — `ads.example.com` and `doubleclick.net` returned `NXDOMAIN` (0 ms, served from filter engine before hitting upstream).
3. **Subdomain blocking** — `deep.sub.tracker.evil.com` blocked by the `tracker.evil.com` apex entry; no wildcard syntax required.
4. **Query log** — all queries logged with outcome (`forwarded` / `blocked`), visible via `GET /api/v1/query-log`.
5. **Live settings update** — upstream resolvers changed at runtime via `PATCH /api/v1/settings`; DNS rebuilt without container restart.

## Scope implemented in M1

- [x] DNS forwarding (UDP/TCP, dual-stack listener)
- [x] Domain filtering — hosts, domainlist, askoed format parsers
- [x] Subdomain blocking by apex entry
- [x] Per-blocklist and global block policy (NXDOMAIN / NULL / NODATA)
- [x] Allowlist overrides blocklist
- [x] Local DNS entries (A, AAAA, CNAME)
- [x] DNS cache (TTL-based)
- [x] Management REST API (blocklists CRUD, allowlist, local DNS, settings, config import/export, query log, auth)
- [x] Basic auth (setup, login, password change)
- [x] Query log with client/outcome filters and pagination
- [x] Config export (YAML archive) and import (atomic, preserves node-local ports)
- [x] Static binary (CGO_ENABLED=0, 8.9 MB) — runs on Alpine/musl
- [x] Multi-stage Dockerfile

## Not implemented in M1 (deferred)

- Web UI — the API is complete; a browser-based UI is a M1 stretch goal deferred to M2.
- Root DNS recursion — mode=recursive is wired and tested but not exercised in this demo (requires broader network access to reach root hints).
- Multi-node sync — M2.
- Client groups / per-client filtering — M3.

## Known limitations

- Config export/import is YAML-over-HTTP, not tar+gzip (simplified from the roadmap spec; behavior is equivalent for single-node use).
- The demo upstream resolvers are corporate DNS (10.15.25.x); public resolvers (9.9.9.9) are not reachable from this host's Docker network.
