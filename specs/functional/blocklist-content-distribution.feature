Feature: Blocklist content distribution from the leader
  As an operator running a skoed cluster in place of AdGuard Home or Pi-hole
  I want the cluster to replicate the blocklist configuration, the leader to
  download the lists, and the other nodes to copy them from the leader
  So that I configure a blocklist once for every node, each list is
  downloaded once per refresh, and memory, disk and Raft traffic stay
  proportional to the configuration and not to the size of the lists.

  FS-AutoRefreshLeaderOnly and FS-AutoRefreshUpdatesAllNodes
  (automated-blocklist-refresh.feature) still hold. See
  decisions/20261125-LeaderDistributedBlocklistContent.md.

  Background:
    Given a 3-node skoed cluster
    And a URL-source blocklist "ads" whose URL serves 1000 domains including "tracker.example.com"

  @fsid:FS-BlocklistContentConfigReplicates
  Scenario: A URL blocklist created on any node is configured on every node
    When the administrator creates the blocklist "ads" on a follower
    Then within 5 seconds every node lists the blocklist "ads" with its URL, format and refresh interval
    And within 30 seconds DNS queries for "tracker.example.com" are blocked on every node

  @fsid:FS-BlocklistContentOnlyLeaderDownloads
  Scenario: Only the leader downloads a URL blocklist from its URL
    Given the blocklist "ads" has a refresh interval of 5 seconds
    When the cluster runs for 12 seconds
    Then the URL has received download requests from the leader only

  @fsid:FS-BlocklistContentFollowersCopyFromLeader
  Scenario: Followers copy the leader's download
    Given the URL of "ads" is reachable from the leader only
    When the leader downloads "ads"
    Then within 30 seconds every follower blocks "tracker.example.com"
    And every node reports the same domain_count and content version for "ads"

  @fsid:FS-BlocklistContentNotReplicated
  Scenario: The downloaded domains are not part of the replicated state
    Given the URL of "ads" serves 500000 domains
    And every node holds the leader's copy of "ads"
    When the administrator exports the cluster configuration
    Then the export contains the blocklist "ads" with its URL
    And the export contains none of the 500000 domains
    And the replicated state of the cluster is smaller than 1 MB

  @fsid:FS-BlocklistContentInlineStillReplicates
  Scenario: Inline blocklist domains are configuration and still replicate
    When the administrator creates an inline blocklist "manual" containing "bad.example.com" on the leader
    Then within 5 seconds DNS queries for "bad.example.com" are blocked on every node
    And the export contains "bad.example.com"

  @fsid:FS-BlocklistContentLeaderFailover
  Scenario: A new leader takes over the downloads when the leader goes down
    Given every node holds the leader's copy of "ads"
    And the blocklist "ads" has a refresh interval of 5 seconds
    When the leader node stops
    Then the remaining nodes elect a new leader
    And the URL receives the next download requests from the new leader only
    And the remaining follower copies "ads" from the new leader

  @fsid:FS-BlocklistContentNewLeaderWithoutCopy
  Scenario: A new leader that holds no copy downloads the list
    Given a node that holds no copy of "ads" becomes the leader
    When the new leader starts
    Then the new leader downloads "ads" from its URL without waiting for the refresh interval

  @fsid:FS-BlocklistContentDownloadFailureKeepsCopy
  Scenario: A failed download on the leader keeps the previous copy everywhere
    Given every node holds the leader's copy of "ads"
    And the URL then starts returning HTTP 500
    When the leader's next refresh runs
    Then every node still blocks "tracker.example.com"
    And every node reports last_refresh_status "error" with an error naming the HTTP status
    And the leader downloads "ads" again at its next refresh

  @fsid:FS-BlocklistContentFollowerCannotReachLeader
  Scenario: A follower that cannot copy from the leader keeps its copy
    Given every node holds the leader's copy of "ads"
    And one follower cannot reach the leader's API
    When the leader downloads a new version of "ads"
    Then that follower still blocks "tracker.example.com" with its previous copy
    And that follower reports that its copy of "ads" is out of date
    And that follower copies the new version within 30 seconds of the leader becoming reachable

  @fsid:FS-BlocklistContentSurvivesRestartOffline
  Scenario: A restarted node serves its last copy without network access
    Given every node holds the leader's copy of "ads"
    And neither the URL nor the other nodes are reachable
    When a follower restarts
    Then that follower blocks "tracker.example.com" as soon as it answers DNS queries

  @fsid:FS-BlocklistContentNewNodeCopiesOnJoin
  Scenario: A node joining the cluster copies every list from the leader
    Given the URL of "ads" is reachable from the leader only
    When a new node joins the cluster
    Then within 30 seconds of joining the new node blocks "tracker.example.com"

  @fsid:FS-BlocklistContentManualRefresh
  Scenario: A manual refresh on any node refreshes the list on every node
    Given every node holds the leader's copy of "ads"
    And the URL now serves 2000 domains
    When the administrator requests a refresh of "ads" on a follower
    Then the leader downloads "ads"
    And within 30 seconds every node reports domain_count 2000

  @fsid:FS-BlocklistContentUpgradeKeepsFiltering
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
    - Followers downloading from the URL themselves, including as a fallback
      when the leader's download fails
    - Changing the leader because its download fails (the leader changes only
      when the leader node goes down, through the Raft election)
    - Copying a list from a follower
    - Guaranteeing that all nodes hold the same list version at the same instant
      (followers converge within 30 seconds of the leader's download)
    - Compacting the Raft log while the daemon is running
    - Changing how inline blocklists, allowlists or custom rules are stored
