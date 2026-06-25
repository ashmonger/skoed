---
x-tsid: TS-DhcpPersistenceDhcpv6
x-fsid-links:
  - FS-DhcpLeasePersistenceRestart
  - FS-DhcpLeasePersistenceFullClusterRestart
  - FS-DhcpLeasePersistenceLeaderFailover
  - FS-DhcpLeasePersistenceSoakRestart
  - FS-DhcpLeasePersistedToRaft
  - FS-DhcpLeaseExpiryRespectedAfterRestart
  - FS-Dhcpv6SarrFlow
  - FS-Dhcpv6IaNaPool
  - FS-Dhcpv6LeaseRenewal
  - FS-Dhcpv6LeaseRelease
  - FS-Dhcpv6DnsOptionDelivered
  - FS-Dhcpv6PoolExhaustion
  - FS-Dhcpv6LeaderOwnsListener
  - FS-Dhcpv6LeaderFailover
  - FS-Dhcpv6DuidProfileMatch
  - FS-Dhcpv6DuidPriorityOverMac
  - FS-Dhcpv6StaticAssignment
  - FS-Dhcpv6StaticAssignmentReplicatedViaRaft
  - FS-Dhcpv6LeasePersistenceRestart
  - FS-Dhcpv6LeasePersistenceFullClusterRestart
  - FS-Dhcpv6LeaseListApi
  - FS-Dhcpv6ServerStatusApi
  - FS-Dhcpv6WebUiConfigPanel
  - FS-Dhcpv6WebUiLeaseTable
  - FS-Dhcpv6WebUiStaticAssignmentCreate
---

# TS-DhcpPersistenceDhcpv6 — DHCP Lease Persistence + DHCPv6 Server

## Problem statement

M23.5 built the DHCPv4 server. Its **config** (pool settings, static assignments) is persisted via the shadow YAML / Raft cluster store. Its **runtime lease table** is held in-memory only. When the owning node restarts, all dynamic leases are lost — clients see a fresh DORA exchange and may receive different IPs.

M6.5 showed the Raft pattern for replicating _external_ DHCP state. M30 applies the same pattern to the _built-in_ server's own dynamic leases, and adds a DHCPv6 server alongside.

---

## Part 1 — DHCPv4 Lease Persistence

### Raft command: `CmdDhcpServerLeasesUpsert` / `CmdDhcpServerLeaseDelete`

Rather than a full snapshot-replace (the M6.5 approach for external leases), the built-in server uses fine-grained Raft commands: one command per lease event. This avoids re-applying the entire table on every DORA cycle and gives precise recovery semantics.

```
On DHCPACK (new or renew):
  raft.Apply(CmdDhcpServerLeasesUpsert{
    IP:        net.IP
    MAC:       net.HardwareAddr
    Hostname:  string
    ExpiresAt: time.Time
    Origin:    "dhcp_dynamic"
  })

On DHCPRELEASE or expiry:
  raft.Apply(CmdDhcpServerLeaseDelete{IP: net.IP})
```

Both commands are applied by `FSM.Apply` on every node, writing to a `bbolt` bucket `dhcp_server_leases` keyed by `[]byte(ip.String())`. Value is a JSON-encoded `DhcpServerLease` struct.

### Expiry purge

On startup, `FSM.Restore` loads the `dhcp_server_leases` bucket and skips leases whose `ExpiresAt` is in the past. No Raft command is needed at startup — stale entries are simply not loaded. A background goroutine running on the leader fires every 30s and applies `CmdDhcpServerLeaseDelete` for any lease that has expired since the last purge cycle.

### ACK ordering guarantee

The leader applies `CmdDhcpServerLeasesUpsert` and waits for quorum commit **before** sending the DHCPACK wire response. This ensures that if the leader crashes immediately after sending ACK, the new leader already has the lease in its replicated state. Latency cost: one Raft round-trip (~1–5 ms on LAN). This is acceptable for DHCP.

```
DHCPREQUEST received
  │
  ▼
allocate IP (in-memory lock)
  │
  ▼
raft.Apply(CmdDhcpServerLeasesUpsert)  ← blocks until quorum
  │
  ▼
send DHCPACK
```

### Startup sequence

```
1. FSM.Restore() loads dhcp_server_leases bucket → populates in-memory lease map
2. DHCP server goroutine starts
3. If node is leader, begin accepting on :67
4. Expiry purge goroutine starts (leader only)
```

---

## Part 2 — DHCPv6 Server

### Wire protocol

The DHCPv6 server listens on UDP port 547 (server port), responds to multicast `ff02::1:2`. Uses the same leader-ownership model as DHCPv4 — only the Raft leader binds port 547.

Message types handled:

| Client → Server | Server → Client | Description |
|----------------|----------------|-------------|
| Solicit (1)    | Advertise (2)  | Discovery — server offers an address |
| Request (3)    | Reply (7)      | Client requests the offered address |
| Renew (5)      | Reply (7)      | Client renews before T1 |
| Rebind (6)     | Reply (7)      | Client rebinds (missed T1 deadline) |
| Release (8)    | Reply (7)      | Client releases its address |
| Confirm (4)    | Reply (7)      | Client confirms on link change |
| Information-request (11) | Reply (7) | Stateless config (DNS options) |

