Feature: Node Enrollment
  As an administrator
  I want to add a replica node to an existing dblock primary
  So that I can scale DNS filtering across multiple machines without re-configuring each one by hand.

  Background:
    Given a running dblock node configured as a primary
    And the primary has at least one blocklist named "ads" containing "tracker.example.com"
    And the administrator is authenticated on the primary

  @fsid:FS-NodeEnrollmentGenerateToken
  Scenario: Admin generates a single-use join token on the primary
    When the administrator requests a new join token from the primary
    Then the response contains a token string
    And the response contains an expiry timestamp 15 minutes in the future
    And the response contains the primary's reachable address

  @fsid:FS-NodeEnrollmentJoinWithValidToken
  Scenario: A fresh node joins the cluster using a valid token
    Given the administrator has generated a join token on the primary
    And a second dblock node is running with no cluster state
    When the second node enrolls with the primary using the token
    Then the second node's role becomes "replica"
    And the second node's blocklists contain "ads" with "tracker.example.com"
    And the primary's cluster status lists the second node

  @fsid:FS-NodeEnrollmentJoinTokenIsSingleUse
  Scenario: A used join token cannot be reused
    Given the administrator has generated a join token on the primary
    And a replica has successfully enrolled using that token
    When a third node attempts to enroll using the same token
    Then enrollment is rejected with status "token already consumed"
    And the third node's role remains "unconfigured"

  @fsid:FS-NodeEnrollmentJoinTokenExpires
  Scenario: An expired join token cannot be used
    Given the administrator has generated a join token on the primary
    And 16 minutes have passed since the token was generated
    When a node attempts to enroll using the token
    Then enrollment is rejected with status "token expired"

  @fsid:FS-NodeEnrollmentInvalidToken
  Scenario: An unknown token cannot be used
    When a node attempts to enroll using a token string that the primary never issued
    Then enrollment is rejected with status "invalid token"

  @fsid:FS-NodeEnrollmentReplicaKeepsLocalListenPorts
  Scenario: Enrollment preserves node-local listen ports
    Given the second node has DNS listen port 5353 and API port 9090 configured locally
    And the primary has DNS listen port 53 and API port 8080
    When the second node enrolls with the primary
    Then the second node's DNS listen port remains 5353
    And the second node's API port remains 9090

  Non-goals:
    - Mutual TLS between nodes (M2.5+).
    - Automatic node discovery (mDNS, service registries).
    - Re-enrollment after the primary is rebuilt (an administrator manually re-issues a token).
