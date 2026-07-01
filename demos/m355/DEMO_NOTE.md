# M35.5 Named Device Registry — Demo Note

## Implemented scope

- **Device entity** — name, profile_id, MACs, IPs, hostnames, client_ids; stored in bbolt bucket `config_devices`
- **REST API** — `GET/POST /api/v1/devices`, `GET/PUT/DELETE /api/v1/devices/:id`; name uniqueness enforced (409 on duplicate)
- **Raft replication** — device CRUD via `CmdDeviceUpsert` / `CmdDeviceDelete` commands; replicates to all cluster nodes
- **DNS Tier 0 matching** — device registry checked before CIDR (Tier 1) and default profile; matched via IP, EDNS0 MAC (option 65501), hostname, or client-ID
- **Query log enrichment** — entries for device-matched queries include `device_name`, `device_id`, `match_source: "device_registry"`
- **Devices UI** — replaces Clients page; table with name/profile/IPs/MACs/hostnames columns; "Register device" side panel; inline edit; real-time search filter
- **Forward-compatibility fix** — `fsm.Restore()` calls `store.init()` after snapshot restore, re-creating any buckets added since the snapshot was taken (prevents nil-panic on rolling upgrade)

## Not implemented (out of scope for M35.5)

- DHCP lease → device auto-registration (import existing DHCP leases as device entries)
- Bulk import (CSV/JSON upload)
- Device groups or tags
- Per-device schedule overrides (depends on M17 schedule bindings, which is shipped)
- Device activity history / usage stats

## Limitations

- MAC matching requires EDNS0 option 65501 to be set by the upstream forwarder (e.g. skoed DHCP server); plain DNS resolvers do not carry MAC
- Device name is a stable slug used as the Raft key; renaming a device creates a new entry and deletes the old one (no rename-in-place)
- Search filter is client-side only; no server-side pagination for very large device registries

## Validation

- 9/9 acceptance tests green (Docker harness)
- 17/17 Proxmox 3-node cluster validation checks pass (skoed-01, skoed-02, skoed-03)
- Rolling upgrade tested: binary deployed to all 3 CTs with existing Raft snapshots — no crash, device registry survives restore
