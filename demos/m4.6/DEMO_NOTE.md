# DEMO NOTE — M4.6 HTTPS Management API

## Scope

Enables TLS on the management API port. Operators can either provide their own certificate or use ACME (Let's Encrypt) for automatic provisioning.

### Implemented

- **TLS on management API**: optional `node.api.tls.*` config block
- **BYO certificate**: `cert_file` + `key_file` paths in config
- **ACME auto-provision**: via `certmagic` library; domains validated via HTTP-01 challenge
- **Redirect**: HTTP → HTTPS redirect when TLS is active
- **mTLS support**: optional `client_ca_file` to require mutual TLS for management API clients

### Not implemented

- ACME DNS-01 challenge provider
- Certificate pinning

## Demo

```yaml
node:
  api:
    tls:
      cert_file: /path/to/cert.pem
      key_file:  /path/to/key.pem
```

## Limitations

ACME requires a publicly routable domain and port 80 reachable. Home deployments typically use BYO cert or plain HTTP.
