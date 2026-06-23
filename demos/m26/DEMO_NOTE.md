# M26 — Custom Block Page: Demo Note

## Implemented scope

### DNS behaviour

- New `filtering.block_policy` value: `"redirect"`.
- When a blocked domain receives an **A query** under redirect policy, DNS returns `NOERROR` with an A record pointing to the configured `block_page.ip` (TTL 0).
- When a blocked domain receives an **AAAA query** under redirect policy, DNS returns `SERVFAIL` (the block page HTTP server is IPv4-only; returning `::` would silently fail in browsers).
- Non-redirect policies (`nxdomain`, `null`, `nodata`) are completely unaffected.

### Block page HTTP server

- New embedded HTTP server (`internal/blockpage`) starts automatically when `block_policy="redirect"`.
- Listens on `0.0.0.0:<block_page.port>` (default 8053).
- Responds to any path with `200 OK`, `Content-Type: text/html`.
- Self-contained HTML (no external CSS/JS/fonts): dark theme, centered card, icon, title, message, optional contact email link.
- Content updates without server restart after `PATCH /api/v1/blockpage`.

### API

- `GET /api/v1/blockpage` — returns current block page config (auth required).
- `PATCH /api/v1/blockpage` — updates config, Raft-replicated (write scope required).
  - Validation: `ip` must be valid IPv4 if present; `port` must be 1–65535 if present.
- Block page config persisted in bbolt `settings/filtering` alongside `block_policy`.

### Web UI

- Settings page "Filtering" section now includes a **Redirect** radio button.
- New **Block Page** section in Settings with fields: IP, Port, Title, Message, Contact email.
- Save button calls `PATCH /api/v1/blockpage`; reads current config on load via `GET /api/v1/blockpage`.

## Not implemented (non-goals)

- HTTPS for the block page HTTP server.
- Per-domain or per-profile block page content.
- IPv6 redirect (AAAA returns SERVFAIL).
- Bypass / allow workflow from the block page.
- Custom HTML templates; only title, message, contact_email are configurable.

## Test results

All 7 M26 acceptance tests pass:

| Test | Status |
|------|--------|
| TestBlockPageRedirectReturnsIP | PASS |
| TestBlockPageRedirectServfailAAAA | PASS |
| TestBlockPageNonRedirectUnaffected | PASS |
| TestBlockPageHttpServerResponds | PASS |
| TestBlockPageConfigGet | PASS |
| TestBlockPageConfigPersisted | PASS |
| TestBlockPageTitleInResponse | PASS |

Full suite of filtering, config, profile, schedule, category, and webhook tests also green.
