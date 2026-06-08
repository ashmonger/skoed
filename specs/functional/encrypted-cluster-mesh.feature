Feature: Encrypted Cluster Mesh
  As an operator deploying skoed across two or more hosts
  I want every inter-node connection (Raft + cluster-internal API)
  encrypted and mutually authenticated
  So replicated state (blocklists, password hashes, query-log aggregates)
  stays off the wire even when the cluster spans a VPN-less link.

  Background:
    Given the operator can set node.cluster.mtls.enabled in config.yaml
    And the M5.3 mTLS layer is OFF by default

  @fsid:FS-MtlsDefaultOff
  Scenario: Default-off — plain Raft still works unchanged
    Given two nodes start with node.cluster.mtls.enabled NOT set
    When node-2 joins node-1
    Then the Raft handshake succeeds
    And the existing M2 acceptance tests still pass

  @fsid:FS-MtlsClusterFormsWhenEnabled
  Scenario: 3-node cluster forms when mTLS is enabled cluster-wide
    Given node-1 boots with node.cluster.mtls.enabled = true
    When node-2 and node-3 join node-1 with mtls.enabled = true
    Then all 3 nodes reach the same Raft commit_index
    And a blocklist created on the leader replicates to every node

  @fsid:FS-MtlsClusterCAGenerated
  Scenario: Bootstrap node generates a cluster CA and a node leaf cert
    Given a fresh single-node cluster boots with mtls.enabled = true
    Then <data_dir>/tls/cluster/ca.crt exists
    And <data_dir>/tls/cluster/ca.key exists with mode 0600
    And <data_dir>/tls/cluster/node.crt exists, signed by ca.crt
    And the CA is recorded in the replicated bbolt cluster_meta bucket

  @fsid:FS-MtlsJoinDistributesCA
  Scenario: Joining node receives CA + signed leaf cert from leader
    Given a leader has bootstrapped with mtls.enabled = true
    When a fresh node joins with the cluster's join token
    Then the joining node receives:
      | the cluster CA certificate    |
      | a leaf cert signed by the CA  |
      | the leaf cert's private key   |
    And stores them under <data_dir>/tls/cluster/
    And uses them for its own Raft + internal API listeners

  @fsid:FS-MtlsRejectsUntrustedPeer
  Scenario: A node with the wrong CA cannot join
    Given a leader cluster running mtls.enabled = true
    When a fresh node tries to enroll with a leaf cert signed by a different CA
    Then the join handshake fails before any Raft state is exchanged
    And node-1's log records a TLS verification error

  @fsid:FS-MtlsInternalApiHTTPS
  Scenario: Cluster-internal API requests use HTTPS with mTLS
    Given a cluster running mtls.enabled = true
    When a follower forwards its hourly aggregate to the leader
    Then the request goes over HTTPS with both peers presenting their cluster certs
    And a request from a client without a cluster cert is rejected

  Non-goals:
    - Per-tenant key segmentation (single CA per cluster)
    - HSM / TPM integration
    - Per-message AEAD on top of TLS (TLS is already AEAD)
    - Live CA rotation in this milestone (operator restarts the cluster
      after re-bootstrapping; rotation = M5.3.1 follow-up)
    - Mixing mTLS-on with mTLS-off nodes in the same cluster (the
      operator flips the bit cluster-wide and restarts every node)
