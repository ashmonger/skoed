Feature: Cluster Config Sync
  As an administrator managing a multi-node dblock cluster
  I want changes made on the primary to propagate to all replicas automatically
  So that I do not have to apply the same change on every node manually.

  Background:
    Given a primary dblock node and one enrolled replica
    And both nodes are reachable on the network

  @fsid:FS-ClusterConfigSyncBlocklistAdd
  Scenario: A new blocklist added on the primary propagates to the replica
    When the administrator adds a blocklist "ads" containing "tracker.example.com" on the primary
    Then within 10 seconds the replica blocks queries for "tracker.example.com"
    And the replica's blocklist "ads" contains the same domains as the primary

  @fsid:FS-ClusterConfigSyncBlocklistRemove
  Scenario: A blocklist removed on the primary is removed from the replica
    Given the primary has blocklist "ads" enabled containing "tracker.example.com"
    And the replica has the same blocklist
    When the administrator deletes the "ads" blocklist on the primary
    Then within 10 seconds the replica no longer blocks "tracker.example.com"

  @fsid:FS-ClusterConfigSyncAllowlist
  Scenario: An allowlist change on the primary propagates to the replica
    Given the primary has blocklist "ads" containing "ads.example.com"
    When the administrator adds "ads.example.com" to the allowlist on the primary
    Then within 10 seconds the replica resolves "ads.example.com" instead of blocking it

  @fsid:FS-ClusterConfigSyncLocalDns
  Scenario: A local DNS entry added on the primary propagates to the replica
    When the administrator adds a local A record "router.lab" → "192.168.1.1" on the primary
    Then within 10 seconds the replica returns "192.168.1.1" for "router.lab"

  @fsid:FS-ClusterConfigSyncReplicaIsReadOnly
  Scenario: Writes to a replica are rejected with status indicating the primary's address
    When the administrator attempts to create a blocklist on the replica
    Then the request is rejected with HTTP status 409
    And the response body indicates the request must be sent to the primary
    And the response body contains the primary's address

  @fsid:FS-ClusterConfigSyncSurvivesReplicaDisconnect
  Scenario: A replica catches up after a temporary disconnect
    Given the primary and replica are in sync at config version N
    And the replica is disconnected from the network
    When the administrator adds a blocklist on the primary
    And the replica reconnects to the network 30 seconds later
    Then within 10 seconds after reconnection the replica has the new blocklist
    And the replica's config version equals the primary's

  @fsid:FS-ClusterConfigSyncQueryLogIsPerNode
  Scenario: Query log entries stay on the node that served the query
    Given the primary has handled 3 DNS queries
    And the replica has handled 2 DNS queries
    When the administrator inspects the query log on each node
    Then the primary's log contains exactly 3 entries
    And the replica's log contains exactly 2 entries

  Non-goals:
    - Bi-directional sync (replicas writing back to the primary).
    - Sync of query log entries.
    - Sync of node-local settings (DNS listen port, API port, role).
    - Conflict resolution beyond "primary wins" — replicas are always read-only.
