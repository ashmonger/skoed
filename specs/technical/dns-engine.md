# DNS Engine — Technical Specification

x-tsid: TS-DnsEngine
x-fsid-links:
  - FS-DnsQueryForwarding
  - FS-DnsQueryForwardingTcp
  - FS-DnsQueryForwardingFallback
  - FS-DnsQueryForwardingAllUpstreamsUnreachable
  - FS-DnsQueryForwardingAAAA
  - FS-DnsQueryForwardingMultipleRecordTypes
  - FS-RootDnsResolution
  - FS-RootDnsResolutionAAAA
  - FS-RootDnsResolutionRestrictedToTrustedSubnets
  - FS-RootDnsResolutionFromTrustedSubnet
  - FS-RootDnsResolutionAirGapped
  - FS-DualStackDnsIPv4Listener
  - FS-DualStackDnsIPv6Listener
  - FS-DualStackDnsIPv6ClientIdentification
  - FS-DualStackDnsNullBlockIPv4
  - FS-DualStackDnsNullBlockIPv6
  - FS-DnssecTransparentProxy
  - FS-DnssecTransparentProxyWithoutDoBit
  - FS-DnssecTransparentProxyBlockedDomain

## Overview

The DNS engine is the core component of dblock. It listens on UDP and TCP port 53 on both IPv4 and IPv6 interfaces, processes incoming queries through the filtering engine, and resolves non-blocked queries via upstream forwarding or recursive root resolution.

Library: `github.com/miekg/dns`

---

## Query Processing Flow

```
Client query (UDP or TCP, IPv4 or IPv6)
        │
        ▼
1. Validate query (well-formed DNS message)
        │
        ▼
2. Extract client IP from source address
        │
        ▼
3. Check local DNS entries
   ├── Match found → return local record, log outcome=local, STOP
   └── No match → continue
        │
        ▼
4. Check allowlist (exact and wildcard)
   ├── Match found → skip blocklist evaluation, go to step 6
   └── No match → continue
        │
        ▼
5. Check blocklists (exact and wildcard, per active blocklist)
   ├── Match found → apply block policy (NXDOMAIN / NULL / NODATA)
   │               → log outcome=blocked, blocklist name
   │               → return block response, STOP
   └── No match → continue
        │
        ▼
6. Forward or recursively resolve
   ├── If root DNS enabled AND client in trusted subnet → recursive resolve
   ├── If root DNS enabled AND client NOT in trusted subnet → return REFUSED
   └── If upstream forwarding configured → forward to upstream list (with fallback)
        │
        ▼
7. Return resolved response to client
8. Log outcome=forwarded (or outcome=cached if from cache)
```

---

## Listeners

| Protocol | Address | Port |
|----------|---------|------|
| UDP | 0.0.0.0 (all IPv4) | 53 |
| TCP | 0.0.0.0 (all IPv4) | 53 |
| UDP | :: (all IPv6) | 53 |
| TCP | :: (all IPv6) | 53 |

Both listeners start on node startup. Either can be disabled via configuration if required (not exposed in M1 UI; config-file only).

---

## Upstream Forwarding

- Upstreams are tried in order; first successful response is used.
- A response is "successful" if the upstream returns any DNS response (including NXDOMAIN). Only a network-level failure (timeout, connection refused) triggers fallback to the next upstream.
- Default timeout per upstream attempt: 3 seconds (configurable).
- If all upstreams fail: return SERVFAIL to client.
- Supported upstream formats: `9.9.9.9:53`, `9.9.9.9` (port 53 implied), `tls://9.9.9.9:853` (DoT, M4), `https://dns.quad9.net/dns-query` (DoH, M4).
- Default resolvers shipped with dblock: Quad9 primary (`9.9.9.9`) and secondary (`149.112.112.112`). Google DNS (`8.8.8.8`) is intentionally excluded from defaults.
- Other supported privacy-respecting resolvers (user-configurable): Mullvad (`194.242.2.2`, `194.242.2.3`), AdGuard DNS (`94.140.14.14`, `94.140.15.15`), Cloudflare (`1.1.1.1`, `1.0.0.1`, `1.1.1.2` for malware blocking).

---

## Root DNS Recursive Resolution

- Uses the embedded IANA root hints (updated at build time from `https://www.iana.org/domains/root/files`).
- Resolution follows standard iterative resolution: root → TLD → authoritative.
- Restricted to client IPs within configured trusted subnets (CIDR list).
- Clients outside trusted subnets receive REFUSED (rcode 5).
- If no trusted subnets are configured and root DNS is enabled, resolution is unrestricted (suitable for single-node home deployments; documented risk).

---

## DNSSEC Transparent Proxy

- If the incoming query has the DO (DNSSEC OK) bit set in the OPT record, the DO bit is forwarded to the upstream resolver unchanged.
- DNSSEC records (RRSIG, DNSKEY, DS, NSEC, NSEC3) in upstream responses are passed through to the client unchanged.
- dblock does not set the AD (Authenticated Data) bit.
- dblock does not validate signatures or manage trust anchors.
- DNSSEC records are never returned for locally resolved entries (local DNS, block responses).

---

## Wildcard Domain Matching

Matching is applied to both blocklist lookups and allowlist lookups.

**Rules:**
- An entry of the form `*.example.com` matches:
  - The apex: `example.com`
  - Any subdomain at any depth: `sub.example.com`, `a.b.example.com`
- An entry without a wildcard prefix (e.g., `ads.example.com`) matches that exact domain only.
- Matching is case-insensitive (DNS is case-insensitive).
- Matching is done by splitting the query FQDN into labels and checking suffix alignment.

**Evaluation order (per query):**
1. Exact match in local entries (highest priority)
2. Exact or wildcard match in allowlist
3. Exact or wildcard match in blocklists (first match across active lists wins)
4. Forward / recursively resolve

---

## Caching

- Responses from upstream are cached in memory keyed by `(qname, qtype, qclass)`.
- TTL from the upstream response is honoured; entries expire after TTL seconds.
- Blocked and local responses are not cached (they are served directly from config).
- Cache is not persisted to disk; it is cleared on restart.
- Maximum cache size: 10,000 entries (configurable). When full, LRU entries are evicted.

---

## Error Handling

| Condition | Response |
|-----------|---------|
| Malformed DNS message | Drop silently (no response) |
| Upstream timeout (all upstreams) | SERVFAIL |
| Root resolution failure (NXDOMAIN from authoritative) | NXDOMAIN |
| Root resolution — client not in trusted subnet | REFUSED |
| Query type not supported by dblock's local handling | Forward upstream |

---

## Implementation Notes

- Use `dns.Server` from `miekg/dns` for UDP and TCP listeners.
- Use `dns.Client` from `miekg/dns` for upstream forwarding.
- Implement recursive resolution as a separate component (`internal/dns/recursor.go`) to keep forwarding and recursive paths isolated.
- The filtering engine is called as a pure function (no I/O); it receives the query name and type and returns a `Disposition` (allow, block with policy, local record).
