# M20 Demo Note — Cluster Security Hardening

## Implemented Scope

- **Token scoping**: `APIToken.Scopes []string` with `"read"`, `"write"`, `"cluster:admin"` values.
  `RequireScope()` middleware enforces scope on protected routes. Default tokens (`"read"`, `"write"`)
  cannot reach cert-rotation routes.
- **mTLS hot-reload** (`CertCache`): atomic pointer swaps in the Raft TLS layer allow cert
  rotation without dropping in-flight Raft connections.
- **`GET /api/v1/cluster/certs/status`**: returns CA expiry and per-node leaf cert expiry; served on
  any node, no forwarding required.
- **`POST /api/v1/cluster/certs/rotate`**: generates a fresh CA + per-node leaf certs, distributes
  them to all members via a new `OpCertRotation` Raft command, hot-swaps TLS configs atomically.
  Requires `cluster:admin` scope. Returns 202; rotation completes asynchronously.

## Acceptance Tests (4/4 pass)

| Test | FSID | Result |
|------|------|--------|
| TestCertStatusReturnsExpiry | FS-CertStatusExposesCertExpiry | PASS (2.3s) |
| TestCertRotateAccepted | FS-CertRotateTriggeredByAdmin | PASS (2.3s) |
| TestCertRotateMaintainsQuorum | FS-CertRotateRollingMaintainsQuorum | PASS (2.5s) |
| TestCertRotateRequiresAdmin | FS-CertRotateRequiresClusterAdminScope | PASS (1.7s) |

Full suite: **all green** (146s).

## Not Implemented in This Milestone

- Automatic cert-expiry alerts / webhook notifications (deferred to M22 Webhooks).
- Certificate revocation lists (CRL / OCSP stapling) — out of scope per spec non-goals.
- UI surface for cert status or rotation trigger — API-only in M20.
- Per-node cert rotation without full cluster rotation.

## Limitations

- Rotation is cluster-wide and atomic via Raft; partial rotation (one node) is not supported.
- If a node is offline during rotation, it will receive the new certs on next Raft catchup.
- `mTLS disabled` clusters return 503 on `/api/v1/cluster/certs/rotate` by design.
- `CertCache` hot-reload replaces the TLS config for new handshakes; existing long-lived
  Raft connections renegotiate at the next handshake boundary.
