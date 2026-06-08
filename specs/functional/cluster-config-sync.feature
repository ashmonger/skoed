Feature: Cluster Config Sync
  As an administrator managing a multi-node skoed cluster
  I want config changes applied via Raft to all nodes
  So that every node has consistent state and I can address any node interchangeably.

  Background:
    Given a 3-node skoed cluster (one leader, two followers)
    And all nodes are reachable on the network
    And all nodes are at the same Raft commit index

  @fsid:FS-ClusterConfigSyncWriteToLeader
  Scenario: A write sent to the leader replicates to all followers
    When the administrator adds a blocklist "ads" containing "tracker.example.com" via the leader
    Then within 5 seconds all 3 nodes' commit indices have advanced by one
    And every node's local view of blocklists contains "ads" with "tracker.example.com"
    And DNS queries for "tracker.example.com" are blocked on every node

  @fsid:FS-ClusterConfigSyncWriteToFollowerIsForwarded
  Scenario: A write sent to a follower is forwarded to the leader transparently
    When the administrator sends a write to a follower
    Then the follower forwards the write to the leader
    And the response to the admin's request is identical to what the leader would have returned
    And within 5 seconds all 3 nodes have the new state

  @fsid:FS-ClusterConfigSyncBlocklistRemove
  Scenario: A blocklist removed from any node is removed from every node
    Given the cluster has blocklist "ads" enabled containing "tracker.example.com"
    When the administrator deletes the "ads" blocklist via any node
    Then within 5 seconds every node has no "ads" blocklist
    And no node blocks "tracker.example.com"

  @fsid:FS-ClusterConfigSyncAllowlist
  Scenario: An allowlist change replicates across the cluster
    Given the cluster has blocklist "ads" containing "ads.example.com"
    When the administrator adds "ads.example.com" to the allowlist via any node
    Then within 5 seconds every node resolves "ads.example.com" instead of blocking it

  @fsid:FS-ClusterConfigSyncLocalDns
  Scenario: A local DNS entry added on any node propagates to every node
    When the administrator adds a local A record "router.lab" → "192.168.1.1" via any node
    Then within 5 seconds every node returns "192.168.1.1" for "router.lab"

  @fsid:FS-ClusterConfigSyncSurvivesFollowerDisconnect
  Scenario: A follower catches up after a temporary disconnect
    Given the cluster is at commit index N
    And one follower is partitioned from the network
    When the administrator commits 5 writes via the leader
    And the partitioned follower reconnects 30 seconds later
    Then within 10 seconds the reconnected follower's commit index equals the leader's
    And the reconnected follower's local state matches the cluster's

  @fsid:FS-ClusterConfigSyncMinorityPartitionRefusesWrites
  Scenario: A partitioned minority cannot accept writes
    Given a 3-node cluster
    And node-3 is partitioned alone from node-1 and node-2
    When the administrator sends a write to node-3
    Then the write is rejected with status indicating no leader is reachable
    And no state mutation occurs on node-3

  @fsid:FS-ClusterConfigSyncMajorityPartitionContinues
  Scenario: A majority partition continues to serve writes
    Given a 3-node cluster
    And node-3 is partitioned alone from node-1 and node-2
    When the administrator sends a write to either node-1 or node-2
    Then the write succeeds within 5 seconds
    And node-1 and node-2 both have the new state
    And node-3 does NOT have the new state until the partition heals

  @fsid:FS-ClusterConfigSyncQueryLogRawIsPerNode
  Scenario: Raw query log entries stay on the node that served the query
    Given the leader has handled 3 DNS queries
    And one follower has handled 2 DNS queries
    When the administrator inspects each node's local query log
    Then the leader's raw log contains exactly 3 entries
    And the follower's raw log contains exactly 2 entries

  Non-goals:
    - Application-level conflict resolution (Raft provides total ordering).
    - Read-your-writes consistency across followers without lease (a write returns from the leader before all followers ack).
    - Replication of raw query log entries.
