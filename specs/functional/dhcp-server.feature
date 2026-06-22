Feature: Built-in DHCP Server
  As a home-lab operator
  I want skoed to act as the DHCP server for my network
  So that I don't need a separate DHCP daemon and client identity is always accurate

  Non-goals:
    - DHCPv6 (IPv4 only in this milestone)
    - Multi-pool / VLAN support — single pool per node
    - DHCP relay agent (giaddr handling) — clients must be on the same L2 segment
    - Per-client DHCP option overrides in the UI (node YAML only)

  Background:
    Given a skoed cluster with at least one node running
    And the admin is authenticated
    And the built-in DHCP server is disabled by default

  # ─── Enable / disable ────────────────────────────────────────────────────────

  @fsid:FS-DhcpServerDisabledByDefault
  Scenario: DHCP server is off by default and does not bind port 67
    Given the node is started with no explicit dhcp.server config
    When a DHCP DISCOVER packet is sent to port 67
    Then no response is received
    And GET /api/v1/dhcp/server/status returns enabled: false

  @fsid:FS-DhcpServerEnableViaApi
  Scenario: Operator enables the DHCP server via API
    Given a valid pool is configured (start, end, gateway)
    When the admin sends PUT /api/v1/settings/dhcp with enabled: true
    Then the response status is 200
    And GET /api/v1/dhcp/server/status returns enabled: true
    And the node begins listening on UDP port 67

  @fsid:FS-DhcpServerDisableViaApi
  Scenario: Operator disables a running DHCP server via API
    Given the DHCP server is running
    When the admin sends PUT /api/v1/settings/dhcp with enabled: false
    Then the response status is 200
    And GET /api/v1/dhcp/server/status returns enabled: false
    And the node stops listening on UDP port 67

  @fsid:FS-DhcpServerConfigPersisted
  Scenario: DHCP server config survives a node restart
    Given the DHCP server is enabled with a configured pool
    When the node is restarted
    Then GET /api/v1/dhcp/server/status returns enabled: true
    And the configured pool is unchanged

  # ─── DHCP state machine ───────────────────────────────────────────────────────

  @fsid:FS-DhcpDiscoverOfferRequestAck
  Scenario: Client receives an IP address via the standard DORA flow
    Given the DHCP server is running with pool 192.168.1.100–192.168.1.200
    When a client sends DHCPDISCOVER with MAC aa:bb:cc:dd:ee:ff
    Then the server sends a DHCPOFFER with an IP in the configured pool range
    When the client sends DHCPREQUEST for the offered IP
    Then the server sends a DHCPACK
    And GET /api/v1/dhcp/leases shows the lease for MAC aa:bb:cc:dd:ee:ff with the assigned IP

  @fsid:FS-DhcpLeaseRenewal
  Scenario: Client renews its lease before expiry
    Given a client holds lease 192.168.1.100 with 1 hour remaining
    When the client sends DHCPREQUEST to renew
    Then the server sends DHCPACK with a refreshed expiry
    And the lease expiry is updated in GET /api/v1/dhcp/leases

  @fsid:FS-DhcpLeaseRelease
  Scenario: Client releases its lease
    Given a client holds lease 192.168.1.100
    When the client sends DHCPRELEASE
    Then the lease is removed from GET /api/v1/dhcp/leases
    And the IP is returned to the available pool

  @fsid:FS-DhcpLeaseExpiry
  Scenario: Expired lease is reclaimed automatically
    Given a lease was assigned with a 2-second lease time
    When 3 seconds elapse with no renewal
    Then the lease no longer appears in GET /api/v1/dhcp/leases
    And the IP is available for reassignment

  # ─── Pool options ─────────────────────────────────────────────────────────────

  @fsid:FS-DhcpOptionsDelivered
  Scenario: Server delivers standard DHCP options with each ACK
    Given the pool is configured with gateway 192.168.1.1 and domain home.local
    When a client completes the DORA flow
    Then the DHCPACK includes:
      | Option | Value             |
      | 1      | subnet mask       |
      | 3      | 192.168.1.1       |
      | 6      | skoed DNS address |
      | 15     | home.local        |
      | 51     | lease time        |
      | 54     | server identifier |

  @fsid:FS-DhcpDnsOptionDefaultsToSelf
  Scenario: DNS option defaults to skoed's own listen address when not overridden
    Given the pool has no explicit dns_server configured
    When a client completes the DORA flow
    Then the DHCPACK option 6 contains the skoed node's DNS listen address

  # ─── Pool exhaustion ─────────────────────────────────────────────────────────

  @fsid:FS-DhcpPoolExhaustion
  Scenario: Server sends DHCPNAK when pool is full
    Given the pool contains exactly one address 192.168.1.100
    And that address is already leased to another client
    When a new client sends DHCPDISCOVER
    Then the server sends DHCPNAK
    And no new lease appears in GET /api/v1/dhcp/leases

  # ─── ARP conflict detection ──────────────────────────────────────────────────

  @fsid:FS-DhcpArpConflictDetection
  Scenario: Server skips an address already in use on the network
    Given address 192.168.1.101 responds to ARP probes
    And the pool includes 192.168.1.101 and 192.168.1.102
    When a client sends DHCPDISCOVER
    Then the server does not offer 192.168.1.101
    And the DHCPOFFER contains 192.168.1.102

  # ─── Static assignments ───────────────────────────────────────────────────────

  @fsid:FS-DhcpStaticAssignmentHonoured
  Scenario: Client with a static assignment always receives its pinned IP
    Given a static assignment exists: MAC de:ad:be:ef:00:01 → IP 192.168.1.50 (hostname: printer)
    And 192.168.1.50 is outside the dynamic pool range
    When the client with MAC de:ad:be:ef:00:01 sends DHCPDISCOVER
    Then the server offers 192.168.1.50
    And the DHCPACK hostname option is "printer"

  @fsid:FS-DhcpStaticAssignmentPriorityOverPool
  Scenario: Static assignment takes priority over pool allocation
    Given a static assignment exists: MAC de:ad:be:ef:00:02 → IP 192.168.1.55
    And 192.168.1.55 is within the dynamic pool range
    When the client with MAC de:ad:be:ef:00:02 completes the DORA flow
    Then the lease IP is 192.168.1.55
    And no other client is offered 192.168.1.55

  @fsid:FS-DhcpStaticAssignmentPersistedToConfig
  Scenario: Static assignments are written to the node config file
    Given the DHCP server is running
    When the admin creates a static assignment via POST /api/v1/dhcp/static-assignments
    Then the assignment appears in GET /api/v1/dhcp/static-assignments
    And the node YAML config file contains the assignment under dhcp.server.static_assignments
    And after a node restart the assignment is still present

  @fsid:FS-DhcpStaticAssignmentReplicatedViaRaft
  Scenario: Static assignment created on leader is visible on all cluster nodes
    Given a 3-node cluster with the DHCP server enabled on the leader
    When the admin creates a static assignment on node 1 (leader)
    Then GET /api/v1/dhcp/static-assignments on node 2 returns the same assignment
    And GET /api/v1/dhcp/static-assignments on node 3 returns the same assignment

  @fsid:FS-DhcpStaticAssignmentDelete
  Scenario: Operator deletes a static assignment
    Given a static assignment exists for MAC de:ad:be:ef:00:03
    When the admin sends DELETE /api/v1/dhcp/static-assignments/de:ad:be:ef:00:03
    Then the response status is 204
    And GET /api/v1/dhcp/static-assignments does not include that MAC
    And the node YAML config file no longer contains that assignment

  # ─── Leader failover ─────────────────────────────────────────────────────────

  @fsid:FS-DhcpLeaderOwnsListener
  Scenario: Only the Raft leader runs the DHCP listener
    Given a 3-node cluster with the DHCP server enabled
    When the cluster is stable
    Then exactly one node is listening on UDP port 67
    And that node is the current Raft leader

  @fsid:FS-DhcpLeaderFailoverTransfersOwnership
  Scenario: DHCP ownership transfers automatically on leader failover
    Given a 3-node cluster with the DHCP server enabled
    And node 1 is the current leader and DHCP owner
    When node 1 is killed
    Then a new leader is elected within 5 seconds
    And the new leader begins listening on UDP port 67
    And clients with existing leases can renew successfully against the new leader

  # ─── Lease API ───────────────────────────────────────────────────────────────

  @fsid:FS-DhcpLeaseListApi
  Scenario: Lease table is readable via API
    Given two clients have active DHCP leases
    When the admin sends GET /api/v1/dhcp/leases
    Then the response status is 200
    And the response contains both leases with IP, MAC, hostname, expires_at, and origin fields
    And the origin field is "dhcp_dynamic" for pool-allocated leases
    And the origin field is "dhcp_static" for static-assignment leases

  @fsid:FS-DhcpServerStatusApi
  Scenario: Server status endpoint reports current state
    Given the DHCP server is enabled with pool 192.168.1.100–192.168.1.200
    And 10 leases are active
    When the admin sends GET /api/v1/dhcp/server/status
    Then the response includes:
      | Field          | Value               |
      | enabled        | true                |
      | is_leader      | true (on leader)    |
      | pool_start     | 192.168.1.100       |
      | pool_end       | 192.168.1.200       |
      | leases_active  | 10                  |
      | pool_total     | 101                 |

  # ─── Web UI — Settings → DHCP tab ───────────────────────────────────────────

  @fsid:FS-DhcpWebUiSettingsTabVisible
  Scenario: Settings → DHCP tab is present in the web UI
    Given the admin navigates to Settings
    When the DHCP tab is selected
    Then the DHCP configuration panel is displayed
    And the panel contains an enable/disable toggle
    And the panel contains pool configuration fields (start IP, end IP, gateway, lease time, domain, DNS server override)

  @fsid:FS-DhcpWebUiToggleEnable
  Scenario: Admin enables the DHCP server from the web UI
    Given the DHCP server is currently disabled
    And a valid pool is configured
    When the admin clicks the enable toggle in the DHCP settings panel
    Then a confirmation or save action is triggered
    And the toggle reflects enabled state after the request succeeds
    And GET /api/v1/dhcp/server/status returns enabled: true

  @fsid:FS-DhcpWebUiToggleDisable
  Scenario: Admin disables the DHCP server from the web UI
    Given the DHCP server is currently enabled
    When the admin clicks the toggle to disable it in the DHCP settings panel
    And confirms or saves the change
    Then the toggle reflects disabled state after the request succeeds
    And GET /api/v1/dhcp/server/status returns enabled: false

  @fsid:FS-DhcpWebUiPoolConfig
  Scenario: Admin configures pool parameters from the web UI
    Given the DHCP settings panel is open
    When the admin fills in pool start, pool end, gateway, lease time, domain, and optional DNS override
    And saves the configuration
    Then the changes are reflected in GET /api/v1/dhcp/server/status
    And the fields retain their values after a page reload

  @fsid:FS-DhcpWebUiStaticAssignmentCreate
  Scenario: Admin creates a static assignment from the web UI
    Given the DHCP settings panel is open
    And the static assignments table is visible
    When the admin clicks "Add static assignment"
    And fills in MAC address, IP address, and hostname
    And saves
    Then the new entry appears in the static assignments table
    And GET /api/v1/dhcp/static-assignments includes the new entry

  @fsid:FS-DhcpWebUiStaticAssignmentDelete
  Scenario: Admin deletes a static assignment from the web UI
    Given at least one static assignment exists in the table
    When the admin clicks the delete icon for that assignment
    And confirms the deletion
    Then the entry is removed from the static assignments table
    And GET /api/v1/dhcp/static-assignments no longer includes that MAC

  @fsid:FS-DhcpWebUiLeaseTable
  Scenario: Live lease table is displayed in the web UI
    Given the DHCP server is running with active leases
    When the admin views the DHCP settings panel
    Then a live lease table is visible showing IP, MAC, hostname, expires_at, and origin for each lease
    And the table refreshes automatically or on demand

  @fsid:FS-DhcpWebUiPoolUtilisationGauge
  Scenario: Pool utilisation gauge reflects current lease count
    Given the pool contains 100 addresses and 40 are leased
    When the admin views the DHCP settings panel
    Then a utilisation indicator shows approximately 40% usage
    And the indicator updates when leases change
