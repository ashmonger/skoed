Feature: Cluster Status
  As an administrator
  I want to see the Raft role, last-contact time, and commit index of every node
  So that I can verify the cluster's health and diagnose replication problems.

  Background:
    Given a 3-node skoed cluster (one leader, two followers)
    And the administrator is authenticated

  @fsid:FS-ClusterStatusListsAllNodes
  Scenario: Cluster status lists every node with role and last-contact
    When the administrator requests the cluster status from any node
    Then the response contains exactly 3 node entries
    And one entry has role "leader"
    And two entries have role "follower"
    And each entry contains a `last_contact` timestamp within the last 5 seconds

  @fsid:FS-ClusterStatusShowsCommitIndex
  Scenario: Cluster status shows commit index per node
    Given the leader is at Raft commit index 42
    And both followers are at commit index 42
    When the administrator requests the cluster status
    Then every node entry reports commit index 42

  @fsid:FS-ClusterStatusShowsLaggingFollower
  Scenario: Cluster status flags a follower that has not caught up
    Given the leader is at commit index 50
    And one follower is at commit index 47
    When the administrator requests the cluster status
    Then the lagging follower's entry reports commit index 47
    And the lagging follower's entry has `sync_state` "behind"

  @fsid:FS-ClusterStatusShowsUnreachableFollower
  Scenario: Cluster status flags a follower that has not been contacted recently
    Given one follower has not been reached by Raft heartbeat for 10 seconds
    When the administrator requests the cluster status
    Then the unreachable follower's entry has `sync_state` "unreachable"

  @fsid:FS-ClusterStatusSameViewFromAnyNode
  Scenario: A follower exposes the same cluster status as the leader
    When the administrator requests the cluster status from a follower
    Then the response contains the same 3 node entries as the leader's view
    And the response identifies the leader correctly

  @fsid:FS-ClusterStatusShowsRaftTerm
  Scenario: Cluster status exposes the current Raft term
    When the administrator requests the cluster status
    Then the response includes the current Raft term

  Non-goals:
    - Real-time push of cluster status updates (poll-only in M2).
    - Aggregated query log statistics across nodes (covered by query-log-aggregates).
