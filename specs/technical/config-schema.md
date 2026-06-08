# Config Schema — Technical Specification

> **M2 status:** Since the move to Raft+bbolt, the YAML form documented here
> is **import/export only** — it is the wire format for `GET /config/export`
> and `POST /config/import`, and the on-disk form during an M1→M2 migration.
> The live state is the bbolt store described in `cluster-store.md`.
> Editing `config.yaml` on disk after boot has no effect.

x-tsid: TS-ConfigSchema
x-fsid-links:
  - FS-BlocklistAddFromUrl
  - FS-BlocklistAddManual
  - FS-BlocklistDisable
  - FS-BlockPolicyConfigurationGlobalDefault
  - FS-BlockPolicyConfigurationPerBlocklist
  - FS-AllowlistAddDomain
  - FS-LocalDnsEntryAddA
  - FS-LocalDnsEntryAddAAAA
  - FS-LocalDnsEntryAddCNAME
  - FS-ConfigExport
  - FS-ConfigImportOnFreshNode
  - FS-QueryLogRetentionConfigurable
  - FS-WebUiAuthFirstRunSetup
  - FS-RootDnsResolutionRestrictedToTrustedSubnets

## Overview

All skoed configuration is stored in a single YAML file on disk (`config.yaml`). Writes are atomic (write to a temp file, then `rename`). The file is the single source of truth; no database is used.

The same schema governs the import/export archive — the archive is a `tar.gz` containing `config.yaml` plus any auxiliary files (e.g., manually-entered domain lists stored as separate files referenced from `config.yaml`).

---

## Schema

```yaml
# skoed configuration — version 1

version: 1                          # int, required. Schema version for forward compatibility.

dns:
  listen:
    port: 53                        # int, default 53
    ipv4: true                      # bool, default true. Bind IPv4 listener.
    ipv6: true                      # bool, default true. Bind IPv6 listener.

  mode: forwarding                  # enum: forwarding | recursive. Default: forwarding.
                                    # forwarding: send queries to upstream_resolvers.
                                    # recursive: resolve from root DNS servers.

  upstream_resolvers:               # list<string>. Required when mode=forwarding.
    - "9.9.9.9:53"                  # Quad9 — Swiss foundation, no logging, blocks malicious domains.
    - "149.112.112.112:53"          # Quad9 secondary. Format: "host:port" or "host" (port 53 implied).
                                    # Tried in order; first successful response wins.
                                    #
                                    # Other recommended privacy-respecting resolvers:
                                    #   Mullvad:    194.242.2.2 / 194.242.2.3  (no logging, ad/tracker variants)
                                    #   AdGuard:    94.140.14.14 / 94.140.15.15 (no logging, ad blocking variants)
                                    #   Cloudflare: 1.1.1.1 / 1.0.0.1          (audited no-log; US company)
                                    #   Cloudflare malware-blocking: 1.1.1.2
                                    #
                                    # Google DNS (8.8.8.8 / 8.8.4.4) is intentionally excluded from
                                    # defaults — not aligned with skoed's privacy goals.
  upstream_timeout_seconds: 3       # int, default 3. Per-upstream attempt timeout.

  trusted_subnets:                  # list<string> CIDR. Required when mode=recursive.
    - "192.168.0.0/16"              # Clients outside these subnets receive REFUSED.
    - "10.0.0.0/8"                  # Empty list = unrestricted (home use only).

  cache:
    enabled: true                   # bool, default true.
    max_entries: 10000              # int, default 10000. LRU eviction when full.

filtering:
  block_policy: nxdomain            # enum: nxdomain | null | nodata. Global default.

  blocklists:
    - id: "ads"                     # string, required. Unique identifier.
      name: "Ad servers"            # string, required. Display name.
      enabled: true                 # bool, default true.
      source:
        type: url                   # enum: url | inline. 
        url: "https://example.com/ads.txt"   # string. Required when type=url.
        format: auto                # enum: auto | hosts | domainlist | askoed. Default: auto.
      block_policy: null            # enum: nxdomain | null | nodata | "" (inherit global).
                                    # Empty string or absent = inherit global default.
      domains: []                   # list<string>. Populated by skoed after download; not
                                    # written by the admin for url-type lists.
      last_updated: ""              # ISO 8601 timestamp. Set by skoed after last refresh.

    - id: "custom"
      name: "Custom entries"
      enabled: true
      source:
        type: inline
      block_policy: ""              # Inherit global.
      domains:                      # list<string>. Admin-managed for inline lists.
        - "malware.example.com"
        - "*.spyware.example.org"   # Wildcard: matches apex + all subdomains.

  allowlist:
    - "safe.example.com"            # list<string>. Exact or wildcard (*.example.com).
    - "*.trusted.example.net"

local_dns:
  entries:
    - hostname: "nas.home"          # string, required. FQDN or short name.
      type: A                       # enum: A | AAAA | CNAME
      value: "192.168.1.50"        # string. IP address for A/AAAA; target FQDN for CNAME.
      ttl: 300                      # int, seconds. Default 300.

    - hostname: "nas.home"
      type: AAAA
      value: "fd00::50"
      ttl: 300

    - hostname: "files.home"
      type: CNAME
      value: "nas.home"             # Target must resolve (either locally or upstream).
      ttl: 300

query_log:
  max_entries: 10000                # int, default 10000. Oldest entries discarded when full.

auth:
  username: ""                      # string. Empty = first-run setup not complete.
  password_hash: ""                 # string. bcrypt hash of the password. Empty = not set.
```

