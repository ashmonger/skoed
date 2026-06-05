# DEMO NOTE — M4 ACME / Let's Encrypt integration

## Scope

dblock obtains its DoH and DoT TLS certificate automatically via the
ACME protocol (Let's Encrypt by default) instead of relying on the
self-signed fallback. autocert handles directory discovery, account
registration, ordering, HTTP-01 challenge serving, cert caching, and
lazy renewal.

### Implemented

- New per-node config block `node.dns.tls.acme.*`:
  - `enabled` — toggle
  - `email` — ACME account contact
  - `domains` — FQDNs the cert covers (HostPolicy whitelist)
  - `directory_url` — override (LE production by default, supports
    LE staging, Pebble, step-ca, internal CAs)
  - `http_challenge_port` — HTTP-01 listener port (80 in prod; the
    harness uses a free port for tests)
- `apps/dblock/internal/dns/acme.go` — `AcmeManager` wraps
  `golang.org/x/crypto/acme/autocert.Manager`. Cert cache lands at
  `<data_dir>/tls/acme-cache/` and survives restarts.
- Self-signed fallback: when ACME is enabled, the M4 self-signed cert
  is still generated and used as a fallback by the `GetCertificate`
  callback. If the ACME directory is unreachable, DoH/DoT keep
  serving the self-signed cert and log the failure — no startup
  hang, no client-visible breakage.
- The HTTP-01 challenge listener binds on a separate port from
  DoH/DoT/DNS so it can be exposed to the public internet without
  cracking open the rest of dblock.
- 4 acceptance tests cover: enabled-from-config boots cleanly, the
  HTTP-01 listener answers, the cache directory is created, and the
  fallback works when the ACME directory is unreachable.

### Not implemented (explicit non-goals)

- **DNS-01 challenge** — needs a per-provider plugin. Operators can
  use a sidecar like `lego` writing to `cert_file` + `key_file`
  instead, then leave `acme.enabled=false`.
- **Wildcard certs** — require DNS-01.
- **Multiple-issuer support** — one CA at a time per node.
- **External Account Binding (EAB)** — deferred.

### Test strategy

The acceptance suite does NOT exercise the live ACME flow (would need
a real CA or Pebble running as a sidecar). It verifies the wiring:
config plumbed correctly, listeners bind, cache dir created, fallback
honoured when the directory is unreachable. The live flow is covered
by the demo recipe below.

**Important:** the acceptance suite must be run in Docker, not on the
host — see `tests/acceptance/run-in-docker.sh` and the in-repo
memory note on docker isolation.

## Demo recipe — live ACME against Pebble

[Pebble](https://github.com/letsencrypt/pebble) is Let's Encrypt's tiny
ACME test server. It runs as a single container and issues real (but
short-lived, untrusted-by-default) certs.

```bash
# 1. Build the dblock image
docker build -t dblock:m4-acme -f apps/dblock/Dockerfile apps/dblock

# 2. Create a private network so dblock can reach Pebble by name
docker network create m4-acme

# 3. Start Pebble — listens on :14000 (directory) and :15000 (mgmt)
docker run -d --name pebble \
  --network m4-acme --ip 172.20.0.10 \
  -p 14000:14000 -p 15000:15000 \
  -e PEBBLE_VA_NOSLEEP=1 \
  letsencrypt/pebble:latest pebble -dnsserver 127.0.0.1:53

# 4. Configure dblock to use the Pebble directory.  Note:
#    - acme.domains must resolve to the dblock container's IP from
#      Pebble's perspective; here we use a host file mapping.
#    - http_challenge_port: 8080 lets Pebble reach us without root.
cat > /tmp/dblock-acme.yaml <<EOF
node:
  id: acme-demo
  raft_address: 172.20.0.20:7000
  api_address:  172.20.0.20:8080
  data_dir:     /var/lib/dblock
  dns:
    listen:
      port: 53
      ipv4: true
      ipv6: false
      doh_port: 8443
      dot_port: 8853
    tls:
      acme:
        enabled: true
        email: ops@example.test
        domains: ["dns.example.test"]
        directory_url: "https://172.20.0.10:14000/dir"
        http_challenge_port: 8080
EOF

# 5. Start dblock on the same network with /etc/hosts pointing at
#    dns.example.test → its own container IP (so Pebble's HTTP-01
#    challenge GET resolves correctly).
docker run -d --name dblock-acme \
  --network m4-acme --ip 172.20.0.20 \
  --add-host=dns.example.test:172.20.0.20 \
  -v /tmp/dblock-acme.yaml:/etc/dblock/config.yaml:ro \
  dblock:m4-acme --config /etc/dblock/config.yaml

# 6. Watch the ACME flow
docker logs -f dblock-acme
#   → "ACME enabled (directory=https://172.20.0.10:14000/dir ...)"
#   → "acme: GetCertificate(\"dns.example.test\") failed ..." (on cold-start handshake)
#   → "acme: order completed, cert cached"

# 7. Verify Pebble issued the cert (use the Pebble root cert from
#    https://github.com/letsencrypt/pebble/blob/main/test/certs/pebble.minica.pem)
docker exec dblock-acme \
  openssl s_client -connect dns.example.test:8443 \
    -servername dns.example.test \
    -CAfile /etc/pebble/pebble.minica.pem </dev/null 2>&1 | grep 'issuer='
#   → issuer=O=Pebble Intermediate CA, CN=Pebble Intermediate CA ...
```

## Cleanup

```bash
docker rm -f dblock-acme pebble
docker network rm m4-acme
```

## What's next

- M3.6 — read-only DHCP integration for client identity. Test design
  drafted (`decisions/20260604-M36DhcpTestDesign.md`); waiting on UoR
  to confirm the 5 open questions before implementation.
- M5 — production hardening (Prometheus, audit log, blocklist
  refresh, multi-arch release, in-place upgrade).
