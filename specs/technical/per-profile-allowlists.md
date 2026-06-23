# Per-Profile Allowlists — Technical Specification

x-tsid: TS-PerProfileAllowlists
x-fsid-links: [FS-PerProfileAllowlistPutReplacesAll, FS-PerProfileAllowlistPutClearsOnEmpty, FS-PerProfileAllowlistDeletePurgesCache, FS-PerProfileAllowlistWildcardSubdomain, FS-PerProfileAllowlistWildcardApex, FS-PerProfileAllowlistCountBadge, FS-GlobalAllowlistScopeSwitcher]

## Endpoints

### Existing endpoints (unchanged contract)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/profiles/{id}/allowlist` | List all allowlist entries for a profile |
| POST | `/api/v1/profiles/{id}/allowlist` | Add a single entry to a profile's allowlist |
| DELETE | `/api/v1/profiles/{id}/allowlist/{domain}` | Remove a single entry; purges DNS cache |

### New endpoint

#### PUT /api/v1/profiles/{id}/allowlist

Atomically replaces the entire allowlist for the named profile.

**Request**

```
PUT /api/v1/profiles/{id}/allowlist
Content-Type: application/json
Authorization: Bearer <token>

["domain1.com", "*.example.com", "trusted.net"]
```

- Body: a JSON array of strings. Empty array `[]` clears the allowlist.
- Each entry must be a non-empty string.
- Wildcard prefix `*.` is accepted and normalised to the apex on storage.
- Duplicate entries in the request body are accepted (stored deduplicated by engine).

**Responses**

| Status | Condition |
|--------|-----------|
| 204 No Content | Allowlist replaced successfully |
| 400 Bad Request | Body is not a valid JSON array or contains an empty string |
| 404 Not Found | Profile `{id}` does not exist |
| 503 Service Unavailable | Cluster not available |

**Behaviour**

1. Decode the JSON array body.
2. Validate each entry is non-empty.
3. Load the current profile from the cluster store.
4. Replace `profile.Allowlist` with the new list.
5. Persist via `cluster.UpsertProfile(updatedProfile)` (Raft-replicated).
6. For each domain that was in the old allowlist but not in the new one, call `cache.PurgeDomain(domain)`.
7. Return 204.

---

## Cache Purge Behaviour on DELETE

**Bug fixed in M27**: `DeleteProfileAllowlistEntry` previously did not call `cache.PurgeDomain(domain)` after removing the domain. Without the purge, a cached DNS response from before the deletion would continue to be served as `NOERROR`, making the domain appear still-allowed despite removal.

**Fix**: After a successful `UpsertProfile`, call:
```go
if cache := h.app.GetDNSCache(); cache != nil {
    cache.PurgeDomain(domain)
}
```

This matches the behaviour already present in `AddProfileAllowlistEntry` and the global `AddAllowlistEntry`.

---

## Wildcard Matching Rules

Wildcard entries use the `*.` prefix syntax (e.g. `*.example.com`).

**Storage**: The `domainSet` in `filter/engine.go` normalises `*.example.com` to `example.com` at parse time via `strings.TrimPrefix(e, "*.")`. Both the apex and all subdomains are matched by the engine's `matches()` walk.

**Engine behaviour** (`domainSet.matches`):

```
"sub.example.com"   → checks "sub.example.com", "example.com" → match
"example.com"       → checks "example.com" → match
"other.com"         → checks "other.com", "com" → no match
```

No engine changes are required for wildcard support — it is already implemented.

**UI display**: wildcard entries (those whose stored value, when prepended with `*.`, differs from the raw input) are displayed with a `*` badge in the profile's Allowlist tab.

---

## Export / Import

Per-profile allowlists are already covered by the config export/import feature. The `exportShape` struct in `config/export.go` includes `Profiles []Profile`, and each `Profile` includes `Allowlist []string`. No changes needed.

---

## Web UI — Profile Detail Allowlist Tab

A dedicated "Allowlist" tab is added to the profile detail drawer/modal in `web/src/views/Profiles.vue`.

**Features:**
- Tab label: "Allowlist" with entry count badge (e.g. `3`)
- List of current entries; wildcard entries shown with a `*` badge
- Per-entry delete button (calls `DELETE /api/v1/profiles/{id}/allowlist/{domain}`)
- "Add domain" input + "Add" button (calls `POST /api/v1/profiles/{id}/allowlist`)
- Bulk-replace not exposed (the PUT endpoint is used internally for future bulk operations)

**API client addition** (`web/src/api/endpoints.ts`):
```typescript
export function replaceProfileAllowlist(profileId: string, domains: string[]): Promise<void> {
  return putJSON(`/api/v1/profiles/${encodeURIComponent(profileId)}/allowlist`, domains)
}
```
