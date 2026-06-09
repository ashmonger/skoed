# Demo Note — M8: Encrypted DNS Expansion (DoH3 + DNSCrypt v2)

## Implemented scope

### DoH3 (HTTP/3 over QUIC, RFC 9230)
- `doh3_port` in node YAML enables an HTTP/3 listener (quic-go v0.60.0).
- Queries arrive as GET or POST `/dns-query` — same RFC 8484 wire format as DoH.
- Shares the same `miekg/dns` handler pipeline as UDP/TCP/DoH/DoT:
  blocklists, allowlist, local DNS, SafeSearch, query log all apply.
- Transport is tagged `"doh3"` in query-log outcomes.
- TLS 1.3 minimum enforced (QUIC requirement).
- Graceful shutdown: `http3.Server.Shutdown(ctx)` called on SIGTERM.

### DNSCrypt v2
- `dnscrypt_port` in node YAML enables a DNSCrypt UDP listener.
- Keypair generation and rotation:
  - Leader generates the initial keypair within 30 s of winning election.
  - Keypair is replicated to all nodes via Raft (`CmdDNSCryptKeysSet`).
  - Cert TTL configurable via `node.dns.dnscrypt.cert_ttl_hours`
    (default 8 760 h = 1 year, matching ameshkov/dnscrypt default).
  - Leader rotates when < 10 % of TTL remains.
- `GET /api/v1/settings` returns `dnscrypt_stamp` (`sdns://…` URI) once
  keys are available — clients can copy this into dnscrypt-proxy.
- Bridge adapts ameshkov/dnscrypt.Handler → miekg/dns.Handler so the
  full filter pipeline applies (same as all other transports).
- Deferred start: if no keypair exists at boot (fresh cluster), the
  DNSCrypt server starts automatically once the first keypair lands via
  the `rebuildDNS` callback.

### Independence
- DoH3 and DNSCrypt can each be enabled or disabled independently via
  their respective port knobs; neither port at 0 is the default.

## Verification steps

```
# Node config snippet to enable all M8 features:
node:
  dns:
    listen:
      doh3_port: 8453
      dnscrypt_port: 5443
    dnscrypt:
      cert_ttl_hours: 24

# 1. After startup, stamp appears in settings:
curl -su admin:pass http://localhost:8080/api/v1/settings | jq .dnscrypt_stamp

# 2. DoH3 query (requires quic-go http3 client or curl with HTTP/3 support):
curl --http3 -sk "https://localhost:8453/dns-query?dns=$(echo -n '...' | base64)" \
  -H "Accept: application/dns-message" | xxd

# 3. DNSCrypt query (requires dnscrypt-proxy or the ameshkov client):
dnscrypt-proxy -stamp sdns://... -list

# 4. Raft replication: both nodes in a 2-node cluster return the same stamp.
```

## Not implemented

- Web UI page for DNSCrypt stamp / DoH3 address display.
- `last_used_at` tracking per transport (query log already records transport tag).
- TCP fallback for DNSCrypt (only UDP is implemented; ameshkov/dnscrypt also
  supports `ServeTCP` — can be added if operators need it).
- Key rotation trigger via API (currently leader-only automated rotation only).

## Limitations

- The DNSCrypt cert is generated with default `XSalsa20Poly1305` cipher;
  `XChacha20Poly1305` is not yet configurable via node YAML.
- No IP filtering on DNSCrypt (all source IPs accepted, consistent with other
  transport trust model).
