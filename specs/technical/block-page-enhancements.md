# Block Page Enhancements — Technical Specification

<!-- x-tsid: TS-BlockPageEnhancements -->
<!-- x-fsid-links: [FS-BlockPagePerProfileContent, FS-BlockPageGlobalFallback, FS-BlockPageProfileContactEmail, FS-BlockPageIPv6Redirect, FS-BlockPageIPv6NotConfigured, FS-BlockPageBypassGranted, FS-BlockPageBypassWrongPasscode, FS-BlockPageBypassExpiry, FS-BlockPageBypassProfileRequired, FS-BlockPageCustomTemplate, FS-BlockPageCustomTemplateVariables, FS-BlockPageCustomTemplateDelete] -->

## Overview

M33 adds four capability groups to the existing M26 block page:

1. **Per-profile block page content** — override title, message, contact_email, and logo_url per profile.
2. **IPv6 redirect** — when `redirect_address_v6` is set, AAAA queries for blocked domains return that address instead of SERVFAIL.
3. **Time-bounded bypass** — a profile-scoped passcode lets a client temporarily pause filtering via `POST /api/v1/bypass`.
4. **Custom HTML template** — operators can upload a full HTML template (Go `html/template` syntax) via `PUT /api/v1/blockpage/template`; `DELETE` reverts to the built-in layout.

---

## Endpoint Table

| Method   | Path                            | Auth     | Description                              |
|----------|---------------------------------|----------|------------------------------------------|
| GET      | `/api/v1/blockpage`             | Required | Get block page config (now includes `redirect_address_v6`) |
| PATCH    | `/api/v1/blockpage`             | Required | Update block page config (accepts `redirect_address_v6`)  |
| PUT      | `/api/v1/blockpage/template`    | Required | Upload custom HTML template (raw body)   |
| DELETE   | `/api/v1/blockpage/template`    | Required | Delete custom template → revert to built-in |
| POST     | `/api/v1/bypass`                | Required | Submit bypass passcode for a profile     |
| PATCH    | `/api/v1/profiles/{id}`         | Required | Now accepts `block_page` field           |

---

## Data Models

### BlockPageConfig (updated)

```yaml
ip:                    string   # IPv4 address for A redirect
port:                  int      # HTTP server port
title:                 string   # Global page title
message:               string   # Global page message
contact_email:         string   # Global contact email
redirect_address_v6:   string   # NEW: IPv6 address for AAAA redirect
```

### ProfileBlockPageConfig (new)

Stored as `block_page` nested in a `Profile`. Fields present here override global `BlockPageConfig` values for clients matching that profile.

```yaml
title:            string   # Override page title
message:          string   # Override page message
contact_email:    string   # Override contact email
logo_url:         string   # Override logo URL
bypass_passcode:  string   # Secret for POST /api/v1/bypass
```

### PATCH /api/v1/blockpage — request body

```json
{
  "ip":                   "string|null",
  "port":                 "int|null",
  "title":                "string|null",
  "message":              "string|null",
  "contact_email":        "string|null",
  "redirect_address_v6":  "string|null"
}
```

Validation: `redirect_address_v6` must be a valid IPv6 address (not IPv4) or empty string.

### PATCH /api/v1/profiles/{id} — block_page field

```json
{
  "block_page": {
    "title":            "string",
    "message":          "string",
    "contact_email":    "string",
    "logo_url":         "string",
    "bypass_passcode":  "string"
  }
}
```

### PUT /api/v1/blockpage/template

- Request body: raw HTML string (Go `html/template` syntax)
- Max size: 1 MiB
- Content-Type: any (body is read as raw text)
- Response: `{"status": "ok"}` with HTTP 200

Template variables available:
- `{{.Title}}` — effective title (profile override or global)
- `{{.Message}}` — effective message
- `{{.ContactEmail}}` — effective contact email
- `{{.Domain}}` — domain from `?domain=` query param (empty if not provided)
- `{{.Profile}}` — profile ID matched for the client IP (empty if none)
- `{{.Joke}}` — random DNS joke

### POST /api/v1/bypass — request body

```json
{
  "passcode":         "string",
  "duration_minutes": "int",
  "client_ip":        "string"
}
```

Response (200 OK):

```json
{
  "profile_id": "string",
  "expires_at":  "RFC3339 timestamp"
}
```

Error responses:
- `400` — missing/invalid fields
- `403` — wrong passcode
- `404` — no profile found for client_ip, or profile has no bypass_passcode

---

## DNS Behaviour Changes

### AAAA queries under redirect policy (FS-BlockPageIPv6Redirect / FS-BlockPageIPv6NotConfigured)

| Condition                              | Before M33      | After M33                |
|----------------------------------------|-----------------|--------------------------|
| `redirect_address_v6` not set          | SERVFAIL        | SERVFAIL (unchanged)     |
| `redirect_address_v6` set to `fd00::1` | SERVFAIL        | NOERROR, answer `fd00::1` |

### Block page server per-request behaviour (M33)

The block page HTTP server reads the `client_ip` query parameter from the request URL. When present, it looks up the corresponding profile and merges per-profile overrides onto the global config before rendering.

Query parameters supported:
- `?client_ip=<ip>` — used for per-profile content lookup
- `?domain=<domain>` — forwarded to template as `.Domain`

---

## Storage

### Custom template

Stored in-memory on the `blockpage.Server` instance. Not replicated via Raft (single-node in-memory only for M33). The `PUT` handler calls `app.SetBlockPageTemplate(html)` and `DELETE` calls `app.ClearBlockPageTemplate()`.

### Per-profile bypass

The bypass mechanism uses the existing `SetProfilePause(id, resumesAt, "bypass")` Raft command (already delivered in M13). No new storage is required.

---

## Security Considerations

- The bypass passcode is stored in plaintext in the profile's `block_page.bypass_passcode` field. Operators should treat it as a low-entropy secret (family-grade PIN, not a cryptographic key).
- The custom HTML template is executed server-side as a Go `html/template`. The template engine escapes output by default, but operators who upload templates with `{{.Domain}}` or `{{.Profile}}` in HTML context should be aware that these values come from HTTP query parameters controlled by the client.
- The `client_ip` query parameter is not authenticated — any client can pass any IP to the block page server. This is by design: the block page server is intentionally public and unauthenticated.
