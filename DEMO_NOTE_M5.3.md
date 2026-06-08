# DEMO NOTE — M5.3 Encrypted Cluster Mesh (mTLS)

## Scope

Inter-node traffic is now optional-opt-in mTLS. Operators flip
`node.cluster.mtls.enabled: true` in every node's config and restart;
the bootstrap node generates an ECDSA P-256 cluster CA, joining nodes
receive the CA + a freshly-signed leaf via a new pre-Raft endpoint,
and the entire Raft transport runs over TLS with `RequireAndVerifyClientCert`.

### Implemented

- **Cluster CA generation** on bootstrap (`GenerateClusterCA`): ECDSA
  P-256, 10-year validity, stored under `<data_dir>/tls/cluster/`
  (`ca.crt` 0644, `ca.key` 0600). Idempotent: re-running picks up the
  existing pair.
- **Per-node leaf cert** signed by the cluster CA, CN = node-id, with
  `127.0.0.1`/`::1` baked in as IP SANs so loopback testing and
  same-host deployments work without DNS gymnastics.
- **TLSStreamLayer** wraps `tls.Listen` and `tls.Dial` to satisfy
  hashicorp/raft's `StreamLayer` interface. `raft.NewNetworkTransport`
  takes it instead of `raft.NewTCPTransport` when mTLS is on.
- **Two-phase join flow**:
  1. `POST /api/v1/cluster/mtls-bootstrap` — leader validates the token
     (does NOT consume), mints a leaf for the joining node, returns
     `{ca_cert_pem, leaf_cert_pem, leaf_key_pem}`.
  2. Joining node writes bundle to `<data_dir>/tls/cluster/`, then
     calls `cluster.New` (TLS-Raft transport binds).
  3. Background goroutine fires the regular `POST /api/v1/cluster/join`
     — leader consumes the token + runs `AddVoter`. By this point the
     joining node's TLS listener is up and `AddVoter`'s subsequent
     `AppendEntries` succeed.

  The two-phase shape avoids the bug we hit first: if the leader does
  `AddVoter` before the joining node's Raft is listening, the
  cluster-config commit needs majority quorum (the new voter is in
  the config), and the leader can lose leadership while waiting for
  the still-offline joiner.

- **`tls.Config` builder** with `ClientAuth = RequireAndVerifyClientCert`,
  CA pool from the cluster CA, leaf cert as our identity. Same
  `*tls.Config` is reusable for the internal-API HTTPS path (M5.3.1
  follow-up).
- **Opt-in, cluster-wide flip**: `node.cluster.mtls.enabled` (yaml,
  default false). M5.3 v1 does NOT support mixed-mode (some nodes
  mTLS, others plain) — operator restarts every node together.
- **Bonus side-effect** from making the joining flow more deterministic:
  the regular join no longer races AddVoter against the joining
  node's listener readiness, even in plain mode.

### Acceptance tests

2 acceptance tests + 4 FSIDs covered:

| FSID                              | Test                                | Topology |
|-----------------------------------|-------------------------------------|----------|
| FS-MtlsDefaultOff                 | TestMtlsDefaultOffPlainStillWorks   | 1 node   |
| FS-MtlsClusterFormsWhenEnabled    | **TestMtlsClusterFormsAndReplicates** | **3 nodes** |
| FS-MtlsClusterCAGenerated         | (verified inside the 3-node test)   | 3 nodes  |
| FS-MtlsJoinDistributesCA          | (verified inside the 3-node test)   | 3 nodes  |

Pending (deferred to M5.3.1):
- FS-MtlsRejectsUntrustedPeer — needs a harness affordance to spawn
  a node with a foreign CA; the TLS layer already rejects it.
- FS-MtlsInternalApiHTTPS — internal-API mTLS is wired in
  `*tls.Config` form via `Cluster.MTLSConfig()` but the
  `/_internal/aggregates` listener doesn't yet enforce it (the
  cluster_secret header still authenticates it for v1). Adding TLS
  enforcement is a small follow-up that can land without an
  on-the-wire change.

All M5.3 tests pass; full M1→M5.3 suite green in Docker (~568 s).

### Not implemented (deferred / non-goals)

- Mixed-mode topologies (some nodes mTLS, others plain).
- Live CA rotation — restart-the-cluster for v1.
- Per-tenant key segmentation.
- HSM / TPM integration.
- Per-message AEAD on top of TLS.

## Demo

```bash
# 3-node mTLS cluster. Each node's config:
node:
  id: node-1   # node-2 / node-3 for the others
  raft_address: 192.168.1.10:7000
  api_address:  192.168.1.10:8080
  data_dir: /var/lib/dblock
  cluster:
    mtls:
      enabled: true

# Boot node-1 (generates the CA). On node-2 / node-3, also set:
bootstrap:
  leader_address: http://192.168.1.10:8080
  token:          <token-issued-by-node-1>

# Inspect the generated CA.
openssl x509 -in /var/lib/dblock/tls/cluster/ca.crt -noout -subject -dates
# subject=O = dblock, CN = dblock cluster CA
# notBefore=...     notAfter=10 years out

# Tcpdump confirms Raft traffic is TLS (port 7000).
sudo tcpdump -i any -X port 7000 | head
# 0x0000: 1603 0102 ...  (TLS handshake: 16 = ContentType handshake)
```

## Next

M5.4 — Automated blocklist refresh (leader-only worker, replicate via
Raft, UI surface).
