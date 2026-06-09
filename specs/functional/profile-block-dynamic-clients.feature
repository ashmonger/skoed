Feature: block-dynamic-clients profile rule
  As a household admin who pre-registers every "trusted" device as a DHCP
    reservation
  I want any client that walked in off a dynamic lease (guest phone, a
    kid's friend's laptop, an unboxed IoT gadget) to fall into a strict
    untrusted profile automatically
  So that the catch-all stays restrictive without me listing every device

  Background:
    Given skoed is configured with a DHCP connector that reports an
      `origin` field per lease (one of "dhcp_static", "dhcp_dynamic",
      "router_advertised", "manual_admin")
    And the connector's lease cache currently contains:
      | ip            | mac               | hostname      | origin        |
      | 192.168.1.10  | aa:bb:cc:dd:ee:10 | home-laptop   | dhcp_static   |
      | 192.168.1.42  | aa:bb:cc:dd:ee:42 | kid-tablet    | dhcp_static   |
      | 192.168.1.77  | aa:bb:cc:dd:ee:77 | guest-phone   | dhcp_dynamic  |
      | 192.168.1.88  | aa:bb:cc:dd:ee:88 | iot-thing     | dhcp_dynamic  |
    And blocklists "ads" and "social" are loaded
    And the default profile uses only "ads"

  @fsid:FS-BlockDynPureBlockDynamicProfileMatchesAllDynamicClients
  Scenario: A profile that ONLY sets block_dynamic_clients matches every dynamic-lease client
    Given a profile "untrusted" exists with body
      {"id":"untrusted","blocklists":["ads","social"],"block_dynamic_clients":true}
    And no client_ips / client_macs / client_hostnames / client_cidrs are set on it
    When 192.168.1.77 (guest-phone, dynamic) queries "facebook.com"
    Then the response is NXDOMAIN
    And the query-log entry has profile = "untrusted" and blocklist_id = "social"
    When 192.168.1.10 (home-laptop, static) queries "facebook.com"
    Then the response is a forwarded answer
    And the query-log entry has profile = "default"

  @fsid:FS-BlockDynMixedCriteriaIsOrNotAnd
  Scenario: block_dynamic_clients composes as OR with the existing matcher fields
    Given a profile "untrusted" exists with body
      {"id":"untrusted","blocklists":["social"],"block_dynamic_clients":true,
       "client_ips":["192.168.1.10"]}
    When 192.168.1.10 (home-laptop, static — matched by client_ips) queries "facebook.com"
    Then the response is NXDOMAIN
    And the query-log entry has profile = "untrusted"
    When 192.168.1.77 (guest-phone, dynamic — matched by block_dynamic_clients) queries "facebook.com"
    Then the response is NXDOMAIN
    And the query-log entry has profile = "untrusted"
    When 192.168.1.42 (kid-tablet, static, not in client_ips) queries "facebook.com"
    Then the response is a forwarded answer

  @fsid:FS-BlockDynEmptyMatchSetIsFine
  Scenario: A block-dynamic profile with zero matching dynamic clients is valid
    Given the connector's lease cache currently contains only "dhcp_static" leases
    When the admin POSTs a profile {"id":"untrusted","blocklists":["social"],"block_dynamic_clients":true}
    Then the response is 201
    And GET /api/v1/profiles/untrusted returns block_dynamic_clients = true
    And no client query is matched by the "untrusted" profile right now
    When a brand-new device receives a dynamic lease on 192.168.1.99
    And 192.168.1.99 queries "facebook.com"
    Then the response is NXDOMAIN

  @fsid:FS-BlockDynRejectedOnDefaultProfile
  Scenario: block_dynamic_clients on the default profile is rejected
    When the admin PATCHes /api/v1/profiles/default with body {"block_dynamic_clients":true}
    Then the response is 400
    And the body's error mentions "default profile" and "block_dynamic_clients"
    And GET /api/v1/profiles/default still reports block_dynamic_clients = false
    (Rationale: making everything-dynamic-blocked everywhere is a footgun —
     the operator should add an explicit untrusted profile instead.)

  @fsid:FS-BlockDynRouterAdvertisedAndManualAdminCountAsNotDynamic
  Scenario: Only "dhcp_dynamic" origin triggers the rule
    Given a profile "untrusted" exists with block_dynamic_clients = true
    And the lease cache also contains:
      | ip            | origin             |
      | 192.168.2.1   | router_advertised  |
      | 192.168.2.5   | manual_admin       |
    When 192.168.2.1 queries "facebook.com"
    Then the response is forwarded (router_advertised is not dynamic)
    When 192.168.2.5 queries "facebook.com"
    Then the response is forwarded (manual_admin is not dynamic)
    When 192.168.1.77 (dhcp_dynamic) queries "facebook.com"
    Then the response is NXDOMAIN

  @fsid:FS-BlockDynUnknownClientIsNotDynamic
  Scenario: A client with no lease at all does NOT match block_dynamic_clients
    Given a profile "untrusted" exists with block_dynamic_clients = true
    When 10.99.99.99 (no lease in cache) queries "facebook.com"
    Then the response is forwarded (default profile applies; absence of
      a lease is not the same as a dynamic lease)
    And the query-log entry has profile = "default"

  @fsid:FS-BlockDynPriorityHigherTierStillWins
  Scenario: A higher-priority match (Client-ID) overrides block_dynamic_clients
    Given a profile "trusted-tablet" with client_ids = ["id:tablet42"] and no block_dynamic_clients
    And a profile "untrusted" with block_dynamic_clients = true
    And the lease cache shows id:tablet42 currently on 192.168.1.200 with origin = "dhcp_dynamic"
    When 192.168.1.200 queries "facebook.com"
    Then the response uses the trusted-tablet profile (Client-ID match is tier 1)
    And the untrusted profile is NOT applied
    (Match priority Client-ID > MAC > hostname > {IP/CIDR ∪ block_dynamic_clients} is preserved.)

  @fsid:FS-BlockDynProfileApiCrud
  Scenario: block_dynamic_clients round-trips through the management API
    When the admin POSTs a profile
      {"id":"untrusted","blocklists":["social"],"block_dynamic_clients":true}
    Then the response is 201
    And GET /api/v1/profiles/untrusted body has block_dynamic_clients = true
    When the admin PATCHes /api/v1/profiles/untrusted with {"block_dynamic_clients":false}
    Then the change replicates cluster-wide within 5 seconds
    And subsequent queries from dynamic-lease clients are no longer pinned to "untrusted"

  @fsid:FS-BlockDynClientLookupSurfacesMatchedProfile
  Scenario: GET /api/v1/clients/{ip} reflects the dynamic-match assignment
    Given a profile "untrusted" exists with block_dynamic_clients = true
    When the admin calls GET /api/v1/clients/192.168.1.77
    Then the response 200 body's `profile_ids` field includes "untrusted"
    And the body's `origin` field equals "dhcp_dynamic"
    When the admin calls GET /api/v1/clients/192.168.1.10
    Then the response 200 body's `profile_ids` field does NOT include "untrusted"
    And the body's `origin` field equals "dhcp_static"

  @fsid:FS-BlockDynUnknownOriginTreatedAsNotDynamic
  Scenario: A lease whose origin the connector could not determine is NOT considered dynamic
    Given the connector reports a lease for 192.168.3.3 with origin "" (unknown / not reported)
    And a profile "untrusted" exists with block_dynamic_clients = true
    When 192.168.3.3 queries "facebook.com"
    Then the response is forwarded
    (Conservative default: only an explicit "dhcp_dynamic" origin triggers
     the rule. Unknown origin is treated as "we don't know enough to
     restrict you" rather than "we'll assume the worst".)

  Non-goals:
    - Auto-creating an "untrusted" profile on first boot (operator
      configures it explicitly)
    - Notifications / Dashboard alerts when a brand-new dynamic client
      first matches the rule (covered by the M3.6 anomaly surface; this
      feature is filtering only)
    - Using DUID (DHCPv6 identifier) as a matcher — observational only at
      M6.5 per the DHCPv6-lease-parsing feature
    - Per-blocklist application of the rule (block_dynamic_clients is
      a profile-level boolean; all of the profile's blocklists apply
      to a matched dynamic client, same as any other matcher)
    - Time-bounded variants ("block dynamic clients only during
      bedtime") — combine this rule with M3 schedule-rules instead
    - A "block_static_clients" inverse rule — not requested; would
      invert the intended trust model
