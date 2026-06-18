# Cluster Security Hardening — Technical Specification

x-tsid: TS-ClusterSecurityHardening
x-fsid-links: [FS-CertStatusExposesCertExpiry, FS-CertRotateTriggeredByAdmin, FS-CertRotateRollingMaintainsQuorum, FS-CertRotateRequiresClusterAdminScope]

## Endpoints

### GET /api/v1/cluster/certs/status

Returns the current certificate expiry for the cluster CA and each known node.

**Auth:** requires `cluster:admin` scope (session or bearer token).

**Response 200:**
```json
{
  "ca_expires_at": "2036-06-18T00:00:00Z",
  "nodes": [
    {
      "node_id": "node-1",
      "cert_expires_at": "2027-06-18T00:00:00Z",
      "rotation_pending": false
    }
  ]
}
```

**Response 503:** when mTLS is disabled or cluster is nil.

---

### POST /api/v1/cluster/certs/rotate

Triggers a cluster-wide mTLS certificate rotation. Only the leader executes this; followers forward via the standard write-forward middleware.

**Auth:** requires `cluster:admin` scope.

**Request body:** empty (no parameters needed).

**Response 202:** rotation accepted and committed via Raft.

**Response 503:** when mTLS is disabled or cluster is nil.

**Response 409:** when this node is not the leader (with leader redirect body).

---

## Rotation Algorithm

1. Leader generates a new CA (fresh ECDSA P-256, 10-year validity) using `GenerateClusterCA`-style logic (always fresh, no idempotency check).
2. Leader queries `store.Members()` to enumerate all known nodes.
3. For each member, leader issues a new leaf cert via `IssueLeafCert(newCA, newCAKey, nodeID, nil)`.
4. Leader encodes a `CmdCertRotation` Raft command with payload `CertRotationPayload{CACertPEM, Nodes: map[nodeID]NodeCerts{CertPEM, KeyPEM}}`.
5. Raft applies the command on every node:
   - Each node looks up its own entry in `Nodes` by its `nodeID`.
   - Writes the new cert+key to disk (overwriting old files).
   - Calls `cc.Update(caCertPEM, certPEM, keyPEM)` on its `CertCache`.
6. New TLS connections immediately use the new cert via `GetCertificate` hook.
7. Existing connections finish with the old cert (no disruption).

---

## Hot-Reload Mechanism

The `CertCache` type (in `cluster/mtls_reload.go`) holds the current TLS material behind a `sync/atomic.Pointer[certBundle]`. The `tls.Config` returned by `BuildClusterTLSConfigDynamic` uses `GetCertificate` and `GetConfigForClient` hooks that dereference the atomic pointer on every new connection handshake. This means:

- New connections immediately pick up the new certificate after `cc.Update()`.
- In-flight connections finish their TLS handshake with the old certificate.
- No listener restart is required.

---

## Raft Command

**Kind:** `cert_rotation`

**Payload:**
```go
type CertRotationPayload struct {
    CACertPEM []byte               `json:"ca_cert_pem"`
    Nodes     map[string]NodeCerts `json:"nodes"` // keyed by node_id
}

type NodeCerts struct {
    CertPEM []byte `json:"cert_pem"`
    KeyPEM  []byte `json:"key_pem"`
}
```

---

## Security Properties

- Only the leader can generate a new CA (it holds the CA private key).
- The CA private key is never sent over the wire — only per-node leaf certs are distributed via the Raft log.
- The Raft log itself is TLS-encrypted (mTLS mesh).
- Scope enforcement (`cluster:admin`) is applied at the API layer.
