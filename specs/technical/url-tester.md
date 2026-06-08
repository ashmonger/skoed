---
x-tsid: TS-UrlTester
x-fsid-links:
  - FS-UrlTesterCliSubcommand
  - FS-UrlTesterPublicLandingShown
  - FS-UrlTesterLoginButtonLeadsToAdminUi
  - FS-UrlTesterPublicEndpointReturnsCountAndFormat
  - FS-UrlTesterRefusesPrivateAddress
  - FS-UrlTesterRateLimited
  - FS-UrlTesterOperatorCanDisable
---

# TS-UrlTester — URL tester (CLI + public landing page)

## Two surfaces, one parser

The CLI subcommand and the public HTTP endpoint share the same fetch +
parse path (`internal/filter.Download`). Format detection, parser
selection (`hosts`/`adblock`/`domainlist`), and the 30 s fetch timeout
are identical — operators see the same answer either way.

| Surface             | Trust          | Auth | SSRF risk        | Rate limit          |
|---------------------|----------------|------|------------------|---------------------|
| `dblock blocklist test` (CLI) | operator's process | none | none (their network) | none |
| `POST /api/v1/_public/test-blocklist` (HTTP) | unauthenticated visitor | none | resolved-IP guard | per-source-IP token bucket |

## CLI (M5.9.1 — already shipped)

Implemented at `apps/dblock/internal/cli/cmd_blocklist.go`. The
M5.9.5 work only verifies it still passes its FSID and references
the existing implementation. No code change there.

Invocation:

```
$ dblock blocklist test https://github.com/StevenBlack/hosts/raw/master/hosts
✓ https://github.com/.../hosts
  format    auto (auto-detected)
  domains   162,481
  elapsed   2.3s
```

Exit codes: `0` ok, `1` HTTP / parse failure, `2` bad invocation
(missing scheme).

## HTTP endpoint

`POST /api/v1/_public/test-blocklist`

Request:

```json
{
  "url": "https://example.com/hosts.txt",
  "format": "hosts" | "domainlist" | "adblock" | "auto"
}
```

`format` defaults to `auto`.

Response shape:

| Code | Body                                                                                         | When |
|------|----------------------------------------------------------------------------------------------|------|
| 200  | `{"ok":true,  "count":N,  "format":"...", "elapsed_ms":millis}`                              | parsed |
| 400  | `{"ok":false, "error":"invalid JSON body"}`                                                  | malformed body |
| 400  | `{"ok":false, "error":"url is required"}`                                                    | empty `url` |
| 400  | `{"ok":false, "error":"URL must use http or https scheme"}`                                  | non-http(s) scheme |
| 403  | `{"ok":false, "error":"refusing private/loopback/link-local address X (resolved from H)"}`   | SSRF guard |
| 404  | (chi 404)                                                                                    | `public_landing.enabled=false` on this node |
| 429  | `{"ok":false, "error":"rate limit exceeded — try again in a minute"}`                        | bucket empty |
| 502  | `{"ok":false, "error":"fetch ...", "elapsed_ms":millis}`                                     | upstream non-200 or unreachable |

## SSRF guard

The host is parsed from the URL, then:

1. If the host is a literal IP, the IP is checked directly.
2. Else `net.LookupIP` resolves the hostname. **Every** answer is
   checked; a single match below triggers refusal.

Refused address families (any match → 403):

| Family                                  | Examples                          | Reason                          |
|-----------------------------------------|-----------------------------------|---------------------------------|
| Loopback                                | `127.0.0.0/8`, `::1`              | local services                  |
| RFC1918                                 | `10/8`, `172.16/12`, `192.168/16` | internal LAN                    |
| Link-local                              | `169.254.0.0/16`, `fe80::/10`     | metadata services, link-local   |
| Unique-local IPv6                       | `fc00::/7`                        | IPv6 LAN equivalent of RFC1918  |
| Multicast / unspecified                 | `224.0.0.0/4`, `0.0.0.0`, `::`    | non-routable                    |

