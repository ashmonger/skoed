Feature: DNSSEC transparent proxy
  As a network administrator
  I want skoed to forward DNSSEC records transparently to clients that request them
  So that DNSSEC-validating clients on my network can perform their own validation

  Non-goals:
    - skoed does not validate DNSSEC signatures itself
    - skoed does not set the AD (Authenticated Data) bit on responses
    - skoed does not manage trust anchors
    - DNSSEC validation by skoed is out of scope for Milestone 1

  Background:
    Given skoed is running with an upstream resolver that supports DNSSEC

  @fsid:FS-DnssecTransparentProxy
  Scenario: Client with DO bit set receives DNSSEC records
    When a client sends an A query for "example.com" with the DNSSEC OK (DO) bit set
    Then skoed forwards the query to the upstream resolver with the DO bit set
    And returns the response including RRSIG records to the client
    And does not strip or modify any DNSSEC records

  @fsid:FS-DnssecTransparentProxyWithoutDoBit
  Scenario: Client without DO bit does not receive DNSSEC records
    When a client sends an A query for "example.com" without the DO bit set
    Then skoed forwards the query to the upstream resolver
    And returns the response without adding DNSSEC records

  @fsid:FS-DnssecTransparentProxyBlockedDomain
  Scenario: DNSSEC records are not returned for blocked domains
    Given "ads.example.com" is on an active blocklist
    When a client sends an A query for "ads.example.com" with the DO bit set
    Then skoed returns the configured block response (NXDOMAIN, NULL, or NODATA)
    And does not contact the upstream resolver for this query
