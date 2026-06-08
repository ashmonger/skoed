---
x-tsid: TS-EncryptedClusterMesh
x-fsid-links:
  - FS-MtlsDefaultOff
  - FS-MtlsClusterFormsWhenEnabled
  - FS-MtlsClusterCAGenerated
  - FS-MtlsJoinDistributesCA
  - FS-MtlsRejectsUntrustedPeer
  - FS-MtlsInternalApiHTTPS
---

# TS-EncryptedClusterMesh — mTLS for Raft + internal API

## Config

```yaml
node:
  cluster:
    mtls:
      enabled: false        # default off; cluster-wide flip required
      # ca_cert_file / ca_key_file: optional operator-supplied CA. When
      # absent on the bootstrap node, dblock generates a fresh ECDSA P-256
      # CA + leaf at boot.
      ca_cert_file: ""
      ca_key_file: ""
```

The setting is node-local but operationally cluster-wide — the operator
flips it everywhere and restarts; the M5.3 cluster does NOT support
mixed-mode topologies.

## On-disk layout

```
<data_dir>/tls/cluster/
  ca.crt        # X.509 cluster CA (public)
  ca.key        # CA private key (mode 0600); only on nodes that may
                # mint new leaves — for M5.3 v1 this is every node so
                # any node can act as enrollment endpoint after a
                # leadership transfer.
  node.crt      # this node's leaf cert
  node.key      # this node's leaf private key
```

Replicated through bbolt under bucket `cluster_meta` keys
`tls_ca_cert` / `tls_ca_key`. When a node boots with mtls.enabled=true
and bbolt already has these keys, the on-disk PEMs are rebuilt from
bbolt so a wiped data_dir cannot poison the cluster.

## Raft transport

`raft.NewNetworkTransport(streamLayer, ...)` with a custom StreamLayer
that wraps `net.Listen("tcp", ...)` in `tls.NewListener` (server side)
and dials with `tls.Dial` (client side). Both directions:

- Present the node's leaf cert as the client/server cert.
- `tls.Config.ClientCAs` / `tls.Config.RootCAs` = the cluster CA.
- `tls.Config.ClientAuth = tls.RequireAndVerifyClientCert`.

ServerName is set to the peer's `node-id` (encoded as the leaf cert's
`Subject.CommonName` and a `DNSNames` SAN), so a peer presenting a
correctly-signed cert for the wrong node still gets rejected at the
TLS layer.

## Internal API

Reuses the M4.6 HTTPS listener. When `mtls.enabled=true`:

- The `/api/v1/cluster/_internal/aggregates` and `/api/v1/cluster/join`
  endpoints are wrapped in a middleware that verifies the request's
  client cert is signed by the cluster CA.
- Forwarders (`internal/cluster/cluster.go` — `forwardAggregate`,
  `joinExistingCluster`) build their HTTP client with the cluster
  CA in `RootCAs` and the node's leaf cert in `Certificates`.
- The cluster_secret header is still sent for defense-in-depth and to
  keep the audit log's `actor` field meaningful.

## Bootstrap & join

**Bootstrap (single-node, fresh):**
1. mtls.enabled=true and no on-disk PEMs.
2. Generate ECDSA P-256 CA, valid 10 years.
3. Mint node-1 leaf cert, valid 1 year, CN=node-1.
4. Write to bbolt + on-disk.
5. Start Raft transport with TLS StreamLayer.

**Join (a new node enrolls into an existing cluster):**
1. Operator runs `dblock --config config.yaml` with a bootstrap token.
2. Joining node POSTs `/api/v1/cluster/join` with the token AND a
   freshly-generated CSR for its own leaf cert.
3. Leader signs the CSR with the cluster CA, returns:
   `{ca_cert: PEM, leaf_cert: PEM, ...existing token response...}`.
4. Joining node writes the bundle to its `<data_dir>/tls/cluster/`,
   then proceeds with the existing Raft enrolment flow — its Raft
   transport now uses TLS.

**Joining without a leader available:** the existing token TTL +
single-use guarantees apply unchanged. A token leaked to an attacker
who can also impersonate the leader's IP is still bad, but the leaf
cert distribution requires a live HTTP roundtrip to the leader, so
purely off-line replay attacks gain nothing.

## Failure modes

- Wrong CA → `tls: failed to verify client certificate: x509: …` at
  the Raft handshake. Caught at TLS layer, before any Raft state is
  exchanged. Logged at WARN.
- Expired leaf → same as above. M5.3 v1 does not yet auto-rotate; the
  operator restarts the node with a fresh CSR.
- mTLS-on node joining an mTLS-off cluster → join 4xx with a clear
  error: "leader has mtls.enabled=false; reconfigure cluster-wide
  first".
