# M23.5 Demo Note — Built-in DHCP Server Core

## Implemented scope

- **RFC 2131 DHCP server** using stdlib only (no external library).
  Handles DISCOVER→OFFER, REQUEST→ACK/NAK, RELEASE.
  Pool allocation from configurable IP range.
- **Leader-owned HA model**: only the Raft leader binds UDP port 67.
  On leader change, the new leader starts the listener; the old leader stops it.
  Verified on 3-node Proxmox cluster (skoed-1 stopped → skoed-3 elected, DHCP migrated).
- **Static MAC→IP assignments**: created via API, Raft-replicated to all nodes.
  Honored immediately on the next DHCP exchange.
  `origin: dhcp_static` returned in lease table.
- **Full lease table API**: GET /api/v1/dhcp/leases returns active leases with IP, MAC, hostname, expiry, origin (dhcp_dynamic | dhcp_static).
- **DHCP settings API**: PUT /api/v1/settings/dhcp enables/disables the server and sets pool, gateway, lease time, domain, DNS server. Merge-patch: only provided fields change.
- **Raft-persisted settings**: all DHCP config (enabled flag, pool, statics) stored in bbolt, replicated across cluster, restored after restart.
- **DHCP status API**: GET /api/v1/dhcp/server/status reports enabled state, is_leader, pool range, leases_active, pool_total.
- **DNS option default**: if dns_server is not configured, DHCP option 6 points to the node's own address (127.0.0.1 default in config, actual node IP in production).

## Real-condition Proxmox validation (3-node cluster, CT 200/201/202 + CT 204 client)

| Scenario | Result |
|----------|--------|
| DHCP DORA: dynamic lease | ✅ CT 204 obtained 10.0.0.200 from skoed-1 |
| DHCP DORA: static assignment | ✅ CT 204 obtained 10.0.0.50 after static MAC→IP configured |
| Static assignment Raft replication | ✅ Assignment visible on all 3 nodes via API |
| Leader failover: DHCP migration | ✅ skoed-1 stopped → skoed-3 became leader (term=6); port 67 bound on skoed-3; CT 204 obtained 10.0.0.50 from 10.0.0.102 |
| Old leader stops DHCP on demotion | ✅ Port 67 not bound on skoed-1 after failover |

## Bug fixed during validation

`cluster.GetDhcpServerSettings()` returned settings without static assignments (stored in a separate bbolt bucket). The in-memory DHCP server's `UpdateConfig()` received an empty static list, so static leases weren't populated in memory. Static assignment was only used during `allocate()` if the MAC had no existing dynamic lease.

**Fix**: `GetDhcpServerSettings()` now fetches and merges static assignments before returning, so all callers (API handlers, startup wiring) get the full picture.

## Not implemented (deferred to M23.6)

- Web UI for DHCP settings, pool config, static assignments table, lease viewer, utilisation gauge
- Per-client DHCP option overrides (node YAML only)

## Limitations

- Lease table is in-memory only (not persisted in bbolt); leases are lost on leader restart. After failover the new leader starts with an empty lease table; clients renew on their natural lease expiry or on next DISCOVER.
- No ARP conflict detection (RFC 2131 §2.2 probe). Omitted as this requires raw socket access; deferred.
- IPv6 (DHCPv6) not in scope for M23.5.
- The DHCP server is not started at boot if the node starts as a follower; it starts only when `LeadershipCh()` fires true.

## Acceptance tests

- **441 pass / 0 fail / 33 skip** (33 skips are IPv6 dnsmasq lease-file tests requiring live files)
- All DHCP-specific tests pass: TestDhcpServerDisabledByDefault, TestDhcpServerEnableViaApi, TestDhcpServerDisableViaApi, TestDhcpServerConfigPersisted, TestDhcpServerStatusReflectsPoolConfig, TestDhcpDnsOptionDefaultsToSelf, TestDhcpServerStatusApi, TestDhcpLeaseListApi, TestDhcpStaticAssignmentPersisted, TestDhcpStaticAssignmentDelete, TestDhcpStaticAssignmentRaft, TestDhcpLeaderOwnsListener
- Full report: `demos/m23.5/test-report.html`
