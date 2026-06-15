Feature: Filtering Pause
  As an operator of a skoed DNS filtering server,
  I want to temporarily suspend DNS filtering for all clients or for a specific profile's clients,
  So that I can handle break-glass situations, debugging, or guest access without permanently altering filtering rules.

  Non-goals:
    - Pausing DNSSEC validation, allowlist enforcement, or local DNS entries.
    - Pausing query forwarding or upstream resolver selection.
    - Permanently disabling filtering (use profile configuration for that).
    - Per-client pause granularity (pause applies to a profile or globally, not to individual clients).
    - Pause durations beyond the configured ceiling (filtering.pause_max_seconds).
    - Authentication or authorization for the pause API (out of scope for this milestone).

  Background:
    Given a skoed node is running
    And a blocklist "ads" exists containing the domain "doubleclick.net"
    And the default profile uses the "ads" blocklist
    And a profile "kids" exists using the "ads" blocklist and matching clients in the subnet 192.168.10.0/24
    And a profile "work" exists using the "ads" blocklist and matching clients in the subnet 192.168.20.0/24
    And no filtering pause is currently active for any scope

  # ---------------------------------------------------------------------------
  # Global pause — core behavior
  # ---------------------------------------------------------------------------

  @fsid:FS-FilterPauseGlobalSuspendsAllProfiles
  Scenario: Global pause suspends filtering for all clients across all profiles
    Given filtering is active and a DNS query for "doubleclick.net" from 192.168.10.5 is blocked
    And a DNS query for "doubleclick.net" from 192.168.20.5 is blocked
    When the operator activates a global filtering pause for 120 seconds with reason "emergency debug"
    Then a DNS query for "doubleclick.net" from 192.168.10.5 resolves to an upstream address
    And a DNS query for "doubleclick.net" from 192.168.20.5 resolves to an upstream address
    And a DNS query for "doubleclick.net" from 10.0.0.1 resolves to an upstream address
    And the global pause status shows duration 120 seconds, reason "emergency debug", and a remaining countdown
    And local DNS entries are still resolved correctly
    And allowlisted domains are still resolved correctly

  @fsid:FS-FilterPauseGlobalExpiresAutomatically
  Scenario: Global pause expires automatically after the configured duration
    Given the operator has activated a global filtering pause for 3 seconds
    When 3 seconds have elapsed
    Then a DNS query for "doubleclick.net" from 192.168.10.5 is blocked again
    And the global pause status shows no active pause

  @fsid:FS-FilterPauseGlobalCancelledEarly
  Scenario: Global pause can be cancelled before it expires
    Given the operator has activated a global filtering pause for 300 seconds
    When the operator cancels the global filtering pause
    Then a DNS query for "doubleclick.net" from 192.168.10.5 is blocked immediately
    And the global pause status shows no active pause

  @fsid:FS-FilterPauseGlobalSurvivesRestart
  Scenario: An active global pause survives a node restart
    Given the operator has activated a global filtering pause for 600 seconds with reason "maintenance"
    When the skoed node is restarted
    Then the global pause is still active after restart
    And a DNS query for "doubleclick.net" from 192.168.10.5 resolves to an upstream address
    And the global pause status shows reason "maintenance" and a remaining countdown less than 600 seconds

  @fsid:FS-FilterPauseGlobalEnforcedCeiling
  Scenario: Global pause duration is capped at the configured ceiling
    Given the operator has set filtering.pause_max_seconds to 300
    When the operator attempts to activate a global filtering pause for 600 seconds
    Then the request is rejected with an error indicating the maximum allowed duration is 300 seconds
    And no global pause is activated

  @fsid:FS-FilterPauseGlobalIdempotentWhileActive
  Scenario: Activating a global pause while one is already active replaces it
    Given the operator has activated a global filtering pause for 600 seconds with reason "first pause"
    When the operator activates a new global filtering pause for 60 seconds with reason "second pause"
    Then the global pause status shows duration 60 seconds and reason "second pause"
    And a DNS query for "doubleclick.net" from 192.168.10.5 resolves to an upstream address

  @fsid:FS-FilterPauseQueryLogMarkedDuringGlobalPause
  Scenario: Query log entries are marked with pause_active during a global pause
    Given the operator has activated a global filtering pause for 120 seconds
    When a DNS query for "doubleclick.net" is made from 192.168.10.5
    Then the query log entry for that request shows pause_active as true
    And after the global pause expires, a new DNS query for "doubleclick.net" from 192.168.10.5 produces a query log entry with pause_active as false

  @fsid:FS-FilterPauseQueryLogMarkedDuringProfilePause
  Scenario: Query log entries are marked with pause_active during a profile pause
    Given the operator has activated a filtering pause on the "kids" profile for 120 seconds
    When a DNS query for "doubleclick.net" is made from 192.168.10.5
    Then the query log entry for that request shows pause_active as true
    And a DNS query for "doubleclick.net" from 192.168.20.5 produces a query log entry with pause_active as false

  # ---------------------------------------------------------------------------
  # Per-profile pause — core behavior
  # ---------------------------------------------------------------------------

  @fsid:FS-FilterPauseProfileSuspendsOneProfile
  Scenario: Profile pause suspends filtering only for that profile's clients
    Given filtering is active for all profiles
    When the operator activates a filtering pause on the "kids" profile for 120 seconds with reason "guests"
    Then a DNS query for "doubleclick.net" from 192.168.10.5 resolves to an upstream address
    And the "kids" profile pause status shows duration 120 seconds, reason "guests", and a remaining countdown

  @fsid:FS-FilterPauseProfileDoesNotAffectOtherProfiles
  Scenario: Profile pause does not affect clients matched by other profiles
    When the operator activates a filtering pause on the "kids" profile for 120 seconds
    Then a DNS query for "doubleclick.net" from 192.168.20.5 is still blocked
    And a DNS query for "doubleclick.net" from 10.0.0.1 is still blocked

  @fsid:FS-FilterPauseProfileExpiresAutomatically
  Scenario: Profile pause expires automatically after the configured duration
    Given the operator has activated a filtering pause on the "kids" profile for 3 seconds
    When 3 seconds have elapsed
    Then a DNS query for "doubleclick.net" from 192.168.10.5 is blocked again
    And the "kids" profile pause status shows no active pause

  @fsid:FS-FilterPauseProfileCancelledEarly
  Scenario: Profile pause can be cancelled before it expires
    Given the operator has activated a filtering pause on the "kids" profile for 300 seconds
    When the operator cancels the filtering pause on the "kids" profile
    Then a DNS query for "doubleclick.net" from 192.168.10.5 is blocked immediately
    And the "kids" profile pause status shows no active pause

  @fsid:FS-FilterPauseProfileMultipleSimultaneous
  Scenario: Multiple profiles can be paused simultaneously and independently
    When the operator activates a filtering pause on the "kids" profile for 60 seconds
    And the operator activates a filtering pause on the "work" profile for 120 seconds
    Then a DNS query for "doubleclick.net" from 192.168.10.5 resolves to an upstream address
    And a DNS query for "doubleclick.net" from 192.168.20.5 resolves to an upstream address
    When 60 seconds have elapsed
    Then a DNS query for "doubleclick.net" from 192.168.10.5 is blocked again
    And a DNS query for "doubleclick.net" from 192.168.20.5 still resolves to an upstream address
    When another 60 seconds have elapsed
    Then a DNS query for "doubleclick.net" from 192.168.20.5 is blocked again

  # ---------------------------------------------------------------------------
  # Global + profile interaction
  # ---------------------------------------------------------------------------

  @fsid:FS-FilterPauseGlobalOverridesProfile
  Scenario: Global pause takes precedence over per-profile filtering regardless of profile pause state
    Given the "kids" profile does not have an active pause
    When the operator activates a global filtering pause for 120 seconds
    Then a DNS query for "doubleclick.net" from 192.168.10.5 resolves to an upstream address
    And a DNS query for "doubleclick.net" from 192.168.20.5 resolves to an upstream address
    When the operator also activates a filtering pause on the "kids" profile for 60 seconds
    Then a DNS query for "doubleclick.net" from 192.168.10.5 still resolves to an upstream address
    When the "kids" profile pause expires after 60 seconds
    Then a DNS query for "doubleclick.net" from 192.168.10.5 still resolves to an upstream address
    And a DNS query for "doubleclick.net" from 192.168.20.5 still resolves to an upstream address

  # ---------------------------------------------------------------------------
  # Settings
  # ---------------------------------------------------------------------------

  @fsid:FS-FilterPauseCeilingEnforced
  Scenario: Pause duration is rejected when it exceeds the configured ceiling
    Given the operator has set filtering.pause_max_seconds to 1800
    When the operator attempts to activate a global filtering pause for 1801 seconds
    Then the request is rejected with an error indicating the maximum allowed duration is 1800 seconds
    And no global pause is activated
    When the operator attempts to activate a filtering pause on the "kids" profile for 1801 seconds
    Then the request is rejected with an error indicating the maximum allowed duration is 1800 seconds
    And no pause is activated on the "kids" profile

  @fsid:FS-FilterPauseFeatureDisabledWhenCeilingZero
  Scenario: Pause feature is disabled entirely when the ceiling is set to zero
    Given the operator has set filtering.pause_max_seconds to 0
    When the operator attempts to activate a global filtering pause for 60 seconds
    Then the request is rejected with an error indicating the filtering pause feature is disabled
    When the operator attempts to activate a filtering pause on the "kids" profile for 60 seconds
    Then the request is rejected with an error indicating the filtering pause feature is disabled
    And filtering continues normally for all clients
