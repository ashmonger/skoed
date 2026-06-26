# TS-CertificateManagement — Certificate Management Technical Specification

<!-- x-tsid: TS-CertificateManagement -->
<!-- x-fsid-links: [FS-CertStatusApiReturnsExpiry, FS-CertStatusShowsAutoRenewConfig, FS-AcmeAutoRenewalEnabled, FS-AcmeAutoRenewalSkipsValidCert, FS-AcmeAutoRenewalDisabledByDefault, FS-AcmeConfigPersisted, FS-PerNodeCertRotation, FS-PerNodeCertRotationUnknownNode, FS-CertStatusVisibleInSettingsUi, FS-AutoRenewToggleInSettingsUi, FS-RotateNowButtonInSettingsUi] -->

## Endpoints

### Existing (unchanged behaviour, extended response)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/api/v1/cluster/certs/status` | Bearer | Returns cluster mesh CA + per-node cert expiry. **Extended** in M34: adds `auto_renew`, `acme_domains`, `days_until_expiry` per node. |
| POST | `/api/v1/cluster/certs/rotate` | Bearer (leader) | Cluster-wide mesh cert rotation. Unchanged. |

### New in M34

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/api/v1/cluster/nodes/{node_id}/rotate-cert` | Bearer (leader) | Rotate the mesh cert for a single node without affecting others. Returns 202 on success, 404 if node unknown. |
| GET | `/api/v1/settings` | Bearer | **Extended**: adds `tls` sub-object (see Settings schema below). |
| PUT | `/api/v1/settings` | Bearer (leader) | **Extended**: accepts `tls` sub-object to configure auto-renew + ACME domains. |

---

## Response schemas

### GET /api/v1/cluster/certs/status (extended)

```json
{
  "ca_expires_at": "2027-06-26T00:00:00Z",
  "ca_days_until_expiry": 365,
  "auto_renew": false,
  "acme_domains": [],
  "nodes": [
    {
      "node_id": "skoed-1",
      "cert_expires_at": "2027-06-26T00:00:00Z",
      "days_until_expiry": 365,
      "rotation_pending": false
    }
  ]
}
```

Fields added vs current response:
- `ca_days_until_expiry` int — `floor((ca_expires_at - now) / 24h)`
- `auto_renew` bool — mirrors `tls.auto_renew` from Raft-replicated settings
- `acme_domains` []string — mirrors `tls.acme.domains`
- `days_until_expiry` int per node — same derivation as CA field

### POST /api/v1/cluster/nodes/{node_id}/rotate-cert

Request body: empty.

Responses:
- `202 Accepted` — rotation scheduled; node regenerates its leaf cert and pushes the new cert to the CA via Raft
- `404 Not Found` — `{"error": "node not found"}`
- `503 Service Unavailable` — `{"error": "mTLS is not enabled on this cluster"}`
- `307 Temporary Redirect` — forwarded to leader (same pattern as other write endpoints)

### Settings TLS sub-object (GET + PUT /api/v1/settings)

```json
{
  "tls": {
    "auto_renew": false,
    "renewal_threshold_days": 30,
    "acme": {
      "domains": [],
      "email": ""
    }
  }
}
```

PUT accepts partial update — omitting `tls` leaves current TLS settings unchanged.
`renewal_threshold_days` defaults to 30; valid range 7–90.

---

## Config changes

### `config.Config` (Raft-replicated)

Add `TLS TLSRenewConfig` field under `Config`:

```go
type TLSRenewConfig struct {
    AutoRenew             bool     `yaml:"auto_renew"              json:"auto_renew"`
    RenewalThresholdDays  int      `yaml:"renewal_threshold_days"  json:"renewal_threshold_days"`
    ACME                  ACMERenewConfig `yaml:"acme"            json:"acme"`
}

