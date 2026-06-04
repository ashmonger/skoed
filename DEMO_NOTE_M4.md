# DEMO NOTE — M4 dblock as a DoH/DoT server

## Scope

dblock now serves DNS over HTTPS (RFC 8484, `/dns-query`) and DNS over TLS
(RFC 7858, port 853 by convention). Every query that flows through DoH or
DoT goes through the **same** filter, allowlist, local-DNS, and query-log
pipeline as plain UDP/TCP DNS — the "fight against DoH" turns into "we
serve DoH, just point your device at us".

### Implemented

- **DoH** on a configurable HTTPS port (`node.dns.listen.doh_port`):
  - `POST /dns-query` with `Content-Type: application/dns-message`
  - `GET /dns-query?dns=<base64url-encoded-query>`
  - Returns the wire-format DNS response with
    `Content-Type: application/dns-message`.
- **DoT** on a configurable TLS port (`node.dns.listen.dot_port`):
  - RFC 7858 framing (2-byte length prefix + DNS wire message).
  - Multiple back-to-back queries per TLS connection (RFC 7766).
- **TLS cert lifecycle**:
  - If `node.dns.tls.cert_file` + `key_file` both exist, those are used
    verbatim.
  - Otherwise: a self-signed ECDSA P-256 cert is generated at
    `<data_dir>/tls/cert.pem` + `key.pem` on first boot and reused on
    every subsequent boot. CN = node_id, SAN includes node_id +
    "localhost", 10-year validity.
- **Query-log outcome suffix**: a query served over DoH gets
  `outcome: blocked-doh` / `forwarded-doh` / `cached-doh` / `local-doh`
  in the log; DoT gets the `-dot` suffix. Plain UDP/TCP queries keep
  the bare outcome strings — no breakage for analytics that already
  match those.
- **Disabled by default**: both `doh_port` and `dot_port` default to 0
  (unset), which skips the EncryptedServer entirely. Existing M1–M3.5
  deployments behave identically after this upgrade.

### Acceptance tests

7 tests, all green:

| Test                                          | FSID                          | Result |
|-----------------------------------------------|-------------------------------|--------|
| `TestDohServerListensPostAndGet`              | FS-DohServerListens           | PASS   |
| `TestDotServerListens`                        | FS-DotServerListens           | PASS   |
| `TestDohAppliesFilter`                        | FS-DohAppliesFilter           | PASS   |
| `TestDotAppliesFilter`                        | FS-DotAppliesFilter           | PASS   |
| `TestDohSelfSignedCertOnFirstBoot`            | FS-DohSelfSignedCert          | PASS   |
| `TestDohDisabledByDefault`                    | FS-DohDisabledByDefault       | PASS   |
| `TestDotDisabledByDefault`                    | FS-DotDisabledByDefault       | PASS   |

Full suite (`go test ./tests/acceptance/...`): 420s, 100% green.

### Not implemented (UoR-approved deferrals)

- **ACME / Let's Encrypt auto-renewal** — operator runs `certbot` (or
  similar) and points `cert_file` / `key_file` at the issued PEMs.
- **HTTP/3 (DoH3)** — clients are slow to adopt; defer.
- **DNSCrypt** — rare in modern clients.
- **Client-cert authentication on DoH/DoT** — orthogonal to filtering.
- **Per-connection rate limiting** — defer to a reverse proxy if
  needed.

## Demo recipe

```bash
# 1. Build
make -C apps/dblock build

# 2. Per-node config with DoH + DoT enabled
cat > /tmp/m4-config.yaml <<'EOF'
node:
  id: demo
  raft_address: 127.0.0.1:17003
  api_address:  127.0.0.1:18083
  data_dir:     /tmp/dblock-m4-demo/data
  dns:
    listen:
      port: 5356
      ipv4: true
      ipv6: false
      doh_port: 8443
      dot_port: 8853
EOF
mkdir -p /tmp/dblock-m4-demo/data

# 3. Boot — self-signed cert generated on first start
DBLOCK_TEST_MODE=1 ./apps/dblock/dblock -config /tmp/m4-config.yaml &
# Logs:
#  DNS server listening on :5356 (mode=forwarding)
#  DoH server listening on :8443 (cert=/tmp/dblock-m4-demo/data/tls/cert.pem)
#  DoT server listening on :8853 (cert=/tmp/dblock-m4-demo/data/tls/cert.pem)

# 4. Set admin + add a tiny blocklist
curl -sX POST http://127.0.0.1:18083/api/v1/auth/setup \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"demopass123"}'

curl -s -u admin:demopass123 -X POST http://127.0.0.1:18083/api/v1/blocklists \
  -H 'Content-Type: application/json' \
  -d '{"id":"m4","name":"M4 demo","enabled":true,
       "source":{"type":"inline"},"domains":["ads.example.com"]}'

# 5. Query over DoH — base64url-encoded wire message
QUERY=$(python3 -c "import dns.message,base64; \
  print(base64.urlsafe_b64encode(dns.message.make_query('ads.example.com','A').to_wire()).rstrip(b'=').decode())")
curl -sk "https://127.0.0.1:8443/dns-query?dns=$QUERY" | \
  python3 -c "import dns.message,sys; print('Rcode:', dns.message.from_wire(sys.stdin.buffer.read()).rcode())"
# → Rcode: 3   (NXDOMAIN — filter applied)

# 6. Query over DoT (RFC 7858 framing)
python3 <<'PYEOF'
import socket, ssl, struct, dns.message
m = dns.message.make_query('ads.example.com', 'A').to_wire()
ctx = ssl.create_default_context(); ctx.check_hostname=False; ctx.verify_mode=ssl.CERT_NONE
s = ctx.wrap_socket(socket.create_connection(('127.0.0.1', 8853), timeout=5))
s.send(struct.pack('>H', len(m)) + m)
n = struct.unpack('>H', s.recv(2))[0]
reply = dns.message.from_wire(s.recv(n))
print('DoT Rcode:', reply.rcode())   # → 3
PYEOF

# 7. Verify the query log split by transport
curl -s -u admin:demopass123 'http://127.0.0.1:18083/api/v1/query-log?domain=ads.example.com&limit=5' | \
  python3 -c "import json,sys; [print(e['outcome'], e['domain']) for e in json.load(sys.stdin)['entries']]"
# → blocked-dot ads.example.com
# → blocked-doh ads.example.com
```

## Cluster behavior

In multi-node deployments, every node serves DoH/DoT on **its own** listen
address (the listen ports are node-local, not Raft-replicated). Clients
can target any node — replicated filtering state means the answer is the
same. No leader-pinning is required for the DoH/DoT paths.

## Cert deployment notes

- For a public deployment, point `node.dns.tls.cert_file` and `key_file`
  at the cert+key issued by your ACME client (certbot, lego, etc.).
  Rotate by writing new files and HUP'ing the process (next start picks
  them up). Hot reload of TLS material is a future improvement —
  filed for M5 production-hardening.
- The default self-signed cert is intentionally a 10-year CA-marked
  certificate so it works as both leaf and trust anchor — operators
  who want strict EE/CA separation should supply their own PKI.

## What's next

Roadmap M5 — production hardening (monitoring, automated upgrades,
alerting). All M1–M4 capabilities are in place.
