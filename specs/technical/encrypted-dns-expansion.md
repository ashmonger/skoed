# Technical Specification: Encrypted DNS Expansion — DoH3 + DNSCrypt v2

x-tsid: TS-EncryptedDnsExpansion
x-fsid-links:
  - FS-Doh3ServerListens
  - FS-Doh3UsesTlsOneTwoThree
  - FS-Doh3AppliesFilter
  - FS-Doh3ServesLocalDns
  - FS-Doh3ForwardsUnmatched
  - FS-Doh3DisabledByDefault
  - FS-Doh3IndependentEnable
  - FS-DnscryptServerListens
  - FS-DnscryptAppliesFilter
  - FS-DnscryptServesLocalDns
  - FS-DnscryptForwardsUnmatched
  - FS-DnscryptStampPublished
  - FS-DnscryptKeyRotation
  - FS-DnscryptKeyReplicatedViaRaft
  - FS-DnscryptDisabledByDefault
  - FS-DnscryptIndependentEnable

---

## 1. Overview

M8 extends `apps/skoed/internal/dns/encrypted.go` (the M4 DoH/DoT server) with
two additional encrypted-DNS transports:

| Transport  | Protocol              | RFC / spec       | Default port |
|------------|-----------------------|------------------|-------------|
| DoH3       | DNS-over-HTTP/3 QUIC  | RFC 9230 + 8484  | 8443 UDP    |
| DNSCrypt v2| DNSCrypt v2 (XSalsa20)| DNSCrypt spec v2 | 5443 UDP    |

Both transports share the same `dns.Handler` that serves UDP/TCP/DoH/DoT today.
The `transportTaggedWriter` is extended with `"doh3"` and `"dnscrypt"` tags.

---

## 2. External dependencies (require UoR approval — Rule 11)

| Library                             | Version | Purpose                             |
|-------------------------------------|---------|-------------------------------------|
| `github.com/quic-go/quic-go`        | v0.48+  | QUIC transport + HTTP/3 server      |
| `github.com/ameshkov/dnscrypt/v2`   | v2.3+   | DNSCrypt v2 server (cert gen, parse)|

Both are MIT-licensed, well-maintained, and widely used in the DNS ecosystem.
`quic-go` is the de-facto Go QUIC implementation; `ameshkov/dnscrypt` is used
by AdGuard Home (the reference self-hosted DNS filter) as its DNSCrypt layer.

**Approval gate:** implementation MUST NOT start until UoR explicitly approves
both dependencies. Record approval in `decisions/` before `go get`.

---

## 3. Config schema additions (`config.Node`)

```yaml
node:
  dns:
    listen:
      doh3_port:     8443    # UDP; 0 = disabled
      dnscrypt_port: 5443    # UDP; 0 = disabled
    dnscrypt:
      cert_ttl_hours: 24     # keypair rotation interval; default 24h
```

The `doh3_port` and `dnscrypt_port` keys follow the same pattern as
`doh_port` and `dot_port` already in `config.Node.DNS.Listen`.

---

## 4. DoH3 implementation

### 4.1 Transport layer

`quic-go/http3.Server` wraps the existing M4 `net/http` handler (`dohHandler`).
The HTTP/3 server shares the same `tls.Config` as the DoH/DoT listeners
(TLS 1.3 only — QUIC mandates it).

```
UDP:<doh3_port>
  └─ quic-go listener (TLS 1.3)
       └─ http3.Server
            └─ http.Handler: /dns-query  ←── same handler as DoH
```

### 4.2 Client IP extraction

QUIC connections carry the peer address in the `net.Addr` from
`quic.Connection.RemoteAddr()`. The HTTP/3 server exposes this via
`r.RemoteAddr`; no X-Forwarded-For forwarding is done (same posture as DoH).

### 4.3 Transport tag

`transportTaggedWriter` gains `"doh3"`. Outcome suffixes follow the pattern:
`"forwarded-doh3"`, `"blocked-doh3"`, `"local-doh3"`, `"cached-doh3"`.

### 4.4 `EncryptedServer` changes

```go
type EncryptedServer struct {
    // existing fields …
    doh3Port int          // 0 = disabled
    doh3Srv  *http3.Server
}

func (s *EncryptedServer) startDoH3() error { … }
func (s *EncryptedServer) shutdownDoH3()    { … }
```

`UpdateHandler` is extended to swap the handler in the HTTP/3 server as well.

---

## 5. DNSCrypt v2 implementation

### 5.1 Protocol summary

DNSCrypt v2 encrypts queries with an ephemeral session key derived from
the server's long-term X25519 key and a client-chosen nonce. The server
certificate (containing the public key + validity window) is fetched by
clients in a plaintext "certificate query" before starting the encrypted
exchange.

