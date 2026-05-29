Feature: Allowlist management
  As a network administrator
  I want to define domains that are always resolved regardless of blocklists
  So that I can permit specific domains that are incorrectly blocked

  Non-goals:
    - This feature does not cover per-client allowlists (Milestone 3)
    - Allowlist entries do not apply to local DNS entries (local entries are always served)

  Background:
    Given dblock is running
    And a blocklist "ads" is active containing "allowed-but-blocked.example.com"

  @fsid:FS-AllowlistAddDomain
  Scenario: Admin adds a domain to the global allowlist
    When the admin adds "allowed-but-blocked.example.com" to the allowlist
    Then the domain is in the global allowlist

  @fsid:FS-AllowlistOverridesBlocklist
  Scenario: Allowlisted domain is resolved even when it appears on a blocklist
    Given "allowed-but-blocked.example.com" is in the global allowlist
    When a client sends an A query for "allowed-but-blocked.example.com"
    Then dblock forwards the query to an upstream resolver
    And returns the upstream answer
    And does not return a block response

  @fsid:FS-AllowlistRemoveDomain
  Scenario: Admin removes a domain from the allowlist
    Given "allowed-but-blocked.example.com" is in the global allowlist
    When the admin removes "allowed-but-blocked.example.com" from the allowlist
    Then queries for "allowed-but-blocked.example.com" are blocked again

  @fsid:FS-AllowlistWildcardEntry
  Scenario: Wildcard allowlist entry overrides blocklist for apex and all subdomains
    Given "*.safe.example.com" is in the global allowlist
    And "safe.example.com" is on an active blocklist
    When a client sends an A query for "safe.example.com"
    Then dblock forwards the query and returns the upstream answer
    When a client sends an A query for "sub.safe.example.com"
    Then dblock forwards the query and returns the upstream answer
    When a client sends an A query for "deep.sub.safe.example.com"
    Then dblock forwards the query and returns the upstream answer

  @fsid:FS-AllowlistDoesNotAffectUnblockedDomains
  Scenario: Allowlist entry for a non-blocked domain has no visible effect
    Given "safe.example.com" is in the global allowlist
    And "safe.example.com" is not on any active blocklist
    When a client sends an A query for "safe.example.com"
    Then dblock forwards the query and returns the upstream answer
