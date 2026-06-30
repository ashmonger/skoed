# M34 — Certificate Management

## Implemented

### TLS Settings API
- `PUT /api/v1/settings/tls` — sets auto-renewal config cluster-wide via Raft
  - Fields: `auto_renew` (bool), `renewal_threshold_days` (int, default 30), `acme.domains` ([]string), `acme.email` (string)
  - Persisted to bbolt `tls_renew_config` bucket, replicated to all nodes
- `GET /api/v1/settings` — includes `tls` sub-object when TLS renew config is present

### Certificate Info
- `GET /api/v1/tls/info` — returns current cert expiry, domains, issuer, and days-until-expiry
- `POST /api/v1/tls/renew` — triggers immediate ACME renewal (Let's Encrypt); returns `{"renewed": true}` on success

### ACME Auto-Renewal
- Background `TLSRenewer` goroutine: checks cert expiry on startup and daily; renews when within `renewal_threshold_days`
- Supports Let's Encrypt (production and staging) via `golang.org/x/crypto/acme`
- New cert + key written atomically; TLS listener hot-swapped without restart
- Renewal events logged to `logs/tls-renewal.log`

### Rolling Upgrade Safety
- Cert files remain intact across rolling upgrades (data dir preserved)
- New node joining cluster fetches TLS config via Raft sync on first startup

### Web UI
- Cluster page "Software Update" card shows current TLS cert expiry
- Settings page "TLS" section: toggle auto-renewal, set threshold days, manage ACME domains and email

## Not Implemented / Limitations

- No OCSP stapling (see roadmap future items)
- No certificate pinning
- DNS-01 challenge not supported (HTTP-01 only); wildcard certs not obtainable
- UI for certificate revocation not implemented

## Acceptance Tests
11/11 pass: `TestM34*`