DUID types supported for client identification: DUID-LLT, DUID-LL, DUID-UUID. The server generates its own DUID-LL from the network interface MAC.

### IA_NA address allocation

Single pool per node. Addresses in the pool are allocated sequentially with a free-list. On `FSM.Restore`, the allocator reconstructs the free list from the persisted `dhcp6_server_leases` bucket.

```
DhcpV6Lease {
  Address   net.IP
  DUID      []byte
  IAID      [4]byte
  Hostname  string
  T1        time.Duration   // typically lease_time / 2
  T2        time.Duration   // typically lease_time * 0.8
  ValidLT   time.Duration   // total valid lifetime
  ExpiresAt time.Time
  Origin    string          // "dhcp6_dynamic" | "dhcp6_static"
  ProfileID string          // populated by DUID profile match
}
```

### DUID profile matching

On receiving a Solicit, the server checks:
1. `dhcp.static_assignments6` for a DUID match → fixed IP + ProfileID from assignment
2. Profile store for `client_duids` containing the client's DUID → assigns ProfileID
3. Fallback: no profile assignment (client inherits default)

Priority: static assignment DUID > `client_duids` in profile > MAC-derived profile (from the IPv4 lease for the same MAC, if known).

The assigned `ProfileID` is written into the `DhcpV6Lease` struct and persisted via Raft so that the DNS engine can look up a client's profile by their IPv6 address without a DUID re-lookup.

### Persistence

Same Raft command pattern as DHCPv4:

```
CmdDhcp6ServerLeasesUpsert{...DhcpV6Lease}  → bbolt bucket: dhcp6_server_leases, key: ip.String()
CmdDhcp6ServerLeaseDelete{IP net.IP}        → removes from bucket
```

Reply is sent only after quorum commit, same as DHCPv4.

### Options delivered

| Option | Description |
|--------|-------------|
| 1  | Client DUID (echo) |
| 2  | Server DUID |
| 3  | IA_NA with assigned address and T1/T2 |
| 23 | DNS_SERVERS — skoed node's IPv6 listen address |
| 24 | DOMAIN_LIST — `dhcp6.search_domain` if configured |

### Configuration additions

```yaml
dhcp:
  server6:
    enabled: false
    prefix: "fd00::/64"
    pool_start: "fd00::100"
    pool_end:   "fd00::1ff"
    lease_time: 86400      # seconds
    search_domain: ""
    static_assignments:
      - duid: "00:01:00:01:aa:bb:cc:dd:ee:ff"
        address: "fd00::200"
        hostname: "server1"
```

`dhcp.server6` is replicated via the existing cluster config Raft path. `static_assignments` under `server6` follow the same replication as `dhcp.server.static_assignments`.

---

## New API endpoints

See `dhcp-persistence-dhcpv6.openapi.yaml` for full schema. Summary:

| Method | Path | Description |
|--------|------|-------------|
| GET    | /api/v1/dhcp/leases6 | List active DHCPv6 leases |
| DELETE | /api/v1/dhcp/leases6/{address} | Force-expire a DHCPv6 lease |
| GET    | /api/v1/dhcp/server/status6 | DHCPv6 pool utilisation |
| PUT    | /api/v1/settings/dhcp6 | Configure DHCPv6 server (enable, pool, lease time) |
| GET    | /api/v1/dhcp/static-assignments6 | List DHCPv6 static assignments |
| POST   | /api/v1/dhcp/static-assignments6 | Create DHCPv6 static assignment |
| DELETE | /api/v1/dhcp/static-assignments6/{duid} | Delete DHCPv6 static assignment |

Existing DHCPv4 endpoints are unchanged. The `/api/v1/dhcp/leases` endpoint gains a `?version=4|6|all` query parameter to optionally merge both lease tables in one response.

---

## Web UI additions

The existing Settings → DHCP panel (M23.6) gains:

1. **DHCPv6 subsection** below the DHCPv4 section: enable toggle, prefix, pool start/end, lease time, search domain.
2. **DHCPv6 static assignments table** with Add/Delete, analogous to the existing DHCPv4 static assignments table.
3. **DHCPv6 lease table** showing: IPv6 address, DUID (abbreviated, full on hover), hostname, expires_at, profile badge.

The DHCPv4 lease table (M23.6) gains a visual note: "Leases are now persisted — data survives restarts."

---

## Enterprise load profile (Proxmox validation)

- **DHCPv4 soak**: 50 concurrent DHCP clients (bash scripts in CT204/205/206 looping `dhclient -v -1`) requesting leases; restart the DHCP-owning node mid-soak; verify all pre-restart leases restore and no double-assignment occurs.
- **DHCPv6 soak**: 20 IPv6 clients (bash + `dhclient -6`) completing SARR on the `fd00::/64` prefix; leader failover mid-soak.
- **Full cluster restart**: all three nodes stopped; all leases present after reform.
- **Expiry boundary**: assign leases with 10s lifetime; verify they are gone after restart once expired.
