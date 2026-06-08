---
x-tsid: TS-LeaseOrigin
x-fsid-links:
  - FS-LeaseOriginKeaReservationsReportedHigh
  - FS-LeaseOriginKeaReservationsUnreachableInferred
  - FS-LeaseOriginDnsmasqDhcpHostParsed
  - FS-LeaseOriginDnsmasqConfigUnreadable
  - FS-LeaseOriginHttpJsonHonoursWireField
---

# TS-LeaseOrigin — per-connector static-vs-dynamic origin tagging

## Where the new fields live

Two purely observational fields are added to the canonical `Lease` struct
(M3.6 `apps/skoed/internal/dhcp/lease.go`). They are populated by the
connector at parse time and ride along through the manager cache, the
clients API, the Clients page, and Prometheus.

```go
// Lease (M6.5 extension — additive, omitempty for wire compat)
type Lease struct {
    IP        string    `json:"ip"`
    MAC       string    `json:"mac"`
    Hostname  string    `json:"hostname"`
    ClientID  string    `json:"client_id"`
    Source    string    `json:"source"`
    ExpiresAt time.Time `json:"expires_at"`

    // M6.5 additions
    Origin           Origin           `json:"origin,omitempty"`
    OriginConfidence OriginConfidence `json:"origin_confidence,omitempty"`
}

type Origin string
const (
    OriginDhcpStatic      Origin = "dhcp_static"
    OriginDhcpDynamic     Origin = "dhcp_dynamic"
    OriginRouterAdvertised Origin = "router_advertised" // reserved (M7+ SLAAC)
    OriginManualAdmin     Origin = "manual_admin"       // reserved (M7+ static admin entries)
)

type OriginConfidence string
const (
    OriginConfidenceHigh    OriginConfidence = "high"
    OriginConfidenceInferred OriginConfidence = "inferred"
    OriginConfidenceUnknown OriginConfidence = "unknown"
)
```

### Wire compatibility (M3.6 → M6.5)

Both fields use `omitempty`. M3.6 readers ignore unknown JSON fields,
so a leader running M6.5 hands replicated state to an M3.6 follower
without breaking the unmarshal. Conversely, an M6.5 reader receiving
an M3.6-shaped payload simply gets the zero value (`""` for both
fields), which the API layer surfaces as "" (see
FS-LeaseOriginUnknownClientOmitsOrigin). No schema version bump, no
FSM command rename, no bbolt migration — the Lease is connector-scoped
data, not Raft-replicated config.

This is deliberately decoupled from TS-LeaseReplication (M6.5
leader-polls work): that spec carries Lease shapes through Raft and is
the one responsible for the on-disk encoding. This spec only adds two
JSON tags.

## Connector behaviour

Each connector implementation is responsible for populating
`Origin` + `OriginConfidence` at parse time. The Manager itself is
origin-agnostic — it stores whatever the connector returned.

### Kea (`apps/skoed/internal/dhcp/connectors.go` → `keaConn`)

The poll cycle gains a SECOND control-agent call on every iteration:

```
POST <kea-control-agent-url>
{"command":"reservation-get-all","service":["dhcp4"],"arguments":{"subnet-id":0}}
```

`subnet-id: 0` is Kea's "all subnets" shorthand. The response is a
list of `{ip-address, hw-address, hostname, client-id}` reservation
records. The connector builds a `set[string]` of reserved IPs and
tags any matching lease as `dhcp_static / high`.

Error handling per FS-LeaseOriginKeaReservationsUnreachableInferred:

| `reservation-get-all` outcome | All leases this poll |
|-------------------------------|----------------------|
| HTTP 200, result=0            | reservation IPs → `dhcp_static / high`, rest → `dhcp_dynamic / high` |
| HTTP 5xx, timeout, parse err  | every lease → `dhcp_dynamic / unknown` + WARN `kea_reservation_lookup_failed` (once per poll) |
| HTTP 200, result≠0            | same as 5xx (treat as unreachable; we never lie with `dhcp_static`) |

The two HTTP requests share the same `keaConn.client` (5-second
timeout, basic-auth header). They run sequentially — `lease4-get-all`
first; if it fails the whole poll fails as before (no degradation),
keeping origin orthogonal to the leases-themselves error path.

### Dnsmasq (`apps/skoed/internal/dhcp/connectors.go` → `dnsmasqConn`)

