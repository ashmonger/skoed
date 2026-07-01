# Named Device Registry — Technical Specification

x-tsid: TS-DeviceRegistry  
x-fsid-links: [FS-DeviceRegistryCreate, FS-DeviceRegistryUpdate, FS-DeviceRegistryDelete, FS-DeviceRegistryNameUnique, FS-DeviceProfileMatchExclusive, FS-DeviceMultiNicSingleConfig, FS-DeviceMatchPriorityHighestTier, FS-DevicesViewReplacesClients, FS-DevicesViewShowsUnifiedTable, FS-DeviceRegisterFromLease, FS-DeviceQueryLogEnrichment]

## Overview

A Device is a named entity that groups multiple network identifiers (MAC addresses, IP addresses, hostnames, client-ids) representing a single physical or virtual machine. Devices are stored in the Raft-replicated configuration and evaluated at the highest priority tier of the profile matching pipeline — above MAC, hostname, client-id, and IP/CIDR selectors on profiles.

## Data model

```go
// internal/config/config.go
type Device struct {
    ID         string   `json:"id"          yaml:"id"`
    Name       string   `json:"name"        yaml:"name"`
    ProfileID  string   `json:"profile_id"  yaml:"profile_id"`
    MACs       []string `json:"macs"        yaml:"macs,omitempty"`
    IPs        []string `json:"ips"         yaml:"ips,omitempty"`
    Hostnames  []string `json:"hostnames"   yaml:"hostnames,omitempty"`
    ClientIDs  []string `json:"client_ids"  yaml:"client_ids,omitempty"`
}
```

`ID` is generated as a URL-safe slug of `Name` (lowercase, spaces → hyphens, non-alphanumeric stripped). Name must be unique across all devices.

`Devices []Device` is added to `Config`.

## Storage

New bbolt bucket: `config_devices`. Keyed by `Device.ID`, value is JSON-marshalled `Device`.

`store.go` changes:
- Add `bucketDevices = []byte("config_devices")` constant.
- Add `bucketDevices` to the `init()` bucket list.
- Add FSM handlers for `CmdDeviceUpsert` and `CmdDeviceDelete`.
- `GetCfg()` reads devices from the bucket into `out.Devices`.
- `importM1Config()` imports devices from the imported config.

## Raft commands

```go
// internal/cluster/commands.go
CmdDeviceUpsert CommandKind = "device.upsert"
CmdDeviceDelete CommandKind = "device.delete"

type DeviceUpsertPayload struct {
    Device config.Device `json:"device"`
}

type DeviceDeletePayload struct {
    ID string `json:"id"`
}
```

## Cluster public methods

```go
// internal/cluster/cluster.go
func (c *Cluster) UpsertDevice(d config.Device) error {
    return c.applyAsLeader(CmdDeviceUpsert, DeviceUpsertPayload{Device: d}, 0)
}

func (c *Cluster) DeleteDevice(id string) error {
    return c.applyAsLeader(CmdDeviceDelete, DeviceDeletePayload{ID: id}, 0)
}
```

## Profile matching — new highest-priority tier

`filter/engine.go` — `profilesMatchingLockedWithIdentity` gains a new first tier evaluated before ClientID:

```
Tier 0 (Device registry): check MAC → check IP → check hostname → check client-id
    If any identifier matches a Device → return []string{device.ProfileID}  (short-circuit)
Tier 1 (ClientID on profile): existing
Tier 2 (MAC on profile): existing
Tier 3 (Hostname on profile): existing
Tier 4 (IP/CIDR on profile): existing (union, fallback)
```

The engine receives a `DeviceLookupFn func(mac, ip, hostname, clientID string) (profileID string, matched bool)` injected at startup. This keeps the engine free of direct cluster dependency (current pattern).

## API contract

See `specs/technical/management-api.openapi.yaml` — paths `/api/v1/devices` and `/api/v1/devices/{id}`.

### GET /api/v1/devices
Returns all registered devices, sorted by name. Query params:
- `q` (optional): case-insensitive substring filter on name, hostname, MAC, or IP.

### POST /api/v1/devices
Creates a new device. Body: `Device` object without `id` (server generates). Returns 201 + created device.  
Returns 409 if name already taken.  
Returns 400 for validation errors (empty name, invalid MAC format, unknown profile_id).

### GET /api/v1/devices/{id}
Returns device by ID. Returns 404 if not found.

### PATCH /api/v1/devices/{id}
Partial update (merge semantics for array fields: client sends full replacement arrays). Returns 200 + updated device.

### DELETE /api/v1/devices/{id}
Removes device. Returns 204. Returns 404 if not found.

## Frontend changes

### Navigation
`web/src/layouts/Shell.vue`: rename the `clients` nav entry to `devices` — label "Devices", same icon, same position in the Filtering section.

### Router
`web/src/router.ts`: rename route from `clients` to `devices`, pointing to `./views/Devices.vue`.

### Devices.vue
New view replacing `Clients.vue`. Table columns:

| Column | Source |
|--------|--------|
| Name | `device.name` or "—" for unregistered |
| IP address(es) | client IPs (comma-joined if >1) |
| Hostname | client hostname |
| MAC address(es) | client MACs (comma-joined if >1) |
| Client-ID | client client_id |
| Source | client source (dhcp / arp / static) |
| Last seen | client last_seen |
| Registered | badge when device entry exists |

Registered devices shown first, then unregistered clients. Within registered, sorted by device name.

"Register" action on unregistered rows opens a side-panel with name input + profile selector. On confirm: POST /api/v1/devices pre-filled with the client's known identifiers.

### types.ts
Add `Device` interface mirroring the Go struct.

## Query log enrichment

`internal/api/handlers/query_log.go`: when building a query log entry, look up the source IP in the device registry. If matched, include `device_name` in the JSON response. Unmatched clients omit the field.

## Validation

| Constraint | Rule |
|-----------|------|
| Name | Non-empty, ≤ 64 chars, unique |
| MACs | RFC 5342 format (aa:bb:cc:dd:ee:ff or AA-BB-CC-DD-EE-FF) |
| IPs | Valid IPv4 or IPv6 address |
| profile_id | Must reference an existing profile ID |
| Replication | Raft (all nodes updated before HTTP response) |