Implementation: `internal/api/handlers/public.go::isUnsafeIP` —
uses `net.IP.IsLoopback() / IsPrivate() / IsLinkLocalUnicast() /
IsLinkLocalMulticast() / IsUnspecified() / IsMulticast()` plus an
explicit `fc00::/7` prefix check for older toolchains.

## Rate limit

Per source IP, hand-rolled token bucket (no `golang.org/x/time/rate`
to avoid the new module dependency for what amounts to ~40 lines):

| Knob       | Value     | Meaning                                |
|------------|-----------|----------------------------------------|
| `burst`    | 5         | quick double-clicks don't trip 429     |
| `interval` | 1 minute  | tokens refill at 1 per minute (≈60/h)  |
| `window`   | 1 hour    | bucket GC — entries older than 1 h dropped |

Source IP resolution:

1. `X-Forwarded-For` first comma-separated entry (if present)
2. `r.RemoteAddr` host portion

State is in-memory only. Buckets are pruned lazily on each call;
no goroutine, no persistence — process restart resets every limit.

## Operator opt-out

`node.api.public_landing.enabled` in `node.yaml`. Type: `*bool`
(tri-state: omitted → true, `false` → off, `true` → on).

When `false`:

- `POST /api/v1/_public/test-blocklist` returns chi's standard 404.
- The SPA static fallback's `GET /` returns `302 → /login`. This
  preserves the **pre-M5.9.5 posture** (legacy admin-only) without
  needing to re-ship the SPA.

Other admin paths are unaffected — `/api/v1/health` stays open,
`/metrics` is gated separately by `node.api.metrics.require_auth`.

## Web UI — Landing.vue

`web/src/views/Landing.vue` renders without the admin Shell layout
(no sidebar, no logged-in chrome). Layout:

```
┌── header ──────────────────────────────────────┐
│  [logo] dblock                       [Login]   │
├── hero ────────────────────────────────────────┤
│  Sanity-check any blocklist before install     │
│  [URL input          ] [format ▼] [Test]       │
│  [result strip when present]                   │
├── 3-up tagline grid ───────────────────────────┤
│  DNS filtering · Multi-node sync · Profiles    │
└── footer (docs / github links) ────────────────┘
```

Vue Router:

- `/`          → `Landing.vue` (public, M5.9.5)
- `/login`    → existing `Login.vue`
- `/setup`    → existing `Setup.vue`
- `/dashboard` + children → existing admin shell (was at `/`)

The `requiresAuth` route-meta guard is unchanged: hitting `/dashboard`
without auth still redirects to `/login`. Authenticated visitors who
land on `/` get bounced to `/dashboard` so admins aren't shown the
marketing page after login.

## Layout

```
apps/dblock/internal/api/
  handlers/
    public.go          (NEW — POST /api/v1/_public/test-blocklist)
  app.go               (wires the endpoint + SPA landing gate)
apps/dblock/internal/cluster/
  node.go              (NodeYAML APISection.PublicLanding *bool)
apps/dblock/cmd/dblock/
  main.go              (SetPublicLandingEnabled call)
web/src/
  views/Landing.vue    (NEW — public landing UI)
  router.ts            (root → Landing.vue, admin → /dashboard)
```

## Acceptance tests

`tests/acceptance/url_tester_test.go`:

- `TestPublicTestBlocklistOK` — POST to an httptest hosts server; assert 200 + count.
- `TestPublicTestBlocklistRefusesPrivateIP` — POST a URL pointing at 127.0.0.1; assert 403.
- `TestPublicTestBlocklistRateLimit` — burst-issue 10 POSTs; assert at least one 429.
- `TestPublicLandingDisabledReturnsLogin` — config with `public_landing.enabled=false`; assert GET `/` redirects to `/login` AND POST returns 404.

FS-UrlTesterCliSubcommand is covered by the M5.9.1
`TestCliBlocklistTest` test — no duplication.

FS-UrlTesterPublicLandingShown + FS-UrlTesterLoginButtonLeadsToAdminUi
are verified visually via the M5.9.5 screenshot (Playwright pulls /,
asserts the form renders, the Login button targets /login).