The connector gains a second filesystem read for the dnsmasq running
config. The config path is supplied via a new optional field:

```go
type Config struct {
    // ... existing M3.6 fields ...
    ConfigPath string // NEW: e.g. "/etc/dnsmasq.conf"; optional
}
```

When `ConfigPath` is empty: every lease is `dhcp_dynamic / unknown`
(matches the existing M3.6 behaviour — no claim made, no warning).

When `ConfigPath` is set, the connector parses lines matching the
prefix `dhcp-host=`. Recognised forms (per the dnsmasq manpage):

```
dhcp-host=<mac>[,<ip>][,<hostname>][,<lease-time>]
dhcp-host=id:<client-id>[,<ip>][,<hostname>][,<lease-time>]
dhcp-host=set:<tag>[,...]                   ← ignored (no IP binding)
```

Lookup keys built per directive: any `<ip>` literal, any
`id:<client-id>`, any `<mac>` (lowercased). A lease matches when its
IP, ClientID, or MAC appears in the directive set → tag
`dhcp_static / inferred` (per FS-LeaseOriginDnsmasqDhcpHostParsed —
"inferred" because we're reading a config file, not a structured
reservation API).

Per FS-LeaseOriginDnsmasqConfigUnreadable: if `os.Open(ConfigPath)`
returns any error (permission denied, ENOENT, EIO), every lease is
`dhcp_dynamic / unknown` + WARN `dnsmasq_config_unreadable` (once
per poll). The lease parser does NOT fail.

#### Config-file size bound

The config parser limits the read to **1 MiB**. dnsmasq configs
larger than that emit WARN `dnsmasq_config_too_large`, are truncated
at the limit, and the parser returns whatever directives it managed
to extract (defence against a misconfigured `--conf-dir=/` pointing
at an entire filesystem).

### HTTP_JSON (`apps/skoed/internal/dhcp/connectors.go` → `httpJSONConn`)

The wire schema gains an optional `origin` field:

```go
type httpJSONLease struct {
    IP        string `json:"ip"`
    MAC       string `json:"mac"`
    Hostname  string `json:"hostname"`
    ClientID  string `json:"client_id"`
    ExpiresAt string `json:"expires_at"`
    Origin    string `json:"origin,omitempty"` // NEW
}
```

Field handling per FS-LeaseOriginHttpJsonHonoursWireField and
FS-LeaseOriginHttpJsonRejectsUnknownValue:

