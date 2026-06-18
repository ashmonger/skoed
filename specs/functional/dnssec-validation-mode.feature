Feature: DNSSEC validation mode
  As a network administrator
  I want skoed to optionally validate DNSSEC signatures on upstream responses
  So that clients are protected from BOGUS DNS data even when they do not perform their own validation

  Non-goals:
    - skoed does not sign local DNS entries (authoritative signing)
    - per-profile DNSSEC policy is out of scope
    - cache eviction or cache-poisoning behavior changes are out of scope
    - RFC 5011 automatic trust-anchor rollover is out of scope
    - full DNSSEC chain validation (RRSIGs verified against trust anchor) is out of scope for this milestone

  Background:
    Given skoed is running with an upstream resolver that supports DNSSEC

  @fsid:FS-DnssecModeConfigurable
  Scenario: Operator configures DNSSEC validation mode via settings API
    Given the current dns.dnssec_mode is "transparent"
    When the operator sends PATCH /api/v1/settings with body {"dns":{"dnssec_mode":"validate"}}
    Then skoed returns HTTP 200
    And a subsequent GET /api/v1/settings returns dns.dnssec_mode as "validate"
    When the operator sends PATCH /api/v1/settings with body {"dns":{"dnssec_mode":"transparent"}}
    Then skoed returns HTTP 200
    And a subsequent GET /api/v1/settings returns dns.dnssec_mode as "transparent"

  @fsid:FS-DnssecValidateBogusServfail
  Scenario: BOGUS response in validate mode causes SERVFAIL to client
    Given dns.dnssec_mode is set to "validate"
    And the upstream resolver returns a response with AD=0 and at least one RRSIG record in the answer section
    When a client sends an A query for "bogus.example.com"
    Then skoed returns SERVFAIL to the client
    And the query log entry for this query has dnssec_status set to "bogus"

  @fsid:FS-DnssecValidateOkPassthrough
  Scenario: AD=1 response in validate mode passes through to client
    Given dns.dnssec_mode is set to "validate"
    And the upstream resolver returns a response with AD=1
    When a client sends an A query for "signed.example.com"
    Then skoed passes the answer through to the client unchanged
    And the query log entry for this query has dnssec_status set to "ok"

  @fsid:FS-DnssecValidateInsecurePassthrough
  Scenario: Response with no RRSIG records in validate mode passes through as insecure
    Given dns.dnssec_mode is set to "validate"
    And the upstream resolver returns a response with AD=0 and no RRSIG records in the answer section
    When a client sends an A query for "unsigned.example.com"
    Then skoed passes the answer through to the client unchanged
    And the query log entry for this query has dnssec_status set to "insecure"

  @fsid:FS-DnssecValidateQueryLogStatus
  Scenario: Query log entries include dnssec_status in validate mode
    Given dns.dnssec_mode is set to "validate"
    When three queries are resolved: one with AD=1, one BOGUS, one with no RRSIG
    Then the query log contains three entries
    And the first entry has dnssec_status "ok"
    And the second entry has dnssec_status "bogus"
    And the third entry has dnssec_status "insecure"
