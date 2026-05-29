Feature: Cluster Status
  As an administrator
  I want to see the role, last-seen time, and sync state of every node in the cluster
  So that I can verify the cluster's health and diagnose sync problems.

  Background:
    Given a primary dblock node and two enrolled replicas
    And the administrator is authenticated on the primary

  @fsid:FS-ClusterStatusListsAllNodes
  Scenario: Cluster status lists every node with role and last-seen
    When the administrator requests the cluster status from the primary
    Then the response contains exactly 3 node entries
    And one entry has role "primary"
    And two entries have role "replica"
    And each entry contains a `last_seen` timestamp within the last 30 seconds

  @fsid:FS-ClusterStatusShowsConfigVersion
  Scenario: Cluster status shows config version per node
    Given the primary is at config version 7
    And both replicas have synced to config version 7
    When the administrator requests the cluster status
    Then every node entry reports config version 7

  @fsid:FS-ClusterStatusShowsLaggingReplica
  Scenario: Cluster status flags a replica that has not caught up
    Given the primary is at config version 8
    And one replica is at config version 7
    When the administrator requests the cluster status
    Then the lagging replica's entry reports config version 7
    And the lagging replica's entry has `sync_state` "behind"

  @fsid:FS-ClusterStatusShowsUnreachableReplica
  Scenario: Cluster status flags a replica that has not been seen recently
    Given one replica has not sent a heartbeat for 30 seconds
    When the administrator requests the cluster status
    Then the unreachable replica's entry has `sync_state` "unreachable"

  @fsid:FS-ClusterStatusAvailableOnReplica
  Scenario: A replica also exposes cluster status
    When the administrator requests the cluster status from a replica
    Then the response contains the same 3 node entries
    And the response is identical in content to the primary's view, except `last_seen` timestamps may differ slightly

  Non-goals:
    - Real-time push of cluster status updates (poll-only).
    - Aggregated query log statistics across nodes.
