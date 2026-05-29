Feature: Leader Failover
  As an administrator
  I want the cluster to elect a new leader automatically when the current leader becomes unreachable
  So that configuration changes remain possible without human intervention during outages.

  Background:
    Given a 3-node dblock cluster (one leader, two followers)
    And all nodes are at the same Raft commit index

  @fsid:FS-LeaderFailoverAutomaticElection
  Scenario: The cluster elects a new leader when the current leader dies
    When the leader process is killed
    Then within 10 seconds one of the remaining followers becomes the new leader
    And the cluster status reports exactly one leader
    And the new leader accepts writes

  @fsid:FS-LeaderFailoverNoSplitBrainAcrossPartition
  Scenario: Only the majority side elects a leader during a partition
    Given the leader and one follower are on side A
    And the other follower is on side B
    When the partition cuts side A from side B
    Then side A continues to have a leader (the majority side)
    And side B does NOT elect a new leader
    And side A continues to accept writes
    And side B refuses writes

  @fsid:FS-LeaderFailoverFormerLeaderRejoinsAsFollower
  Scenario: A returning former leader rejoins as a follower
    Given a new leader has been elected after the original leader was killed
    When the original leader process is restarted
    Then the original leader recognizes the new leader via Raft term comparison
    And the original leader's role becomes follower
    And the original leader catches up to the cluster's commit index within 10 seconds

  @fsid:FS-LeaderFailoverWritesDuringTransition
  Scenario: A write submitted during leader transition succeeds after election completes
    Given the leader is about to be killed
    When the administrator submits a write that arrives just as the leader becomes unreachable
    Then the write either succeeds within 30 seconds against the new leader
    Or the write returns a clear "no leader" error that the admin can retry

  @fsid:FS-LeaderFailoverManualTransfer
  Scenario: Admin can manually transfer leadership to a specific node
    When the administrator invokes the leadership-transfer API targeting a specific follower
    Then within 5 seconds the target follower becomes the leader
    And the former leader is now a follower
    And no Raft log entries are lost

  Non-goals:
    - Leader failover faster than ~3 seconds (Raft heartbeat tuning; defaults are fine for home/lab).
    - Surviving simultaneous loss of a majority (impossible by definition; documented as a known limitation).
