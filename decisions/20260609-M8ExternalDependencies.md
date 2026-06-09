# Decision: M8 External Dependencies — quic-go and ameshkov/dnscrypt

**Date:** 2026-06-09
**Milestone:** M8 — Encrypted DNS Expansion

## Context

M8 adds DoH3 (HTTP/3 over QUIC) and DNSCrypt v2 to skoed. Both protocols
require functionality that is not in the Go standard library:

- QUIC and HTTP/3 have no stdlib implementation.
- DNSCrypt v2 uses X25519 key exchange and XSalsa20-Poly1305 encryption with
  a custom certificate format; implementing from scratch is high-risk.

Per AGENTS.md Rule 11: non-standard library use requires explicit UoR approval.

## Problem

Which libraries to use for QUIC/HTTP3 and DNSCrypt v2?

## Options considered

### QUIC / HTTP3

| Option | Notes |
|--------|-------|
| `github.com/quic-go/quic-go` | De-facto Go QUIC implementation; MIT; used by Caddy, Traefik, Hugo, AdGuard. Active. |
| Implement from scratch | Months of work; QUIC is a complex RFC. Not viable. |
| Skip DoH3 | Contradicts M8 scope. |

### DNSCrypt v2

| Option | Notes |
|--------|-------|
| `github.com/ameshkov/dnscrypt/v2` | MIT; used by AdGuard Home (the reference self-hosted DNS filter). Provides both client and server. Active. |
| `github.com/folbricht/routedns` | BSD; more general routing framework, heavier. |
| Implement from scratch | DNSCrypt v2 spec is ~2000 lines of crypto + protocol; high risk. |

## Decision

**Approved by UoR 2026-06-09** ("go M8" after explicit dependency disclosure):

- `github.com/quic-go/quic-go` v0.48+ for QUIC transport and HTTP/3
- `github.com/ameshkov/dnscrypt/v2` v2.3+ for DNSCrypt v2 server

## Consequences

- Two new entries in `apps/skoed/go.mod`.
- Binary size increase: ~2–3 MB (quic-go is the larger one).
- Both are pure Go — CGO_ENABLED=0 static build is unaffected.

## Related hypotheses

None.

## Affected features

FS-Doh3ServerListens, FS-DnscryptServerListens, and all M8 FSIDs.
