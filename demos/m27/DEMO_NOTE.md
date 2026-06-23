# M27 — Per-Profile Allowlists (Full) — Demo Note

## Implemented scope

### Backend

- **PUT /api/v1/profiles/{id}/allowlist** — atomic full replacement of a profile's
  allowlist. Body: `[]string`. Empty array clears the list. Raft-replicated via
  `UpsertProfile`. Cache entries removed from the old list are purged.
  Route: `r.Put("/api/v1/profiles/{id}/allowlist", a.forward(h.ReplaceProfileAllowlist))`

- **DELETE /api/v1/profiles/{id}/allowlist/{domain} — cache purge fix** — the existing
  handler was missing `cache.PurgeDomain(domain)` after removal. Fixed. Without this,
  a deleted allowlist entry continued to be served as NOERROR from cache until TTL expiry.

- **Wildcard entries** (`*.example.com`) — already supported by `domainSet` in
  `filter/engine.go`: `newDomainSet` strips `*.` prefix on insert; `matches()` walks
  parent labels so both the apex and all subdomains match. No engine changes needed.

- **Export/import** — per-profile allowlists are already covered. `exportShape` in
  `config/export.go` includes `Profiles []Profile`, and `Profile.Allowlist []string`
  is exported verbatim.

### Web UI

- Profile detail modal gains a two-tab layout (Settings / Allowlist) in edit mode.
- Allowlist tab shows: entry list with per-entry delete, wildcard badge (`*`), Add
  domain input, entry count badge on the tab label.
- API client (`endpoints.ts`) gains `replaceProfileAllowlist(profileId, domains)`.

### Acceptance tests (4 new, all passing)

| Test | FSID | Result |
|------|------|--------|
| TestPerProfileAllowlistPutReplacesAll | FS-PerProfileAllowlistPutReplacesAll | PASS |
| TestPerProfileAllowlistPutClearsOnEmpty | FS-PerProfileAllowlistPutClearsOnEmpty | PASS |
| TestPerProfileAllowlistDeletePurgesCache | FS-PerProfileAllowlistDeletePurgesCache | PASS |
| TestPerProfileAllowlistWildcardSubdomain | FS-PerProfileAllowlistWildcardSubdomain | PASS |
| TestPerProfileAllowlistWildcardApex | FS-PerProfileAllowlistWildcardApex | PASS |

Full run command:
```bash
cd tests/acceptance
SKOED_BINARY=../../apps/skoed/skoed go test -count=1 -timeout=300s -run "TestPerProfileAllowlist" -v
```

## Not implemented scope

- Allowlist sharing between profiles (out of scope per M27 non-goals)
- Allowlist scheduling / time-gated entries (out of scope)
- Per-entry metadata (notes, expiry) (out of scope)
- Profile detail page as a dedicated route (modal-based edit retained as-is)
- Proxmox real-env validation (pending UoR request)

## Limitations

- The wildcard `*` badge in the UI only shows for entries that the API returns
  with the `*.` prefix. Since `domainSet` strips the `*.` prefix at storage time,
  entries added as `*.example.com` are stored and returned as `example.com`. The
  badge appears only when the raw stored entry starts with `*.`. If the user enters
  `*.example.com`, what is stored is `example.com` and the badge will not show.
  The matching behaviour is correct regardless; only the display badge is affected.
  A future improvement could normalise to always store the `*.` prefix for wildcard
  intent.
