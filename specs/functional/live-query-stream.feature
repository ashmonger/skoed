Feature: Live Query Stream
  As a network administrator
  I want to watch DNS queries stream in real time
  So that I can observe client behaviour, spot anomalies, and debug filtering without polling

  Non-goals:
    - Historical replay or backfill of queries made before the connection opened
    - Write operations on the stream (SSE is read-only)
    - WebSocket bidirectional communication
    - Per-query DNSSEC chain details (query log stores the outcome only)
    - Cluster-wide fan-out (each node streams its own queries only)

  @fsid:FS-LiveQueryStreamConnect
  Scenario: Client opens a live stream and receives new queries
    Given the admin is authenticated with a valid Bearer token
    When the admin opens GET /api/v1/query-log/stream
    Then the response Content-Type is text/event-stream
    And the connection stays open
    And each subsequent DNS query processed by the node is delivered as an SSE event

  @fsid:FS-LiveQueryStreamEventShape
  Scenario: Each SSE event contains the expected query fields
    Given the admin has an open live stream connection
    When a client resolves "example.com" A and the result is "forwarded"
    Then the next SSE event has event type "query"
    And the event data contains fields: domain, type, client_ip, profile_id, result, duration_ms, timestamp

  @fsid:FS-LiveQueryStreamBlockedQuery
  Scenario: Blocked queries are streamed with result "blocked"
    Given the admin has an open live stream connection
    And "ads.example.com" is on an active blocklist
    When a client resolves "ads.example.com" A
    Then the next SSE event has result "blocked"

  @fsid:FS-LiveQueryStreamFilterByProfile
  Scenario: Stream can be filtered to a single profile
    Given the admin has an open live stream connection on /api/v1/query-log/stream?profile_id=kids
    When a query arrives from the "kids" profile
    And a query arrives from the "adults" profile
    Then only the "kids" query event is delivered on the stream

  @fsid:FS-LiveQueryStreamFilterByResult
  Scenario: Stream can be filtered to blocked queries only
    Given the admin has an open live stream connection on /api/v1/query-log/stream?result=blocked
    When a forwarded query and a blocked query arrive
    Then only the blocked query event is delivered on the stream

  @fsid:FS-LiveQueryStreamUnauthenticated
  Scenario: Unauthenticated stream request is rejected
    Given no Authorization header is provided
    When the client opens GET /api/v1/query-log/stream
    Then the response status is 401

  @fsid:FS-LiveQueryStreamDisconnect
  Scenario: Server cleans up cleanly when the client disconnects
    Given the admin has an open live stream connection
    When the client closes the connection
    Then no goroutine leak or error is logged for that connection

  @fsid:FS-LiveQueryStreamHeartbeat
  Scenario: Server sends periodic keep-alive comments to prevent proxy timeouts
    Given the admin has an open live stream connection with no queries arriving
    When 15 seconds elapse
    Then at least one SSE comment line (": keep-alive") is sent on the stream
