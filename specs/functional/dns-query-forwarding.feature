Feature: DNS query forwarding
  As a network administrator
  I want dblock to forward DNS queries to configured upstream resolvers
  So that clients on my network can resolve internet domains

  Non-goals:
    - This feature does not cover root DNS recursive resolution (see root-dns-resolution.feature)
    - This feature does not cover domain blocking (see domain-blocking.feature)
    - This feature does not cover local DNS entries (see local-dns-entry-management.feature)

  Background:
    Given dblock is running with upstream resolvers configured as ["1.1.1.1:53", "8.8.8.8:53"]

  @fsid:FS-DnsQueryForwarding
  Scenario: Client resolves an internet domain
    When a client sends an A query for "example.com"
    Then dblock forwards the query to an upstream resolver
    And returns the upstream answer to the client
    And the response status is NOERROR

  @fsid:FS-DnsQueryForwardingTcp
  Scenario: Client resolves a domain over TCP
    When a client sends a TCP A query for "example.com"
    Then dblock forwards the query to an upstream resolver over TCP
    And returns the upstream answer to the client

  @fsid:FS-DnsQueryForwardingFallback
  Scenario: Primary upstream resolver is unreachable
    Given the first configured upstream resolver is unreachable
    When a client sends an A query for "example.com"
    Then dblock forwards the query to the next upstream resolver
    And returns the upstream answer to the client

  @fsid:FS-DnsQueryForwardingAllUpstreamsUnreachable
  Scenario: All upstream resolvers are unreachable
    Given all configured upstream resolvers are unreachable
    When a client sends an A query for "example.com"
    Then dblock returns a SERVFAIL response to the client

  @fsid:FS-DnsQueryForwardingAAAA
  Scenario: Client resolves an AAAA record
    When a client sends an AAAA query for "example.com"
    Then dblock forwards the query to an upstream resolver
    And returns the AAAA answer to the client

  @fsid:FS-DnsQueryForwardingMultipleRecordTypes
  Scenario: Client queries for a non-A/AAAA record type
    When a client sends an MX query for "example.com"
    Then dblock forwards the query to an upstream resolver
    And returns the upstream answer to the client
