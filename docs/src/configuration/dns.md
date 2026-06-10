# DNS — listener, upstreams, cache

skoed acts as a full DNS resolver for your network. This page covers the
plain DNS listener (UDP + TCP port 53), upstream resolver selection, and
the in-process response cache. For encrypted transports (DoH, DoT, DoH3,
DNSCrypt) see [DoH / DoT serving](doh-dot.md).

---

## Listener configuration

The listener settings live under `node.dns.listen` in `config.yaml`.

| Key | Default | Description |
|-----|---------|-------------|
| `node.dns.listen.port` | `53` | UDP and TCP port to bind |
| `node.dns.listen.ipv4` | `true` | Bind on `0.0.0.0` (IPv4) |
| `node.dns.listen.ipv6` | `true` | Bind on `[::]` (IPv6) |

When both `ipv4` and `ipv6` are `false` (or absent) skoed defaults to
enabling both.

**Example — standard dual-stack listener:**

```yaml
node:
  dns:
    listen:
      port: 53
      ipv4: true
      ipv6: true
```

**Example — IPv4-only on a non-standard port (testing):**

```yaml
node:
  dns:
    listen:
      port: 5353
      ipv4: true
      ipv6: false
```

> **Note:** binding port 53 on Linux requires either running as root or
> granting the `CAP_NET_BIND_SERVICE` capability:
> `setcap cap_net_bind_service=+ep /usr/local/bin/skoed`

---

## Upstream resolvers

skoed forwards queries it cannot resolve locally to an ordered list of
upstream resolvers. Configure them under `dns.upstream_resolvers` in the
cluster-replicated section of `config.yaml`.

### Supported upstream schemes

| Format | Example | Protocol |
|--------|---------|----------|
| Plain IP (UDP/TCP) | `8.8.8.8` | DNS over UDP (fallback TCP on truncation) |
| IP + port | `1.1.1.1:53` | DNS over UDP |
| DoH | `https://dns.cloudflare.com/dns-query` | DNS-over-HTTPS (RFC 8484) |
| DoT | `tls://1.1.1.1` | DNS-over-TLS (RFC 7858) |
| DoH3 | `h3://dns.cloudflare.com/dns-query` | DNS-over-HTTPS/3 (HTTP/3 + QUIC) |
| DNSCrypt | `sdns://…` | DNSCrypt v2 stamp |

Plain IP addresses without a port have `:53` appended automatically.

### Resolver selection

Resolvers are tried **in order**. skoed moves to the next resolver only on
a network-level or timeout error — a DNS-level response (NXDOMAIN,
SERVFAIL, etc.) from the first reachable resolver is returned immediately.
If all resolvers fail, skoed returns SERVFAIL.

Configure the upstream timeout (seconds) with `dns.upstream_timeout_seconds`
(default `3`).

**Example — two plain resolvers with a DoH fallback:**

```yaml
dns:
  upstream_resolvers:
    - 9.9.9.9
    - 149.112.112.112
    - https://dns.quad9.net/dns-query
  upstream_timeout_seconds: 3
```

**Example — privacy-focused setup using DoH only:**

```yaml
dns:
  upstream_resolvers:
    - https://dns.cloudflare.com/dns-query
    - https://dns.google/dns-query
  upstream_timeout_seconds: 5
```

---

## Cache

skoed keeps a least-recently-used (LRU) in-memory cache of DNS responses.
Cache settings are under `dns.cache` in the cluster-replicated section.

| Key | Default | Description |
|-----|---------|-------------|
| `dns.cache.enabled` | `true` | Enable or disable the response cache |
| `dns.cache.max_entries` | `10000` | Maximum number of cached responses (LRU eviction) |

Cached entries respect the TTL returned by the upstream resolver. There is
no configurable `min_ttl` or `max_ttl` override at this time — skoed
honours the upstream TTL as-is.

**Example:**

```yaml
dns:
  cache:
    enabled: true
    max_entries: 20000
```

To disable caching entirely (useful when debugging upstream behaviour):

```yaml
dns:
  cache:
    enabled: false
    max_entries: 0
```

> Disabling the cache increases upstream query volume and latency for
> clients. Re-enable it for production use.

---

## Trusted subnets

By default skoed answers queries from any client address. To restrict
recursive resolution to specific CIDRs — for example to prevent open
resolver abuse if the DNS port is exposed on a public interface — list
the allowed client subnets under `dns.trusted_subnets`:

```yaml
dns:
  trusted_subnets:
    - 192.168.0.0/16
    - 10.0.0.0/8
    - fd00::/8
```

Clients outside the listed subnets receive REFUSED. Leave the list empty
(the default) to allow all clients.

---

## Complete config.yaml example

The following snippet shows all DNS-related fields in one place. The `node:`
section is per-host; the `dns:` section is cluster-replicated (every node
shares the same value via Raft).

```yaml
# ── Node-local settings ───────────────────────────────────────────────────────
node:
  id: skoed-01
  raft_address: "192.168.1.10:7000"
  api_address: ":8080"
  data_dir: /var/lib/skoed
  dns:
    listen:
      port: 53
      ipv4: true
      ipv6: true
      # Encrypted transports (optional — see doh-dot.md)
      doh_port: 443
      dot_port: 853

# ── Cluster-replicated settings ───────────────────────────────────────────────
dns:
  upstream_resolvers:
    - 9.9.9.9
    - 149.112.112.112
    - https://dns.quad9.net/dns-query
  upstream_timeout_seconds: 3
  trusted_subnets:
    - 192.168.0.0/16
    - 10.0.0.0/8
  cache:
    enabled: true
    max_entries: 10000
```
