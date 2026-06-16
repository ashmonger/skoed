---
x-tsid: TS-ProfilesAndSchedules
x-fsid-links:
  - FS-ProfileAssignByIp
  - FS-ProfileAssignByCidr
  - FS-ProfileDefaultFallback
  - FS-ProfilePerClientAllowlist
  - FS-ProfileApiCrud
  - FS-ProfileSharedClientGroups
  - FS-ScheduleActiveWindow
  - FS-ScheduleAllowMode
  - FS-ScheduleMultipleProfiles
  - FS-ScheduleApiCrud
  - FS-ScheduleTimezoneIsNodeLocal
  - FS-ScheduleBindingsList
  - FS-ScheduleBindingsListEmpty
  - FS-ScheduleBindingsListNotFound
  - FS-ScheduleConfigYaml
---

# TS-ProfilesAndSchedules — Per-client profiles and schedule rules

## Data model

Two new replicated entities, both stored in `cluster.bbolt` under their
own buckets and committed via Raft like every other piece of cluster state.

### Profile

```go
type Profile struct {
    ID              string   // stable identifier; "default" is reserved
    Name            string   // human-friendly label
    Blocklists      []string // ids referencing config/blocklists
    Allowlist       []string // per-profile allow domains (exact or *.wildcard)
    SafeSearch      []string // list of providers: "google", "bing", "youtube", "duckduckgo"
    ClientIPs       []string // exact IPs (v4 or v6)
    ClientCIDRs     []string // CIDR ranges
}
```

Storage: bucket `config_profiles`, key `<id>`, value JSON.

### Schedule

```go
type Schedule struct {
    ID      string
    Name    string
    Mode    string         // "block_only_inside" | "allow_only_inside"
    Windows []TimeWindow
}

type TimeWindow struct {
    Days  []string // "Mon","Tue",…,"Sun"
    Start string   // "HH:MM" 24h, node-local
    End   string   // "HH:MM" — if End < Start, the window crosses midnight
}
```

Storage: bucket `config_schedules`, key `<id>`, value JSON.

### Schedule binding

A many-to-many between schedules and (profile, blocklist) pairs.

```go
type ScheduleBinding struct {
    ScheduleID  string
    ProfileID   string
    BlocklistID string
}
```

Storage: bucket `config_schedule_bindings`, key
`<schedule_id>:<profile_id>:<blocklist_id>`, value JSON.

## FSM commands

Added in `internal/cluster/commands.go`:

| Kind | Payload |
|---|---|
| `profile.upsert` | full `Profile` |
| `profile.delete` | `{id}` |
| `schedule.upsert` | full `Schedule` |
| `schedule.delete` | `{id}` (cascades: drops bindings referencing it) |
| `schedule_binding.upsert` | `{schedule_id, profile_id, blocklist_id}` |
| `schedule_binding.delete` | same composite key |

All replicated via Raft. `Store.applyTx` handles each kind exactly like the
existing M2 commands. The shadow YAML writer adds two new top-level sections
(`profiles:` and `schedules:`) so PBS-style backups capture them.

**Shadow YAML schedule sections** (M17):

```yaml
schedules:
  - id: evening-clamp
    name: Evening clamp
    mode: block_only_inside
    windows:
      - days: [Mon, Tue, Wed, Thu, Fri]
        start: "20:00"
        end: "23:59"

schedule_bindings:
  - schedule_id: evening-clamp
    profile_id: kids
    blocklist_id: social
```

Both sections are omitempty: absent from the file when no schedules exist.

## Profile resolution

The DNS handler now does an extra step on every query:

```
client_ip = extractIP(w.RemoteAddr())
profiles  = engine.profilesMatching(client_ip)  // 0..N, plus implicit "default"
allowed   = unionAllowlists(profiles)
blocked   = unionBlocklists(profiles)
```

