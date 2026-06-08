Feature: DHCP-enriched client identity in query log and profile matching
  As a household admin with hostnames I recognize
  I want hostname + MAC + Client-ID to appear next to IPs in skoed
  So that the query log and dashboards are human-readable
  And profiles can pin a child's device by Client-ID instead of fragile IP

  Background:
    Given a skoed node with a DHCP connector configured
    And the connector's lease cache contains at least:
      | client_id   | mac               | ip            | hostname    |
      | id:tablet42 | aa:bb:cc:dd:ee:42 | 192.168.1.42  | kid-tablet  |
      | id:laptop10 | aa:bb:cc:dd:ee:10 | 192.168.1.10  | home-laptop |

  @fsid:FS-ClientLookupReturnsEnrichedRecord
  Scenario: GET /api/v1/clients/{ip} returns the enriched record
    When the admin calls GET /api/v1/clients/192.168.1.42
    Then the response 200 body contains:
      | field      | value             |
      | ip         | 192.168.1.42      |
      | mac        | aa:bb:cc:dd:ee:42 |
      | hostname   | kid-tablet        |
      | client_id  | id:tablet42       |
      | source     | dnsmasq           |
    And `last_seen` is a recent RFC3339 timestamp

  @fsid:FS-ClientLookupFallsBackToIp
  Scenario: GET /api/v1/clients/{ip} for an unknown client returns IP-only
    When the admin calls GET /api/v1/clients/192.168.99.99
    Then the response 200 body has ip set and hostname / mac / client_id empty
    And `source` is "none"

  @fsid:FS-QueryLogShowsHostname
  Scenario: Query log entries carry client_hostname when a lease matched
    When client 192.168.1.42 queries "example.com"
    Then the query-log entry has client_hostname = "kid-tablet"
    And client_mac = "aa:bb:cc:dd:ee:42"
    And client_id = "id:tablet42"

  @fsid:FS-QueryLogOmitsEnrichmentWhenNoLease
  Scenario: Query log entries from unknown IPs omit enrichment fields
    When client 192.168.99.99 (not in any lease) queries "example.com"
    Then the query-log entry has client_hostname / client_mac / client_id absent or empty
    And the bare IP is still recorded in `client`

  @fsid:FS-ProfileMatchesByClientId
  Scenario: Profile pinned by Client-ID matches across IP changes
    Given a profile "kids" with client_ids = ["id:tablet42"]
    And the tablet's lease has just been renewed onto 192.168.1.99
    When the tablet queries a domain
    Then the kids profile applies to the query

  @fsid:FS-ProfileMatchesByMac
  Scenario: Profile pinned by MAC matches when Client-ID is absent
    Given a profile "guests" with client_macs = ["aa:bb:cc:dd:ee:99"]
    And the device has no Client-ID in its lease
    When the device queries a domain
    Then the guests profile applies

  @fsid:FS-ProfileMatchesByHostname
  Scenario: Profile pinned by hostname matches as a third-tier fallback
    Given a profile "office" with client_hostnames = ["work-laptop"]
    And the device's lease hostname is "work-laptop" with neither MAC nor Client-ID in the profile
    When the device queries a domain
    Then the office profile applies

  @fsid:FS-ProfileMatchPriority
  Scenario: Match priority is Client-ID > MAC > hostname > IP/CIDR
    Given a profile "A" with client_ids = ["id:tablet42"]
    And a profile "B" with client_macs = ["aa:bb:cc:dd:ee:42"]
    When the tablet queries a domain
    Then profile A is chosen, not B

  @fsid:FS-LeaseCacheRefreshInterval
  Scenario: New leases appear in skoed within one refresh interval
    Given the connector's refresh_seconds = 30
    When a new lease is created on the DHCP source for 192.168.1.77
    And 35 seconds pass
    Then GET /api/v1/clients/192.168.1.77 returns the new lease

  Non-goals:
    - DHCPv6 client matching
    - mDNS / WSD / NetBIOS hostname discovery (the lease is the source
      of truth)
    - Hostname-as-DNS-A-record (M1 local DNS entries already do this)
    - Sub-second freshness
