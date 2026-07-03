Feature: Filtering Pause Enhancements (M35)
  As an operator
  I want per-client pause granularity, pause history, and webhook notification on expiry
  So that individual devices can get temporary unfiltered access without lifting the profile-wide pause

  Non-goals:
    - Per-domain pause (use allowlist expiry from M36 instead)
    - Auto-scheduled recurring pauses (use schedule bindings from M17/M37)
    - Pause propagation without Raft

  # ── Per-client pause ───────────────────────────────────────────────────────

  @fsid:FS-PerClientPauseActivates
  Scenario: Per-client pause suspends filtering for specific IPs only
    Given a profile "kids" with a blocklist containing "adsite.example"
    And the profile has clients "10.0.0.5" and "10.0.0.6"
    When POST /api/v1/profiles/kids/pause with duration_seconds=300 and client_ips=["10.0.0.5"]
    Then DNS query for "adsite.example" from "10.0.0.5" returns an answer (not NXDOMAIN)
    And DNS query for "adsite.example" from "10.0.0.6" returns NXDOMAIN

  @fsid:FS-PerClientPauseStateVisible
  Scenario: Per-client pause state is returned by the pause GET endpoint
    Given a per-client pause is active on profile "kids" for IP "10.0.0.5"
    When GET /api/v1/profiles/kids/pause
    Then the response contains active=true and client_ips=["10.0.0.5"]

  @fsid:FS-PerClientPauseCancelledEarly
  Scenario: DELETE /pause removes per-client pause
    Given a per-client pause is active on profile "kids" for IP "10.0.0.5"
    When DELETE /api/v1/profiles/kids/pause
    Then GET /api/v1/profiles/kids/pause returns active=false
    And DNS query for "adsite.example" from "10.0.0.5" returns NXDOMAIN

  @fsid:FS-PerClientPauseOtherClientsUnaffected
  Scenario: Profile-wide pause is not created when client_ips is provided
    Given a profile "kids" with clients "10.0.0.5" and "10.0.0.6"
    When POST /api/v1/profiles/kids/pause with duration_seconds=300 and client_ips=["10.0.0.5"]
    Then the pause applies only to "10.0.0.5"
    And clients not in client_ips remain filtered

  # ── Pause history ──────────────────────────────────────────────────────────

  @fsid:FS-PauseHistoryRecorded
  Scenario: Pause start and end are recorded in history
    Given filtering is paused then resumed on profile "kids"
    When GET /api/v1/profiles/kids/pause/history
    Then the response contains at least one entry with started_at, ended_at, and scope fields

  @fsid:FS-PauseHistoryCappedAt50
  Scenario: Pause history returns at most 50 entries
    Given 60 past pause events exist for profile "kids"
    When GET /api/v1/profiles/kids/pause/history
    Then the response contains exactly 50 entries (most recent first)

  @fsid:FS-PauseHistoryNotFoundForUnknownProfile
  Scenario: Pause history returns 404 for unknown profile
    When GET /api/v1/profiles/nonexistent/pause/history
    Then the response is HTTP 404

  # ── Pause-expiry webhook ───────────────────────────────────────────────────

  @fsid:FS-PauseExpiryWebhookFired
  Scenario: filter.pause_expired webhook is dispatched when a pause expires
    Given a webhook endpoint is configured for "filter.pause_expired"
    When a profile pause expires naturally (duration elapses)
    Then the webhook endpoint receives a POST with event="filter.pause_expired"
    And the payload contains profile_id and expired_at fields

  # ── Dashboard alert for new dynamic clients ────────────────────────────────

  @fsid:FS-NewDynamicClientAlertEndpoint
  Scenario: GET /api/v1/clients/new-dynamic returns clients seen for the first time
    Given a DHCP dynamic client "10.0.0.99" has appeared for the first time
    When GET /api/v1/clients/new-dynamic
    Then "10.0.0.99" appears in the response
    And the response includes first_seen timestamp

  @fsid:FS-NewDynamicClientAlertDismissed
  Scenario: POST /api/v1/clients/new-dynamic/dismiss removes a client from the alert list
    Given "10.0.0.99" is in the new-dynamic alert list
    When POST /api/v1/clients/new-dynamic/dismiss with client_ip="10.0.0.99"
    Then GET /api/v1/clients/new-dynamic no longer includes "10.0.0.99"
