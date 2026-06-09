# M5.9.7 — "Would this domain be blocked?" tester

## Scope (implemented)

- **Backend** (`apps/skoed/internal/api/handlers/test_domain.go`):
  - `POST /api/v1/_public/test-domain` — guest endpoint, default profile only, stripped response (`{would_block, reason}`).
  - `POST /api/v1/test-domain` — authenticated endpoint, full chain (matched profile, matched blocklist, block policy, local-DNS / SafeSearch overrides, `client_ip` + `profile_id` overrides).
  - Both endpoints call the **same** `filter.Engine.EvaluateForClientID` the DNS handler uses — verdicts cannot drift from real-query behaviour.
  - Refuses literal IPs, `.invalid`, `.local`, `.localhost`, `.example`, `.onion`, `.test`, `.alt`.
  - Guest endpoint shares the per-IP token bucket with the M5.9.5 URL tester — combined 60/h budget across all public test endpoints.
  - Guest endpoint gated by `node.api.public_landing.enabled`.

- **CLI** (`apps/skoed/internal/cli/cmd_domain.go`):
  - `skoed domain test <domain> [--client IP] [--profile ID] [--public]`.
  - Smart routing: uses auth endpoint when credentials are present, falls back to the public endpoint otherwise.
  - Lipgloss-styled verdict (✓/✗) + reason chip (color-coded per reason).
  - Exit 0 regardless of verdict (verdict is data, not an error).

- **SPA** (Vue 3):
  - **Landing page** (`/`): a second card beneath the M5.9.5 URL tester. Single input + Test button → ✓/✗ chip and reason. `data-testid="domain-tester-card"`.
  - **`/dashboard/tools/test-domain`** (auth): full admin form with domain + client-IP + profile-override dropdown. Renders the rationale as a step list (Local DNS → Allowlist → Blocklist → SafeSearch → Forwarded), plus an attribution table (matched profile, matched blocklist, block policy, evaluated_at).
  - Sidebar adds a **Test a domain** entry between Cluster and Settings.

- **Metrics** (`apps/skoed/internal/metrics/metrics.go`):
  - `skoed_test_domain_requests_total{surface,verdict}` — 4 series total (`auth|guest` × `allow|block`). Domain is **never** a label (cardinality safe).

- **Audit**: the auth route is read-only and exempt from the audit middleware — operators can probe freely without flooding `bbolt` or the Audit page.

## Verification

- 11/11 acceptance tests pass in Docker (`./tests/acceptance/run-in-docker.sh -run TestTestDomain`):
  - `TestTestDomainGuestBlocked`, `GuestAllowed`, `GuestRefusesIP`, `GuestRateLimited`
  - `TestTestDomainAuthRequiresAuth`, `AuthFullChain`, `AuthLocalDnsPriority`, `AuthAllowlistOverrides`
  - `TestTestDomainMatchesRealQuery` — confirms the verdict matches what a real DNS query produces.
  - `TestTestDomainCli`, `TestTestDomainMetricsCounter`.
- SPA built clean (`web && npm run build`).

## Not implemented (deferred)

- "Test against historical config" (only current cluster state is evaluable).
- Bulk-test (upload a list of domains, get a CSV back) — not requested; would need its own cardinality story.
- "Why this verdict" link-out to a specific blocklist rule line — engine returns the blocklist ID; resolving to the source line is a separate feature.

## Limitations

- The forwarded-upstream verdict only tells the operator that the cluster would forward — it does not synthesize what the upstream would answer.
- SafeSearch is informational ("would rewrite") — it is not classified as a block.
- The guest endpoint always uses the default profile; client-IP and profile-override are silently ignored on that surface.
- `DBLOCK_PUBLIC_TESTER_ALLOW_PRIVATE=1` (test-only escape hatch from M5.9.5) MUST NOT be set in production — the env-var name kept for backward compatibility despite the rename.

## Files touched

```
specs/functional/domain-tester.feature                  (new)
specs/technical/domain-tester.md                        (new)
tests/acceptance/test_domain_test.go                    (new)
apps/skoed/internal/api/handlers/test_domain.go         (new)
apps/skoed/internal/api/handlers/handler.go             (+ObserveTestDomain on AppState)
apps/skoed/internal/api/app.go                          (+route wiring + publicTestDomainHandler)
apps/skoed/internal/api/audit_middleware.go             (+test-domain exemption)
apps/skoed/internal/metrics/metrics.go                  (+testDomainTotal CounterVec)
apps/skoed/internal/cli/cmd_domain.go                   (new)
apps/skoed/internal/cli/root.go                         (+newDomainCmd registration)
web/src/views/Landing.vue                               (+domain tester card)
web/src/views/TestDomain.vue                            (new admin view)
web/src/router.ts                                       (+/dashboard/tools/test-domain)
web/src/layouts/Shell.vue                               (+sidebar entry + page title)
web/src/api/endpoints.ts                                (+testDomain wrapper + types)
ROADMAP.md                                              (M5.9.7 → shipped)
```
