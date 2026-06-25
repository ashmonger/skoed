# M30 — DHCPv4 Lease Persistence + DHCPv6 Server

## Implemented

### DHCPv4 Lease Persistence
- `CmdDhcpServerLeasesUpsert` / `CmdDhcpServerLeaseDelete` — Raft commands write leases to bbolt `dhcp4_leases` bucket
- `SetLeaseCallbacks(onUpsert, onDelete)` wired to `c.PersistDhcpLease()` and `c.DeleteDhcpLease()` — every ACK/RELEASE is persisted via Raft
- `Server.LoadLeases([]Lease4)` — bulk-loads persisted leases on leader election, skipping expired entries
- On startup: if node is already leader, persisted leases are loaded before accepting clients
- Leader-change goroutine: single goroutine manages both DHCPv4 and DHCPv6 servers on `LeadershipCh()` to avoid single-consumer race

### DHCPv6 Server (SARR flow)
- UDP 547 listener bound to `[::]` (dual-stack)
- Multicast group join: `ff02::1:2` (All_DHCP_Servers) joined on all multicast-capable interfaces via `syscall.SetsockoptIPv6Mreq`
- Full SARR (Solicit → Advertise → Request → Reply) flow implemented in `internal/dhcp/server6.go`
- Lease persistence: `SetLeaseCallbacks` wired to `c.PersistDhcp6Lease()` / `c.DeleteDhcp6Lease()` — every SARR Reply and Release persists via Raft
- `Server6.LoadLeases([]Lease6)` — restores DHCPv6 leases on leader election
- API routes: `GET /api/v1/dhcp/leases6`, `GET /api/v1/dhcp/server/status6`

### Raft Store
- 4 new bbolt buckets: `dhcp4_leases`, `dhcp4_leases_deleted`, `dhcp6_leases`, `dhcp6_leases_deleted`
- Commands: `CmdDhcp6ServerLeasesUpsert`, `CmdDhcp6ServerLeaseDelete`
- `GetDhcp6Leases()` returns non-expired persisted DHCPv6 leases

## Not Implemented

- DHCPv6 Renew/Rebind timer enforcement (server tracks T1/T2 but does not proactively revoke)
- DHCPv6 Prefix Delegation (IA_PD)
- DHCPv6 DNS option (option 23) delivery — leases are assigned IPs only
- DHCP failover protocol (RFC 3074) — leader-only model, non-leader nodes don't answer
- DHCPv6 static assignments via API (read-only static list not yet implemented)

## Validation

Acceptance tests: 9/9 pass (Docker harness):
- `TestDhcpV4LeasePersistedAfterLeaderFailover`
- `TestDhcpV4LeasesRestoredAfterFullClusterRestart`
- `TestDhcpV6ServerSarr`
- `TestDhcpV6LeasePersistedAfterLeaderFailover`
- `TestDhcpV6LeasesRestoredAfterFullClusterRestart`
- `TestDhcpV4PersistLeaseOnAck`
- `TestDhcpV4DeleteLeaseOnRelease`
- `TestDhcpV6PersistLeaseOnReply`
- `TestDhcpV6DeleteLeaseOnRelease`

## Proxmox Enterprise Validation (2026-06-24)

3-node Raft cluster: CT200 (skoed-1), CT201 (skoed-2), CT202 (skoed-3) — Alpine Linux.
3 client containers: CT204 (kids), CT205 (adults), CT206 (IoT) — each with 10 macvlan interfaces.
Binary: `skoed v0.2.4-12-g975baaa` (M30 commit).

**DHCPv4 Enterprise Soak:**
- 10 macvlan interfaces per client CT (MACs 02:30:a3:00:XX:YY)
- 30 clients across CT204/205/206 using `udhcpc`
- Result: **30/30 leases acquired** ✓

**DHCPv6 Soak:**
- Custom Python SARR client (bypasses Router Advertisement requirement)
- 1 client per CT, accumulated 15 leases across test runs
- Result: **15 DHCPv6 leases active** ✓

**TEST 1 — Leader Failover:**
- Pre-failover: DHCPv4=30, DHCPv6=15
- Killed leader (CT201); new leader elected (CT200) within 8s
- Post-failover: DHCPv4=30, DHCPv6=15
- Result: **PASS — all leases survived leader failover** ✓

**TEST 2 — Full Cluster Restart:**
- Pre-restart: DHCPv4=30, DHCPv6=15
- All 3 nodes stopped, then restarted; new leader elected (CT201)
- Post-restart: DHCPv4=30, DHCPv6=15
- Result: **PASS — all leases restored from Raft bbolt** ✓

Demo: `test-report.html` — full test suite report with Proxmox results.
