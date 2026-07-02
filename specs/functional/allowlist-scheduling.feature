Feature: Allowlist Scheduling and Per-Entry Metadata
  As an operator
  I want allowlist entries to carry optional expiry timestamps, audit notes, and schedule references
  So that time-limited exceptions and weekend-only allows are enforced automatically

  Non-goals:
  - Per-entry different block policies (allowlist means allow, full stop)
  - Time-gated entries based on client IP (profile-level granularity only)

  Background:
    Given the skoed API is running
    And I am authenticated as admin

  @fsid:FS-AllowlistEntryWithExpiry
  Scenario: An allowlist entry with expires_at is automatically inactive after expiry
    Given I add an allowlist entry for domain "youtube.com" to profile "kids"
    And the entry has expires_at set to 1 hour in the future
    When the current time passes the expires_at timestamp
    Then DNS queries for "youtube.com" from a "kids" profile client are blocked
    And the entry is pruned from the allowlist within 24 hours

  @fsid:FS-AllowlistEntryNote
  Scenario: An allowlist entry can carry an audit note
    Given I add an allowlist entry for domain "school.edu" to profile "kids"
    And the entry has a note "Approved by parent on 2026-07-01"
    When I list the allowlist for profile "kids"
    Then the entry for "school.edu" includes the note text
    And the note is returned in the GET /api/v1/profiles/{id}/allowlist response

  @fsid:FS-AllowlistTimeGatedEntry
  Scenario: An allowlist entry with a schedule reference is only active during schedule windows
    Given a schedule "weekends" with windows covering Saturday and Sunday
    And I add an allowlist entry for domain "gaming.com" to profile "kids"
    And the entry references schedule "weekends"
    When DNS queries arrive on a weekday
    Then "gaming.com" is blocked for "kids" profile clients
    When DNS queries arrive on a Saturday
    Then "gaming.com" is allowed for "kids" profile clients

  @fsid:FS-SharedAllowlist
  Scenario: A shared allowlist can be linked to multiple profiles
    Given I create a shared allowlist named "approved-edu-sites"
    And I add domain "wikipedia.org" to the shared allowlist
    And I link "approved-edu-sites" to profiles "kids" and "teens"
    When a "kids" profile client queries "wikipedia.org"
    Then the query is allowed
    When a "teens" profile client queries "wikipedia.org"
    Then the query is allowed

  @fsid:FS-AllowlistBulkImport
  Scenario: Domains can be bulk-imported to a profile's allowlist
    Given profile "kids" has an empty allowlist
    When I POST a newline-delimited list of 3 domains to the import endpoint
    Then the profile's allowlist contains all 3 domains
    And the response includes a count of entries added
