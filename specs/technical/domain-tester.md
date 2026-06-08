---
x-tsid: TS-DomainTester
x-fsid-links:
  - FS-TestDomainGuestVerdictBlocked
  - FS-TestDomainGuestVerdictAllowed
  - FS-TestDomainGuestRefusesInvalidInput
  - FS-TestDomainGuestRateLimited
  - FS-TestDomainGuestDisabledWithPublicLanding
  - FS-TestDomainAuthRequiresAuth
  - FS-TestDomainAuthReturnsFullChain
  - FS-TestDomainAuthLocalDnsTakesPriority
  - FS-TestDomainAuthAllowlistOverridesBlocklist
  - FS-TestDomainAuthFiresOnSameEvaluatorAsRealQueries
  - FS-TestDomainCliVerb
  - FS-TestDomainMetricsCounter
---

# TS-DomainTester — verdict + rationale endpoints

## Two surfaces, one evaluator

| Path                            | Auth | Body                                              | Returns                                          |
|---------------------------------|------|---------------------------------------------------|--------------------------------------------------|
| `POST /api/v1/_public/test-domain` | none | `{domain}`                                       | `{would_block, reason}` (default profile only)   |
| `POST /api/v1/test-domain`         | yes  | `{domain, client_ip?, profile_id?}`              | full chain (see below)                            |

Both delegate to **the same** evaluator path the DNS handler uses
(`filter.Engine.EvaluateForClientID`), so the test verdict can never
drift from a real query's outcome. That's the property the spec asks
for in FS-TestDomainAuthFiresOnSameEvaluatorAsRealQueries.

## Domain validation (shared)

Refuses inputs that would either crash the parser or be meaningless:

- empty / > 253 chars
- IP literals (v4 or v6) — would never reach the filter engine via a
  real DNS query and just confuses the operator
- reserved suffixes the operator can't meaningfully test: `.invalid`,
  `.local`, `.localhost`, `.example`, `.onion`, `.test`,
  `.alt`, plus single-label hostnames without a dot

Returns 400 with `{"error":"..."}` for any of the above.

## Authenticated response shape

```json
{
  "domain":             "doubleclick.net",
  "client_ip":          "10.42.10.50",
  "would_block":        true,
  "reason":             "blocklist",
  "matched_profile_id": "kids",
  "matched_blocklist_id": "hagezi-pro",
  "block_policy":       "nxdomain",
  "local_dns_answer":   null,
  "safesearch_rewrite": null,
  "evaluated_at":       "2026-06-08T17:42:11Z"
}
```

`reason` is one of:

| value          | what it means                                                       |
|----------------|---------------------------------------------------------------------|
| `"blocklist"`  | matched an active blocklist for the resolved profile                |
| `"allowlist"`  | matched the allowlist — overrides any blocklist hit                 |
| `"local-dns"`  | a local DNS entry exists; the query would be answered locally       |
| `"safesearch"` | SafeSearch would CNAME-rewrite the query (would_block remains false)|
| `"forwarded"`  | no rule matched; the query would forward upstream                   |

When `client_ip` is omitted: the engine treats the request as coming from
"no specific client" → falls through to the default profile. When
`profile_id` is supplied: overrides client-IP-based profile resolution
(useful for "what would the Kids profile see for this domain?").

## Guest response shape

```json
{
  "would_block": true,
  "reason": "blocklist"
}
```

No internals exposed (no blocklist id, no profile, no block policy). The
endpoint is for "does my router actually point at skoed?" / "is this
ad-tracker reachable from here?" — not for fingerprinting the
configuration.

## SSRF / probe-prevention notes

Unlike M5.9.5's URL tester, this endpoint does NOT fetch anything from
the network. The domain is only used as a string key into the in-memory
filter engine. There's no SSRF surface.

The remaining abuse vector is *cardinality* — an attacker scanning
random domains to inflate the bbolt audit log or skew metrics.
Mitigation:

- The test endpoints are NOT audited (read-only, no state change). The
  M5.2 audit middleware's GET/POST exemption logic gets a one-liner
  add for `POST /api/v1/_public/test-domain` and `POST /api/v1/test-domain`.
- Metrics carry only the verdict label (`block` / `allow`), not the
  domain — cardinality stays at 2.
- Guest endpoint reuses the M5.9.5 per-IP token bucket (60/h burst 5).

## Rate limit + opt-out

Guest endpoint is gated by the same `node.api.public_landing.enabled`
flag as the M5.9.5 blocklist tester. Off → 404. The auth endpoint is
unaffected (always available).

The rate-limit token bucket is the SAME instance as the M5.9.5
blocklist-tester limiter, so a hostile caller can't quietly burn
through both endpoints at 120/h combined — they're capped at 60/h
*total* across all public test endpoints.

## Metrics

```
skoed_test_domain_requests_total{surface="guest|auth", verdict="block|allow"}
```

Single CounterVec, four series total. Bumped after each verdict is
computed (counted regardless of whether the request was admitted —
rate-limited 429s do NOT increment because no verdict was produced).

## CLI

```
skoed domain test <domain> [flags]
  --client IP         test against a specific client IP (auth endpoint)
  --profile ID        override profile resolution (auth endpoint)
  --public            force the guest endpoint even if credentials present
```

Smart routing: when no credentials are configured (no `~/.skoed/credentials`,
no `SKOED_AUTH`), falls back to the guest endpoint automatically; otherwise
prefers the auth endpoint for the richer rationale.

## Web UI

- **Landing page** (`/`): a second card under the M5.9.5 blocklist URL
  tester, same visual treatment. Input + button → ✓/✗ chip + reason.
- **`/dashboard/tools/test-domain`** (auth): full form with client-IP +
  profile-override pickers; renders the rationale as a vertical step
  list (Did local-DNS match? → Did allowlist match? → Did blocklist
  match? → Forwarded). Each step has a ✓/✗/— chip.

Sidebar nav addition: a "Tools" group with this entry. M5.9.7.1 can
later add other tools under the same group (e.g. a "Why is this query
slow?" trace tool).

## Implementation map

```
apps/skoed/internal/api/handlers/
  test_domain.go        (new: TestDomainGuest + TestDomainAuth)
apps/skoed/internal/api/
  public.go             (extend: share the rate-limit token bucket)
  audit_middleware.go   (extend: exempt test-domain routes from audit)
apps/skoed/internal/cli/
  cmd_domain.go         (new: `skoed domain test`)
apps/skoed/internal/metrics/
  metrics.go            (extend: ObserveTestDomain + register counter)
web/src/views/
  Landing.vue           (new card alongside URL tester)
  TestDomain.vue        (new: auth-side tool page)
web/src/router.ts       (add /dashboard/tools/test-domain)
web/src/layouts/Shell.vue (sidebar Tools group)
tests/acceptance/
  test_domain_test.go   (all FSIDs)
```
