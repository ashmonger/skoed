# DoH / DoT serving

skoed can act as a DNS-over-HTTPS (RFC 8484), DNS-over-TLS (RFC 7858),
DNS-over-HTTPS/3 (HTTP/3 over QUIC), and DNSCrypt v2 server for your
clients. All four encrypted transports run the same filter, allowlist,
local-DNS, and query-log pipeline as the plain DNS listener.

TLS certificates are shared between DoH, DoT, and DoH3. Configure them
under `node.dns.tls` (see [HTTPS for the management API](api-https.md) for
the equivalent management-API TLS settings).

---

## DNS-over-HTTPS (DoH)

Enable DoH by setting `node.dns.listen.doh_port` in `config.yaml`:

```yaml
node:
  dns:
    listen:
      doh_port: 443        # standard HTTPS port; requires root or CAP_NET_BIND_SERVICE
      # or:
      # doh_port: 8443     # non-privileged alternative
```

skoed listens for DoH queries on the path `/dns-query`. Both `GET`
(base64url-encoded `dns` parameter) and `POST` (`Content-Type:
application/dns-message`) are supported per RFC 8484.

A TLS certificate is required. Configure it under `node.dns.tls`
(self-signed fallback is generated automatically if no cert is specified —
see below).

---

## DNS-over-TLS (DoT)

Enable DoT by setting `node.dns.listen.dot_port`:

```yaml
node:
  dns:
    listen:
      dot_port: 853        # IANA-assigned port for DoT
```

DoT uses the same TLS certificate as DoH. One TCP connection can carry
multiple back-to-back queries (RFC 7766 pipelining).

---

## DNS-over-HTTPS/3 (DoH3)

DoH3 uses HTTP/3 over QUIC and requires TLS 1.3. Enable it with
`node.dns.listen.doh3_port`:

```yaml
node:
  dns:
    listen:
      doh3_port: 443       # typically the same port as DoH (QUIC is UDP-based)
```

Clients that support HTTP/3 will use QUIC; others fall back to DoH over
TCP automatically when both ports are the same.

---

## DNSCrypt v2

skoed generates an ephemeral signing keypair on startup (rotated by the
cluster leader before the certificate expires). Enable DNSCrypt with
`node.dns.listen.dnscrypt_port`:

```yaml
node:
  dns:
    listen:
      dnscrypt_port: 8443  # conventional port; 443 also works
    dnscrypt:
      cert_ttl_hours: 24   # optional; default 24 h
```

The `sdns://` stamp URL for this node is printed to the startup log:

```
dnscrypt: stamp sdns://AQAAAAAAAAAAETEyNy4wLjAuMTo4NDQzINk...
```

Copy the stamp into your DNSCrypt client configuration. The stamp changes
on each keypair rotation.

---

## TLS certificates

All encrypted transports share the same certificate. skoed loads it from
`node.dns.tls`:

```yaml
node:
  dns:
    tls:
      cert_file: /etc/skoed/tls/cert.pem
      key_file:  /etc/skoed/tls/key.pem
```

**Self-signed (default):** if `cert_file` and `key_file` are both empty,
skoed auto-generates a self-signed ECDSA certificate under
`<data_dir>/tls/` on first boot and reuses it on subsequent starts.
Clients will show a certificate warning; pin the certificate or install the
self-signed CA in your client trust store to suppress it.

**ACME / Let's Encrypt:** set `node.dns.tls.acme.enabled: true` to obtain
and auto-renew a publicly trusted certificate. See
[HTTPS for the management API](api-https.md) for the full ACME
configuration reference — the same `node.dns.tls.acme` block drives both
the DoH/DoT listeners and the management API.

---

## Full example

```yaml
node:
  id: skoed-01
  data_dir: /var/lib/skoed
  dns:
    listen:
      port: 53
      ipv4: true
      ipv6: true
      doh_port: 443
      dot_port: 853
      doh3_port: 443
      dnscrypt_port: 8443
    tls:
      acme:
        enabled: true
        email: admin@example.com
        domains:
          - dns.example.com
```

---

## Client configuration

### Browser (Firefox / Chrome)

1. Open **Settings → Privacy → DNS over HTTPS**.
2. Set provider to **Custom** and enter `https://dns.example.com/dns-query`.
3. Accept the certificate warning if using a self-signed cert (not
   recommended for production).

### Android (Private DNS)

Go to **Settings → Network → Private DNS → Hostname of provider** and
enter `dns.example.com` (DoT, port 853).

### iOS / macOS (configuration profile)

Generate a `.mobileconfig` profile pointing to your DoH or DoT endpoint
using [Apple Configurator](https://support.apple.com/guide/apple-configurator-mac/)
or a tool such as [dnscrypt-proxy](https://github.com/DNSCrypt/dnscrypt-proxy).

### Linux (systemd-resolved)

```ini
# /etc/systemd/resolved.conf
[Resolve]
DNS=192.0.2.1
DNSOverTLS=yes
```

---

## Security note

Enabling DoH or DoT on skoed does not automatically force all clients to
use them. Clients that are not configured to use encrypted DNS will
continue to send plain DNS queries. To close this gap:

- Configure client devices to use DoH or DoT explicitly (see above).
- Add firewall rules that redirect or block plain DNS traffic from clients
  that should use encrypted DNS only.

See the M6 DoH-gap firewall rule generator (`GET /api/v1/firewall-rules`)
for ready-made rulesets for iptables, nftables, MikroTik, OPNsense, and
Ubiquiti UniFi.
