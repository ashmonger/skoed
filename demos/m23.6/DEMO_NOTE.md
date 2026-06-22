# M23.6 — DHCP Server Web UI — Demo Note

## Implemented scope

- **Dedicated DHCP page** at `/dashboard/dhcp` — own navbar entry under System, not inside Settings
- **Enable/disable toggle** — immediate PUT to `/api/v1/settings/dhcp`; optimistic UI, reverts on error
- **Pool configuration form** — pool_start, pool_end, gateway, lease_time_seconds, domain, dns_server; saves via PUT
- **Pool utilisation gauge** — progress bar from `leases_active / pool_total`; updated every 10 s
- **Static assignments CRUD** — inline add form (MAC + IP + optional hostname) and delete with confirmation dialog
- **Live lease table** — IP, MAC, Hostname, Expires (relative), Origin badge (`dhcp_static` / `dynamic`); 10 s auto-refresh
- **Shadow YAML persistence** — `dhcp_server` section including `static_assignments` now written by `shadow_yaml.go`; full cluster cold-start requires no manual reconfiguration

## Not implemented / out of scope

- Per-client DHCP option overrides (YAML-only, no UI)
- DHCPv6 configuration
- Lease persistence across leader restart (in-memory — M23.5 limitation, deferred)
- Bulk import of static assignments

## Limitations

- DHCP service only runs on the Raft leader. The page reflects leader state; follower nodes display the same config but the DHCP socket is inactive on non-leaders.
- Lease table shows static-origin leases even after the client renews from the in-memory pool; the `dhcp_static` origin badge is accurate for protocol-level origin, not current socket state.

## Validation

- **446 / 482 acceptance tests pass** (36 skipped — unrelated features), **0 failures** on 3-node Proxmox cluster
- All 8 M23.6 FSID-tagged tests pass: FS-DhcpWebUiSettingsTabVisible through FS-DhcpWebUiPoolUtilisationGauge
- Screenshots taken in Lipgloss dark theme on skoed-1 (10.0.0.100)

## Enterprise load test v2 (2026-06-22)

**20/21 PASS · 514s · Proxmox proxtest2 (16 CPUs, 62 GiB RAM) · [Full report with screenshots](enterprise/test-report.html)**

9-phase test: 60 concurrent clients (30 WS + 15 IoT + 15 mobile), sustained DNS storm (~400 QPS, 16 domains/cycle), multi-node sysadmin, API perf, Raft failover mid-storm, lease renewal flood, new clients post-failover, full cluster recovery.

| Metric | Result |
|--------|--------|
| 60 concurrent DHCP clients (Alpine LXC) | All 61 leases in **5 seconds** |
| DNS traffic | **~400 QPS** sustained (60 CTs × 16 domains/2s cycle) |
| DNS queries logged | **10,000+** (ring buffer max) |
| API latency under full load | **avg 2ms / max 4ms** |
| Raft leader failover mid DNS storm | **6.4 seconds** (term 13) |
| Post-failover new clients | 5 new leases from new leader |
| Full 3-node recovery consistency | **67 / 67 / 67** leases |
| 1 known failure | `cluster/stats` shows 0 (hourly flush — not a bug) |
