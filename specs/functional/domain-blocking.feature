Feature: Domain blocking
  As a network administrator
  I want skoed to intercept DNS queries for domains on active blocklists
  So that clients on my network cannot reach blocked domains

  Non-goals:
    - This feature does not cover blocklist management (see blocklist-management.feature)
    - This feature does not cover allowlist overrides (see allowlist-management.feature)
    - This feature does not cover per-client profiles (Milestone 3)
    - skoed does not block at the HTTP/HTTPS layer; only DNS resolution is intercepted

  Background:
    Given skoed is running
    And a blocklist "ads" is active containing the domain "ads.example.com"

  @fsid:FS-DomainBlockingNxdomain
  Scenario: Blocked domain returns NXDOMAIN (global default)
    Given the global block policy is NXDOMAIN
    When a client sends an A query for "ads.example.com"
    Then skoed returns a NXDOMAIN response
    And does not contact any upstream resolver

  @fsid:FS-DomainBlockingNull
  Scenario: Blocked domain returns NULL IP (per-blocklist policy)
    Given the "ads" blocklist has block policy NULL
    When a client sends an A query for "ads.example.com"
    Then skoed returns an A record with address "0.0.0.0"
    And does not contact any upstream resolver

  @fsid:FS-DomainBlockingNodata
  Scenario: Blocked domain returns NODATA (per-blocklist policy)
    Given the "ads" blocklist has block policy NODATA
    When a client sends an A query for "ads.example.com"
    Then skoed returns a NOERROR response with an empty answer section
    And does not contact any upstream resolver

  @fsid:FS-DomainBlockingSubdomain
  Scenario: Subdomain of a blocked domain is blocked
    Given the blocklist "ads" contains the domain "tracker.example.com"
    When a client sends an A query for "deep.sub.tracker.example.com"
    Then skoed returns the configured block response

  @fsid:FS-DomainBlockingNotBlocked
  Scenario: Non-blocked domain is resolved normally
    When a client sends an A query for "safe.example.com"
    Then skoed forwards the query to an upstream resolver
    And returns the upstream answer

  @fsid:FS-DomainBlockingPerBlocklistPolicyOverridesGlobal
  Scenario: Per-blocklist policy takes precedence over global default
    Given the global block policy is NXDOMAIN
    And the "ads" blocklist has block policy NULL
    When a client sends an A query for "ads.example.com"
    Then skoed returns an A record with address "0.0.0.0"
