Feature: Node Enrollment
  As an administrator
  I want to add a node to an existing skoed cluster
  So that I can scale DNS filtering across multiple machines without re-configuring each one by hand.

  Background:
    Given a running skoed node bootstrapped as a single-node cluster
    And the cluster has at least one blocklist named "ads" containing "tracker.example.com"
    And the administrator is authenticated on a node in the cluster

  @fsid:FS-NodeEnrollmentGenerateToken
  Scenario: Admin generates a single-use join token on an existing cluster member
    When the administrator requests a new join token from any cluster node
    Then the response contains a token string
    And the response contains an expiry timestamp 15 minutes in the future
    And the response contains the leader's reachable address

  @fsid:FS-NodeEnrollmentJoinWithValidToken
  Scenario: A fresh node joins the cluster using a valid token
    Given the administrator has generated a join token
    And a second skoed node is running with no cluster state
    When the second node enrolls using the token
    Then the cluster's Raft configuration contains the second node as a voter
    And within 10 seconds the second node has replayed the Raft log to the current commit index
    And the second node's local view of blocklists contains "ads" with "tracker.example.com"

  @fsid:FS-NodeEnrollmentJoinTokenIsSingleUse
  Scenario: A used join token cannot be reused
    Given the administrator has generated a join token
    And a node has successfully enrolled using that token
    When a third node attempts to enroll using the same token
    Then enrollment is rejected with status "token already consumed"

  @fsid:FS-NodeEnrollmentJoinTokenExpires
  Scenario: An expired join token cannot be used
    Given the administrator has generated a join token
    And 16 minutes have passed since the token was generated
    When a node attempts to enroll using the token
    Then enrollment is rejected with status "token expired"

  @fsid:FS-NodeEnrollmentInvalidToken
  Scenario: An unknown token cannot be used
    When a node attempts to enroll using a token string that was never issued
    Then enrollment is rejected with status "invalid token"

  @fsid:FS-NodeEnrollmentPreservesNodeLocalSettings
  Scenario: Enrollment preserves node-local settings
    Given the second node has DNS listen port 5353 and API port 9090 configured in its node.yaml
    And the cluster's leader is using DNS listen port 53 and API port 8080
    When the second node enrolls
    Then the second node's DNS listen port remains 5353
    And the second node's API port remains 9090

  @fsid:FS-NodeEnrollmentSingleNodeBootstrap
  Scenario: A node started with no peers initializes itself as a single-node cluster
    Given a skoed node with an empty data directory and no peers configured
    When the node starts
    Then the node initializes itself as a single-node Raft cluster
    And the node is the leader
    And the node accepts writes

  @fsid:FS-NodeEnrollmentM1ConfigMigration
  Scenario: A node started with an existing M1 config.yaml imports it into bbolt on first run
    Given a skoed node has an existing M1-shaped config.yaml in its data directory
    And no cluster.bbolt exists yet
    When the node starts for the first time as M2
    Then the YAML config is imported into the bbolt state
    And the YAML file is preserved as an export artifact
    And the node bootstraps as a single-node Raft cluster
    And subsequent writes go through Raft and not to the YAML file

  @fsid:FS-NodeRemoval
  Scenario: Admin removes a node from the cluster
    Given a cluster of 3 nodes
    When the administrator removes one non-leader node via the API
    Then the cluster's Raft configuration contains 2 voters
    And the removed node refuses to participate in further Raft operations
    And the cluster continues to accept writes

  Non-goals:
    - Mutual TLS between nodes (M2.5+).
    - Automatic node discovery (mDNS, service registries).
    - Removing the leader (admin must transfer leadership first, then remove).
