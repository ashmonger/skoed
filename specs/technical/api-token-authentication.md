# API Token Authentication — Technical Specification

x-tsid: TS-ApiToken
x-fsid-links:
  - FS-TokenMintReturnsValueOnce
  - FS-TokenMintDefaultScopeIsReadWrite
  - FS-TokenMintRequiresClusterAdminScope
  - FS-TokenMintWithExpiry
  - FS-TokenListNeverExposesRawValue
  - FS-TokenRevokeImmediatelyInvalidates
  - FS-TokenRevokeRequiresClusterAdminScope
  - FS-TokenBearerAuthenticatesRequest
  - FS-TokenInvalidBearer401
  - FS-TokenBasicAuthStillWorks
  - FS-TokenReadScopeBlocksWrites
  - FS-TokenWriteScopeAllowsMutations
  - FS-TokenNeverExpiresWhenNoExpirySet
  - FS-TokenExpiryEnforced
  - FS-TokenPatchRelabelsAndUpdatesExpiry
  - FS-TokenAuditEntryRecordsTokenId
  - FS-TokenAuditEntryForPasswordAuth

---

## 1. Token store (bbolt, Raft-replicated)

Tokens are stored in the cluster's bbolt bucket `api_tokens` and replicated
via the existing Raft FSM. Every cluster node can therefore validate an
inbound Bearer token without leader round-trips.

### Schema (per token, JSON-encoded in bbolt)

```json
{
  "id":           "tok_<16 random lowercase hex chars>",
  "token_hash":   "<bcrypt hash of the raw token>",
  "label":        "home-assistant",
  "scopes":       ["read", "write"],
  "created_at":   "2026-06-09T10:00:00Z",
  "last_used_at": "2026-06-09T11:30:00Z",
  "expires_at":   "2027-01-01T00:00:00Z"  // omitted = never expires
}
```

- `id` — stable public identifier; safe to log.
- `token_hash` — bcrypt cost 12; the raw token is NEVER stored.
- `scopes` — subset of `["read","write","cluster:admin"]`.
- `last_used_at` — updated asynchronously (best-effort, not Raft-replicated
  per use; snapshotted periodically or on revocation).
- `expires_at` — RFC 3339; absent = never expires.

### Raw token format

```
skoed_<32 random URL-safe base64 chars>
```

The `skoed_` prefix distinguishes skoed tokens from other secrets in
config files and logs (secret-scanner rules can catch accidental leaks).

---

## 2. Scope model

| Scope           | Allows                                                            |
|-----------------|-------------------------------------------------------------------|
| `read`          | All GET / HEAD endpoints on `/api/v1/`                           |
| `write`         | All POST / PATCH / DELETE endpoints EXCEPT token management      |
| `cluster:admin` | Token mint (`POST /api/v1/tokens`), token revoke (`DELETE /api/v1/tokens/{id}`), leader transfer (`POST /api/v1/cluster/leader/transfer`) |

Rules:
- Default scope when omitted from the mint body: `["read","write"]`.
- `cluster:admin` does NOT imply `read` or `write` — declare all required
  scopes explicitly.
- Scopes are additive; order is irrelevant.
- Scopes cannot be changed after minting (PATCH body with `scopes` → 400).

---

## 3. Authentication middleware

Order of precedence for each request:

1. `Authorization: Bearer <token>` — look up `id` by scanning tokens whose
   `token_hash` matches; reject if expired or not found → 401.
2. `Authorization: Basic <base64>` — verify username/password against the
   existing `auth.Store`; accepted as a deprecated transition path.
3. No `Authorization` → 401.

Scope enforcement fires AFTER authentication:
- If the authenticated principal (token or Basic Auth user) lacks the scope
  required by the route, respond 403.
- Basic Auth always has the equivalent of `["read","write","cluster:admin"]`
  (it is the admin; no narrowing).

`/api/v1/health` and `/api/v1/_public/*` remain unauthenticated.

---

## 4. HTTP API

All endpoints under `/api/v1/tokens` require the caller to hold
`cluster:admin` scope (or Basic Auth).

### POST /api/v1/tokens — mint a token

**Request**
```json
{
  "label":      "ci-pipeline",
  "scopes":     ["read","write"],
  "expires_at": "2027-01-01T00:00:00Z"
}
```
`scopes` and `expires_at` are optional; `label` is required (1–64 chars).