type ACMERenewConfig struct {
    Domains []string `yaml:"domains" json:"domains"`
    Email   string   `yaml:"email"   json:"email"`
}
```

`TLSRenewConfig` is cluster-replicated (stored in bbolt `config` bucket, synced via Raft). It controls the background renewal job on every node — all nodes share the same ACME domain list and threshold.

---

## ACME auto-renewal background job

### Trigger conditions

The renewal job runs on a 12-hour tick. On each tick:

1. Read `TLSRenewConfig` from the Raft snapshot.
2. If `auto_renew == false` → skip.
3. For each configured ACME domain, check the current cert expiry via `autocert.Manager.GetCertificate`.
4. If `days_until_expiry <= renewal_threshold_days` → call `autocert.Manager` to force renewal.
5. On success: reload the cert into the running TLS server via `tls.Config.GetCertificate` (no restart needed — autocert handles this).
6. Log `tls: renewed cert for <domain>, expires <date>`.

### Skips and guards

- Job only runs on nodes where `node.api.tls.acme.enabled == true` (node-local config). Cluster-level `auto_renew` is the operator intent; node-local ACME config is the mechanism.
- If the ACME HTTP-01 challenge server is not reachable on port 80 (or the configured challenge port), the job logs a warning and retries on the next tick — it does NOT crash.
- Cert with `days_until_expiry > renewal_threshold_days` → skip silently (`FS-AcmeAutoRenewalSkipsValidCert`).

```
AcmeRenewalJob.Start()
  └─ goroutine: loop()
       ├─ ticker: 12h
       └─ onTick():
            1. cfg = app.GetTLSRenewConfig()       // from Raft snapshot
            2. if !cfg.AutoRenew → return
            3. for domain in cfg.ACME.Domains:
                 cert = acmeMgr.GetCertificate(domain)
                 if daysUntil(cert.Leaf.NotAfter) <= cfg.RenewalThresholdDays:
                   acmeMgr.renewNow(domain)        // forces autocert renewal
```

---

## Per-node cert rotation

Existing `POST /api/v1/cluster/certs/rotate` rotates ALL nodes in one Raft command.

New `POST /api/v1/cluster/nodes/{node_id}/rotate-cert`:

1. Validate `node_id` exists in the current cluster membership.
2. Generate a new leaf cert for that node using the cluster CA.
3. Store the new cert via a targeted Raft command `RotateNodeCert{NodeID: id, Cert: pem, Key: pem}`.
4. The target node's mTLS reload hook (`mtls_reload.go`) picks up the new cert from the store on next snapshot application.
5. Return 202 immediately — rotation completes asynchronously (typically <500ms on LAN).

---

## Settings UI changes

### Location: Settings page → new "TLS Certificates" section

Rendered between the DNS Upstream section and the Auth section.

```
┌─ TLS Certificates ────────────────────────────────────────────┐
│  Cluster mesh CA     expires 2027-06-26  (365 days)           │
│                                                                │
│  Node           Cert expires          Days    Action          │
│  skoed-1        2027-06-26            365     [Rotate now]    │
│  skoed-2        2027-06-26            365     [Rotate now]    │
│  skoed-3        2027-06-26            365     [Rotate now]    │
│                                                                │
│  Auto-renew  [toggle: off]                                    │
│  ACME domain  ____________________________________________    │
│  Contact email ___________________________________________    │
│  Renewal threshold  [30] days                                  │
│  [Save TLS settings]                                           │
└────────────────────────────────────────────────────────────────┘
```

- Section only rendered when `GET /api/v1/cluster/certs/status` returns 200 (i.e. mTLS is enabled).
- If mTLS is disabled, section shows: "mTLS is not enabled — enable it in node.yaml to manage certificates."
- "Rotate now" calls `POST /api/v1/cluster/nodes/{id}/rotate-cert` and refreshes the section on success.
- Auto-renew toggle + domain/email/threshold fields PUT to `/api/v1/settings` on save.
- Days remaining shown with colour coding: green ≥ 60, yellow 30–59, red < 30.

---

## Sequence: auto-renewal flow

```
AcmeRenewalJob (12h tick)
  │
  ├─ GET TLSRenewConfig from bbolt (local read)
  │   └─ auto_renew=true, domains=["skoed.example.com"], threshold=30
  │
  ├─ GetCertificate("skoed.example.com")
  │   └─ cert.Leaf.NotAfter = 15 days → threshold exceeded
  │
  ├─ autocert.Manager.GetCertificate (forces renewal)
  │   ├─ ACME server: POST /acme/new-order
  │   ├─ HTTP-01 challenge: GET /.well-known/acme-challenge/{token}
  │   │   └─ skoed serves challenge response on port 80 (or redirect port)
  │   └─ ACME server: POST /acme/finalize → new cert DER
  │
  ├─ tls.Config.GetCertificate hot-reloads new cert (no restart)
  └─ log "tls: renewed cert for skoed.example.com, expires 2026-09-26"
```

---

## Non-goals (restated from functional spec)

- OCSP stapling
- ACME DNS-01 provider (Cloudflare, Route53)
- HSM/TPM key storage
- External CA integration
- Multi-domain SAN certs beyond hostname+IP
- Client cert auth on the management API
