Feature: DNSSEC Detail on Query Stream
  As a network operator with DNSSEC validation enabled
  I want query log entries and SSE stream events to expose the DNSSEC validation outcome
  So that I can distinguish secure, insecure, and bogus responses in real time

  # Non-goals:
  # - Full DNSSEC chain display (individual RRSIGs, DS records) in the stream
  # - Changing how DNSSEC validation works (M21 is the implementation)
  # - DNSSEC detail on the WebSocket stream (covered by M42 combined with M43)

  @fsid:FS-DnssecStatusOnStreamEvent
  Scenario: SSE stream event includes dnssec_status when DNSSEC validation is active
    Given DNSSEC validation mode is "validate"
    When a DNS query resolves with a valid DNSSEC signature
    Then the SSE stream event for that query contains "dnssec_status": "secure"
    And the event does not contain "dnssec_error"

  @fsid:FS-DnssecBogusErrorOnStreamEvent
  Scenario: SSE stream event includes dnssec_error when DNSSEC is bogus
    Given DNSSEC validation mode is "validate"
    When a DNS query returns a bogus DNSSEC response
    Then the SSE stream event contains "dnssec_status": "bogus"
    And the event contains a non-empty "dnssec_error" string

  @fsid:FS-DnssecStatusFilterOnStream
  Scenario: Operator filters live stream by DNSSEC status
    Given an active SSE subscription to /api/v1/query-log/stream?dnssec_status=bogus
    When DNS queries arrive with various DNSSEC outcomes
    Then only events with "dnssec_status": "bogus" are delivered to the subscriber
    And events with "dnssec_status": "secure" or "" are suppressed

  @fsid:FS-DnssecStatusOmittedWhenDisabled
  Scenario: DNSSEC fields are omitted when validation is disabled
    Given DNSSEC mode is "transparent"
    When a DNS query is processed
    Then the SSE stream event does not contain "dnssec_status"
    And the paginated query log entry does not contain "dnssec_status"

  @fsid:FS-DnssecStatusInPaginatedLog
  Scenario: Paginated query log exposes dnssec_status and dnssec_error
    Given DNSSEC validation mode is "validate"
    When a DNS query resolves with an insecure delegation
    Then GET /api/v1/query-log returns an entry with "dnssec_status": "insecure"

  @fsid:FS-DnssecColumnInWebUI
  Scenario: Web UI query log table shows a DNSSEC status column
    Given the operator opens the Query Log page
    And DNSSEC validation is enabled
    Then the table displays a DNSSEC column
    And each row shows an icon matching the dnssec_status value (secure / insecure / bogus)
