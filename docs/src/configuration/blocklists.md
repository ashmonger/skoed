# Blocklists and allowlists

skoed blocks domains by matching them against one or more blocklists. An
allowlist provides a permanent override: any domain on the allowlist is
always resolved, regardless of blocklist matches.

---

## Adding a blocklist

Send a `POST` request to `/api/v1/blocklists`. skoed fetches the list
immediately and stores it in the cluster-replicated state.

**Fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | Human-readable label shown in the UI |
| `source.type` | yes | `"url"` to fetch from a remote URL, or `"inline"` for a manually managed list |
| `source.url` | when `type=url` | HTTPS URL to download the list from |
| `source.format` | no | Parser hint: `"hosts"`, `"domainlist"`, or `"askoed"`. Auto-detected when omitted |
| `enabled` | no | `true` (default) or `false` |
| `block_policy` | no | Per-list policy: `"nxdomain"`, `"null"`, or `"nodata"`. Inherits global policy when omitted |
| `refresh_interval_seconds` | no | Auto-refresh interval in seconds. `0` disables auto-refresh |

**Example — add a URL-sourced blocklist:**

```bash
curl -s -u admin:password \
  -X POST http://skoed:8080/api/v1/blocklists \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "StevenBlack unified hosts",
    "source": {
      "type": "url",
      "url": "https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts",
      "format": "hosts"
    },
    "enabled": true,
    "refresh_interval_seconds": 86400
  }'
```

**Example — add a plain domain list:**

```bash
curl -s -u admin:password \
  -X POST http://skoed:8080/api/v1/blocklists \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "OISD small",
    "source": {
      "type": "url",
      "url": "https://small.oisd.nl/domainswild",
      "format": "domainlist"
    },
    "enabled": true,
    "refresh_interval_seconds": 43200
  }'
```

---

## Supported list formats

### Hosts file (`"hosts"`)

The classic `/etc/hosts` format. Each non-comment line is
`<IP> <domain>`. The IP is ignored — only the domain is extracted.
Lines starting with `#` and entries for `localhost`, `broadcasthost`,
and `localhost.localdomain` are skipped automatically.

```
# Example hosts file
0.0.0.0 ads.example.com
0.0.0.0 tracker.example.net
127.0.0.1 malware.example.org
```

### Plain domain list (`"domainlist"`)

One domain per line, no IP prefix. Lines starting with `#` are comments.
Wildcard prefixes (`*.example.com` or `.example.com`) match all
subdomains of the named domain.

```
ads.example.com
*.tracker.example.net
.social.example.org
```

### AdBlock / ABP syntax (`"askoed"`)

A subset of AdBlock Plus filter syntax. skoed recognises domain-blocking
rules of the form `||domain.example^` and ignores cosmetic filters,
element-hiding rules, and any rule with a path component (`/`).

```
! AdBlock comment
||ads.example.com^
||tracker.example.net^$third-party
```

---

## Block policy

Each blocklist can override the global block policy. The policy controls
what response is returned for a blocked domain:

| Policy | Response | Notes |
|--------|----------|-------|
| `nxdomain` | `NXDOMAIN` (default) | Domain not found — most compatible |
| `null` | `A 0.0.0.0` / `AAAA ::` | Null-route; some apps retry on NXDOMAIN |
| `nodata` | Empty answer, `NOERROR` | Useful when clients distinguish NXDOMAIN from no data |

The global default is `nxdomain`. Set it under `filtering.block_policy` in
`config.yaml`, or override per-list via the `block_policy` field on the
blocklist object.

---

## Allowlist

Domains on the allowlist are **always resolved**, bypassing every blocklist.
Use the allowlist for false-positive corrections or to exempt specific
services from filtering.

**Add a domain to the allowlist:**

```bash
curl -s -u admin:password \
  -X POST http://skoed:8080/api/v1/allowlist \
  -H 'Content-Type: application/json' \
  -d '{"domain": "cdn.example.com"}'
```

**Remove a domain from the allowlist:**

```bash
curl -s -u admin:password \
  -X DELETE http://skoed:8080/api/v1/allowlist/cdn.example.com
```

**List the current allowlist:**

```bash
curl -s -u admin:password http://skoed:8080/api/v1/allowlist
```

---

## Automated refresh

Set `refresh_interval_seconds` on a URL-sourced blocklist to have skoed
re-download it automatically. The scheduler runs on the current Raft leader
and propagates updates to all nodes via the Raft log.

- The interval is per-blocklist. There is no global `blocklists.refresh_interval` key.
- `refresh_interval_seconds: 0` (or absent) disables auto-refresh for that list.
- Typical values: `86400` (24 h), `43200` (12 h), `21600` (6 h).

The `last_refresh_at`, `last_refresh_status`, and `last_refresh_error` fields
on each blocklist object report the outcome of the most recent automatic or
manual refresh.

---

## Manual refresh

Trigger an immediate re-download for a specific blocklist:

```bash
curl -s -u admin:password \
  -X POST http://skoed:8080/api/v1/blocklists/<id>/refresh
```

Only `type=url` blocklists can be refreshed; `type=inline` lists return
`400 Bad Request`.

---

## Metrics and query log

The Prometheus endpoint exposes per-blocklist refresh health:

- `skoed_blocklist_last_refresh_seconds{id}` — Unix timestamp of the last refresh attempt.
- `skoed_blocklist_refresh_failures_total{id}` — Cumulative refresh failures.

The query log records the blocklist ID that matched each blocked query,
visible in the Web UI under **Query Log** and via
`GET /api/v1/query-log`.
