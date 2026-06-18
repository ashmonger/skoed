Feature: Rolling Cluster Upgrade
  As a cluster operator
  I want to upgrade all nodes sequentially via a single API call
  So that the cluster stays available throughout the upgrade without losing quorum

  Non-goals:
    - Canary-style partial rollouts (upgrade all or none)
    - Automated rollback on version mismatch (operator keeps prior binary)
    - Blue-green node replacement
    - Upgrade coordination for single-node deployments (M16 binary swap handles that)

  Background:
    Given a 3-node skoed cluster (node-1 leader, node-2 follower, node-3 follower)
    And all nodes are healthy and in quorum

  @fsid:FS-RollingUpgradeOrchestrated
  Scenario: Leader orchestrates sequential node upgrades
    Given a new binary URL is available at a valid tarball URL
    When the operator calls POST /api/v1/cluster/upgrade/apply with the URL
    Then the leader upgrades node-2, waits for it to rejoin quorum
    And the leader upgrades node-3, waits for it to rejoin quorum
    And the leader transfers leadership to an already-upgraded node
    And the new leader upgrades node-1 (former leader)
    And GET /api/v1/cluster/upgrade/status returns completed with all nodes listed
    And the cluster has never dropped below 2 healthy nodes during the process

  @fsid:FS-RollingUpgradeStatus
  Scenario: Upgrade status is queryable during and after upgrade
    Given a rolling upgrade is in progress
    When the operator calls GET /api/v1/cluster/upgrade/status
    Then the response includes in_progress true
    And pending_nodes lists nodes not yet upgraded
    And completed_nodes lists nodes successfully upgraded
    And failed_node is null

  @fsid:FS-RollingUpgradeAbortOnFailure
  Scenario: Rolling upgrade aborts if a node fails to rejoin
    Given a rolling upgrade is in progress
    When a node fails to rejoin quorum within upgrade_node_timeout_seconds
    Then the upgrade stops immediately
    And GET /api/v1/cluster/upgrade/status returns in_progress false and failed_node set
    And remaining nodes are not upgraded
    And the cluster continues serving DNS with the nodes that are healthy

  @fsid:FS-RollingUpgradeLeadershipTransfer
  Scenario: Leader transfers leadership before upgrading itself
    Given node-1 is the current leader
    When the rolling upgrade reaches node-1 as the last node
    Then node-1 triggers a Raft leadership transfer to an already-upgraded follower
    And node-1 confirms it is no longer leader before applying the binary swap
    And the new leader confirms node-1 rejoined after its restart

  @fsid:FS-FollowerReadsDirectly
  Scenario: Followers serve GET requests without proxying to the leader
    Given the cluster is healthy
    When a client sends GET /api/v1/blocklists to a follower node
    Then the follower responds directly with 200 and current blocklist data
    And the response includes X-Served-By: <follower node id>
    And no forwarding to the leader occurs for this read request

  @fsid:FS-FollowerForwardsMutations
  Scenario: Followers still forward mutating requests to the leader
    Given the cluster is healthy
    When a client sends POST /api/v1/blocklists to a follower node
    Then the follower forwards the request to the leader
    And the leader processes it and responds
    And the response includes X-Raft-Leader: <leader node id>
