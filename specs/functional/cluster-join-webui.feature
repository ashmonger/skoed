Feature: Cluster join via web UI
  As a network administrator
  I want to add a new node to an existing skoed cluster entirely through the web interface
  So that I do not need SSH or manual config-file edits to grow a cluster.

  The join flow has two sides:
    - Leader side: the administrator generates a join payload (token + leader address)
      on any existing cluster member via the Cluster page.
    - Follower side: the administrator opens the Cluster page on the new node,
      pastes the join payload into a dialog, and the new node joins the cluster.

  Non-goals:
    - Automatic peer discovery (mDNS, Consul, etc.) is out of scope.
    - Joining via CLI or config file is not changed — the web UI is an additional path.
    - Kubernetes or Docker Swarm cluster formation is out of scope.
    - The web UI does not expose Raft address configuration; nodes derive these from
      their local node configuration file.

  Background:
    Given a running skoed node bootstrapped as a single-node cluster ("the leader")
    And the administrator is authenticated on the leader node's web UI

  @fsid:FS-ClusterJoinWebUiTokenDisplay
  Scenario: Leader generates a join payload visible in the Cluster page
    Given the administrator is on the Cluster page of the leader node
    When the administrator clicks "Generate token"
    Then a join payload block appears on the page containing:
      | field           | format                     |
      | token           | non-empty opaque string    |
      | leader_address  | host:port of the leader API|
      | expires_at      | ISO-8601 timestamp         |
    And a "Copy to clipboard" button copies the full payload as a single block of text
    And the payload block displays a warning that the token is single-use

  @fsid:FS-ClusterJoinWebUiFollowerDialog
  Scenario: Administrator joins a new node to the cluster via the follower's web UI
    Given a second skoed node running as a single-node cluster ("the new node")
    And the administrator is authenticated on the new node's web UI
    And the administrator has a valid join payload copied from the leader
    When the administrator opens the Cluster page on the new node
    Then a "Join an existing cluster" section is visible because the node is in single-node mode
    When the administrator pastes the join payload into the join dialog and clicks "Join"
    Then the new node's web UI shows a progress indicator while joining
    And within 30 seconds the Cluster page on the new node shows mode "cluster"
    And the new node appears in the nodes table of both the leader and the new node's Cluster page
    And the new node's blocklist configuration matches the leader's

  @fsid:FS-ClusterJoinWebUiHiddenWhenAlreadyMember
  Scenario: The join section is hidden on a node that is already a cluster member
    Given a node that is part of a multi-node cluster
    When the administrator opens the Cluster page on that node
    Then the "Join an existing cluster" section is not visible
    And only the token generation section (for adding further nodes) is displayed

  @fsid:FS-ClusterJoinWebUiExpiredToken
  Scenario: Pasting an expired token shows an error
    Given the administrator is on the "Join an existing cluster" section of a new node
    And an expired join payload (token that has passed its expires_at timestamp)
    When the administrator pastes the payload and clicks "Join"
    Then an error message is displayed indicating the token has expired
    And the node remains in single-node mode

  @fsid:FS-ClusterJoinWebUiInvalidPayload
  Scenario: Pasting a malformed payload shows a validation error
    Given the administrator is on the "Join an existing cluster" section of a new node
    When the administrator pastes an invalid or incomplete payload and clicks "Join"
    Then a validation error is shown before the join request is sent
    And the node remains in single-node mode
