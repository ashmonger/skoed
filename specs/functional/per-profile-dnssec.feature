Feature: Per-Profile DNSSEC Policy
  As an operator
  I want each profile to have its own DNSSEC validation mode
  So that high-security devices get strict validation while IoT devices remain compatible

  Non-goals:
  - DNSSEC signing of skoed-served local DNS entries (skoed is a recursive resolver, not an authoritative server)
  - Per-domain DNSSEC policy exceptions — use transparent mode at the profile level

  Background:
    Given the skoed API is running
    And I am authenticated as admin

  @fsid:FS-PerProfileDnssecInherit
  Scenario: A profile with dnssec_mode "inherit" uses the cluster-wide default
    Given the global dnssec_mode is "transparent"
    And profile "guests" has dnssec_mode "inherit"
    When a "guests" profile client queries a domain
    Then the DNS handler uses transparent mode for that query

  @fsid:FS-PerProfileDnssecValidate
  Scenario: A profile with dnssec_mode "validate" rejects bogus responses
    Given profile "corporate" has dnssec_mode "validate"
    When a "corporate" profile client queries a domain with an invalid DNSSEC signature
    Then the response is SERVFAIL
    And the query log records dnssec_status "bogus"

  @fsid:FS-PerProfileDnssecTransparent
  Scenario: A profile with dnssec_mode "transparent" passes responses without validation
    Given profile "iot" has dnssec_mode "transparent"
    And the global dnssec_mode is "validate"
    When an "iot" profile client queries a domain with an invalid DNSSEC signature
    Then the response is returned as-is (NOERROR)
    And the query log records dnssec_status ""

  @fsid:FS-DnssecUiDropdown
  Scenario: The profile edit modal exposes a DNSSEC dropdown
    Given I navigate to the profile editor for profile "corporate"
    When I open the DNSSEC settings
    Then I see a dropdown with options: Inherit (default), Validate, Transparent
    When I select "Validate" and save
    Then the profile's dnssec_mode is updated to "validate"

  @fsid:FS-DnssecCacheKeyIsolation
  Scenario: Validated and non-validated cache entries are stored separately
    Given profile "corporate" has dnssec_mode "validate"
    And profile "guests" has dnssec_mode "transparent"
    When "corporate" and "guests" clients both query the same domain
    Then the cache stores separate entries for validate and transparent mode
    And the corporate client always receives the validated answer
