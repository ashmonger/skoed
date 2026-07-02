Feature: Cluster-wide Live Query Stream
  As a network operator managing a multi-node cluster
  I want to subscribe to a single SSE endpoint and receive DNS query events from all nodes
  So that I can monitor the full cluster without opening multiple streams

  # Non-goals:
  # - Cross-cluster (multi-cluster) aggregation
  # - WebSocket transport for the cluster stream
  # - Historical replay (backfill) for the cluster stream

  @fsid:FS-ClusterStreamAggregatesAllNodes
  Scenario: Cluster stream delivers events from all cluster nodes
    Given a 3-node cluster (skoed-1, skoed-2, skoed-3)
    When a client connects to /api/v1/query-log/stream?cluster=true on skoed-1
    And DNS queries occur on skoed-1, skoed-2, and skoed-3
    Then the client receives events from all three nodes
    And each event includes a "node_id" field identifying its source node

  @fsid:FS-ClusterStreamNodeIdField
  Scenario: Each stream event carries the originating node ID
    Given a client connected to the cluster-wide stream
    When a DNS query is processed on node "skoed-2"
    Then the event received by the client has "node_id": "skoed-2"

  @fsid:FS-ClusterStreamGracefulDegradation
  Scenario: Stream continues when a peer node becomes unavailable
    Given a client connected to the cluster-wide stream with 3 nodes
    When node "skoed-3" becomes unreachable
    Then the client receives "event: node_unavailable" with data {"node_id":"skoed-3"}
    And the stream continues delivering events from the remaining two nodes

  @fsid:FS-ClusterStreamDeduplication
  Scenario: Duplicate events during leader re-election are suppressed
    Given a client connected to the cluster-wide stream
    When a leader re-election causes the same query event to be received twice within 100ms
    Then only one copy of the event is delivered to the client

  @fsid:FS-ClusterStreamFiltersApply
  Scenario: Filters are applied after fan-in
    Given a client connected to /api/v1/query-log/stream?cluster=true&result=blocked
    When queries with results "blocked" and "allowed" occur on different nodes
    Then only "blocked" events are delivered to the client regardless of originating node

  @fsid:FS-ClusterStreamFallsBackToSingleNode
  Scenario: Single-node stream is unaffected by cluster parameter
    Given a single-node deployment
    When a client connects to /api/v1/query-log/stream (without cluster=true)
    Then the stream behaves identically to the existing M29 single-node stream
    And no "node_id" is required on events (may be empty or absent)
