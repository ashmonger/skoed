Feature: Named Device Registry
  As a Network Administrator
  I want to register named devices with multiple network identifiers
  So that I can configure a single device once regardless of how many NICs or addresses it has

  Non-goals:
  - Device groups or device hierarchies
  - Automatic device discovery or network scanning
  - DHCP reservation management (DHCP static assignments are a separate concern)
  - Per-device filtering rules (devices are matched to profiles; profile rules are unchanged)

  Background:
    Given skoed is running in a 3-node cluster
    And the administrator is authenticated

  # ────────────────────────────────────────────────────────────
  # CRUD
  # ────────────────────────────────────────────────────────────

  @fsid:FS-DeviceRegistryCreate
  Scenario: Administrator creates a named device with multiple identifiers
    Given no device named "workstation-01" exists
    When the administrator submits a POST /api/v1/devices with:
      | name       | workstation-01                    |
      | macs       | aa:bb:cc:dd:ee:01, aa:bb:cc:dd:ee:02 |
      | ips        | 192.168.1.10                      |
      | hostnames  | workstation-01.lan                |
      | client_ids | (empty)                           |
      | profile_id | adults                            |
    Then the response status is 201
    And GET /api/v1/devices returns a device with name "workstation-01" containing both MACs
    And the device is replicated to all cluster nodes within 10 seconds

  @fsid:FS-DeviceRegistryUpdate
  Scenario: Administrator adds a network identifier to an existing device
    Given a device named "workstation-01" exists with MAC "aa:bb:cc:dd:ee:01"
    When the administrator submits a PATCH /api/v1/devices/workstation-01 adding MAC "aa:bb:cc:dd:ee:02"
    Then the response status is 200
    And GET /api/v1/devices/workstation-01 returns both MACs
    And the update is replicated to all cluster nodes within 10 seconds

  @fsid:FS-DeviceRegistryDelete
  Scenario: Administrator removes a named device
    Given a device named "workstation-01" exists
    When the administrator submits DELETE /api/v1/devices/workstation-01
    Then the response status is 204
    And GET /api/v1/devices returns no device named "workstation-01"
    And the deletion is replicated to all cluster nodes within 10 seconds

  @fsid:FS-DeviceRegistryNameUnique
  Scenario: Duplicate device name is rejected
    Given a device named "workstation-01" exists
    When the administrator submits a POST /api/v1/devices with name "workstation-01"
    Then the response status is 409
    And the response body contains an error describing the name conflict

  # ────────────────────────────────────────────────────────────
  # Profile matching
  # ────────────────────────────────────────────────────────────

  @fsid:FS-DeviceProfileMatchExclusive
  Scenario: A device match short-circuits profile selection
    Given a device "workstation-01" with MAC "aa:bb:cc:dd:ee:01" is assigned to profile "adults"
    And profile "default" has CIDR 192.168.1.0/24 with all blocklists active
    When a DNS query arrives from MAC "aa:bb:cc:dd:ee:01"
    Then only profile "adults" is applied to that query
    And profile "default" is NOT applied to that query

  @fsid:FS-DeviceMultiNicSingleConfig
  Scenario: A machine with two NICs is configured once via a device
    Given a device "dual-nic-server" with MACs "aa:bb:cc:dd:ee:01" and "aa:bb:cc:dd:ee:02" assigned to profile "servers"
    When a DNS query arrives from MAC "aa:bb:cc:dd:ee:01"
    Then profile "servers" is applied
    When a DNS query arrives from MAC "aa:bb:cc:dd:ee:02"
    Then profile "servers" is applied
    And the administrator only needed to configure one device entry

  @fsid:FS-DeviceMatchPriorityHighestTier
  Scenario: Device identifier takes priority over profile MAC/IP selectors
    Given a device "trusted-pc" with MAC "aa:bb:cc:dd:ee:ff" is assigned to profile "trusted"
    And profile "restricted" has MAC "aa:bb:cc:dd:ee:ff" in its client_macs selector
    When a DNS query arrives from MAC "aa:bb:cc:dd:ee:ff"
    Then only profile "trusted" is applied
    And profile "restricted" is NOT applied

  # ────────────────────────────────────────────────────────────
  # Devices view (replaces Clients in Filtering nav)
  # ────────────────────────────────────────────────────────────

  @fsid:FS-DevicesViewReplacesClients
  Scenario: Filtering navigation shows Devices instead of Clients
    When the administrator opens the web UI and navigates to Filtering
    Then the navigation bar shows "Devices" where "Clients" previously appeared
    And the Devices page is the default landing page for that nav item

  @fsid:FS-DevicesViewShowsUnifiedTable
  Scenario: Devices table shows all known clients with registration status
    Given three clients have made DNS queries: one registered as a device, two unregistered
    When the administrator opens the Devices page
    Then the table shows all three clients
    And each row contains: name (or "—" if unregistered), IP address(es), hostname, MAC address(es), client-id, source, last seen, and a "Registered" badge for registered devices
    And registered devices appear at the top of the table, sorted by name

  @fsid:FS-DeviceRegisterFromLease
  Scenario: Administrator registers an unregistered client as a named device from the Devices table
    Given an unregistered client with IP "192.168.1.55" and hostname "laptop-kids.lan" appears in the Devices table
    When the administrator clicks "Register" on that client row
    And fills in the name "kids-laptop" and selects profile "kids"
    And confirms the registration
    Then the client row shows the "Registered" badge and name "kids-laptop"
    And a device entry "kids-laptop" appears in GET /api/v1/devices

  # ────────────────────────────────────────────────────────────
  # Query log enrichment
  # ────────────────────────────────────────────────────────────

  @fsid:FS-DeviceQueryLogEnrichment
  Scenario: Query log entries show device name when client matches a device
    Given a device "workstation-01" with IP "192.168.1.10" is registered
    When the client at "192.168.1.10" sends DNS queries
    Then the query log entries for "192.168.1.10" include the field device_name: "workstation-01"
    And unregistered clients have no device_name field in their query log entries
