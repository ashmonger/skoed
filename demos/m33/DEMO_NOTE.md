# M33 — Block Page Enhancements

## Implemented

### Per-Profile Block Page Content
- `PATCH /api/v1/profiles/{id}` — accepts `block_page` override: `title`, `message`, `contact_email`, `logo_url`, `bypass_passcode`
- Block page server: `?client_ip=` query param triggers profile lookup; per-profile content overrides global defaults when set
- Falls back to global block page title/message when no profile-level override is configured

### IPv6 Redirect (AAAA)
- `PUT /api/v1/blockpage` — added `redirect_address_v6` field
- DNS handler: when `redirect_address_v6` is set and block policy is `redirect`, AAAA queries for blocked domains return NOERROR + AAAA answer with the configured v6 address
- When `redirect_address_v6` is not set, AAAA queries return NXDOMAIN (`FS-BlockPageIPv6NotConfigured`)

### Time-Bounded Bypass
- `POST /api/v1/bypass` — verifies `bypass_passcode` from the profile matching the client IP; on success applies a time-bounded profile pause for `duration_minutes`
- Returns `{profile_id, expires_at}` on success; 403 on wrong passcode; 404 when no passcode configured for the profile
- Implementation note: uses `SetProfilePause` (whole-profile pause) rather than a per-client allowlist entry; simpler and reuses existing M13 infrastructure

### Custom HTML Template
- `PUT /api/v1/blockpage/template` — upload HTML template (`text/html` body); stored in memory
- `DELETE /api/v1/blockpage/template` — reverts to built-in default template
- Template variables: `{{ .Domain }}`, `{{ .Profile }}`, `{{ .ClientIP }}`, `{{ .Joke }}`

## Not Implemented / Deferred

- Rich media block pages (video, iframe) — plain HTML only
- Per-domain block page granularity (per-profile is the granularity)
- Bypass button rate-limiting (passcode controls access)
- HTTPS / ACME for the block page redirect server
- Per-client bypass (allowlist entry) — current bypass pauses the whole profile

## Validation

### Web UI
Settings page block page section:
- `redirect_address_v6` field: sets IPv6 address returned for blocked AAAA queries; empty → NXDOMAIN

Profiles page — new "Block page" tab per profile:
- Title, message, contact email overrides (fallback to global when empty)
- Bypass passcode: end-users enter this on the block page to pause filtering for a set duration

### Acceptance Tests (Proxmox host, go test direct)
19/19 pass:
- `TestBlockPagePerProfileContent` ✓
- `TestBlockPageGlobalFallback` ✓
- `TestBlockPageProfileContactEmail` ✓
- `TestBlockPageIPv6Redirect` ✓
- `TestBlockPageIPv6NotConfigured` ✓ (NXDOMAIN, not SERVFAIL)
- `TestBlockPageBypassGranted` ✓
- `TestBlockPageBypassWrongPasscode` ✓
- `TestBlockPageBypassExpiry` ✓
- `TestBlockPageBypassProfileRequired` ✓
- `TestBlockPageCustomTemplate` ✓
- `TestBlockPageCustomTemplateVariables` ✓
- `TestBlockPageCustomTemplateDelete` ✓
- `TestBlockPageRedirectReturnsIP` ✓
- `TestBlockPageRedirectServfailAAAA` ✓ (now NXDOMAIN)
- `TestBlockPageNonRedirectUnaffected` ✓
- `TestBlockPageHttpServerResponds` ✓
- `TestBlockPageConfigGet` ✓
- `TestBlockPageConfigPersisted` ✓
- `TestBlockPageTitleInResponse` ✓

### Enterprise Validation (Proxmox proxtest2, 2026-06-26)

**19/19 PASS · 3.5s · Proxmox proxtest2 (16 CPUs, 62 GiB RAM) · [Full report](enterprise/test-report.html)**

3-node Raft cluster: CT200 (skoed-1 leader), CT201 (skoed-2 follower), CT202 (skoed-3 follower) — Alpine Linux, all `in_sync` at commit_index=29. M33 binary built from rebased `feature/m33-block-page-enhancements` branch and deployed to cluster before test run. Tests run via `go test` directly on the Proxmox host against isolated per-test skoed instances (same harness as CI).
