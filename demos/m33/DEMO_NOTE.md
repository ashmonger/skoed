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