### 5.2 Library usage (`ameshkov/dnscrypt/v2`)

```go
import dncrypt "github.com/ameshkov/dnscrypt/v2"

// Server cert (rotated every cert_ttl_hours):
cert, privateKey, err := dnscrypt.GenerateCert(resolverName, ttl)

// UDP server wrapping our dns.Handler:
srv := &dnscrypt.Server{
    ProviderName: "2.dnscrypt-cert.<node_id>",
    ResolverCert: cert,
    Handler:      dnscryptAdapter{handler},
}
srv.ListenAndServe(addr)
```

`dnscryptAdapter` bridges `ameshkov/dnscrypt`'s `Handler` interface to
`miekg/dns.Handler`, injecting the `"dnscrypt"` transport tag.

### 5.3 Keypair storage and Raft replication

The DNSCrypt server keypair (cert + private key) is stored in a new bbolt
bucket `dnscrypt_keys`. A new FSM command `CmdDNSCryptKeysSet` replicates it.

```go
type DNSCryptKeys struct {
    Cert       []byte    `json:"cert"`       // serialised dnscrypt.Cert
    PrivateKey []byte    `json:"private_key"` // 32-byte X25519 scalar (encrypted at rest?)
    CreatedAt  time.Time `json:"created_at"`
    ExpiresAt  time.Time `json:"expires_at"`
}
```

Only the **leader** generates new keypairs. On expiry (checked on each
`onApply` and on a 1-minute ticker on the leader), the leader generates a
new keypair and applies `CmdDNSCryptKeysSet`. Followers receive it via
normal Raft replication.

### 5.4 Stamp construction (FS-DnscryptStampPublished)

An `sdns://` stamp is constructed per RFC from:
- Protocol version (0x01 = DNSCrypt)
- Server address (`<ip>:<port>`)
- Provider name (`2.dnscrypt-cert.<node_id>`)
- Public key fingerprint (SHA-256 of cert's resolver public key)

The stamp is exposed via `GET /api/v1/settings` in a new top-level
`dnscrypt_stamp` field (read-only; computed on-the-fly from the current
keypair). It is also returned in `GET /api/v1/cluster/self` for convenience.

### 5.5 Key rotation transition

Before replacing the active cert, the server briefly serves **both** the old
and new cert in the certificate query response. The `ameshkov/dnscrypt`
library supports this natively via `Server.ResolverCert` slice. The old cert
continues to validate in-flight sessions during a 60-second grace window, then
is removed.

### 5.6 Private key security

The private key bytes are stored in bbolt unencrypted at this milestone.
A future milestone (M8.1 or M9) may add envelope encryption via a node-local
passphrase. This is noted as a known limitation in the demo note.

---

## 6. Config API changes

### `GET /api/v1/settings`

Adds two new read-only fields in the response:

```json
{
  "node": {
    "dns": {
      "listen": {
        "doh3_port": 8443,
        "dnscrypt_port": 5443
      }
    }
  },
  "dnscrypt_stamp": "sdns://AQcAAAAAAAAADzEwLjAuMC4xOjU0NDM…"
}
```

No new `PATCH /api/v1/settings` fields — port changes require a node restart
(same as `doh_port` and `dot_port`).

---

## 7. Audit log entries

DNSCrypt and DoH3 are DNS transports — they do not generate management-API
audit entries. The query log already distinguishes transports via the
outcome suffix, which is sufficient.

---

## 8. Acceptance test strategy

- **DoH3 tests**: use `quic-go/http3.Client` in the test harness to send
  DNS-over-HTTP/3 queries to the running node. Verify filter application,
  local DNS, forwarding, and query-log outcome.
- **DNSCrypt tests**: use `ameshkov/dnscrypt/v2` client to send encrypted
  queries. Verify filter application, local DNS, forwarding, stamp content,
  and that keypair rotation causes seamless reconnect (no dropped queries).
- Both test types run inside Docker (per project feedback rules).

---

## 9. Delivery sequence

1. Add `doh3_port` + `dnscrypt_port` + `dnscrypt.cert_ttl_hours` to config schema.
2. Implement `startDoH3` in `encrypted.go` using `quic-go/http3`.
3. Add `CmdDNSCryptKeysSet` FSM command + `dnscrypt_keys` bucket to cluster/store.
4. Implement `DNSCryptServer` wrapper in `dns/dnscrypt.go`.
5. Implement leader-side keypair rotation ticker.
6. Expose `dnscrypt_stamp` in settings API.
7. Write acceptance tests.
8. Demo.