`profilesMatching` walks every Profile and tests both `ClientIPs` (exact
string match) and `ClientCIDRs` (parsed `net.IPNet.Contains`). If no
profile matches, the implicit default profile is used. If multiple match
(allowed; FS-ProfileSharedClientGroups), the union of blocklists and
allowlists is applied — same union semantics already locked in for client
groups during M1 design.

## Schedule evaluation

At query time, after the (profile, blocklist) match is found:

```
for each ScheduleBinding(s, p.id, bl.id):
    if isActive(s, now_local):
        if s.Mode == "block_only_inside": → blocked
        if s.Mode == "allow_only_inside": → forwarded (override)
    else:
        if s.Mode == "block_only_inside": → forwarded (override)
        if s.Mode == "allow_only_inside": → blocked
```

`isActive` walks `s.Windows` and returns true if `now_local`'s weekday is
in `Days` AND its time-of-day is in `[Start, End)` (handling the
midnight-wrap case when `End < Start`).

Timezone: read from `node.yaml`'s new optional `node.timezone` field
(IANA name, e.g., `"Europe/Paris"`); defaults to UTC. Same code path used
on every node so a clustered deployment evaluates identically wherever the
DNS query lands.

## API surface

New endpoints (all under `/api/v1/`):

```
GET    /profiles              list
POST   /profiles              create
GET    /profiles/{id}         get
PATCH  /profiles/{id}         update
DELETE /profiles/{id}         delete (id="default" rejected)

GET    /schedules             list
POST   /schedules             create
GET    /schedules/{id}        get
PATCH  /schedules/{id}        update
DELETE /schedules/{id}        delete (cascades bindings)

POST   /schedules/{id}/bindings              attach to (profile,blocklist)
DELETE /schedules/{id}/bindings/{profile}/{blocklist}   detach
GET    /schedules/{id}/bindings              list bindings for schedule
```

**GET /schedules/{id}/bindings** response shape:

```json
[
  {"schedule_id": "evening-clamp", "profile_id": "kids",  "blocklist_id": "social"},
  {"schedule_id": "evening-clamp", "profile_id": "teens", "blocklist_id": "gaming"}
]
```

Returns `[]` (empty array, not null) when no bindings exist. Returns 404
when the schedule itself does not exist.

Auth + leader-forwarding behave exactly like M2 endpoints.

## Web UI

Three new pages (M3 Web UI work, not deferred to M3.5):

- `/profiles` — table of profiles with inline assignment editor (IP + CIDR
  lists), blocklist multi-select, allowlist textarea, SafeSearch toggles.
- `/schedules` — table of schedules with a weekly-grid widget (7 columns ×
  24 hours, click-to-toggle a window). Bindings panel attaches schedules
  to (profile, blocklist) rows.
- The Dashboard gains a "DoH attempts today" widget surfacing the
  per-client probe counts derived from query-log entries tagged
  `doh-probe`.

## Default profile bootstrapping

On first bootstrap (no `config_profiles` bucket entries), `main.go`'s
post-`WaitForLeader` block applies a `profile.upsert` for the canonical
default profile:

```yaml
id: default
name: Default
blocklists: []           # operator's responsibility
allowlist: []
safesearch: []
client_ips: []
client_cidrs: []         # empty CIDRs = fallback for unmatched IPs
```

The category-blocking work (TS-Categories) then adds the `cat:doh`
blocklist to the default profile so DoH detection is on out of the box.

## Acceptance test contract

`tests/acceptance/profiles_test.go` exercises FS-ProfileAssignByIp,
ByCidr, DefaultFallback, PerClientAllowlist, ApiCrud, SharedClientGroups.
`tests/acceptance/schedules_test.go` covers FS-Schedule*. Both run against
single-node clusters with `SKOED_TEST_MODE=1`.

## Non-goals (explicit)

- MAC-address identification (M3.5 + firewall recipes).
- Per-application scoping (DNS-only).
- Auto-discovery of devices (hand-authored profiles only at M3).
