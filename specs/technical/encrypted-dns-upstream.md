# TS-EncryptedDnsUpstream — Encrypted DNS Upstream Resolvers

x-tsid: TS-EncryptedDnsUpstream
x-fsid-links: [FS-DotUpstreamForwards, FS-DotUpstreamCertVerified, FS-DotUpstreamSkipVerify, FS-DohUpstreamForwards, FS-DohUpstreamGetMethod, FS-DohUpstreamCertVerified, FS-MixedUpstreamFallback, FS-AllUpstreamsFail, FS-UpstreamSchemeValidation, FS-UpstreamConfigPersisted, FS-UpstreamStatusApi]

## URL format for upstream_resolvers

Each entry in `dns.upstream_resolvers` is a URL. Supported schemes:

| Scheme | Example | Default port | Protocol |
|--------|---------|--------------|----------|
| *(none / plain)* | `1.1.1.1:53` or `1.1.1.1` | 53 | Plain UDP + TCP fallback |
| `tls://` | `tls://1.1.1.1:853` | 853 | DNS-over-TLS (RFC 7858) |
| `https://` | `https://cloudflare-dns.com/dns-query` | 443 | DNS-over-HTTPS (RFC 8484) |

All other schemes (`ftp://`, `udp://`, etc.) are rejected.

### Query parameters

| Parameter | Applies to | Effect |
|-----------|------------|--------|
| `skip_verify=true` | `tls://`, `https://` | Disables TLS certificate verification |
| `method=get` | `https://` | Uses GET with base64url-encoded `dns=` query param instead of POST |

Examples:
- `tls://192.168.1.1:853?skip_verify=true`
- `https://dns.google/dns-query?method=get`

## Forwarder dispatch (dns/forwarder.go)

Each upstream is parsed at `NewForwarder` time into an `upstreamFn`:

```
type upstreamFn func(*dns.Msg) (*dns.Msg, error)
```

Dispatch logic per scheme:

### Plain upstream
Unchanged: `dns.Client{Net:"udp"}`, TCP fallback on truncation.

### DoT upstream (`tls://`)
```
dns.Client{
    Net:       "tcp-tls",
    Timeout:   timeout,
    TLSConfig: &tls.Config{
        ServerName:         host,          // from URL
        InsecureSkipVerify: skipVerify,    // from ?skip_verify=true
    },
}
```
No UDP → no truncation retry needed. Single `Exchange` call.

### DoH upstream (`https://`)
POST (default):
```
POST <url>
Content-Type: application/dns-message
Body: dns.Msg.Pack() (wire format)

Response:
Content-Type: application/dns-message
Body: wire-format DNS response → dns.Msg.Unpack()
```

GET (when `?method=get`):
```
GET <url>?dns=<base64url(wire)>
Accept: application/dns-message
```

HTTP client uses the default system CA pool. `?skip_verify=true` sets
`Transport.TLSClientConfig.InsecureSkipVerify = true`.

## Config normalisation (config/config.go)

`normaliseUpstream(s string) (string, error)` — extended signature.

Rules:
1. Empty string → return `("", nil)` (filtered out by caller).
2. Has scheme (`://` present):
   - Parse with `net/url.Parse`.
   - Scheme must be `tls` or `https`. Anything else → error.
   - `tls://` without port → append `:853`.
   - `https://` left unchanged (http.Client handles the port).
   - Return the normalised URL string.
3. No scheme: existing logic — append `:53` if no port present.

Called from:
- `Config.Defaults()` at load time (now returns error; callers propagate it).
- `UpdateSettings` handler before applying `d.UpstreamResolvers` (400 on error).

## Settings API changes

### GET /api/v1/settings
`dns.upstream_resolvers` is returned as-is including full scheme URLs.
No change to response shape — already returns the config field.

### PATCH /api/v1/settings
When `dns.upstream_resolvers` is present, each entry is normalised and
validated before being saved. Invalid scheme → 400 with message:
```json
{"error": "dns.upstream_resolvers[1]: unsupported scheme \"ftp\""}
```

## Sequence: DoT query

```
Client → skoed UDP:53 → Handler → Forwarder.Forward()
  → parse "tls://1.1.1.1:853" → dotFn
  → dns.Client{Net:"tcp-tls"}.Exchange(msg, "1.1.1.1:853")
      (TLS handshake, cert verify)
  → DNS response wire
  ← skoed UDP:53 ← Client
```

## Error handling

| Condition | Behaviour |
|-----------|-----------|
| TLS handshake fails (bad cert, timeout) | Skip upstream, try next |
| HTTP 4xx/5xx from DoH server | Skip upstream, try next |
| DoH response body not valid DNS wire | Skip upstream, try next |
| All upstreams fail | Return SERVFAIL |
| Invalid scheme at config load | Config load fails with error |
| Invalid scheme at PATCH | HTTP 400 returned |

## Files changed

| File | Change |
|------|--------|
| `apps/skoed/internal/config/config.go` | `normaliseUpstream` validates schemes, returns error |
| `apps/skoed/internal/dns/forwarder.go` | `upstreamFn` dispatch; DoT and DoH dialers added |
| `apps/skoed/internal/api/handlers/settings.go` | Propagate scheme validation error as 400 |
