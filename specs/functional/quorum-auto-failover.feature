Feature: Quorum Auto-Failover
  As an administrator running a multi-node dblock cluster without on-call coverage
  I want replicas to automatically elect a new primary when the current one disappears
  So that configuration changes are still possible during off-hours without human intervention.

  Background:
    Given a cluster of one primary and two replicas
    And all nodes have `cluster.auto_failover` set to true
    And all three nodes are in sync at the same config version

  @fsid:FS-QuorumAutoFailoverPrimaryLossDetected
  Scenario: Replicas detect primary loss via missed heartbeats
    When the primary becomes unreachable for 15 seconds
    Then both replicas record the primary as "unreachable"
    And both replicas log a "primary-lost" event

  @fsid:FS-QuorumAutoFailoverMajorityElectsNewPrimary
  Scenario: A majority of replicas elects a new primary
    Given the primary has been unreachable for 15 seconds
    When the two replicas exchange election votes
    Then exactly one of them is elected as the new primary within 30 seconds
    And the other replica recognizes the new primary
    And the new primary begins emitting `config-changed` events

  @fsid:FS-QuorumAutoFailoverDisabledByDefault
  Scenario: Auto-failover is disabled by default
    Given a cluster where no node has `cluster.auto_failover` explicitly set
    When the primary becomes unreachable for 60 seconds
    Then no replica is promoted automatically
    And the cluster has zero primaries until an administrator promotes a replica manually

  @fsid:FS-QuorumAutoFailoverNoMajorityNoElection
  Scenario: A single replica without quorum does not self-promote
    Given a cluster of one primary and one replica
    And both nodes have `cluster.auto_failover` set to true
    When the primary becomes unreachable for 60 seconds
    Then the lone replica does NOT promote itself
    And the cluster has zero primaries until an administrator intervenes manually

  @fsid:FS-QuorumAutoFailoverReturningPrimaryDemotes
  Scenario: A returning primary demotes after auto-failover
    Given a cluster where auto-failover has promoted a new primary
    When the former primary returns online
    Then the former primary discovers the new primary
    And the former primary demotes itself to replica
    And the former primary pulls the latest config

  Non-goals:
    - Strong consensus guarantees (Raft) — quorum-based step-down is best-effort.
    - Recovery from a network partition where both sides have quorum (impossible by definition; documented limitation).
    - Election when fewer than 2 replicas remain reachable.