**Response 201**
```json
{
  "id":         "tok_a1b2c3d4e5f60708",
  "token":      "skoed_AbCdEfGhIjKlMnOpQrStUvWxYz012345",
  "label":      "ci-pipeline",
  "scopes":     ["read","write"],
  "created_at": "2026-06-09T10:00:00Z",
  "expires_at": "2027-01-01T00:00:00Z"
}
```
`token` appears in this response ONLY. Subsequent GET responses omit it.

**Errors**: 400 (validation), 401 (unauthenticated), 403 (insufficient scope).

---

### GET /api/v1/tokens — list tokens

**Response 200**
```json
[
  {
    "id":           "tok_a1b2c3d4e5f60708",
    "label":        "ci-pipeline",
    "scopes":       ["read","write"],
    "created_at":   "2026-06-09T10:00:00Z",
    "last_used_at": "2026-06-09T11:00:00Z",
    "expires_at":   "2027-01-01T00:00:00Z"
  }
]
```
No `token` or `token_hash` field ever appears.

---

### DELETE /api/v1/tokens/{id} — revoke a token

**Response 204** — token deleted; subsequent requests bearing it → 401.  
**Response 404** — id not found.

---

### PATCH /api/v1/tokens/{id} — update label or expiry

**Request** (all fields optional)
```json
{
  "label":      "new-label",
  "expires_at": "2028-01-01T00:00:00Z"
}
```

**Response 200** — updated token metadata (no `token` field).  
**Response 400** — if `scopes` is present in the body.  
**Response 404** — id not found.

---

## 5. Audit log integration

Every state-changing request (POST, PATCH, DELETE) records:

```json
{
  "actor": "token:tok_a1b2c3d4e5f60708",
  "action": "blocklist.create",
  "target": "blocklist:ads",
  ...
}
```

For Basic Auth requests the actor is `user:<username>`.
Read-only requests (GET) are not audited (no behavior change from M5.2).

---

## 6. bbolt / Raft command

New FSM commands (in `cluster/commands.go`):

| Command                | Payload fields                                         |
|------------------------|--------------------------------------------------------|
| `UpsertAPIToken`       | id, token_hash, label, scopes, created_at, expires_at  |
| `DeleteAPIToken`       | id                                                     |
| `TouchAPITokenLastUsed`| id, last_used_at (async, best-effort)                  |

`UpsertAPIToken` is used for both creation and PATCH (label / expires_at update).
`TouchAPITokenLastUsed` may be coalesced: fire no more than once per 60 s per
token to avoid flooding the Raft log with `last_used` updates.

---

## 7. In-memory cache

The auth middleware maintains an in-memory `map[string]*TokenEntry`
(keyed by id) loaded from bbolt at startup and kept in sync by the
`onApply` Subscribe callback. This avoids a bbolt read on every request.

Invalidation: `onApply` rebuilds the map after every committed apply.

---

## 8. Web UI routes

| Route                  | Description                                       |
|------------------------|---------------------------------------------------|
| `/settings/tokens`     | Token list + mint form + revoke buttons           |

Token value is shown once (JavaScript `prompt`-style modal or copy-and-close
banner) immediately after minting. The banner auto-closes after 60 s or on
user dismissal and never re-shows.

---

## 9. Migration path (Basic Auth deprecation)

Phase 1 (M7): Basic Auth works; no deprecation header yet.  
Phase 2 (M8+): Basic Auth returns `Deprecation: true` and
`Sunset: <date>` headers on every response.  
Phase 3 (M9+): Basic Auth removed; only Bearer tokens accepted.

The two-release window gives operators time to migrate scripts.

---

## 10. Security notes

- bcrypt cost 12: ~200–400 ms per hash on modern hardware, acceptable for
  an admin-only mint operation (not on the critical path of DNS resolution).
- The raw token is computed as `crypto/rand` 24 bytes → base64 URL encoding
  → prefix with `skoed_`. Entropy: 192 bits.
- `last_used_at` is updated with `TouchAPITokenLastUsed` dispatched in a
  goroutine; a crash between use and commit is acceptable (best-effort).
- Token validation on the hot path: bcrypt is NOT run per-request. Instead
  the in-memory cache stores the hash; the middleware computes bcrypt once
  per unique raw token value presented (cached result keyed by the raw token
  using a small LRU, invalidated on revocation).