| Incoming `origin` value | Resulting Lease |
|-------------------------|-----------------|
| `"dhcp_static"`         | `Origin=dhcp_static, OriginConfidence=high` |
| `"dhcp_dynamic"`        | `Origin=dhcp_dynamic, OriginConfidence=high` |
| `""` / field absent     | `Origin=dhcp_dynamic, OriginConfidence=unknown` |
| any other string        | `Origin=dhcp_dynamic, OriginConfidence=unknown` + WARN `http_json_unknown_origin_value value=<offending>` (logged per-record, but deduped within a poll cycle so one bad upstream doesn't flood) |

`router_advertised` and `manual_admin` are accepted on the wire as
high-confidence but are NEVER produced by any M6.5 connector — they
exist in the enum for forward compatibility with M7+ SLAAC/RA snooping
and admin-curated static entries.

## HTTP contracts

Both endpoints already exist (M3.6 + M5.x). M6.5 only extends the
response payload — no new routes, no breaking changes.

```yaml
# Extends TS-ManagementApi (specs/technical/management-api.openapi.yaml)
paths:
  /api/v1/clients/{ip}:
    get:
      summary: Per-client identity + DHCP origin
      parameters:
        - in: path
          name: ip
          required: true
          schema: { type: string, format: ipv4 }
      responses:
        '200':
          description: client snapshot (origin fields are "" when no lease known)
          content:
            application/json:
              schema:
                type: object
                properties:
                  ip:                { type: string }
                  mac:               { type: string }
                  hostname:          { type: string }
                  client_id:         { type: string }
                  source:            { type: string, enum: [kea, dnsmasq, http_json, none] }
                  origin:            { type: string, enum: ["", dhcp_static, dhcp_dynamic, router_advertised, manual_admin] }
                  origin_confidence: { type: string, enum: ["", high, inferred, unknown] }
              examples:
                known_static:
                  value: { ip: "192.168.1.42", mac: "aa:bb:cc:dd:ee:42", hostname: "kid-tablet",
                           client_id: "id:tablet42", source: "kea",
                           origin: "dhcp_static", origin_confidence: "high" }
                unknown:
                  value: { ip: "192.168.99.99", mac: "", hostname: "", client_id: "",
                           source: "none", origin: "", origin_confidence: "" }

  /api/v1/clients:
    get:
      summary: List all known DHCP clients with origin badge data
      responses:
        '200':
          description: client list, sorted by IP
          content:
            application/json:
              schema:
                type: object
                properties:
                  clients:
                    type: array
                    items:
                      type: object
                      properties:
                        ip:                { type: string }
                        mac:               { type: string }
                        hostname:          { type: string }
                        client_id:         { type: string }
                        origin:            { type: string }
                        origin_confidence: { type: string }
```

### Why not a dedicated `/origin` sub-resource?

The fields ride on the existing lease object — adding
`GET /api/v1/clients/{ip}/origin` would force the SPA to make N+1
calls to render the Clients page. The Clients page already needs
hostname/MAC/profile per row; serving origin on the same payload is
the cheap path.

## Raft / bbolt impact

**None.** Lease data is connector-scoped per-node state (M3.6
contract). It is not in any bbolt bucket and is not carried through
any FSM command. The two new struct fields exist only in the manager's
in-memory map and on the API response wire.

The M6.5 lease-replication work (TS-LeaseReplication, separate spec)
WILL replicate leases through Raft — when it lands, the
`Origin` + `OriginConfidence` fields ride along inside the
`leases.replace` payload free of charge (they're already on the
struct). No additional command kinds, no additional buckets, no
additional snapshot keys are needed for origin tagging alone.

## Scheduler jobs

None new. Origin computation is in-line with the existing M3.6
`pollOnce()` cycle:

```
pollOnce()
  ├─ Fetch leases       (M3.6 — connector-dependent)
  ├─ Fetch origin hints (M6.5 — kea: reservation-get-all
  │                            dnsmasq: read ConfigPath
  │                            http_json: free, already on wire)
  ├─ Annotate leases with Origin + OriginConfidence
  └─ apply(leases)      (M3.6 — anti-spoof + cache replace)
```

Polling cadence is unchanged (default 60 s, configurable). The
extra Kea HTTP call adds one round-trip per poll cycle; the extra
dnsmasq file read adds one `stat`+`read` of the config file (bounded
1 MiB) per poll cycle.

## Metrics

```
# Per FS-LeaseOriginPrometheusGauges
skoed_dhcp_leases{source="kea|dnsmasq|http_json", origin="dhcp_static|dhcp_dynamic|router_advertised|manual_admin"}
```

This **replaces** the M3.6 `skoed_dhcp_leases{source}` gauge with a
two-label version. Series are emitted lazily — a label combination
appears in `/metrics` only when at least one lease holds it (per the
spec: "no series is emitted for an origin value with zero leases").

### Cardinality bound

3 sources × 4 origin values = **12 series maximum per node**, in
practice ~3 (one source per node × `dhcp_static` + `dhcp_dynamic`).
Well inside the M5.x metrics budget.

No `origin_confidence` label — that's a UI signal, not an alerting
signal. Splitting on confidence would 3× the series for no operator
value.

## Posture

### Auth

| Endpoint                  | Auth                                      |
|---------------------------|-------------------------------------------|
| `GET /api/v1/clients/{ip}`| existing M3.6 basic-auth (no change)      |
| `GET /api/v1/clients`     | existing M3.6 basic-auth (no change)      |

No new auth surface. Origin tagging is read-only metadata on an
existing authenticated endpoint.

### Audit

No new audit events. Origin annotation happens inside the polling
loop, not via an API mutation. The audit middleware's M5.2 exemption
list is unchanged.

A configuration change to `dhcp.config_path` (the new dnsmasq config
location) flows through the existing `settings.patch` Raft command
and is audited like every other settings change.

### Metrics

Covered above. One existing series gets a new label dimension; total
cardinality per node stays ≤ 12 series.

### SSRF / PII

- **Kea `reservation-get-all`** hits the same operator-configured
  control-agent URL as `lease4-get-all`. No SSRF expansion — the URL
  is M3.6 trust-boundary input, validated at config-load time.
- **Dnsmasq config read** is filesystem-only. The path is operator-
  configured, normalised through `filepath.Clean`, and the 1 MiB read
  cap prevents an unbounded read of `/dev/zero`-style traps.
- **HTTP_JSON `origin` field** is treated as untrusted enum input.
  Unknown values fall back to `dhcp_dynamic / unknown` (never crash,
  never panic, never escalate to `dhcp_static`).
- **PII**: origin and origin_confidence are non-PII tags. They do not
  appear in the query log (per FS-LeaseOriginQueryLogDoesNotChange) —
  they live only on the Lease, the Clients endpoint, and the
  Prometheus gauge.

### Netlink / capabilities

Not relevant for this spec. Origin tagging is a pure
file/HTTP-parsing concern and runs in the same goroutine as the
existing M3.6 connector polls. No CAP_NET_ADMIN, no raw sockets, no
netlink subscription.

(The neighbouring TS-DhcpArpCrossCheck spec is the one that needs
netlink. Origin tagging does not depend on or interact with it.)

### Failure modes

| Failure                               | Behaviour                                                          |
|---------------------------------------|--------------------------------------------------------------------|
| Kea `reservation-get-all` 5xx         | every lease tagged `dhcp_dynamic / unknown`, WARN once             |
| Kea reservations response malformed   | same as 5xx; never panic                                           |
| Dnsmasq `ConfigPath` unset            | every lease tagged with empty fields (M3.6 behaviour preserved)    |
| Dnsmasq config unreadable             | every lease tagged `dhcp_dynamic / unknown`, WARN once             |
| Dnsmasq config > 1 MiB                | truncated; whatever parses is honoured; WARN `dnsmasq_config_too_large` |
| HTTP_JSON `origin` unknown            | record-level fallback to `dhcp_dynamic / unknown`, WARN deduped    |
| Connector fails entirely              | M3.6 path — prior snapshot kept, no origin changes applied         |

## Web UI

The Clients page (`web/src/views/Clients.vue`) gains a small chip
column per FS-LeaseOriginClientsListSurfacesBadge:

| `origin`         | Chip text  | Chip colour | Tooltip                              |
|------------------|-----------|-------------|--------------------------------------|
| `dhcp_static`    | "static"  | green       | (no tooltip)                         |
| `dhcp_dynamic`   | "dynamic" | grey        | (no tooltip)                         |
| `router_advertised` | "RA"   | blue        | "router-advertised (SLAAC)"          |
| `manual_admin`   | "manual"  | purple      | "manually entered by admin"          |
| `""` (unknown)   | "—"        | muted       | "origin unknown"                     |

A row whose `origin_confidence == "unknown"` renders the chip muted
regardless of origin value, with a tooltip "origin unknown". This is
the visual signal that the upstream DHCP source didn't tell us, so
the dynamic/static labelling shouldn't be trusted.

No new sidebar entry; this is an additive column on an existing page.

## Implementation map

```
apps/skoed/internal/dhcp/
  lease.go              (extend: Origin + OriginConfidence types & enums)
  connectors.go         (extend: keaConn fetches reservation-get-all,
                                  dnsmasqConn parses ConfigPath,
                                  httpJSONConn honours .origin)
  config.go (or wherever Config lives)
                        (extend: Config.ConfigPath for dnsmasq)
apps/skoed/internal/api/handlers/
  clients.go            (extend: response includes new fields;
                                  unknown-IP path leaves them "")
apps/skoed/internal/metrics/
  metrics.go            (extend: skoed_dhcp_leases gets `origin` label,
                                  emit lazily per (source, origin) seen)
web/src/views/
  Clients.vue           (extend: render origin chip column)
specs/technical/
  management-api.openapi.yaml   (extend: response schema fragment from above)
tests/acceptance/
  lease_origin_test.go  (NEW — all 12 FSIDs)
```

## Out of scope

- Editing reservations from skoed (read-only — origin reflects what
  the upstream DHCP source already knows).
- DHCPv6 origin tagging — covered separately under TS-DhcpV6LeaseParsing.
- Origin-based blocking semantics — the `block_dynamic_clients`
  profile rule lives in TS-ProfileBlockDynamicClients; this feature
  only TAGS leases.
- Synthesising `router_advertised` from SLAAC/RA snooping. The value
  is reserved in the enum for future use; M6.5 only emits
  `dhcp_static` and `dhcp_dynamic` from the three connectors.
- Per-IP origin timeline / history (the Lease carries the current
  origin only; the M3.6 anti-spoof history table is not extended).
