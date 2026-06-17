# DEMO NOTE — M4.5 API Documentation Browser

## Scope

Embeds a Swagger UI at `/api/docs` (redirects from `/api/v1/docs`) serving the self-contained `management-api.openapi.yaml` specification inline. The OpenAPI spec is embedded in the binary via `go:embed`.

### Implemented

- **Swagger UI** served at `/api/docs` and `/api/v1/docs` (redirect)
- **OpenAPI spec** served at `/api/openapi.yaml`
- Full interactive documentation: expand/collapse endpoints, try-it-out with BasicAuth
- Dark-mode compatible via Swagger UI config overrides
- Endpoint groupings: blocklists, allowlist, local-dns, query-log, settings, config, auth, cluster, schedules, profiles, filtering-pause

### Not implemented

- GraphQL / AsyncAPI browsing
- Per-user token scoping in Swagger UI (plain BasicAuth only)

## Demo

```bash
# Access the API docs
curl http://localhost:8080/api/openapi.yaml
open http://localhost:8080/api/docs
```

## Limitations

None for this feature. The OpenAPI spec may lag behind implementation (new endpoints added after spec last updated).
