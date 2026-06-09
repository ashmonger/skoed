Feature: DHCPv6 lease parsing (Kea + dnsmasq)
  As a skoed operator running a dual-stack LAN
  I want skoed to read DHCPv6 leases from Kea and dnsmasq
  So that IPv6-addressed clients appear in the Clients page with their DUID
  And dual-stack devices show both their IPv4 and IPv6 addresses on one row

  Background:
    Given skoed extends the canonical Lease record with three optional fields:
      | field         | type                          |
      | ipv6_addresses| array of strings (IA_NA+IA_PD)|
      | duid          | string (DHCPv6 client DUID)   |
      | is_dual_stack | boolean                       |
    And the existing M3.6 fields (ip, mac, hostname, client_id, source, expires_at) remain populated for IPv4-only consumers
    And the Kea connector at "http://kea.lan:8000/" supports lease6-get-all in addition to lease4-get-all
    And the dnsmasq connector reads /var/lib/misc/dnsmasq.leases6 in addition to /var/lib/misc/dnsmasq.leases

  @fsid:FS-Dhcpv6LeaseKeaReadsLease6
  Scenario: Kea connector reads lease6-get-all from the control-agent
    Given a Kea control-agent returns a lease6-get-all command-response wrapper with three active IA_NA leases
    When the skoed Kea connector polls the source
    Then the parsed lease set contains three records carrying ipv6_addresses (one address each) and duid
    And each record's `source` field is "kea"
    And lease expiry is computed from valid-lft + cltt

  @fsid:FS-Dhcpv6LeaseKeaMergesIaNaAndIaPd
  Scenario: Kea IA_NA + IA_PD entries for the same DUID merge into one Lease
    Given a Kea control-agent returns one IA_NA lease "2001:db8::1234" and one IA_PD lease "2001:db8:abcd::/56" both for DUID "00:01:00:01:aa:bb:cc:dd:ee:ff"
    When the skoed Kea connector polls the source
    Then GET /api/v1/clients/2001:db8::1234 returns 200
    And the response body's `ipv6_addresses` field contains both "2001:db8::1234" and "2001:db8:abcd::/56"
    And `duid` equals "00:01:00:01:aa:bb:cc:dd:ee:ff"

  @fsid:FS-Dhcpv6LeaseDnsmasqParsesLease6File
  Scenario: dnsmasq connector parses /var/lib/misc/dnsmasq.leases6
    Given a dnsmasq v6 lease file with three entries of the form
      "<expiry-epoch> <iaid> <ipv6> <hostname> <duid>"
    When the skoed dnsmasq connector reads the v6 file
    Then three Lease records are produced
    And each record's `ipv6_addresses` contains exactly one address
    And each record's `duid` is populated
    And entries with hostname "*" yield an empty hostname

  @fsid:FS-Dhcpv6LeaseDnsmasqSkipsExpired
  Scenario: dnsmasq v6 connector drops leases whose expiry epoch is in the past
    Given a dnsmasq v6 lease file containing one expired and two active leases
    When the connector reads the file
    Then only the two active leases appear in the parsed set

  @fsid:FS-Dhcpv6LeaseDualStackMerge
  Scenario: A client present in both v4 and v6 sources merges into one Lease
    Given a v4 lease "id:laptop10 aa:bb:cc:dd:ee:10 192.168.1.10 home-laptop"
    And a v6 lease for DUID "00:01:00:01:aa:bb:cc:dd:ee:10" with address "2001:db8::10" and hostname "home-laptop"
    And the connector's merge heuristic considers DUID-LL / DUID-LLT MAC suffixes plus hostname
    When the admin calls GET /api/v1/clients/192.168.1.10
    Then the response body has `is_dual_stack` = true
    And `ipv6_addresses` contains "2001:db8::10"
    And `duid` is populated

  @fsid:FS-Dhcpv6LeaseV6OnlyClientLookupByV6
  Scenario: GET /api/v1/clients/{ip} accepts an IPv6 literal
    Given a v6-only client with DUID "00:01:00:01:de:ad:be:ef:00:01" and address "2001:db8::dead"
    When the admin calls GET /api/v1/clients/2001:db8::dead
    Then the response 200 body contains:
      | field          | value                             |
      | ip             | (empty)                           |
      | mac            | (empty)                           |
      | duid           | 00:01:00:01:de:ad:be:ef:00:01     |
      | ipv6_addresses | ["2001:db8::dead"]                |
      | is_dual_stack  | false                             |
    And `source` reflects the connector that produced the lease

  @fsid:FS-Dhcpv6LeaseProfileMatchingPriorityUnchanged
  Scenario: Profile matching priority is unchanged at M6.5 (DUID is observational only)
    Given a profile "kids" pinned by client_ids = ["id:tablet42"] (the M3.6 IPv4 Client-ID)
    And the tablet's lease carries both client_id "id:tablet42" and a DUID "00:01:00:01:aa:bb:cc:dd:ee:42"
    When the tablet queries a domain from its IPv6 address
    Then the kids profile applies (matched by client_id, not by DUID)
    And no profile field referencing DUID is required or accepted at M6.5

  @fsid:FS-Dhcpv6LeaseClientsPageShowsV6Column
  Scenario: The Clients page renders IPv6 addresses next to the IPv4 row
    Given the lease cache contains one dual-stack client and one v6-only client
    When the admin loads /clients
    Then the dual-stack client's row shows both its IPv4 address and its IPv6 addresses
    And the v6-only client's row shows an empty IPv4 cell and a populated IPv6 cell
    And a small "dual-stack" chip appears on the merged row

  @fsid:FS-Dhcpv6LeaseMalformedV6LineSkipped
  Scenario: Malformed v6 lease lines are skipped, not fatal
    Given a dnsmasq v6 lease file with one malformed line and four valid lines
    When the connector reads the file
    Then the malformed line is logged at WARN
    And the four valid leases are parsed and surfaced

  @fsid:FS-Dhcpv6LeaseV6DisabledLegacyShapeUnchanged
  Scenario: When no v6 source is configured the API shape stays M3.6-compatible
    Given the operator's config enables only the IPv4 Kea endpoint (no lease6-get-all, no .leases6 path)
    When the admin calls GET /api/v1/clients/192.168.1.42
    Then the response body's `ipv6_addresses` field is absent or an empty array
    And `duid` is absent or empty
    And `is_dual_stack` is absent or false
    And no warning about missing v6 source is logged

  Non-goals:
    - DHCPv6 profile matching by DUID (observational only at M6.5; M7+ may add a `client_duids` profile field)
    - Writing or assigning IPv6 leases (read-only, like M3.6)
    - SLAAC / RA-only clients without any DHCPv6 lease (no DHCPv6 record means no enrichment — covered by router-advertised origin tagging in a sibling spec)
    - ISC dhcpd6 lease-file format (declared EOL by ISC in 2022, same exclusion as M3.6)
    - Cross-node DUID correlation (each node polls its configured source; cluster-wide merge is the same model as M3.6)
    - Per-IA_PD prefix delegation routing or firewall integration (lease is surfaced as an address string only)
