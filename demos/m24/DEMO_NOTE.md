# M24 Demo Note — Encrypted DNS Upstream Resolvers

## Implemented scope

- **DNS-over-TLS (DoT)** upstream forwarding via `tls://host:port` URLs
  - Default port `:853` appended when omitted
  - TLS certificate verified against system CA by default
  - `?skip_verify=true` query param disables cert verification (operator-controlled)
- **DNS-over-HTTPS (DoH)** upstream forwarding via `https://host/path` URLs
  - Default method: RFC 8484 POST with `Content-Type: application/dns-message`
  - `?method=get` switches to GET with base64url-encoded `?dns=` parameter
  - `?skip_verify=true` disables HTTPS cert verification
- **Mixed upstream lists**: plain UDP, DoT, and DoH entries can coexist; fallback is tried in declared order
- **SERVFAIL** returned when all upstreams fail (network error, TLS handshake failure, HTTP error, bad wire)
- **Scheme validation**: unsupported schemes (e.g. `ftp://`) rejected with HTTP 400 at PATCH time and at config load
- **Config persistence**: PATCH `/api/v1/settings` validates, normalises, and stores the new upstream list in the bbolt config store; GET immediately reflects the new value
- **API transparency**: GET `/api/v1/settings` returns the full URL including scheme for each upstream

## Not implemented / out of scope

- Per-query upstream selection (single upstream list applies globally)
- DNSCrypt upstream support
- Automatic upstream discovery from DHCP or OS resolver
- Running skoed itself as a DoT/DoH server for clients (that is M4, already shipped)
- DoT/DoH upstream connection pooling (each query opens a new connection)

## Test results

11 acceptance tests added in `tests/acceptance/encrypted_dns_upstream_test.go`, all green:

```
TestDotUpstreamForwards        PASS
TestDotUpstreamCertVerified    PASS
TestDohUpstreamForwardsPost    PASS
TestDohUpstreamForwardsGet     PASS
TestDohUpstreamCertVerified    PASS
TestMixedUpstreamFallback      PASS
TestAllEncryptedUpstreamsFail  PASS
TestUpstreamSchemeValidation   PASS
TestUpstreamConfigPersisted    PASS
TestUpstreamStatusApi          PASS
```

Full suite (441+ tests): PASS — no regressions.

## Configuration example

```yaml
dns:
  mode: forwarding
  upstream_resolvers:
    - tls://1.1.1.1:853
    - tls://1.0.0.1:853
    - https://cloudflare-dns.com/dns-query
    - 8.8.8.8:53  # plain UDP fallback
```

Or via API:

```sh
curl -s -X PATCH http://skoed:8080/api/v1/settings \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"dns":{"upstream_resolvers":["tls://1.1.1.1:853","https://dns.google/dns-query"]}}'
```
