# HTTPS for the management API

By default the management API listens on plain HTTP. This page explains how
to enable HTTPS for the API using ACME / Let's Encrypt, a manually supplied
certificate, or a self-signed certificate generated automatically by skoed.

The TLS settings live under `node.api.tls` in `config.yaml`.

---

## ACME / Let's Encrypt

ACME is the recommended option for nodes that are reachable from the public
internet or from clients that need a trusted certificate.

```yaml
node:
  api_address: ":443"      # or ":8443" if you can't bind 443
  api:
    tls:
      enabled: true
      mode: single_port    # default; serves HTTPS only on api_address
```

ACME certificates are configured under `node.dns.tls.acme` and shared
between the management API, DoH, DoT, and DoH3 listeners:

```yaml
node:
  dns:
    tls:
      acme:
        enabled: true
        email: admin@example.com
        domains:
          - skoed.example.com
        # Optional: override the ACME directory (default = Let's Encrypt production)
        # directory_url: https://acme-staging-v02.api.letsencrypt.org/directory
        # HTTP-01 challenge port (default 80; requires root or CAP_NET_BIND_SERVICE)
        http_challenge_port: 80
```

**How it works:**

1. On first boot skoed registers with Let's Encrypt (or the configured
   ACME directory) using the supplied email address.
2. It responds to HTTP-01 challenges on `http_challenge_port` (default 80).
3. The certificate is cached under `<data_dir>/tls/acme-cache/` and
   renewed automatically before expiry.
4. While ACME has not yet issued the first certificate (or when the ACME
   server is unreachable) skoed falls back to the self-signed certificate
   so the listener stays up.

> **DNS requirement:** the domain listed in `acme.domains` must resolve to
> the node's public IP address before the HTTP-01 challenge can succeed.

---

## Manual certificate

Supply existing PEM files when you manage certificates through an external
CA, internal PKI, or a wildcard certificate:

```yaml
node:
  api:
    tls:
      enabled: true
  dns:
    tls:
      cert_file: /etc/skoed/tls/cert.pem
      key_file:  /etc/skoed/tls/key.pem
```

Both `cert_file` and `key_file` must be set. Renewal is the operator's
responsibility; reload skoed after replacing the files.

---

## Self-signed certificate

If neither ACME nor manual cert paths are configured, skoed generates a
self-signed ECDSA P-256 certificate on first boot and stores it under
`<data_dir>/tls/`. The certificate is reused on subsequent starts.

This requires no configuration beyond enabling TLS:

```yaml
node:
  api:
    tls:
      enabled: true
```

Browsers will show a "Your connection is not private" warning. You can
suppress it by:

- Installing the self-signed certificate in your browser or OS trust store.
- Using the ACME option for a publicly trusted certificate.

---

## Dual-port mode

By default (`mode: single_port`) the API serves HTTPS only on
`api_address`, and plain HTTP requests on the same port receive a `308
Permanent Redirect` to the HTTPS URL.

To keep plain HTTP on the original address and add a separate HTTPS
listener, use `mode: dual_port`:

```yaml
node:
  api_address: ":8080"     # plain HTTP kept here
  api:
    tls:
      enabled: true
      mode: dual_port
      https_address: ":8443"
```

---

## HSTS

HTTP Strict Transport Security is off by default. Enable it only when all
clients will always reach the API over HTTPS — HSTS is hard to reverse
once a browser has cached the policy.

```yaml
node:
  api:
    tls:
      enabled: true
      hsts: true
```

---

## Port reference

| Setting | Default | Notes |
|---------|---------|-------|
| `node.api_address` | `:8080` | Plain HTTP (or HTTPS when `tls.enabled`) |
| `node.api.tls.https_address` | (empty) | HTTPS address in `dual_port` mode |
| `node.api.tls.mode` | `single_port` | `single_port` or `dual_port` |
| `node.dns.tls.acme.http_challenge_port` | `80` | HTTP-01 challenge port |

---

## Full example

```yaml
node:
  id: skoed-01
  api_address: ":443"
  data_dir: /var/lib/skoed
  api:
    tls:
      enabled: true
      mode: single_port
      hsts: false
  dns:
    tls:
      acme:
        enabled: true
        email: admin@example.com
        domains:
          - skoed.example.com
        http_challenge_port: 80
```
