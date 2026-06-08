# Add your first blocklist

A blocklist is a named set of domain rules. skoed supports three
source types:

- **`url`** — fetched from an HTTP(S) URL on a schedule
  ([automated refresh](../operations/automated-refresh.md)).
- **`manual`** / **`inline`** — you paste domains via the API or UI.
- **Categories** (`cat:*`) — bundled curated lists (DoH probes,
  social, gambling). See [Categories](../configuration/categories.md).

## URL blocklist (Hagezi Pro)

```sh
curl -fsS -u admin:<password> http://<HOST>:8080/api/v1/blocklists \
  -H 'content-type: application/json' \
  -d '{
    "id":     "hagezi-pro",
    "name":   "Hagezi Pro",
    "enabled": true,
    "source": {
      "type":   "url",
      "url":    "https://raw.githubusercontent.com/hagezi/dns-blocklists/main/hosts/pro.txt",
      "format": "hosts"
    },
    "refresh_interval_seconds": 86400
  }'
```

`refresh_interval_seconds: 86400` (24 h) hands the blocklist to the
[automated refresh](../operations/automated-refresh.md) worker. Set
to `0` to disable auto-refresh (manual `POST /refresh` still works).

## Verify

```sh
# The blocklist row now has a domain_count + LastRefresh* fields.
curl -fsS -u admin:<password> http://<HOST>:8080/api/v1/blocklists/hagezi-pro | jq

# Test resolution: a domain in the list should be blocked.
dig @<HOST> doubleclick.net
```

In the Web UI, the Blocklists table shows the row with status chip
(`ok` / `unchanged` / `error`), interval (`every 24h`), and the
relative `last refresh` timestamp.

## Block policy

Per-blocklist override of how blocked queries are answered:

- `nxdomain` (default) — pretend the domain doesn't exist.
- `null` — return `0.0.0.0` / `::`.
- `nodata` — return NOERROR with empty answer.

Set `block_policy` on the blocklist body or change the cluster-wide
default in `/api/v1/settings`.

## Next

- [Configure profiles and schedules](../configuration/profiles.md)
- [Browse pre-baked categories](../configuration/categories.md)
