Feature: Query Log Stream Enhancements (Backfill + WebSocket)
  As a network operator
  I want the live query stream to replay recent history on connect
  And to be accessible via WebSocket for environments where SSE is blocked

  # Non-goals:
  # - Bidirectional commands over WebSocket (read-only stream)
  # - Cluster-wide aggregation via WebSocket (M41 covers SSE only)
  # - Persistent subscriptions surviving server restart

  @fsid:FS-BackfillOnStreamConnect
  Scenario: Client receives recent entries on connection with backfill parameter
    Given the query log contains at least 10 recent entries
    When a client connects to /api/v1/query-log/stream?backfill=10
    Then the server sends the last 10 entries as "event: query" frames before live events
    And the server sends "event: backfill_end" after the last backfilled entry
    And subsequent DNS queries arrive as normal live events

  @fsid:FS-BackfillFiltersApply
  Scenario: Backfill entries respect active filters
    Given the query log contains entries with result "blocked" and "allowed"
    When a client connects to /api/v1/query-log/stream?backfill=50&result=blocked
    Then only entries with result "blocked" are included in the backfill
    And the "event: backfill_end" frame is sent after the filtered backfill

  @fsid:FS-BackfillZeroDefault
  Scenario: No backfill when parameter is absent or zero
    When a client connects to /api/v1/query-log/stream without a backfill parameter
    Then no historical entries are sent before live events
    And no "event: backfill_end" frame is sent

  @fsid:FS-BackfillCappedAt500
  Scenario: Backfill request larger than 500 is capped
    When a client connects to /api/v1/query-log/stream?backfill=1000
    Then at most 500 entries are backfilled
    And no error is returned

  @fsid:FS-WebSocketStreamConnects
  Scenario: Client connects via WebSocket and receives live query events
    When a client opens a WebSocket connection to /api/v1/query-log/ws
    With a valid Authorization Bearer token
    Then the connection is accepted
    And DNS query events arrive as JSON text frames with the same schema as SSE events

  @fsid:FS-WebSocketKeepAlive
  Scenario: WebSocket connection receives keep-alive frames
    Given a connected WebSocket client with no DNS traffic
    When 15 seconds pass without any DNS query
    Then the server sends a text frame {"type":"keep-alive"}

  @fsid:FS-WebSocketAuthRequired
  Scenario: WebSocket upgrade without authentication is rejected
    When a client attempts to open /api/v1/query-log/ws without an Authorization header
    Then the WebSocket upgrade is rejected with HTTP 401
