Feature: Active-Active Cluster
  As an administrator operating a multi-node skoed cluster
  I want any node to accept and commit write requests without redirecting to the leader
  So that multi-DC deployments do not need to pin all writes to a single node.

  Background:
    Given a 3-node skoed cluster where all nodes have completed Raft leader election
    And all nodes are at the same commit position
    And all nodes are reachable on the network

  @fsid:FS-AaWriteAcceptedOnAnyNode
  Scenario: Any node accepts a write request and returns a successful response
    When the administrator sends a write to a follower node
    Then the follower returns a successful response
    And no redirect or forwarding error is returned to the caller

  @fsid:FS-AaFollowerWriteProducesSameStateAsLeaderWrite
  Scenario: A write sent to a follower produces the same cluster state change as a write sent directly to the leader
    When the administrator adds a local DNS entry "test.internal" → "10.0.0.1" via a follower
    And the administrator adds a local DNS entry "test2.internal" → "10.0.0.2" via the leader
    Then within 5 seconds every node resolves "test.internal" to "10.0.0.1"
    And within 5 seconds every node resolves "test2.internal" to "10.0.0.2"
    And the cluster's commit position has advanced by two on every node

  @fsid:FS-AaReadServedLocallyWithoutLeaderContact
  Scenario: Read requests are served by the local node without contacting the leader
    Given the leader is isolated from the network
    When the administrator queries configuration from a follower
    Then the follower returns the configuration immediately
    And no timeout or leader-contact error occurs
    And the follower's request log shows no outbound call to the leader

  @fsid:FS-AaResponseSurfacesServingNodeAndCommitPosition
  Scenario: Every API response includes metadata identifying the serving node and the cluster commit position
    When the administrator sends any read or write request to any node
    Then the response includes the identifier of the node that served it
    And the response includes the cluster commit position at the time it was served

  @fsid:FS-AaWriteWithNoLeaderReturnsUnavailable
  Scenario: A write to a follower when no leader is available returns an explicit unavailable error
    Given no leader is elected in the cluster
    When the administrator sends a write request to any node
    Then the node returns an error indicating the cluster has no leader
    And no redirect to another node is returned
    And no state mutation occurs on any node

  @fsid:FS-AaPerNodeTelemetryIsLocal
  Scenario: Per-node metrics and telemetry are served locally and are not cluster-replicated
    Given each node has served a different number of DNS queries
    When the administrator fetches metrics from each node individually
    Then each node returns only its own query counts
    And the counts on one node do not appear in the metrics of another node
    And a follower serves its own metrics without contacting the leader

  @fsid:FS-AaDistributedWritesConvergeOnAllNodes
  Scenario: After writes distributed across multiple nodes all nodes eventually reflect the same state
    When the administrator adds blocklist entry "adblock" → "tracker.example.com" via node-1
    And the administrator adds allowlist entry "safe.example.com" via node-2
    And the administrator adds local DNS entry "gw.internal" → "192.168.0.1" via node-3
    Then within 10 seconds every node has "tracker.example.com" in blocklist "adblock"
    And within 10 seconds every node has "safe.example.com" in the allowlist
    And within 10 seconds every node resolves "gw.internal" to "192.168.0.1"
    And all nodes report the same commit position

  # Non-goals:
  #   - Geo-distributed write tolerance: the cluster assumes ≤ 50 ms RTT between all voters.
  #     Deployments with higher latency are not supported and may cause election instability.
  #   - Eventual-consistency mode: all reads reflect a consistent Raft-committed state;
  #     stale reads are not an accepted trade-off.
  #   - Multi-leader without Raft: there is exactly one Raft leader at any time;
  #     "active-active" means any node may submit a Raft entry, not that multiple leaders coexist.
  #   - CRDT merge semantics: conflict-free data types are used internally where applicable
  #     but are an implementation detail; the functional contract is last-writer-wins with
  #     explicit commit ordering, not CRDT-visible merge operations exposed to callers.
