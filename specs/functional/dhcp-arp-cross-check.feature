Feature: DHCP layer-3 ARP/NDP cross-check
  As a household admin who already trusts skoed's lease-history anomalies
  I want a THIRD signal that compares the DHCP server's (IP -> MAC) belief
  Against what the local kernel is actually seeing on the link right now
  So that stale leases, ghost reservations, and active L2 spoofers stand out
  Even when the lease history itself looks internally consistent.

  Background:
    Given skoed runs on a node with a working DHCP connector
    And the lease cache currently contains:
      | ip            | mac               | hostname    | client_id   |
      | 192.168.1.42  | aa:bb:cc:dd:ee:42 | kid-tablet  | id:tablet42 |
      | 192.168.1.10  | aa:bb:cc:dd:ee:10 | home-laptop | id:laptop10 |
      | fd00::42      | aa:bb:cc:dd:ee:42 | kid-tablet  | id:tablet42 |
    And the node has CAP_NET_ADMIN (the kernel ARP and NDP tables are readable)

  @fsid:FS-ArpCheckArpStateAgreesWithLease
  Scenario: Kernel ARP entry matches the DHCP lease — no anomaly
    Given the kernel ARP table reports 192.168.1.42 -> aa:bb:cc:dd:ee:42 in state "reachable"
    When the admin calls GET /api/v1/clients/192.168.1.42/arp-state
    Then the response is 200
    And the body has shape
      | field              | value             |
      | ip                 | 192.168.1.42      |
      | mac_dhcp           | aa:bb:cc:dd:ee:42 |
      | mac_kernel         | aa:bb:cc:dd:ee:42 |
      | kernel_state       | reachable         |
    And `last_observed_unix` is a recent epoch timestamp
    And the body has NO `anomaly` field
    And GET /api/v1/anomalies does NOT list a new entry for this IP

  @fsid:FS-ArpCheckArpMacMismatchFlagsAnomaly
  Scenario: Kernel ARP entry disagrees with the DHCP lease's MAC
    Given the kernel ARP table reports 192.168.1.42 -> 11:22:33:44:55:66 in state "reachable"
    When the next ARP cross-check sweep runs
    Then an anomaly is recorded with kind "arp_mac_mismatch"
    And the anomaly references both MACs (DHCP-believed and kernel-observed) and the IP
    And GET /api/v1/clients/192.168.1.42/arp-state body has shape
      | field        | value             |
      | ip           | 192.168.1.42      |
      | mac_dhcp     | aa:bb:cc:dd:ee:42 |
      | mac_kernel   | 11:22:33:44:55:66 |
      | kernel_state | reachable         |
      | anomaly      | arp_mac_mismatch  |
    And GET /api/v1/anomalies lists the new entry

  @fsid:FS-ArpCheckNdpMacMismatchFlagsAnomaly
  Scenario: Kernel NDP entry disagrees with the DHCPv6 lease's MAC
    Given the kernel NDP neighbour cache reports fd00::42 -> 99:88:77:66:55:44 in state "stale"
    When the next ARP cross-check sweep runs
    Then an anomaly is recorded with kind "ndp_mac_mismatch"
    And the anomaly references both MACs and the IPv6 address
    And GET /api/v1/clients/fd00::42/arp-state body's `anomaly` field is "ndp_mac_mismatch"
    And `kernel_state` is "stale"

  @fsid:FS-ArpCheckGhostLeaseLongLivedButNeverInKernel
  Scenario: DHCP lease has been around for hours but kernel has never seen the MAC
    Given the lease 192.168.1.10 -> aa:bb:cc:dd:ee:10 was first observed more than 6 hours ago
    And the kernel ARP table has no entry for 192.168.1.10
    And the kernel has never reported aa:bb:cc:dd:ee:10 on any interface
    When the next ARP cross-check sweep runs
    Then an anomaly is recorded with kind "ghost_lease"
    And GET /api/v1/clients/192.168.1.10/arp-state body has `mac_kernel` empty
    And `kernel_state` is "none"
    And `anomaly` is "ghost_lease"

  @fsid:FS-ArpCheckUnseenByKernelFreshLeaseStaysQuiet
  Scenario: A freshly-issued lease that the kernel hasn't observed yet is NOT flagged
    Given a brand-new lease 192.168.1.77 -> aa:bb:cc:dd:ee:77 first observed 12 seconds ago
    And the kernel ARP table has no entry for 192.168.1.77
    When the next ARP cross-check sweep runs
    Then NO anomaly is recorded for 192.168.1.77
    And GET /api/v1/clients/192.168.1.77/arp-state body has `anomaly` absent
    And `kernel_state` is "none"

  @fsid:FS-ArpCheckUnseenByKernelAfterGracePeriod
  Scenario: A lease the kernel cannot see after the grace window is flagged unseen_by_kernel
    Given the lease 192.168.1.55 -> aa:bb:cc:dd:ee:55 was first observed 45 minutes ago
    And the kernel ARP table has no entry for 192.168.1.55
    When the next ARP cross-check sweep runs
    Then an anomaly is recorded with kind "unseen_by_kernel"
    And GET /api/v1/clients/192.168.1.55/arp-state body's `anomaly` is "unseen_by_kernel"

  @fsid:FS-ArpCheckGracefulWhenNetlinkUnavailable
  Scenario: Node without CAP_NET_ADMIN reports netlink_unavailable once and stops spamming
    Given skoed runs unprivileged and netlink ARP/NDP probes return permission errors
    When the ARP cross-check sweep runs three times in a row
    Then a single structured-log event "netlink_unavailable" is emitted (deduped per node)
    And GET /api/v1/clients/192.168.1.42/arp-state returns 200
    And the body has `mac_kernel` empty
    And `kernel_state` is "netlink_unavailable"
    And NO `arp_mac_mismatch` / `ndp_mac_mismatch` / `ghost_lease` / `unseen_by_kernel` anomalies are recorded

  @fsid:FS-ArpCheckUnknownIpReturns404
  Scenario: GET /api/v1/clients/{ip}/arp-state for an unknown IP returns 404
    When the admin calls GET /api/v1/clients/10.99.99.99/arp-state
    Then the response is 404
    And the body's error mentions "no lease"

  @fsid:FS-ArpCheckAnomaliesListIncludesNewKinds
  Scenario: GET /api/v1/anomalies surfaces the new layer-3 anomaly kinds alongside M3.6 kinds
    Given one anomaly of kind "mac_changed_for_client_id" already exists (M3.6)
    And the ARP sweep records one anomaly of kind "arp_mac_mismatch" for 192.168.1.42
    And the ARP sweep records one anomaly of kind "ghost_lease" for 192.168.1.10
    When the admin calls GET /api/v1/anomalies
    Then the response is 200
    And the body lists all three anomalies
    And each entry carries `kind`, `detected_at`, `ip`, and `details`
    And the response can be filtered by `?kind=arp_mac_mismatch` to return only ARP mismatches

  @fsid:FS-ArpCheckSweepCadenceIsBestEffort
  Scenario: ARP cross-check is best-effort and never blocks DHCP polling
    Given the netlink probe takes longer than the configured ARP sweep interval
    When the DHCP refresh interval elapses during a slow netlink probe
    Then the DHCP poll still runs on schedule (lease cache stays fresh)
    And the ARP sweep skips the overlapping run rather than queuing it
    And a structured-log event "arp_sweep_skipped" is emitted at most once per minute

  Non-goals:
    - Active mitigation (no gratuitous-ARP / RA-guard / port-shutdown — alert only)
    - Cross-node ARP correlation (each node cross-checks its own kernel only;
      followers do NOT probe the leader's link)
    - ARP table seeding / poisoning by skoed
    - Layer-2 switch CAM-table queries (out of scope; would need SNMP/LLDP)
    - Per-anomaly confidence scoring (kinds are binary, like M3.6)
    - Replacing the M3.6 lease-history detector — this is an additional signal
