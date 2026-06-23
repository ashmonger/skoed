Feature: Per-Profile Allowlists (Full)
  As a network administrator
  I want complete management of per-profile allowlists
  So that individual profiles can exempt specific domains from blocking with atomic updates,
  correct cache invalidation, and wildcard support.

  Non-goals:
    - Allowlist sharing between profiles
    - Allowlist scheduling / time-gated entries
    - Per-entry metadata (notes, expiry)

  Background:
    Given a skoed node is running
    And a profile "kids" exists with a blocklist that blocks "blocked.example.com"
    And the kids profile has client IP "192.168.1.10" assigned

  @fsid:FS-PerProfileAllowlistPutReplacesAll
  Scenario: PUT replaces full allowlist atomically
    Given the kids profile allowlist contains ["old1.com", "old2.com"]
    When I PUT ["new1.com", "new2.com"] to /api/v1/profiles/kids/allowlist
    Then the response status is 204
    And GET /api/v1/profiles/kids/allowlist returns exactly ["new1.com", "new2.com"]
    And "old1.com" is no longer in the allowlist

  @fsid:FS-PerProfileAllowlistPutClearsOnEmpty
  Scenario: PUT with empty list clears the allowlist
    Given the kids profile allowlist contains ["domain.com"]
    When I PUT [] to /api/v1/profiles/kids/allowlist
    Then the response status is 204
    And GET /api/v1/profiles/kids/allowlist returns an empty list

  @fsid:FS-PerProfileAllowlistDeletePurgesCache
  Scenario: DELETE purges DNS cache entry on removal
    Given "allowed.example.com" is in the kids profile allowlist
    And the DNS resolver has a cached response for "allowed.example.com"
    When I DELETE /api/v1/profiles/kids/allowlist/allowed.example.com
    Then the response status is 204
    And a DNS query for "allowed.example.com" from a kids profile client returns NXDOMAIN
    # The domain must be blocked immediately; no stale cache entry is served.

  @fsid:FS-PerProfileAllowlistWildcardSubdomain
  Scenario: Wildcard entry allows subdomains
    Given "*.example.com" is in the kids profile allowlist
    When a kids profile client queries "sub.example.com"
    Then the response is NOERROR (not NXDOMAIN)

  @fsid:FS-PerProfileAllowlistWildcardApex
  Scenario: Wildcard entry also allows the apex domain
    Given "*.example.com" is in the kids profile allowlist
    When a kids profile client queries "example.com"
    Then the response is NOERROR (not NXDOMAIN)

  @fsid:FS-PerProfileAllowlistCountBadge
  Scenario: Profile count badge reflects allowlist size
    Given the kids profile allowlist contains ["a.com", "b.com", "c.com"]
    When I GET /api/v1/profiles/kids/allowlist
    Then the response contains exactly 3 entries

  @fsid:FS-GlobalAllowlistScopeSwitcher
  Scenario: Global allowlist page scope switcher shows profile list
    Given profiles "kids" and "adults" exist
    When I load the allowlist management page
    Then the scope selector contains an option for "Global (all clients)"
    And the scope selector contains an option for "kids"
    And the scope selector contains an option for "adults"
