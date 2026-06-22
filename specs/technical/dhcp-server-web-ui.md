# DHCP Server Web UI — Technical Specification

x-tsid: TS-DhcpServerWebUi
x-fsid-links:
  - FS-DhcpWebUiSettingsTabVisible
  - FS-DhcpWebUiToggleEnable
  - FS-DhcpWebUiToggleDisable
  - FS-DhcpWebUiPoolConfig
  - FS-DhcpWebUiStaticAssignmentCreate
  - FS-DhcpWebUiStaticAssignmentDelete
  - FS-DhcpWebUiLeaseTable
  - FS-DhcpWebUiPoolUtilisationGauge

## Scope

A new **DHCP** tab inside the existing `Settings.vue` page. No new API endpoints —
all operations use the six endpoints from TS-DhcpServer.

## API surface used

| Method | Path | Purpose |
|--------|------|---------|
| GET  | `/api/v1/dhcp/server/status` | Load current config + live metrics (enabled, pool, leases_active, pool_total) |
| PUT  | `/api/v1/settings/dhcp` | Save pool config and enable/disable toggle |
| GET  | `/api/v1/dhcp/leases` | Fetch live lease table |
| GET  | `/api/v1/dhcp/static-assignments` | Fetch static assignment list |
| POST | `/api/v1/dhcp/static-assignments` | Create static assignment |
| DELETE | `/api/v1/dhcp/static-assignments/{mac}` | Delete static assignment |

## Component: `Dhcp.vue` — dedicated DHCP page

DHCP is a full feature with its own route at `/dashboard/dhcp` and a dedicated
navbar entry under **System**. The page is composed of stacked
`<section class="card">` blocks.

### Layout

```
DHCP Server                          [toggle enabled]

┌─ Pool configuration ───────────────────────────────┐
│  Start IP  [__________]  End IP [__________]       │
│  Gateway   [__________]  Lease time [__________]   │
│  Domain    [__________]  DNS server [__________]   │
│                                    [Save DHCP settings] │
└────────────────────────────────────────────────────┘

┌─ Pool utilisation ─────────────────────────────────┐
│  ████░░░░░░░░░░░░░░░░  1 / 51                      │
└────────────────────────────────────────────────────┘

┌─ Static assignments ──────────────────── [+ Add] ──┐
│  MAC              IP           Hostname            │
│  bc:24:11:…  10.0.0.50  dhcp-client  [✕]           │
└────────────────────────────────────────────────────┘

┌─ Active leases ──────────────────── [↺ Refresh] ───┐
│  IP        MAC            Hostname  Expires  Origin │
│  10.0.0.50  bc:24:11:…   dhcp-client  expired  static │
└────────────────────────────────────────────────────┘
```

### Behaviour

**Enable/disable toggle**
- On mount, GET `/api/v1/dhcp/server/status` populates all fields.
- Toggle immediately calls PUT `/api/v1/settings/dhcp` with `{"enabled": <bool>}`.
  Pool fields must be non-empty to enable; show inline error on 409.
- Toggle reflects optimistic state, reverts on error.

**Pool config form**
- Fields: `pool_start`, `pool_end`, `gateway`, `lease_time_seconds` (integer),
  `domain`, `dns_server` (optional — leave blank to use node default).
- Save button calls PUT `/api/v1/settings/dhcp` with all non-empty field values.
- On success response the form re-populates from the returned status object.

**Pool utilisation gauge**
- Derived from `leases_active` / `pool_total` (from GET `/api/v1/dhcp/server/status`).
- Simple progress bar + `<leases_active> / <pool_total>` label.
- Updates on each status poll (every 10 s) and after any write.

**Static assignments table**
- On mount (and after each write) GET `/api/v1/dhcp/static-assignments`.
- Columns: MAC, IP, Hostname, delete action.
- **Add row** — inline form: MAC (validated as `XX:XX:XX:XX:XX:XX`), IP (validated as IPv4), hostname (optional). POST on save; show conflict error on 409.
- **Delete** — trash icon + confirm dialog → DELETE `/api/v1/dhcp/static-assignments/{mac}` (colons passed as-is in the path; chi's router preserves them). Remove row on success.

**Live lease table**
- On mount GET `/api/v1/dhcp/leases`; re-fetched every 10 s and on demand.
- Columns: IP, MAC, Hostname, Expires (relative), Origin (badge: `dynamic` / `static`).
- Empty-state message when no leases.

### Error handling

All API errors surface as an inline `<p class="text-danger">` below the relevant
section. Network errors use the standard `w(err, fallback)` helper already used
in other Vue components.

### Shadow YAML (FS-DhcpWebUiPoolConfig persistence note)

Pool config and static assignments written via the UI are stored in bbolt
(Raft-replicated). The shadow YAML writer **must** be extended to include the
`dhcp_server` section so the config survives a full cluster cold-start. This is
a Go-side concern tracked in M23.6 implementation (separate from the Vue layer).

## Routing

| Route | Name | Component |
|-------|------|-----------|
| `/dashboard/dhcp` | `dhcp` | `Dhcp.vue` |

DHCP has its own dedicated route; it is not a section inside Settings.
The sidebar **System** section contains a **DHCP Server** entry that links
to this route.

## Non-goals

- Per-client DHCP option overrides in the UI (node YAML only).
- DHCPv6 configuration.
- Lease persistence across leader restart (in-memory lease table — M23.5 limitation).
- Bulk import of static assignments.
