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

## Enterprise load test

The enterprise full-cluster test was introduced at M23.6. Starting from M31, it is run as the default validation test for each milestone that ships cluster features. The latest enterprise test results live in the most recent milestone's demo folder.

- **M23.6 run (2026-06-22):** 20/21 PASS (1 known non-bug: cluster/stats hourly flush)
- **M31 run (2026-06-25):** [**36/36 PASS** · 664s · full report](../m31/enterprise/test-report.html)
