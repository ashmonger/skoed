Feature: DHCP Lease Persistence + DHCPv6 Server
  As a home-lab or enterprise operator
  I want active DHCP leases to survive cluster restarts and failovers
  And I want skoed to serve IPv6 addresses via DHCPv6
  So that clients retain their addresses across maintenance windows and dual-stack networks are fully managed

  Non-goals:
    - DHCP relay agent / giaddr handling (clients must be on the same L2 segment)
    - Multi-pool / VLAN DHCP (single pool per address family)
    - DHCPv6 prefix delegation (IA_PD) — stateful IA_NA only
    - SLAAC / Router Advertisement serving (OS-level; skoed is not a router)
    - Per-client DHCP option overrides via the web UI (YAML only)
    - DHCPv6 failover protocol (RFC 8156) — ownership follows the Raft leader as with DHCPv4

  Background:
    Given a skoed cluster with at least one node running
    And the admin is authenticated
    And the DHCP server has been enabled with a configured pool

  # ─── DHCPv4 lease persistence ────────────────────────────────────────────────

  @fsid:FS-DhcpLeasePersistenceRestart
  Scenario: Dynamic DHCPv4 leases survive a node restart
    Given 5 clients have active DHCPv4 leases assigned from the dynamic pool
    When the skoed node is stopped and restarted
    Then GET /api/v1/dhcp/leases returns all 5 leases with their original IPs, MACs, and expiry times
    And clients that renew after restart receive DHCPACK for the same IP they held before

  @fsid:FS-DhcpLeasePersistenceFullClusterRestart
  Scenario: Dynamic leases survive a full cluster restart (all nodes down simultaneously)
    Given a 3-node cluster with 20 active dynamic DHCPv4 leases
    When all three nodes are stopped simultaneously and then restarted
    Then GET /api/v1/dhcp/leases returns all 20 leases after the new leader is elected
    And no client is offered a conflicting IP on reconnect

  @fsid:FS-DhcpLeasePersistenceLeaderFailover
  Scenario: Lease table is immediately available on the new leader after failover
    Given a 3-node cluster where node 1 is the Raft leader and DHCP owner
    And node 1 has 15 active dynamic leases
    When node 1 is killed without a graceful shutdown
    Then a new leader is elected within 5 seconds
    And GET /api/v1/dhcp/leases on the new leader returns all 15 leases
    And the new leader accepts DHCPREQUEST renewals from existing clients

  @fsid:FS-DhcpLeasePersistenceSoakRestart
  Scenario: Lease table is consistent after restart under enterprise load
    Given 50 clients have active DHCPv4 leases and new clients are continuously requesting
    When the DHCP-owning node is restarted mid-load
    Then within 10 seconds of restart the server is accepting new DHCPDISCOVER packets
    And all pre-restart leases are preserved and renewable
    And no IP address is double-assigned after restart

  @fsid:FS-DhcpLeasePersistedToRaft
  Scenario: Each new dynamic lease is written to Raft state before ACK is sent
    Given the DHCP server is running in a 3-node cluster
    When a client completes the DORA flow
    Then the lease appears in GET /api/v1/dhcp/leases on all three nodes within 2 seconds
    And the lease survives a restart of any single node immediately after assignment

  @fsid:FS-DhcpLeaseExpiryRespectedAfterRestart
  Scenario: Expired leases are not restored after restart
    Given a lease was assigned with a 5-second lease time
    When 10 seconds elapse and then the node is restarted
    Then the expired lease does not appear in GET /api/v1/dhcp/leases after restart
    And the IP is available for a new client

  # ─── DHCPv6 server — SARR flow ───────────────────────────────────────────────

  @fsid:FS-Dhcpv6SarrFlow
  Scenario: IPv6 client receives an address via the DHCPv6 SARR flow
    Given the DHCPv6 server is enabled with pool fd00::/64 and range fd00::100 to fd00::1ff
    When an IPv6 client sends a DHCPv6 Solicit (with IA_NA option)
    Then the server sends a DHCPv6 Advertise with an address from the configured prefix
    When the client sends a DHCPv6 Request for the advertised address
    Then the server sends a DHCPv6 Reply with the confirmed address and T1/T2 timers
    And GET /api/v1/dhcp/leases6 shows the new lease with the assigned IPv6 address and DUID

  @fsid:FS-Dhcpv6IaNaPool
  Scenario: DHCPv6 pool is configurable and respects the defined range
    Given the DHCPv6 pool is configured with prefix fd00::/64 and range fd00::100–fd00::1ff
    When 5 different clients complete the SARR flow
    Then each client receives a unique address in the fd00::100–fd00::1ff range
    And GET /api/v1/dhcp/server/status6 reports pool_total: 256 and leases_active: 5

  @fsid:FS-Dhcpv6LeaseRenewal
  Scenario: DHCPv6 client successfully renews its lease
    Given a client holds DHCPv6 lease fd00::102 with T1 = 30 seconds
    When T1 elapses and the client sends a DHCPv6 Renew
    Then the server sends a DHCPv6 Reply with a refreshed T1/T2 and valid lifetime
    And GET /api/v1/dhcp/leases6 shows the updated expiry

  @fsid:FS-Dhcpv6LeaseRelease
  Scenario: DHCPv6 client releases its lease
    Given a client holds DHCPv6 lease fd00::103
    When the client sends a DHCPv6 Release
    Then GET /api/v1/dhcp/leases6 no longer contains that address
    And the address is available for reassignment

  @fsid:FS-Dhcpv6DnsOptionDelivered
  Scenario: DHCPv6 Reply carries the DNS server option
    Given the DHCPv6 server is enabled
    When a client completes the SARR flow
    Then the DHCPv6 Reply includes option 23 (DNS_SERVERS) containing the skoed node's IPv6 address
    And option 24 (DOMAIN_LIST) contains the configured search domain if set

  @fsid:FS-Dhcpv6PoolExhaustion
  Scenario: DHCPv6 server sends NoAddrsAvail when pool is full
    Given the DHCPv6 pool contains exactly one address and it is leased
    When a second client sends a DHCPv6 Solicit
    Then the server sends a DHCPv6 Advertise with status code NoAddrsAvail
    And no new lease appears in GET /api/v1/dhcp/leases6

  @fsid:FS-Dhcpv6LeaderOwnsListener
  Scenario: Only the Raft leader runs the DHCPv6 listener
    Given a 3-node cluster with the DHCPv6 server enabled
    When the cluster is stable
    Then exactly one node is listening on UDP port 547
    And that node is the current Raft leader

  @fsid:FS-Dhcpv6LeaderFailover
  Scenario: DHCPv6 listener transfers on leader failover
    Given node 1 is the Raft leader and DHCPv6 owner with 5 active leases
    When node 1 is killed
    Then a new leader begins listening on UDP port 547 within 5 seconds
    And existing clients can renew their leases against the new leader

  # ─── DHCPv6 DUID-based profile matching ──────────────────────────────────────

  @fsid:FS-Dhcpv6DuidProfileMatch
  Scenario: Profile is assigned by DUID for a DHCPv6-only client
    Given a profile "iot" has `client_duids: ["00:01:00:01:aa:bb:cc:dd:ee:01"]` configured
    When the client with that DUID completes the DHCPv6 SARR flow
    Then GET /api/v1/clients shows that client assigned to the "iot" profile
    And DNS queries from that IPv6 address are filtered under the "iot" profile rules

  @fsid:FS-Dhcpv6DuidPriorityOverMac
  Scenario: DUID match takes priority over MAC-based profile match when both are configured
    Given a profile "kids" has client_macs matching the client's MAC
    And a profile "adults" has client_duids matching the same client's DUID
    When the client sends a DHCPv6 Solicit
    Then the client is assigned to the "adults" profile (DUID match has higher priority)

  # ─── DHCPv6 static assignments ───────────────────────────────────────────────

  @fsid:FS-Dhcpv6StaticAssignment
  Scenario: DHCPv6 client with a static assignment always receives its pinned address
    Given a static DHCPv6 assignment: DUID "00:01:00:01:aa:bb:cc:dd:ee:ff" → fd00::200 (hostname: server1)
    When the client with that DUID sends a DHCPv6 Solicit
    Then the DHCPv6 Advertise offers fd00::200
    And the DHCPv6 Reply confirms fd00::200
    And GET /api/v1/dhcp/leases6 shows hostname "server1" for that DUID

  @fsid:FS-Dhcpv6StaticAssignmentReplicatedViaRaft
  Scenario: DHCPv6 static assignment created on leader is visible on all nodes
    Given a 3-node cluster
    When the admin creates a DHCPv6 static assignment on the leader
    Then GET /api/v1/dhcp/static-assignments6 on every node returns that assignment

  # ─── DHCPv6 lease persistence ────────────────────────────────────────────────

  @fsid:FS-Dhcpv6LeasePersistenceRestart
  Scenario: Active DHCPv6 leases survive a node restart
    Given 10 clients hold active DHCPv6 leases
    When the skoed node is restarted
    Then GET /api/v1/dhcp/leases6 returns all 10 leases with their original addresses and DUIDs
    And clients can renew their leases after restart without going through SARR again

  @fsid:FS-Dhcpv6LeasePersistenceFullClusterRestart
  Scenario: DHCPv6 leases survive a full cluster restart
    Given a 3-node cluster with 15 active DHCPv6 leases
    When all nodes are restarted simultaneously
    Then all 15 DHCPv6 leases are present after the cluster reforms

  # ─── API ─────────────────────────────────────────────────────────────────────

  @fsid:FS-Dhcpv6LeaseListApi
  Scenario: DHCPv6 lease table is accessible via API
    Given three clients have active DHCPv6 leases
    When the admin sends GET /api/v1/dhcp/leases6
    Then the response status is 200
    And each lease entry contains: address, duid, hostname, expires_at, origin, profile_id

  @fsid:FS-Dhcpv6ServerStatusApi
  Scenario: DHCPv6 server status endpoint reports pool utilisation
    Given the DHCPv6 server is enabled with pool fd00::100–fd00::1ff (256 addresses) and 12 leases active
    When the admin sends GET /api/v1/dhcp/server/status6
    Then the response includes pool_start, pool_end, prefix, pool_total: 256, leases_active: 12, enabled: true, is_leader

  # ─── Web UI ──────────────────────────────────────────────────────────────────

  @fsid:FS-Dhcpv6WebUiConfigPanel
  Scenario: DHCPv6 configuration section is present in the DHCP settings panel
    Given the admin opens Settings → DHCP
    Then a DHCPv6 subsection is visible alongside the DHCPv4 section
    And it contains fields for: enable toggle, IPv6 prefix, pool start/end, lease time, search domain

  @fsid:FS-Dhcpv6WebUiLeaseTable
  Scenario: DHCPv6 live lease table is displayed in the web UI
    Given the DHCPv6 server is running with active leases
    When the admin views the DHCP settings panel
    Then a DHCPv6 lease table shows address, DUID (abbreviated), hostname, expires_at, and profile_id
    And the table refreshes automatically or on demand

  @fsid:FS-Dhcpv6WebUiStaticAssignmentCreate
  Scenario: Admin creates a DHCPv6 static assignment from the web UI
    Given the DHCP settings panel is open and the DHCPv6 static assignments table is visible
    When the admin clicks "Add IPv6 static assignment" and fills in DUID, IPv6 address, and hostname
    Then the new entry appears in the DHCPv6 static assignments table
    And GET /api/v1/dhcp/static-assignments6 includes the new entry
