Feature: Dual-stack DNS listener
  As a network administrator
  I want skoed to accept DNS queries over both IPv4 and IPv6
  So that all clients on my network can use skoed regardless of their network stack

  Non-goals:
    - This feature does not cover DNS-over-HTTPS or DNS-over-TLS server endpoints
    - This feature does not cover IPv6 upstream resolver selection (covered in dns-query-forwarding.feature)

  Background:
    Given skoed is running with dual-stack enabled

  @fsid:FS-DualStackDnsIPv4Listener
  Scenario: Client resolves a domain over IPv4
    When a client with IPv4 address "192.168.1.10" sends an A query for "example.com"
    Then skoed accepts the query on its IPv4 listener (port 53)
    And returns the answer to the client

  @fsid:FS-DualStackDnsIPv6Listener
  Scenario: Client resolves a domain over IPv6
    When a client with IPv6 address "fd00::1" sends an A query for "example.com"
    Then skoed accepts the query on its IPv6 listener (port 53)
    And returns the answer to the client

  @fsid:FS-DualStackDnsIPv6ClientIdentification
  Scenario: IPv6 client is identified in the query log
    When a client with IPv6 address "fd00::42" sends an A query for "example.com"
    Then the query is logged with client address "fd00::42"

  @fsid:FS-DualStackDnsNullBlockIPv4
  Scenario: NULL block response for an A query
    Given "ads.example.com" is on an active blocklist with block policy NULL
    When a client sends an A query for "ads.example.com"
    Then skoed returns an A record with address "0.0.0.0"

  @fsid:FS-DualStackDnsNullBlockIPv6
  Scenario: NULL block response for an AAAA query
    Given "ads.example.com" is on an active blocklist with block policy NULL
    When a client sends an AAAA query for "ads.example.com"
    Then skoed returns an AAAA record with address "::"
