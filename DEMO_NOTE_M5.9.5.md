# DEMO NOTE — M5.9.5 URL tester (CLI + public landing page)

## Scope

dblock gets a single unauthenticated landing surface at `/`: a tiny
public-facing URL tester so operators can answer "does this blocklist
URL parse?" **before** installing dblock or wiring up admin auth. The
existing CLI subcommand (`dblock blocklist test <url>`, shipped in
M5.9.1) is now the local-process counterpart of the same parser. Apart
from the landing page, dblock remains a private-network admin tool —
no other unauthenticated paths are exposed.

### Implemented

- **CLI surface (already shipped in M5.9.1, re-verified)**
  `dblock blocklist test <url> [--format hosts|domainlist|adblock|auto]`
  fetches in-process with a 30 s timeout, parses via
  `internal/filter.Download`, and prints a styled summary. No daemon,
  no auth, no SSRF risk.

- **Public landing page at `/`**
  New `web/src/views/Landing.vue` rendered without the admin Shell:
  header (logo + name + top-right **Login** button), hero, URL-tester
  card (input + format select + result strip), 3-up tagline grid,
  footer with docs / GitHub links. Authenticated visitors are
  auto-routed past it to `/dashboard`.

- **Public endpoint `POST /api/v1/_public/test-blocklist`**
  Unauthenticated by design. Body `{url, format?}`; response
  `{ok, count, format, elapsed_ms, error?}`.

- **SSRF guard**
  The URL's host is parsed; literal IPs are checked directly. Hostnames
  are resolved via `net.LookupIP` and **every** answer is screened
  against:
  loopback (`127.0.0.0/8`, `::1`),
  RFC1918 (`10/8`, `172.16/12`, `192.168/16`),
  link-local (`169.254.0.0/16` — catches the EC2/GCE/Azure metadata
  service — and `fe80::/10`),
  unique-local IPv6 (`fc00::/7`),
  multicast and unspecified.
  Any single match → HTTP 403, no fetch attempted. An attacker passing
  `http://internal.example.com/` where the name resolves to 10.0.0.5
  is refused.

- **Per-source-IP rate limit**
  Hand-rolled token bucket (no `golang.org/x/time/rate` dep). Budget:
  burst 5, refill 1 per minute → effective 60 req/h per IP. Source IP
  is the first `X-Forwarded-For` entry when present, else `r.RemoteAddr`.
  State is in-memory and lazily garbage-collected (entries older than
  1 hour are dropped on the next request).

- **Operator opt-out**
  `node.api.public_landing.enabled` in `node.yaml`. Default: **on**
  (omitted key → true). When `false`:
  – `POST /api/v1/_public/test-blocklist` → HTTP 404
  – `GET /` → `302 → /login` (preserves the pre-M5.9.5 admin-only posture)

- **Route changes**
  `/` is now the public landing (was: admin Shell + Dashboard).
  The admin shell moved to `/dashboard` with the same children
  (`/dashboard/blocklists`, `/dashboard/profiles`, etc.). The
  `requiresAuth` guard is unchanged so admin paths still kick to
  `/login` when not authenticated. `Login.vue` post-login redirect
  defaults to `/`, which the router then bounces to `/dashboard` for
  authenticated users.

### Acceptance tests

4 Go acceptance tests in `tests/acceptance/url_tester_test.go`:

| FSID                                              | Test                                       | Notes |
|---------------------------------------------------|--------------------------------------------|-------|
| FS-UrlTesterPublicEndpointReturnsCountAndFormat   | TestPublicTestBlocklistOK                  | 1 node + httptest hosts server. Uses `DBLOCK_PUBLIC_TESTER_ALLOW_PRIVATE=1` (test-only env var) so the SSRF guard accepts 127.0.0.1; production builds never set this. |
| FS-UrlTesterRefusesPrivateAddress                 | TestPublicTestBlocklistRefusesPrivateIP    | 5 sub-cases: 127.0.0.1, 10.x, 192.168.x, 169.254.169.254 (cloud meta), [::1]. All return 403. |
| FS-UrlTesterRateLimited                           | TestPublicTestBlocklistRateLimit           | 10-request burst from one source IP; at least one 429 expected. |
| FS-UrlTesterOperatorCanDisable                    | TestPublicLandingDisabledReturnsLogin      | Config with `node.api.public_landing.enabled: false`; asserts POST returns 404 AND GET / returns 302 → /login. |

All 4 PASS — full run in 7.1 s on this box:

```
--- PASS: TestPublicTestBlocklistOK (1.73s)
--- PASS: TestPublicTestBlocklistRefusesPrivateIP (1.59s)
    --- PASS: TestPublicTestBlocklistRefusesPrivateIP/http://127.0.0.1:99/x (0.00s)
    --- PASS: TestPublicTestBlocklistRefusesPrivateIP/http://10.0.0.1/x (0.00s)
    --- PASS: TestPublicTestBlocklistRefusesPrivateIP/http://192.168.1.1/x (0.00s)
    --- PASS: TestPublicTestBlocklistRefusesPrivateIP/http://169.254.169.254/latest/meta-data/ (0.00s)
    --- PASS: TestPublicTestBlocklistRefusesPrivateIP/http://[::1]/x (0.00s)
--- PASS: TestPublicTestBlocklistRateLimit (1.50s)
--- PASS: TestPublicLandingDisabledReturnsLogin (2.09s)
PASS
ok  	dblock/acceptance	7.101s
```

