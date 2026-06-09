Feature: Per-connector static-vs-dynamic lease origin tagging
  As a household admin who hand-registers trusted devices in DHCP
  I want skoed to know which leases were hand-assigned vs pool-allocated
  So that the Clients UI shows it at a glance
  And profile rules can treat dynamic clients (guests, IoT, kids' friends)
  differently from reserved ones

  Background:
    Given skoed supports three connector kinds: kea, dnsmasq, http_json
    And every Lease now carries two extra observational fields:
      | field              | type    | values                                                                |
      | origin             | string  | "dhcp_static" / "dhcp_dynamic" / "router_advertised" / "manual_admin" |
      | origin_confidence  | string  | "high" / "inferred" / "unknown"                                       |
    And the lease cache contains the following baseline:
      | client_id   | mac               | ip            | hostname    | origin        | origin_confidence |
      | id:tablet42 | aa:bb:cc:dd:ee:42 | 192.168.1.42  | kid-tablet  | dhcp_static   | high              |
      | id:laptop10 | aa:bb:cc:dd:ee:10 | 192.168.1.10  | home-laptop | dhcp_dynamic  | high              |
      | id:guest99  | aa:bb:cc:dd:ee:99 | 192.168.1.99  | iphone-of-x | dhcp_dynamic  | high              |

  @fsid:FS-LeaseOriginKeaReservationsReportedHigh
  Scenario: Kea host-reservation IPs are tagged dhcp_static with high confidence
    Given the Kea control-agent at "http://kea.lan:8000/" responds to
      "reservation-get-all" with two reservations for 192.168.1.42 and 192.168.1.50
    And lease4-get-all returns five leases including those two IPs plus 192.168.1.10
    When the Kea connector polls the source
    Then the Lease for 192.168.1.42 has origin = "dhcp_static" and origin_confidence = "high"
    And the Lease for 192.168.1.50 has origin = "dhcp_static" and origin_confidence = "high"
    And the Lease for 192.168.1.10 has origin = "dhcp_dynamic" and origin_confidence = "high"

  @fsid:FS-LeaseOriginKeaReservationsUnreachableInferred
  Scenario: Kea reservation API down — origin falls back to "unknown" not a lie
    Given lease4-get-all succeeds with three leases
    And "reservation-get-all" returns 500 on this poll
    When the Kea connector polls the source
    Then each lease's origin is "dhcp_dynamic"
    And each lease's origin_confidence is "unknown"
    And a WARN log "kea_reservation_lookup_failed" is emitted once per poll cycle

  @fsid:FS-LeaseOriginDnsmasqDhcpHostParsed
  Scenario: dnsmasq dhcp-host= directives in the running config produce inferred static tags
    Given the dnsmasq running config contains
      """
      dhcp-host=aa:bb:cc:dd:ee:42,192.168.1.42,kid-tablet
      dhcp-host=id:laptop10,192.168.1.10,home-laptop
      """
    And the dnsmasq lease file lists 192.168.1.42, 192.168.1.10, and 192.168.1.99
    When the dnsmasq connector reads both sources
    Then the lease for 192.168.1.42 has origin = "dhcp_static" and origin_confidence = "inferred"
    And the lease for 192.168.1.10 has origin = "dhcp_static" and origin_confidence = "inferred"
    And the lease for 192.168.1.99 has origin = "dhcp_dynamic" and origin_confidence = "high"

  @fsid:FS-LeaseOriginDnsmasqConfigUnreadable
  Scenario: dnsmasq config file unreadable — leases stay dynamic with confidence "unknown"
    Given the dnsmasq lease file has three entries
    And the dnsmasq config file cannot be read (permission denied)
    When the dnsmasq connector polls
    Then every Lease has origin = "dhcp_dynamic"
    And every Lease has origin_confidence = "unknown"
    And a WARN log "dnsmasq_config_unreadable" is emitted once per poll cycle

  @fsid:FS-LeaseOriginHttpJsonHonoursWireField
  Scenario: Generic HTTP_JSON connector honours an explicit "origin" field on the wire
    Given the operator-supplied endpoint returns
      """
      [
        {"ip":"192.168.1.50","mac":"aa:bb:cc:dd:ee:50","client_id":"id:nas","origin":"dhcp_static"},
        {"ip":"192.168.1.77","mac":"aa:bb:cc:dd:ee:77","client_id":"id:guest","origin":"dhcp_dynamic"},
        {"ip":"192.168.1.88","mac":"aa:bb:cc:dd:ee:88","client_id":"id:?"}
      ]
      """
    When the http_json connector polls the endpoint
    Then the lease for 192.168.1.50 has origin = "dhcp_static" with origin_confidence = "high"
    And the lease for 192.168.1.77 has origin = "dhcp_dynamic" with origin_confidence = "high"
    And the lease for 192.168.1.88 (no origin field) has origin = "dhcp_dynamic" with origin_confidence = "unknown"

  @fsid:FS-LeaseOriginHttpJsonRejectsUnknownValue
  Scenario: Generic HTTP_JSON connector rejects garbage origin values, does not crash
    Given the endpoint returns one lease with origin = "totally-made-up"
    When the http_json connector parses the response
    Then the lease is still ingested
    And its origin is "dhcp_dynamic" with origin_confidence = "unknown"
    And a WARN log "http_json_unknown_origin_value" is emitted with the offending value

  @fsid:FS-LeaseOriginClientLookupExposesFields
  Scenario: GET /api/v1/clients/{ip} returns origin and origin_confidence
    When the admin calls GET /api/v1/clients/192.168.1.42
    Then the response 200 body contains:
      | field              | value         |
      | ip                 | 192.168.1.42  |
      | origin             | dhcp_static   |
      | origin_confidence  | high          |
    And the same call against 192.168.1.10 returns origin = "dhcp_dynamic"

  @fsid:FS-LeaseOriginUnknownClientOmitsOrigin
  Scenario: GET /api/v1/clients/{ip} for an unknown IP leaves origin blank
    When the admin calls GET /api/v1/clients/192.168.99.99
    Then the response 200 body has origin = "" and origin_confidence = ""
    And source is "none"

  @fsid:FS-LeaseOriginClientsListSurfacesBadge
  Scenario: Clients page surfaces an origin badge per row
    Given the SPA is loaded and three clients are visible
    When the operator views the Clients page
    Then the row for 192.168.1.42 shows a chip labelled "static" (green)
    And the row for 192.168.1.10 shows a chip labelled "dynamic" (grey)
    And the row for 192.168.1.99 shows a chip labelled "dynamic" (grey)
    And rows whose origin_confidence is "unknown" render the chip as muted with a tooltip "origin unknown"

  @fsid:FS-LeaseOriginQueryLogDoesNotChange
  Scenario: Origin tagging is observational — it does not alter the query log shape
    Given the M3.6 query-log entry shape is unchanged
    When client 192.168.1.42 queries "example.com"
    Then the query-log entry has client_hostname, client_mac, client_id as before
    And the query-log entry has NO `origin` field
    (origin lives on the Lease and the Clients endpoint, not on per-query records)

  @fsid:FS-LeaseOriginPrometheusGauges
  Scenario: Prometheus exposes per-origin lease counts
    Given the lease cache holds 2 static and 1 dynamic lease
    When /metrics is scraped
    Then the response contains
      skoed_dhcp_leases{origin="dhcp_static"} = 2
      skoed_dhcp_leases{origin="dhcp_dynamic"} = 1
    And no series is emitted for an origin value with zero leases

  @fsid:FS-LeaseOriginRefreshFlipsTag
  Scenario: A previously-dynamic IP becomes static after a Kea reservation is added
    Given 192.168.1.77 currently shows origin = "dhcp_dynamic"
    When the operator adds a Kea host reservation for 192.168.1.77
    And the connector polls again
    Then GET /api/v1/clients/192.168.1.77 returns origin = "dhcp_static" with origin_confidence = "high"
    And no anomaly is recorded (origin flip alone is not a spoof signal)

  Non-goals:
    - Editing reservations from skoed (read-only — origin reflects what
      the upstream DHCP source already knows)
    - DHCPv6 origin tagging (covered separately under dhcpv6 lease parsing)
    - Origin-based blocking semantics (the block_dynamic_clients profile
      rule lives in its own spec; this feature only TAGS leases)
    - Synthesising "router_advertised" from SLAAC/RA snooping (the value
      is reserved in the enum for future use; M6.5 only emits dhcp_static
      and dhcp_dynamic from the three connectors)
    - History of past origin values (the Lease carries the current
      origin only; no per-IP origin timeline)
