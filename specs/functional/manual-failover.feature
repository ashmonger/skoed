Feature: Manual Failover
  As an administrator
  I want to promote a replica to primary when the previous primary is offline or being decommissioned
  So that the cluster keeps accepting configuration changes without waiting for the failed primary to come back.

  Background:
    Given a primary dblock node and one enrolled replica
    And the administrator is authenticated on the replica

  @fsid:FS-ManualFailoverPromoteReplica
  Scenario: Admin promotes a replica to primary
    When the administrator triggers promote on the replica
    Then the replica's role becomes "primary"
    And the replica starts accepting write requests
    And the replica stops opening sync events to the former primary

  @fsid:FS-ManualFailoverFormerPrimaryDemotesOnReconnect
  Scenario: The former primary demotes itself when it reaches the new primary
    Given the replica has been promoted to primary
    And the former primary is offline
    When the former primary returns online and discovers a newer primary in the cluster
    Then the former primary's role becomes "replica"
    And the former primary pulls the latest config from the new primary
    And the former primary's writes return HTTP 409 redirecting to the new primary

  @fsid:FS-ManualFailoverPromoteFailsWithoutAuth
  Scenario: Unauthenticated promote is rejected
    When an unauthenticated client attempts to promote the replica
    Then the request is rejected with HTTP status 401
    And the replica's role remains "replica"

  @fsid:FS-ManualFailoverNewPrimaryAcceptsWrites
  Scenario: A promoted replica accepts writes that the cluster then propagates
    Given the replica has been promoted to primary
    And a second replica has reconnected and recognized the new primary
    When the administrator adds a blocklist on the new primary
    Then within 10 seconds the second replica has the new blocklist

  @fsid:FS-ManualFailoverSplitBrainProtection
  Scenario: A would-be primary with stale config defers to a newer primary
    Given the cluster has been operating with a new primary at config version 42
    When a former primary with config version 30 comes online
    Then the former primary detects the higher config version
    And the former primary demotes itself to replica before serving any writes

  Non-goals:
    - Automatic detection of primary failure (covered by Quorum Auto-Failover).
    - Two-node split-brain when both think they are primary at the same version — this requires the quorum protocol.