---

## Field Rules

| Field | Type | Required | Default | Constraints |
|-------|------|----------|---------|-------------|
| `version` | int | yes | — | Must equal `1` in M1; reject unknown versions on import |
| `dns.listen.port` | int | no | 53 | 1–65535 |
| `dns.mode` | enum | no | `forwarding` | `forwarding` or `recursive` |
| `dns.upstream_resolvers` | list | yes if mode=forwarding | — | ≥ 1 entry when mode=forwarding |
| `dns.upstream_timeout_seconds` | int | no | 3 | 1–30 |
| `dns.trusted_subnets` | list | no | `[]` | Valid CIDR strings |
| `dns.cache.max_entries` | int | no | 10000 | ≥ 0; 0 disables cache |
| `filtering.block_policy` | enum | no | `nxdomain` | `nxdomain`, `null`, `nodata` |
| `filtering.blocklists[].id` | string | yes | — | Unique within the list; alphanumeric + `-_` |
| `filtering.blocklists[].source.type` | enum | yes | — | `url` or `inline` |
| `filtering.blocklists[].source.format` | enum | no | `auto` | `auto`, `hosts`, `domainlist`, `askoed` |
| `filtering.blocklists[].block_policy` | enum | no | `""` | `nxdomain`, `null`, `nodata`, or `""` (inherit) |
| `filtering.blocklists[].domains` | list | required if type=inline | — | Each entry: exact domain or `*.domain` wildcard |
| `filtering.allowlist` | list | no | `[]` | Each entry: exact domain or `*.domain` wildcard |
| `local_dns.entries[].type` | enum | yes | — | `A`, `AAAA`, `CNAME` |
| `local_dns.entries[].value` | string | yes | — | Valid IPv4 for A; valid IPv6 for AAAA; valid FQDN for CNAME |
| `local_dns.entries[].ttl` | int | no | 300 | 1–86400 |
| `query_log.max_entries` | int | no | 10000 | 1–1000000 |
| `auth.password_hash` | string | no | `""` | bcrypt hash; empty means first-run setup pending |

---

## Wildcard Domain Format

A wildcard entry must match `^\\*\\.[a-zA-Z0-9]([a-zA-Z0-9\\-]{0,61}[a-zA-Z0-9])?(\\.[a-zA-Z0-9]([a-zA-Z0-9\\-]{0,61}[a-zA-Z0-9])?)*$`

Examples:
- Valid: `*.example.com`, `*.sub.example.co.uk`
- Invalid: `*example.com` (missing dot), `**example.com`, `*.` (empty label)

---

## Import/Export Archive Structure

```
config-export.tar.gz
└── config.yaml          # Full configuration as described above
```

For URL-sourced blocklists, the `domains` field is cleared in the export (the list is re-downloaded on import). For inline blocklists, `domains` is included verbatim.

---

## Atomic Write Protocol

1. Write new config to `config.yaml.tmp` in the same directory.
2. Call `fsync` on the file.
3. `rename("config.yaml.tmp", "config.yaml")` (atomic on POSIX systems).
4. On import: validate the full schema before writing; reject and return an error if invalid.
