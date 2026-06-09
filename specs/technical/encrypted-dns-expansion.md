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
| `github.com/quic-go/quic-go`        | v0.60.0 | QUIC transport + HTTP/3 server      |
| `github.com/ameshkov/dnscrypt/v2`   | v2.4.0  | DNSCrypt v2 server (cert gen, parse)|

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
      doh3_port:     8443    # UDP; 0 = disabled (default)
      dnscrypt_port: 5443    # UDP; 0 = disabled (default)
    dnscrypt:
      cert_ttl_hours: 8760   # keypair TTL in hours; 0 = library default (1 year)
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
// Shutdown() calls doh3Srv.Shutdown(ctx) inline — no separate shutdownDoH3 helper.
```

`UpdateHandler` swaps the shared `swappableHandler`; all transports (DoH, DoT, DoH3)
see the new handler automatically because they all hold a pointer to the same wrapper.
No HTTP/3-specific handler-swap logic is required.

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
import dnscrypt "github.com/ameshkov/dnscrypt/v2"

// Keypair generation (leader only):
rc, err := dnscrypt.GenerateResolverConfig(providerName, nil /* = generate new key */)
cert, err := rc.CreateCert()   // produces the signed cert from the resolver config

// Serialize for Raft replication and bbolt storage:
configJSON, _ := json.Marshal(rc)   // stores the full ResolverConfig (incl. private key)

// UDP server wrapping our dns.Handler:
srv := &dnscrypt.Server{
    ProviderName: rc.ProviderName,
    ResolverCert: cert,
    Handler:      &dnscryptHandlerBridge{wrapper: swappable},
}
conn, _ := net.ListenUDP("udp", &net.UDPAddr{Port: port})
go srv.ServeUDP(conn)   // ServeUDP blocks; runs in its own goroutine
```

`dnscryptHandlerBridge` bridges `ameshkov/dnscrypt`'s `Handler` interface to
`miekg/dns.Handler`, injecting the `"dnscrypt"` transport tag.
`dnscryptResponseWriter` adapts `dnscrypt.ResponseWriter` (3 methods) to the
full `miekg/dns.ResponseWriter` interface (8 methods); the extra methods are no-ops.

### 5.3 Keypair storage and Raft replication

The DNSCrypt server keypair is stored as a JSON-serialised `ResolverConfig` in a
new bbolt bucket `dnscrypt_keys`. A new FSM command `CmdDNSCryptKeysSet` replicates it.

```go
type DNSCryptKeys struct {
    Config    string    `json:"config"`      // JSON-marshalled dnscrypt.ResolverConfig
    CreatedAt time.Time `json:"created_at"`
    ExpiresAt time.Time `json:"expires_at"`
}
```

Storing the full `ResolverConfig` (rather than splitting cert and private key into
separate byte fields) lets the library round-trip its own types without any custom
serialisation logic. The private key is embedded in the marshalled JSON.

Only the **leader** generates new keypairs. Rotation works in two phases:

1. **Initial key generation** — a goroutine started at boot retries every 30 s
   until this node becomes leader, then calls `GenerateResolverConfig` + `SetDNSCryptKeys`
   exactly once to seed the cluster.
2. **Ongoing rotation** — an hourly ticker on the leader checks whether the current
   cert is within 10 % of its TTL. If so, a new keypair is generated and applied
   via `CmdDNSCryptKeysSet`. Followers receive it through normal Raft log replication.

When a follower node that has `dnscrypt_port > 0` receives a new `CmdDNSCryptKeysSet`
log entry and `dnscryptSrv == nil` (server not yet started), the `rebuildDNS`
callback attempts to start the DNSCrypt server at that moment (deferred start).

### 5.4 Stamp construction (FS-DnscryptStampPublished)

An `sdns://` stamp is constructed per RFC from:
- Protocol version (0x01 = DNSCrypt)
- Server address (`<ip>:<port>`)
- Provider name (`2.dnscrypt-cert.<node_id>`)
- Public key fingerprint (SHA-256 of cert's resolver public key)

The stamp is exposed via `GET /api/v1/settings` in a new top-level
`dnscrypt_stamp` field (read-only; computed on-the-fly from the current keypair).
`dnscrypt_stamp` is absent from the response when no keypair exists yet or when
`dnscrypt_port` is 0.

### 5.5 Key rotation transition

**Not implemented in M8.** The leader replaces the keypair by shutting down the
old `DNSCryptServer` and starting a new one with the rotated cert. In-flight
sessions from clients still using the old cert will fail until those clients
re-fetch the certificate (which their resolver is designed to do on any
decryption error).

Serving both old and new certs during a grace window (the `ameshkov/dnscrypt`
library does support a `ResolverCert` slice for this) is deferred to a future
milestone. This limitation is documented in `demos/m8/DEMO_NOTE.md`.

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
