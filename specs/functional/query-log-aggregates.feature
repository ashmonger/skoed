Feature: Query Log Aggregates
  As an administrator looking at a cluster-wide dashboard
  I want top-N domains, top-N clients, and total counters available cluster-wide
  So that a single page summarises blocking activity across all nodes without expensive fan-out per page load.

  Background:
    Given a 3-node skoed cluster
    And the administrator is authenticated

  @fsid:FS-QueryLogAggregatesPerNodePerHour
  Scenario: Each node writes hourly aggregates into the replicated bbolt bucket
    Given node-1 has handled 100 queries this hour, 30 of them blocked
    When the current hour rolls over OR 60 seconds elapse, whichever comes first
    Then node-1 commits its hourly aggregate via Raft
    And the aggregate contains: node id, hour-start timestamp, total=100, blocked=30
    And the aggregate contains the top-N domains queried, blocked, and the top-N clients
    And within 5 seconds the aggregate is visible on every other node

  @fsid:FS-QueryLogAggregatesClusterStats
  Scenario: Admin reads cluster-wide stats from any node
    Given each node has committed hourly aggregates for the last 24 hours
    When the administrator requests cluster stats from any node
    Then the response contains per-hour totals summed across all nodes
    And the response contains the cluster-wide top-N domains
    And the response is identical regardless of which node served it

  @fsid:FS-QueryLogAggregatesAvailableDuringLeaderLoss
  Scenario: Cluster stats remain readable even when the leader is down
    Given each node has committed aggregates for the last 24 hours
    And the leader is down
    When the administrator requests cluster stats from a surviving follower
    Then the response contains the same aggregates that were committed before leader loss
    And the response does not depend on writing new data

  @fsid:FS-QueryLogAggregatesFanOutForRawEntries
  Scenario: Individual-entry search fans out to every node
    Given each node has its own per-node raw query log
    When the administrator searches for queries from client "192.168.1.50" cluster-wide
    Then the request fans out to every node in parallel
    And the merged response is sorted by timestamp descending
    And entries from each node are tagged with the node id that served them

  @fsid:FS-QueryLogAggregatesFanOutPartialFailure
  Scenario: Fan-out tolerates one unreachable node
    Given one of three nodes is unreachable
    When the administrator searches for queries cluster-wide
    Then the response contains entries from the two reachable nodes
    And the response indicates which node was unreachable
    And the request returns within the configured fan-out timeout

  @fsid:FS-QueryLogAggregatesRetention
  Scenario: Aggregates respect a bounded retention window
    Given hourly aggregates have been committed for 100 days
    And the configured retention is 30 days
    When the next aggregate is committed
    Then aggregates older than 30 days are removed from the replicated bbolt bucket
    And the removal itself goes through Raft

  Non-goals:
    - Sub-hour granularity for cluster-wide stats (per-node logs cover that).
    - Streaming push of new aggregates to the dashboard (poll-only in M2).
    - Time-series storage beyond simple bucketed counts (no histograms, percentiles, or rate calculations).
