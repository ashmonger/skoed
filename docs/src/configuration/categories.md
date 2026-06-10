# Category-based filtering

Categories are named groups of domains maintained by curated upstream
blocklists. Instead of finding and subscribing to individual lists for
common topics, you enable a category and skoed handles the rest.

---

## Built-in categories

| Category name | Description |
|---------------|-------------|
| `adult` | Adult content (OISD curated set) |
| `gambling` | Gambling sites (Steven Black gambling extension) |
| `social` | Social-media platforms (Steven Black social extension) |
| `gaming` | Online gaming services |
| `streaming` | Video streaming services (Netflix, Disney+, etc.) |
| `doh` | Public DoH/DoT resolver hostnames (bundled — no network fetch required) |

The `doh` category is built into the binary. All other categories fetch
their domain list from the configured upstream URL when first enabled.

---

## Listing categories

```bash
curl -s -u admin:password http://skoed:8080/api/v1/categories
```

Each entry includes:

- `name` — canonical identifier used in API paths
- `description` — human-readable explanation
- `default_url` — the upstream URL the category fetches from by default
- `url` — the effective URL (operator override if set, otherwise `default_url`)
- `format` — list format used by the parser (`hosts`, `domainlist`, `askoed`)
- `enabled_for_profiles` — list of profile IDs that currently have this category active

---

## Enabling a category for a profile

Each category must be enabled per profile. When enabled, skoed creates a
managed blocklist (`cat:<name>`) and attaches it to the named profile.

```bash
curl -s -u admin:password \
  -X POST http://skoed:8080/api/v1/categories/adult/enable \
  -H 'Content-Type: application/json' \
  -d '{"profile_id": "kids"}'
```

If the managed blocklist does not exist yet, it is created and the domain
list is fetched immediately.

---

## Disabling a category for a profile

```bash
curl -s -u admin:password \
  -X POST http://skoed:8080/api/v1/categories/adult/disable \
  -H 'Content-Type: application/json' \
  -d '{"profile_id": "kids"}'
```

This detaches the managed blocklist from the profile. The blocklist object
itself is not deleted and can be re-attached to another profile.

---

## Overriding the upstream URL

Some operators run a local mirror or prefer an alternative provider for a
category. Use `PATCH /api/v1/categories/<name>` to point the category at a
different URL:

```bash
curl -s -u admin:password \
  -X PATCH http://skoed:8080/api/v1/categories/adult \
  -H 'Content-Type: application/json' \
  -d '{
    "url": "https://mirror.example.com/adult-domains.txt",
    "format": "domainlist"
  }'
```

The override is stored in `config.yaml` under `category_overrides` and
takes effect on the next refresh. To revert to the catalog default, patch
with an empty `url`.

---

## Managed blocklists

Each category is backed by a **managed blocklist** with the ID `cat:<name>`
(for example `cat:adult`). Managed blocklists:

- Are labelled with `"managed": true` in the API.
- Cannot have their domain list edited manually via the blocklist API; the
  content is owned by the category refresh cycle.
- Support the same `refresh_interval_seconds` field as regular blocklists.
- Can be refreshed manually: `POST /api/v1/blocklists/cat:adult/refresh`.

---

## Filter priority

When evaluating a query, skoed applies rules in this order:

1. **Allowlist** (global + per-profile) — always wins; query is resolved.
2. **Category block** — checked if the domain matches a managed blocklist
   attached to the client's profile.
3. **Blocklist block** — checked against manually added blocklists on the
   profile.
4. **Forward** — query is sent to the upstream resolver.

An allowlist entry overrides any category or blocklist block.
