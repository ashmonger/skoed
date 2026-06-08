Feature: DHCP source connectors
  As a dblock operator with a LAN DHCP server
  I want dblock to read leases from my DHCP source
  So that hostname + MAC + Client-ID enrich every query

  Background:
    Given dblock supports three connector kinds: kea, dnsmasq, http_json
    And every connector produces the same canonical Lease record:
      | field      | type           |
      | ip         | string (v4)    |
      | mac        | string (lower) |
      | hostname   | string         |
      | client_id  | string         |
      | source     | string         |
      | expires_at | RFC3339 string |

  @fsid:FS-DhcpKeaReadsLeases
  Scenario: Kea connector reads lease4-get-all from the control-agent
    Given a Kea control-agent at "http://kea.lan:8000/" returns a lease4-get-all
      command-response wrapper with three active leases
    When the dblock Kea connector polls the source
    Then the parsed lease set contains three records
    And each record carries ip, mac (lowercase), hostname, and client-id
    And lease expiry is computed from valid-lft + cltt

  @fsid:FS-DhcpKeaHandlesAuth
  Scenario: Kea connector sends configured Basic Auth credentials
    Given the connector config provides username + password
    When the connector polls the Kea source
    Then the HTTP request carries the Authorization: Basic header
    And on 401 the connector logs the failure and reuses the cached lease set

  @fsid:FS-DhcpDnsmasqParsesLeaseFile
  Scenario: dnsmasq connector parses /var/lib/misc/dnsmasq.leases
    Given a dnsmasq lease file with 5 entries of the form
      "<expiry-epoch> <mac> <ip> <hostname> <client-id>"
    When the dblock dnsmasq connector reads the file
    Then five Lease records are produced
    And entries with hostname "*" (the dnsmasq unknown-hostname marker) yield an empty hostname
    And the 5th column (client-id) is preserved verbatim

  @fsid:FS-DhcpDnsmasqSkipsExpired
  Scenario: dnsmasq connector drops leases whose expiry epoch is in the past
    Given a dnsmasq lease file containing one expired and two active leases
    When the connector reads the file
    Then only the two active leases appear in the parsed set

  @fsid:FS-DhcpGenericJsonRoundtrip
  Scenario: Generic HTTP JSON connector reads the documented shape
    Given an operator-supplied HTTP endpoint returns a JSON array
      `[{"ip":"...","mac":"...","hostname":"...","client_id":"...","expires_at":"..."}]`
    When the connector polls the endpoint
    Then the parsed Lease records match the JSON exactly (after MAC lowercasing)

  @fsid:FS-DhcpGenericRetry
  Scenario: Generic HTTP connector tolerates transient errors
    Given the endpoint returns 503 on the first call and 200 on the second
    When the connector polls twice with 500 ms between attempts
    Then the second call's leases are cached and surfaced

  @fsid:FS-DhcpConnectorRefreshInterval
  Scenario: Refresh interval is honoured per connector
    Given the connector config sets refresh_seconds = 30
    When the node has been running for 35 seconds
    Then the source has been polled exactly twice (boot + one refresh)

  @fsid:FS-DhcpConnectorMalformedSkips
  Scenario: Malformed lines / fields are skipped, not fatal
    Given a dnsmasq lease file with one malformed line in the middle
    When the connector reads the file
    Then the malformed line is logged at WARN
    And the rest of the file is still parsed

  Non-goals:
    - ISC dhcpd lease-file format (declared EOL by ISC in 2022)
    - DHCPv6 leases
    - Lease writes (read-only)
    - Failover between two connectors (one source per node; operator
      picks the primary)
