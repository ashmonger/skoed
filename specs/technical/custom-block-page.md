# Custom Block Page — Technical Specification

x-tsid: TS-CustomBlockPage
x-fsid-links: [FS-BlockPageRedirectReturnsIP, FS-BlockPageRedirectServfailAAAA, FS-BlockPageNonRedirectUnaffected, FS-BlockPageHttpServerResponds, FS-BlockPageConfigGet, FS-BlockPageConfigPatch, FS-BlockPageTitleInResponse]

## Overview

When `filtering.block_policy` is `"redirect"`, the DNS server returns the configured block page IP for
blocked A queries and SERVFAIL for blocked AAAA queries. An embedded HTTP server on a configurable port
serves a self-contained HTML block page for every inbound GET request.

---

## Config changes

### `FilteringConfig` (existing struct, extended)

```yaml
filtering:
  block_policy: "redirect"   # new value; existing: nxdomain | null | nodata
  block_page:
    ip: "203.0.113.1"        # IPv4 address returned for blocked A queries; default: node's API host IP
    port: 8053               # TCP port the block page HTTP server listens on; default: 8053
    title: "Access Blocked"  # optional; default: "Access Blocked"
    message: "..."           # optional; default: "This website has been blocked by your network administrator."
    contact_email: ""        # optional; shown in the block page when non-empty
```

### `BlockPageConfig` struct

```go
type BlockPageConfig struct {
    IP           string `yaml:"ip,omitempty"            json:"ip,omitempty"`
    Port         int    `yaml:"port,omitempty"          json:"port,omitempty"`
    Title        string `yaml:"title,omitempty"         json:"title,omitempty"`
    Message      string `yaml:"message,omitempty"       json:"message,omitempty"`
    ContactEmail string `yaml:"contact_email,omitempty" json:"contact_email,omitempty"`
}
```

Added to `FilteringConfig`:
```go
BlockPage BlockPageConfig `yaml:"block_page,omitempty" json:"block_page,omitempty"`
```

---

## DNS behaviour

### A query for blocked domain (redirect policy)

- Returns RCODE NOERROR with an A record pointing to `block_page.ip`.
- TTL: 0 (prevents caching so the redirect stays hot).

### AAAA query for blocked domain (redirect policy)

- Returns RCODE SERVFAIL.
- Rationale: the block page HTTP server is IPv4-only; returning `::` would give browsers a useless zero address.

### Other query types (redirect policy)

- Returns RCODE NXDOMAIN (same as the existing default behaviour).

### Non-redirect policies

- `nxdomain`, `null`, `nodata`: unaffected; no block page server started.

---

## Block page HTTP server

- Listens on `0.0.0.0:<block_page.port>` (TCP, plain HTTP).
- Responds to any path with `200 OK`, `Content-Type: text/html; charset=utf-8`.
- Body is a self-contained HTML page (no external CSS/JS/fonts).
- Template variables rendered from `BlockPageConfig`: `Title`, `Message`, `ContactEmail`.
- Server starts when the node is ready and `block_policy == "redirect"`.
- Server stops when block_policy changes away from "redirect" or on node shutdown.

---

## API endpoints

### GET /api/v1/blockpage

Returns the current block page configuration.

**Authentication:** Bearer token required.

**Response 200:**
```json
{
  "ip": "203.0.113.1",
  "port": 8053,
  "title": "Access Blocked",
  "message": "This website has been blocked by your network administrator.",
  "contact_email": "admin@example.com"
}
```

### PATCH /api/v1/blockpage

Updates the block page configuration. Raft-replicated.

**Authentication:** Bearer token required (write scope).

**Request body (all fields optional):**
```json
{
  "ip": "203.0.113.1",
  "port": 8053,
  "title": "Access Denied",
  "message": "Contact your administrator.",
  "contact_email": "admin@example.com"
}
```

**Validation:**
- `ip`: must be a valid IPv4 address when present and non-empty.
- `port`: must be 1–65535 when present and non-zero.

**Response 200:** same shape as GET /api/v1/blockpage.

**Response 400:** validation error with `{"error": "..."}`.

---

## Lifecycle

1. `main.go` constructs a `blockpage.Server` after the cluster is up.
2. After every Raft apply (`onApply`), the App checks whether block_policy changed:
   - If policy is now `"redirect"` and the server is not running → start it.
   - If policy changed away from `"redirect"` and the server is running → stop it.
3. On `PATCH /api/v1/blockpage`, the App restarts the block page server if it is running so the new config takes effect immediately.
4. On node shutdown, the block page server is stopped.
