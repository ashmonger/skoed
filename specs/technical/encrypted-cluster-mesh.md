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
      # absent on the bootstrap node, skoed generates a fresh ECDSA P-256
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

Revised 2026-11-25 (M50). Until then no peer HTTP client used TLS:
`apiBaseURL` (`internal/cluster/cluster.go:1001`) always returned
`http://`, so the `X-Cluster-Secret` header, which grants `cluster:admin`,
went over plain HTTP on every forwarded write. The earlier text reused
the M4.6 HTTPS listener, which serves the operator's certificate and may
be disabled, so it cannot verify cluster peers.

When `mtls.enabled=true`:

- **Peer listener.** Each node opens a second HTTP server on
  `node.cluster.peer_api_address`, default `<raft host>:<raft port + 1>`.
  It uses `BuildClusterTLSConfigDynamic` (`internal/cluster/mtls_reload.go:96`)
  with `ClientAuth: tls.RequireAndVerifyClientCert` and the cluster CA as
  `ClientCAs`, so certificate rotation applies to it as it does to Raft.
  It serves the same router as the main API.
- **Member record.** `Member` gains `PeerAPIAddress`, set by each node when
  it upserts its own record. `Cluster.LeaderAPIAddress()` and
  `MemberAPIURL` return `https://<PeerAPIAddress>` when mTLS is enabled.
- **Peer clients.** `ForwardWrite`, `forwardAggregate`, the query-log
  fan-out, the cluster upgrade calls and the blocklist sync worker share
  one `*http.Client` whose `tls.Config` has the cluster CA in `RootCAs`,
  the node's leaf through `GetClientCertificate`, and `ServerName` set to
  the target's node ID (the leaf's CN and DNS SAN), so a valid
  certificate for another node is rejected.
- **Secret only over TLS.** With mTLS enabled, the main API listener
  rejects requests that carry `X-Cluster-Secret` (401), so the secret is
  never accepted over plain HTTP. On the peer listener the secret is still
  required in addition to the client certificate, and keeps the audit
  log's `actor` field meaningful.
- **Join and bootstrap.** `/api/v1/cluster/join` and
  `/api/v1/cluster/mtls-bootstrap` stay on the main listener: a joining
  node has no leaf yet. They are authenticated by the single-use join
  token, as today.

With `mtls.enabled=false` (the default) peer HTTP stays plain, as before;
the docs state that the cluster secret then crosses the network in clear.

**No plain-HTTP fallback (UoR decision, 2026-11-25).** With mTLS enabled, a
peer is always called over https. When its member record has no
`PeerAPIAddress` yet (an older version during a rolling upgrade, or a node
that just upgraded), the client uses the default address: the member's Raft
host and Raft port + 1. A node that does not serve that listener (an older
version) cannot be reached for peer HTTP: writes forwarded to an older
leader fail with 503 until the leader is upgraded. `ClusterUpgradeApply`
upgrades the leader last, so this lasts for the duration of the rolling
upgrade. The cluster secret never crosses the network in clear.

**Member records.**

- `Member` carries `PeerAPIAddress` (set only with mTLS enabled) and
  `Capabilities` (see blocklist-content-distribution.md §3.3).
- `POST /api/v1/cluster/join` accepts the joining node's
  `peer_api_address` and `capabilities`, so the leader writes the full
  record at enrollment.
- After start, each node compares its own record with what it should
  advertise every 10 s (1 s with `SKOED_TEST_MODE`), and writes only when
  they differ. The leader applies the change itself; a follower calls
  `POST /api/v1/cluster/_internal/members/self` on the leader. That route
  requires the cluster secret; on the peer listener the client
  certificate's CN must also equal `node_id` (403 otherwise); an unknown
  `node_id` gets 404, so a removed node cannot add itself back.
- A node ID that cannot be pinned is refused: `localhost` (any case, with
  or without a trailing dot) and any IP literal. Every leaf carries the SANs
  `localhost`, 127.0.0.1 and ::1, and Go matches an IP `ServerName` against
  IP SANs only. Join and mtls-bootstrap answer 400; a node whose own ID is
  such a value refuses to start with mTLS enabled (this covers the
  bootstrap node, which never joins).
- With mTLS enabled, the peer transport refuses any `http://` URL, so no
  caller can send the cluster secret in clear.

**Other rules.**

- If `peer_api_address` cannot be derived (unparseable `raft_address`, or
  Raft port 65535) while mTLS is enabled, the daemon refuses to start.
- Peer clients do not follow redirects.
- Leader-redirect error bodies (409/503) report the leader's main API
  address, not its peer address.
- The self-upgrade call of the rolling upgrade goes to the node's own peer
  listener, at its configured `peer_api_address`.
- Certificate rotation needs a restart: like Raft, the peer listener and
  clients build their certificate cache once at start (live rotation is a
  non-goal). Upgrading an mTLS cluster opens a new port (Raft port + 1 by
  default), which firewalls must allow between nodes.

**Tests.** The rules above that black-box tests cannot reach without the
cluster secret are covered by Go unit tests in `internal/cluster` and
`internal/api`:

- the main listener answers 401 to any request carrying `X-Cluster-Secret`
  when mTLS is enabled, including with the right secret;
- a peer client pinned to node A rejects a valid cluster leaf of node B;
- `members/self` returns 403 on a CN mismatch and 404 for an unknown member;
- the self-correction pass writes no Raft entry when the record already
  matches;
- with mTLS enabled and no `PeerAPIAddress` recorded, the peer URL is
  `https://<raft host>:<raft port + 1>`, never `http://`.

## Bootstrap & join

**Bootstrap (single-node, fresh):**
1. mtls.enabled=true and no on-disk PEMs.
2. Generate ECDSA P-256 CA, valid 10 years.
3. Mint node-1 leaf cert, valid 1 year, CN=node-1.
4. Write to bbolt + on-disk.
5. Start Raft transport with TLS StreamLayer.

**Join (a new node enrolls into an existing cluster):**
1. Operator runs `skoed --config config.yaml` with a bootstrap token.
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
