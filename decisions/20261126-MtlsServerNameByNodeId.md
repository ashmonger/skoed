# Raft over mTLS verifies peers by node ID, as specified

## Context

The M50 demo on 2026-11-25 (three Proxmox containers on separate LAN
addresses) showed that a cluster with `node.cluster.mtls.enabled: true`
does not form across hosts:

```
raft: failed to appendEntries to: peer="{Voter demo2 192.168.1.192:7000}" error="tls: failed to verify certificate: x509: certificate is valid for 127.0.0.1, ::1, not 192.168.1.192"
```

`specs/technical/encrypted-cluster-mesh.md` ("Raft transport") already
says: "ServerName is set to the peer's `node-id` (encoded as the leaf
cert's `Subject.CommonName` and a `DNSNames` SAN)". The implementation
does not do it: `TLSStreamLayer.Dial` (`internal/cluster/mtls.go:264`)
calls `tls.DialWithDialer` with no `ServerName`, so Go verifies the leaf
against the dialed IP, and leaves carry only the node ID, `localhost`,
127.0.0.1 and ::1. Every acceptance test runs on 127.0.0.1, which hides
it. In addition, `ResetRaftForJoin` rebuilds the Raft node without the
TLS stream layer, so a joined node speaks plain TCP to TLS peers.

## Problem

FS-MtlsClusterFormsWhenEnabled is validated and its acceptance test
passes, but the behavior fails on any real multi-host cluster.

## Options considered

1. Set `ServerName` to the peer's node ID, resolved from the Raft
   configuration (the address → ID mapping), as the spec says.
2. Put each node's Raft IP in its leaf's IP SANs, so verification by
   address works; existing leaves would need re-issuing.
3. Verify only the CA chain and skip the name check.

## Decision

Option 1, the specified behavior. No functional or technical
specification changes. The defect is fixed test-first: the acceptance
harness runs mTLS clusters on distinct loopback addresses (127.0.0.2,
127.0.0.3, ...), which reproduces the cross-host failure, and
FS-MtlsClusterFormsWhenEnabled is checked on them. `ResetRaftForJoin`
keeps the TLS stream layer. A dial to an address that is not in the Raft
configuration fails rather than skipping the name check.

## Consequences

* Existing leaves stay valid; no re-issue or rotation is needed.
* The quorum loss when a join adds a voter that cannot be reached is a
  separate issue, left in TODO.md.

## Related hypotheses

None.

## Affected features

M5.3 Encrypted cluster mesh (FS-MtlsClusterFormsWhenEnabled,
FS-MtlsRejectsUntrustedPeer), M50 peer mTLS.
