Feature: Root DNS resolution
  As a network administrator
  I want dblock to resolve DNS queries recursively from IANA root nameservers
  So that my network does not depend on any third-party DNS provider

  Non-goals:
    - This feature does not cover DNSSEC validation (dblock proxies DNSSEC records; it does not validate)
    - This feature does not cover upstream forwarding (see dns-query-forwarding.feature)

  Background:
    Given dblock is running with root DNS resolution enabled
    And no upstream resolvers are configured

  @fsid:FS-RootDnsResolution
  Scenario: Client resolves a domain via root nameservers
    When a client sends an A query for "example.com"
    Then dblock performs recursive resolution starting from the IANA root nameservers
    And returns the resolved answer to the client
    And the response status is NOERROR

  @fsid:FS-RootDnsResolutionAAAA
  Scenario: Client resolves an AAAA record via root nameservers
    When a client sends an AAAA query for "example.com"
    Then dblock performs recursive resolution starting from the IANA root nameservers
    And returns the AAAA answer to the client

  @fsid:FS-RootDnsResolutionRestrictedToTrustedSubnets
  Scenario: Recursive resolution is restricted to trusted client subnets
    Given dblock has trusted subnets configured as ["192.168.0.0/16", "10.0.0.0/8"]
    When a client with IP "203.0.113.1" (outside trusted subnets) sends an A query for "example.com"
    Then dblock returns a REFUSED response
    And does not perform recursive resolution

  @fsid:FS-RootDnsResolutionFromTrustedSubnet
  Scenario: Client in trusted subnet can use recursive resolution
    Given dblock has trusted subnets configured as ["192.168.0.0/16"]
    When a client with IP "192.168.1.10" sends an A query for "example.com"
    Then dblock performs recursive resolution
    And returns the resolved answer to the client

  @fsid:FS-RootDnsResolutionAirGapped
  Scenario: Node operates without any upstream DNS when root resolution is enabled
    Given dblock is running with root DNS resolution enabled
    And no upstream resolvers are configured
    And the node has no internet access to any third-party DNS provider
    When a client sends an A query for "example.com"
    Then dblock resolves the query via root nameservers
    And returns the answer to the client
