# config.yaml Schema Reference

This page documents every field in `config.yaml`. All durations use Go's `time.ParseDuration` format (e.g. `"24h"`, `"30m"`, `"1h30m"`).

## Annotated example

```yaml
node:
  id: "skoed-1"              # string, required. Unique node identifier.
  raft_address: "0.0.0.0:9000"  # host:port for Raft consensus traffic
  api_address:  "0.0.0.0:8080"  # host:port for management API
  data_dir: "/var/lib/skoed"    # path to bbolt data directory
  dns:
    listen:
      port: 53           # DNS listener port
      ipv4: true         # bind IPv4 listener
      ipv6: true         # bind IPv6 listener
    upstreams:           # list of upstream resolvers
      - "https://dns.cloudflare.com/dns-query"
    cache:
      enabled: true
      min_ttl: 0         # seconds; 0 = honour upstream TTL
      max_ttl: 3600      # seconds
      size: 10000        # max cached entries
    trusted_subnets:     # recursive resolution allowed from these CIDRs
      - "192.168.0.0/16"
      - "10.0.0.0/8"
    doh:
      enabled: false
      port: 443
    dot:
      enabled: false
    doh3:
      enabled: false
    dnscrypt:
      enabled: false
  api:
    port: 8080
    tls:
      enabled: false
      acme:
        enabled: false
        domain: ""
        email: ""
      cert_file: ""
      key_file: ""
      self_signed: false
      http_redirect: false
  metrics:
    enabled: false
    port: 9100
    auth:
      enabled: false
      username: ""
      password: ""
  audit:
    max_entries: 10000
  cluster:
    tls:
      enabled: false
      ca_cert: ""
      cert: ""
      key: ""
  blocklists:
    refresh_interval: "24h"
bootstrap:                 # present only on non-bootstrap nodes
  leader_address: ""       # HTTP URL of existing cluster node
  token: ""                # join token from POST /api/v1/cluster/tokens
```

## Field reference

### `node`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `node.id` | string | — | **Required.** Unique identifier for this node within the cluster. Used in Raft peer lists and log output. |
| `node.raft_address` | string | `"0.0.0.0:9000"` | `host:port` on which this node listens for Raft consensus traffic. Must be reachable by all other cluster members. |
| `node.api_address` | string | `"0.0.0.0:8080"` | `host:port` for the management REST API and web UI. |
| `node.data_dir` | string | `"/var/lib/skoed"` | Directory where bbolt database files are stored. Must be writable by the skoed process. |

### `node.dns`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `node.dns.listen.port` | integer | `53` | UDP/TCP port for the DNS listener. Use `5353` or another unprivileged port if not running as root. |
| `node.dns.listen.ipv4` | boolean | `true` | Bind the DNS listener on IPv4. |
| `node.dns.listen.ipv6` | boolean | `true` | Bind the DNS listener on IPv6. |
| `node.dns.upstreams` | list of strings | — | **Required.** Upstream resolvers. Supports plain UDP (`8.8.8.8`), DNS-over-HTTPS (`https://…`), and DNS-over-TLS (`tls://…`). At least one entry is required. |
| `node.dns.trusted_subnets` | list of strings (CIDR) | `[]` | Clients within these subnets are allowed to perform recursive resolution. Queries from other sources are refused. |

### `node.dns.cache`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `node.dns.cache.enabled` | boolean | `true` | Enable the in-process DNS response cache. |
| `node.dns.cache.min_ttl` | integer (seconds) | `0` | Minimum TTL enforced on cached responses. `0` honours the upstream TTL as-is. |
| `node.dns.cache.max_ttl` | integer (seconds) | `3600` | Maximum TTL enforced on cached responses. Responses with a higher upstream TTL are clamped to this value. |
| `node.dns.cache.size` | integer | `10000` | Maximum number of entries in the cache. Least-recently-used entries are evicted when the limit is reached. |

### `node.dns.doh`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `node.dns.doh.enabled` | boolean | `false` | Enable DNS-over-HTTPS (RFC 8484) listener. |
| `node.dns.doh.port` | integer | `443` | Port for the DoH listener. Requires a valid TLS certificate (see `node.api.tls`). |

### `node.dns.dot`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `node.dns.dot.enabled` | boolean | `false` | Enable DNS-over-TLS (RFC 7858) listener on port 853. |

### `node.dns.doh3`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `node.dns.doh3.enabled` | boolean | `false` | Enable DNS-over-HTTP/3 (QUIC) listener. |

### `node.dns.dnscrypt`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `node.dns.dnscrypt.enabled` | boolean | `false` | Enable DNSCrypt listener. |

### `node.api`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `node.api.port` | integer | `8080` | Port for the management REST API. Overrides the port portion of `node.api_address` when set. |

### `node.api.tls`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `node.api.tls.enabled` | boolean | `false` | Enable TLS on the management API. |
| `node.api.tls.cert_file` | string | `""` | Path to a PEM-encoded TLS certificate file. Used when not using ACME or self-signed. |
| `node.api.tls.key_file` | string | `""` | Path to the PEM-encoded private key matching `cert_file`. |
| `node.api.tls.self_signed` | boolean | `false` | Generate and use a self-signed certificate on startup. Useful for internal/dev deployments. |
| `node.api.tls.http_redirect` | boolean | `false` | Redirect HTTP requests to HTTPS. Only effective when TLS is enabled. |

### `node.api.tls.acme`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `node.api.tls.acme.enabled` | boolean | `false` | Obtain and auto-renew a certificate via ACME (Let's Encrypt). Requires port 80 or 443 to be accessible from the internet. |
| `node.api.tls.acme.domain` | string | `""` | Fully-qualified domain name to request the certificate for. |
| `node.api.tls.acme.email` | string | `""` | Contact email for Let's Encrypt expiry notices. |

### `node.metrics`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `node.metrics.enabled` | boolean | `false` | Expose a Prometheus-compatible `/metrics` endpoint. |
| `node.metrics.port` | integer | `9100` | Port for the metrics endpoint. |
| `node.metrics.auth.enabled` | boolean | `false` | Require HTTP Basic authentication on the metrics endpoint. |
| `node.metrics.auth.username` | string | `""` | Username for metrics Basic auth. |
| `node.metrics.auth.password` | string | `""` | Password for metrics Basic auth. Store this in a secrets manager rather than plain text in config. |

### `node.audit`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `node.audit.max_entries` | integer | `10000` | Maximum number of audit log entries retained in memory. Oldest entries are dropped when the ring buffer is full. |

### `node.cluster.tls`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `node.cluster.tls.enabled` | boolean | `false` | Enable mutual TLS authentication on Raft inter-node traffic. Recommended for production clusters. |
| `node.cluster.tls.ca_cert` | string | `""` | Path to the PEM CA certificate used to verify peer certificates. |
| `node.cluster.tls.cert` | string | `""` | Path to this node's PEM TLS certificate for cluster traffic. |
| `node.cluster.tls.key` | string | `""` | Path to this node's PEM private key for cluster traffic. |

### `node.blocklists`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `node.blocklists.refresh_interval` | duration string | `"24h"` | Global default interval between automatic blocklist refreshes. Can be overridden per blocklist via the API. |

### `bootstrap`

Present only on nodes that join an existing cluster. Omit this section entirely on the first (bootstrap) node.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `bootstrap.leader_address` | string | — | HTTP URL of any existing cluster node (e.g. `http://skoed-1:8080`). Used once at first startup to join the cluster. |
| `bootstrap.token` | string | — | Join token obtained from `POST /api/v1/cluster/tokens` on the existing cluster. Single-use; invalidated after successful join. |