`FS-UrlTesterCliSubcommand` is covered by M5.9.1's existing
`TestCliBlocklistTest` — no duplication here.

`FS-UrlTesterPublicLandingShown` and `FS-UrlTesterLoginButtonLeadsToAdminUi`
are visual: the M5.9.5 screenshot captures the landing page with the
URL-tester card visible and the "Login" button top-right.

### Screenshot

`docs/screenshots/m5.9.5-landing.png` (1280 × 820, 73 kB):
The landing page rendered with the **lipgloss** dark palette, the URL
tester card filled out with a hosts-format URL, and the success result
strip showing `domains 6 · format hosts · elapsed 2 ms`. Three-card
tagline strip below (DNS filtering · Multi-node sync · Profiles &
schedules). Login button top-right.

Re-capture with:

```sh
# 1. Boot dblock with the test bypass + a local hosts file
mkdir -p /tmp/demo && cat > /tmp/demo/config.yaml <<EOF
version: 1
node:
  id: demo-node
  api_address: :18995
  data_dir: /tmp/demo
  dns:
    listen: {port: 18596, ipv4: true}
dns:
  listen: {port: 18596, ipv4: true}
  mode: forwarding
  upstream_timeout_seconds: 3
  cache: {enabled: true, max_entries: 1000}
filtering: {block_policy: nxdomain}
EOF
echo "0.0.0.0 a.test
0.0.0.0 b.test
0.0.0.0 c.test
0.0.0.0 d.test
0.0.0.0 e.test
0.0.0.0 f.test" > /tmp/demo/hosts.txt
python3 -m http.server 19595 --directory /tmp/demo &
DBLOCK_PUBLIC_TESTER_ALLOW_PRIVATE=1 ./apps/dblock/dblock --config /tmp/demo/config.yaml &
# 2. Capture
cd web && node shoot-m5.9.5.mjs
```

### Not implemented (deferred / non-goals)

- **"Try it on dblock.io" hosted demo** — dblock stays private-network
  for v1; no central tester service.
- **Authenticated tester from the public surface** — admins already
  have the Create Blocklist modal, which fetches via the daemon's
  authenticated path with no SSRF check (operators inside their LAN
  may legitimately want to test internal mirrors). This unauthenticated
  variant is strictly for the pre-install evaluation flow.
- **Per-account rate-limit budgets** — only per-source-IP, in-memory.
- **Persistence of test results across requests** — single-shot only.
- **Configurable rate-limit knobs in YAML** — the burst/interval/window
  constants live in `public.go`; flip the file to retune. A future
  M5.9.5.x can wire these into `node.api.public_landing.*` if the
  defaults turn out wrong.

### Files added / changed

```
specs/functional/url-tester.feature                       (NEW — 7 FSIDs)
specs/technical/url-tester.md                             (NEW — TS-UrlTester)
apps/dblock/internal/api/handlers/public.go               (NEW — endpoint + SSRF + rate limit)
apps/dblock/internal/api/app.go                           (wire route + landing gate)
apps/dblock/internal/cluster/node.go                      (APIPublicLandingSection)
apps/dblock/cmd/dblock/main.go                            (SetPublicLandingEnabled call)
web/src/views/Landing.vue                                 (NEW)
web/src/router.ts                                         (root → Landing; admin → /dashboard)
web/shoot-m5.9.5.mjs                                      (NEW)
tests/acceptance/url_tester_test.go                       (NEW — 4 tests)
docs/screenshots/m5.9.5-landing.png                       (NEW)
DEMO_NOTE_M5.9.5.md                                       (this file)
```

## Demo

```sh
# CLI surface — already shipped in M5.9.1, still works.
$ dblock blocklist test https://github.com/StevenBlack/hosts/raw/master/hosts
✓ https://github.com/.../hosts
  format    auto (auto-detected)
  domains   162,481
  elapsed   2.3s

# Public landing — unauthenticated.
$ curl -s -X POST -H "Content-Type: application/json" \
       -d '{"url":"http://127.0.0.1:19595/hosts.txt","format":"hosts"}' \
       http://127.0.0.1:18995/api/v1/_public/test-blocklist
{"count":6,"elapsed_ms":2,"format":"hosts","ok":true}

# SSRF guard — refuses RFC1918.
$ curl -s -X POST -H "Content-Type: application/json" \
       -d '{"url":"http://10.0.0.1/x"}' \
       http://127.0.0.1:18995/api/v1/_public/test-blocklist
{"error":"refusing private/loopback/link-local address 10.0.0.1","ok":false}

# Operator opt-out — node.yaml: api: {public_landing: {enabled: false}}
$ curl -s -o /dev/null -w "%{http_code} %{redirect_url}\n" http://127.0.0.1:18995/
302 http://127.0.0.1:18995/login
```

## Next

Per the M5.9 umbrella, M5.9.5 is the last sub-milestone — the M5
production-hardening track closes here. M6 (closing the DoH gap) is
the next track.
