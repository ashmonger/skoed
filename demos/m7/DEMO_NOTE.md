# M7 Demo Note — API Token Authentication

## Environment

Single-node skoed running locally (or via Docker):

```bash
cd apps/skoed
CGO_ENABLED=0 go build -ldflags="-s -w" -o skoed ./cmd/skoed/
./skoed --config demos/m7/node.yaml
```

Auth set up with the first-run endpoint or via the M6.5 demo environment.

## Implemented scope

All 17 FSIDs from `specs/functional/api-token-authentication.feature` are
implemented and verified by 13 acceptance tests (all green, 180s suite run).

### Mint a token

```bash
# Basic Auth creates a token with cluster:admin scope
curl -su admin:password -X POST http://localhost:8080/api/v1/tokens \
  -H 'Content-Type: application/json' \
  -d '{"label":"ci-job","scopes":["read","write"]}'
# Returns 201 with {"id":"tok_<12hex>","token":"skoed_<48hex>","label":"ci-job",...}
# Raw token value is shown ONCE here only.
```

### Use the token

```bash
TOKEN=skoed_<value from above>

# GET with Bearer token
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/blocklists

# POST with Bearer token (write scope)
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  http://localhost:8080/api/v1/blocklists \
  -d '{"name":"test","url":"https://example.com/list.txt","enabled":false}'
```

### Scope enforcement

```bash
# Mint a read-only token (requires cluster:admin scope to mint)
curl -su admin:password -X POST http://localhost:8080/api/v1/tokens \
  -d '{"label":"readonly","scopes":["read"]}'

READONLY_TOKEN=skoed_<value>

# GET succeeds
curl -s -H "Authorization: Bearer $READONLY_TOKEN" http://localhost:8080/api/v1/blocklists
# → 200

# POST returns 403
curl -s -X POST -H "Authorization: Bearer $READONLY_TOKEN" \
  http://localhost:8080/api/v1/blocklists -d '{...}'
# → 403 forbidden — write scope required
```

### Revoke a token

```bash
TOKEN_ID=tok_<id from mint response>
curl -su admin:password -X DELETE http://localhost:8080/api/v1/tokens/$TOKEN_ID
# → 204 No Content

# Token no longer works
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/blocklists
# → 401
```

### Audit log shows token actor

```bash
curl -su admin:password http://localhost:8080/api/v1/audit | jq '.[0:3]'
# Entries from Bearer token calls show: "actor": "token:tok_<id>"
# Entries from Basic Auth calls show:   "actor": "user:admin"
```

### List tokens (no raw value)

```bash
curl -su admin:password http://localhost:8080/api/v1/tokens
# Returns [{id, label, scopes, created_at, last_used_at?}] — no "token" field
```

## Not implemented in M7

- Web UI token-management page (Account → API Access) — deferred to a UI sprint
- `last_used_at` timestamp update on each authenticated request — the field is
  stored and returned but not yet updated on each use (would require a write per
  request; deferred to avoid hot-path Raft applies)
- Migration guide (Basic Auth → Bearer) in the documentation site — deferred to M8.1 docs

## Known limitations

- Basic Auth via `-u admin:pass` continues to work (deprecated transition path per
  spec); no removal countdown timer has been started yet.
- The `last_used_at` field is always `null` for now (see above).
- Cluster: token revocation is Raft-replicated, but in-flight requests already
  past the `lookupAPIToken` call will complete (no mid-request abort).
