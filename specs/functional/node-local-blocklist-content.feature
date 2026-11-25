Feature: Node-local blocklist content
  As an operator running a skoed cluster in place of AdGuard Home or Pi-hole
  I want the cluster to replicate the blocklist configuration but not the
  downloaded domains
  So that I configure a blocklist once for every node, while each node's
  memory, disk and Raft traffic stay proportional to the configuration and
  not to the size of the lists.

  Supersedes FS-AutoRefreshLeaderOnly and FS-AutoRefreshUpdatesAllNodes
  (automated-blocklist-refresh.feature). See
  decisions/20261125-NodeLocalBlocklistContent.md.

  Background:
    Given a 3-node skoed cluster
    And a URL-source blocklist "ads" whose URL serves 1000 domains including "tracker.example.com"

  @fsid:FS-NodeLocalBlocklistConfigReplicates
  Scenario: A URL blocklist created on any node is configured on every node
    When the administrator creates the blocklist "ads" on a follower
    Then within 5 seconds every node lists the blocklist "ads" with its URL, format and refresh interval
    And within 30 seconds DNS queries for "tracker.example.com" are blocked on every node

  @fsid:FS-NodeLocalBlocklistEachNodeDownloads
  Scenario: Every node downloads a URL blocklist itself
    Given the blocklist "ads" has a refresh interval of 5 seconds
    When the cluster runs for 12 seconds
    Then the URL has received at least one download request from each of the 3 nodes

  @fsid:FS-NodeLocalBlocklistContentNotReplicated
  Scenario: The downloaded domains are not part of the replicated state
    Given the URL of "ads" serves 500000 domains
    And every node has downloaded "ads"
    When the administrator exports the cluster configuration
    Then the export contains the blocklist "ads" with its URL
    And the export contains none of the 500000 domains
    And the replicated state of the cluster is smaller than 1 MB

  @fsid:FS-NodeLocalBlocklistInlineStillReplicates
  Scenario: Inline blocklist domains are configuration and still replicate
    When the administrator creates an inline blocklist "manual" containing "bad.example.com" on the leader
    Then within 5 seconds DNS queries for "bad.example.com" are blocked on every node
    And the export contains "bad.example.com"

  @fsid:FS-NodeLocalBlocklistPerNodeStatus
  Scenario: Each node reports its own copy of a URL blocklist
    Given every node has downloaded "ads"
    When the administrator reads the blocklist "ads" on a node
    Then domain_count, last_refresh_at, last_refresh_status and last_refresh_error describe that node's copy

  @fsid:FS-NodeLocalBlocklistFailureStaysOnOneNode
  Scenario: A download failure on one node does not affect the others
    Given every node has downloaded "ads"
    And the URL becomes unreachable from one node only
    When that node's next refresh runs
    Then that node still blocks "tracker.example.com"
    And that node reports last_refresh_status "error" with an error naming the failure
    And the other nodes report last_refresh_status "ok" or "unchanged"

  @fsid:FS-NodeLocalBlocklistSurvivesRestartOffline
  Scenario: A restarted node serves its last downloaded copy without network access
    Given every node has downloaded "ads"
    And the URL is unreachable
    When a node restarts
    Then that node blocks "tracker.example.com" as soon as it answers DNS queries

  @fsid:FS-NodeLocalBlocklistFirstStartWithoutDownload
  Scenario: A node that has never downloaded a list still serves DNS
    Given a node that joins the cluster while the URL of "ads" is unreachable
    When the node starts
    Then the node answers DNS queries and applies every other blocklist, allowlist and rule
    And the node reports domain_count 0 and last_refresh_status "error" for "ads"
    And the node downloads "ads" on its first refresh after the URL becomes reachable

  @fsid:FS-NodeLocalBlocklistManualRefreshEveryNode
  Scenario: A manual refresh refreshes the list on every node
    Given every node has downloaded "ads"
    And the URL now serves 2000 domains
    When the administrator requests a refresh of "ads" on any node
    Then the response describes the refresh on the node that received the request
    And within 30 seconds every node reports domain_count 2000

  @fsid:FS-NodeLocalBlocklistUpgradeKeepsFiltering
  Scenario: Upgrading from a version that replicated the domains keeps filtering
    Given a cluster running a version that stored the downloaded domains of "ads" in the replicated state
    And the URL of "ads" is unreachable
    When every node is upgraded to this version
    Then every node blocks "tracker.example.com" without downloading "ads"
    And the replicated state no longer contains the downloaded domains

  @fsid:FS-RaftLogStaysBounded
  Scenario: The Raft log is truncated after each snapshot
    When the administrator commits 2000 configuration changes
    Then each node keeps at most 256 Raft log entries older than its latest snapshot

  @fsid:FS-RaftLogCompactOffline
  Scenario: An operator shrinks a Raft log file left oversized by earlier versions
    Given a node whose Raft log file holds mostly free space
    And the skoed daemon on that node is stopped
    When the operator runs the Raft log compaction command
    Then the Raft log file is smaller than before
    And the node rejoins the cluster with the same state after it starts

  @fsid:FS-RaftLogCompactRefusedWhileRunning
  Scenario: Compaction is refused while the daemon is running
    Given the skoed daemon on a node is running
    When the operator runs the Raft log compaction command
    Then the command fails with an error saying the daemon must be stopped first
    And the Raft log file is unchanged

  Non-goals:
    - Copying a list from a peer node when a node's own download fails
    - Downloading once on the leader and distributing the content to followers
    - Guaranteeing that all nodes hold the same list version at the same instant
      (nodes converge within one refresh interval, or within 30 seconds of a
      manual refresh)
    - Compacting the Raft log while the daemon is running
    - Changing how inline blocklists, allowlists or custom rules are stored
