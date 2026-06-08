---
x-tsid: TS-ArpCheck
x-fsid-links:
  - FS-ArpCheckArpStateAgreesWithLease
  - FS-ArpCheckArpMacMismatchFlagsAnomaly
  - FS-ArpCheckNdpMacMismatchFlagsAnomaly
  - FS-ArpCheckGhostLeaseLongLivedButNeverInKernel
  - FS-ArpCheckUnseenByKernelFreshLeaseStaysQuiet
---

# TS-ArpCheck — layer-3 ARP/NDP cross-check for DHCP leases

## Where this signal sits

M3.6 gave skoed the *intra-DHCP* anti-spoof detector: it compares a new
lease against history (Client-ID ↔ MAC ↔ hostname) and flags
`mac_changed_for_client_id`, `client_id_changed_for_mac`, and
`new_device_steals_hostname`. That detector is **internally consistent
with the DHCP server** — if a spoofer can talk to the DHCP server they
look fine on that channel.

TS-ArpCheck adds a **third, independent signal**: the local kernel's
own view of `(IP → MAC)` on the link, read from netlink. The DHCP
server can be wrong, can be stale, can have been lied to — the kernel's
ARP/NDP cache is what packets *actually* answered to in the last few
seconds. When the two disagree, that's worth surfacing.

Four new anomaly kinds (added to the existing `AnomalyKind` enum in
`apps/skoed/internal/dhcp/lease.go`):

| Kind                | Trigger                                                                                    |
|---------------------|--------------------------------------------------------------------------------------------|
| `arp_mac_mismatch`  | IPv4 lease's `MAC` ≠ kernel ARP entry's `lladdr` for the same `IP`                         |
| `ndp_mac_mismatch`  | IPv6 lease's `MAC` (or M6.5 DHCPv6 DUID-derived) ≠ kernel NDP entry's `lladdr` for the `IP`|
| `ghost_lease`       | Lease first observed > 6 h ago, kernel has *never* seen the IP **and** never seen the MAC  |
| `unseen_by_kernel`  | Lease first observed > 30 min ago, kernel has no ARP/NDP entry for the IP right now        |

The probe is **best-effort and per-node**. It does not block DHCP
polling, it does not replicate across the cluster (each node
cross-checks its own kernel), and it degrades gracefully when the
kernel refuses to talk (see § Netlink capability degradation).

## HTTP surface

### GET /api/v1/clients/{ip}/arp-state

```yaml
paths:
  /api/v1/clients/{ip}/arp-state:
    get:
      summary: Compare DHCP's MAC for this IP with the local kernel's ARP/NDP cache
      tags: [clients]
      security: [{ bearerAuth: [] }]
      parameters:
        - name: ip
          in: path
          required: true
          schema: { type: string }   # IPv4 or IPv6 literal
      responses:
        '200':
          description: Cross-check result. `anomaly` present only when one was flagged.
          content:
            application/json:
              schema:
                type: object
                required: [ip, mac_dhcp, mac_kernel, kernel_state, last_observed_unix]
                properties:
                  ip:                 { type: string }
                  mac_dhcp:           { type: string, description: "MAC from the DHCP lease (lowercased)" }
                  mac_kernel:         { type: string, description: "MAC from netlink — empty when kernel has no entry or netlink is unavailable" }
                  kernel_state:
                    type: string
                    enum: [reachable, stale, delay, probe, failed, none, netlink_unavailable]
                  last_observed_unix: { type: integer, description: "Unix epoch seconds when this node last sampled netlink for this IP" }
                  anomaly:
                    type: string
                    enum: [arp_mac_mismatch, ndp_mac_mismatch, ghost_lease, unseen_by_kernel]
                    description: "Omitted when no anomaly is currently flagged for this IP/MAC pair"
        '404':
          description: No lease exists in the local snapshot for this IP
          content:
            application/json:
              schema:
                type: object
                properties:
                  error: { type: string, example: "no lease for 10.99.99.99" }
        '401': { description: Missing/invalid bearer token }
```

The `kernel_state` enum maps directly to Linux's `NUD_*` neighbour
states (`NUD_REACHABLE` → `"reachable"`, `NUD_STALE` → `"stale"`, etc.),
plus two skoed-specific values:

- `"none"` — kernel returned no entry for this IP (RTNETLINK ack with
  empty payload).
- `"netlink_unavailable"` — this node could not open or query the
  netlink socket (typically missing CAP_NET_ADMIN). See § Netlink
  capability degradation.

### GET /api/v1/anomalies (extended)

This endpoint already exists at `/api/v1/clients/anomalies` (M3.6).
M6.5 extends the response — no new path, no new schema fields, just
four new values in the existing `kind` enum:

```yaml
paths:
  /api/v1/clients/anomalies:
    get:
      summary: List recent anti-spoof anomalies (M3.6 + M6.5 layer-3 kinds)
      tags: [clients]
      security: [{ bearerAuth: [] }]
      parameters:
        - name: kind
          in: query
          required: false
          schema:
            type: string
            enum:
              - mac_changed_for_client_id      # M3.6
              - client_id_changed_for_mac      # M3.6
              - new_device_steals_hostname     # M3.6
              - arp_mac_mismatch               # M6.5 (new)
              - ndp_mac_mismatch               # M6.5 (new)
              - ghost_lease                    # M6.5 (new)
              - unseen_by_kernel               # M6.5 (new)
          description: When provided, return only anomalies of this kind.
      responses:
        '200':
          description: Anomaly list (oldest first within the AnomalyRetention window)
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: '#/components/schemas/Anomaly'
```

The `Anomaly` schema (already defined for M3.6) is reused unchanged.
`details` is the existing free-form map; for the new kinds it contains
`{ "mac_dhcp": "...", "mac_kernel": "...", "kernel_state": "..." }`.

## Detection logic (sweep loop)

A second goroutine — independent of the DHCP poll loop — runs the ARP
cross-check sweep. Pseudocode:

```
every arp_sweep_interval (default 90s):
    if already running: log "arp_sweep_skipped" (rate-limited 1/min); return
    leases := manager.Snapshot()                  // M3.6 snapshot, no I/O
    table  := netlink.NeighborList(AF_INET)       // single RTM_GETNEIGH dump
    table6 := netlink.NeighborList(AF_INET6)      //   "         "      for v6
    for each lease in leases:
        cross_check(lease, table, table6, now)
```

The sweep takes one RTM_GETNEIGH dump per address family (not one
syscall per lease) — even with 1000 leases this is two netlink dumps
per 90 s, well under 1 RPS. Empirically a 250-entry neighbour table
dump completes in < 10 ms.

`cross_check` decision tree:

```
kernel_entry := table[lease.IP]                   // O(1) map lookup

if netlink_unavailable:
    set kernel_state = "netlink_unavailable"; record NO anomaly; return

if kernel_entry == nil:
    set mac_kernel = ""; kernel_state = "none"
    age := now - lease.FirstSeen                  // from M3.6 historyEntry
    if age > ghost_lease_threshold (default 6h)
       AND kernel has never seen lease.MAC on any iface in the last 24h:
        record ghost_lease (deduped by IP+MAC)
    else if age > unseen_grace (default 30 min):
        record unseen_by_kernel (deduped by IP+MAC)
    else:
        // fresh lease, no anomaly yet — kernel may just not have ARP-resolved
    return

set mac_kernel = kernel_entry.lladdr; kernel_state = kernel_entry.state
if normalize(mac_kernel) != normalize(lease.MAC):
    kind := arp_mac_mismatch if lease.IP is v4 else ndp_mac_mismatch
    record kind (deduped by IP+mac_dhcp+mac_kernel)
```

"Kernel has never seen lease.MAC on any iface in the last 24h" requires
a second rolling cache: `macSeen map[string]time.Time` — every sweep
extends a (lowercase MAC → last-observed-by-kernel) timestamp from the
neighbour dump. The cache is bounded by the size of the neighbour
table (typically a few hundred entries on a /24); we evict entries
older than 24 h on each sweep.

Anomaly deduplication uses the existing `Manager.recordAnomaly` path —
same kind + same IP + same MAC = no second insertion while unacknowledged.

## Config

New top-level block in `node.dhcp.arp_check` (default-on when a DHCP
connector is configured):

```yaml
node:
  dhcp:
    arp_check:
      enabled: true                # default true; set false to disable the sweep entirely
      sweep_interval: 90s          # default 90s; min 10s
      ghost_lease_threshold: 6h    # lease age before "kernel never saw it" becomes ghost_lease
      unseen_grace: 30m            # lease age before "no current ARP/NDP entry" becomes unseen_by_kernel
      mac_seen_retention: 24h      # how long to remember a MAC was once in the kernel's neighbour table
```

All values are bounded:

- `sweep_interval` ≥ 10 s (lower would risk overlapping sweeps; the
  spec's `arp_sweep_skipped` log line catches the safety net).
- `ghost_lease_threshold` ≥ `unseen_grace` (validated at config load;
  hard error if violated).
- `unseen_grace` ≥ 5 × `sweep_interval` (validated; prevents flapping).

## Netlink capability degradation

The sweep opens one RTNETLINK socket on first use and caches it. Two
failure paths exist:

### 1. Socket open fails (no CAP_NET_ADMIN)

`netlink.NewHandle()` (or equivalent) returns `EPERM` /
`EACCES`. The sweep:

1. Sets a process-wide flag `netlinkUnavailable = true`.
2. Emits **one** structured log event with `event="netlink_unavailable"`,
   `cap_net_admin=false`, plus the underlying errno. **No retry storm**:
   the flag short-circuits every subsequent sweep before any syscall.
3. The next sweep tick still fires (so the sweep counter advances and
   future re-permissions are picked up), but it only attempts a fresh
   socket open every `netlink_reprobe_interval` (default 1 h). Between
   probes the cross-check is a no-op.

`GET /api/v1/clients/{ip}/arp-state` returns 200 with
`mac_kernel=""`, `kernel_state="netlink_unavailable"`, and **no
anomaly field**. No new anomalies of the four M6.5 kinds are recorded
while the flag is set.

This means an unprivileged deployment loses the layer-3 signal entirely
but keeps the M3.6 lease-history detector — the API surface stays
honest about why (`kernel_state="netlink_unavailable"` is the operator-
visible breadcrumb).

### 2. Per-dump failure (transient ENOBUFS, EINTR, …)

Single dump fails → the sweep logs a structured `event="arp_sweep_error"`
with the errno (rate-limited 1/min), skips this tick, and tries again
next cycle. The `netlinkUnavailable` flag is **not** set: this is
transient, not a capability problem. No anomalies cleared, no anomalies
added.

### Why no kernel-config-time check?

We deliberately do **not** consult `/proc/self/status` or `prctl(PR_CAPBSET_READ)`
at boot: capability sets can be granted to a single goroutine via
`setcap` after process start in some deployments (containerd, k8s
securityContext). The "try once, fail fast, log once, retry hourly"
loop is the only check that's actually correct in all deployments.

## Implementation choices

**Library:** `github.com/vishvananda/netlink` for the
`RTM_GETNEIGH` dump and state-bit decoding. It's pure Go (no cgo), it
matches our existing dependency policy (Rule 11 — standard / common
libs only; this one is the de-facto standard for netlink in Go and is
already used by Docker, runc, k8s). Decision recorded in
`decisions/20260608-NetlinkLibrary.md`.

**Scheduler:** a single `time.Ticker` in the dhcp package, owned by
`Manager` alongside the existing poll loop. The sweep goroutine is
launched from `Manager.Start()` only when `arp_check.enabled=true`
and a connector is configured. `Manager.Shutdown()` stops both loops.

**No new bbolt buckets / no Raft commands.** Anomalies are surfaced
via the existing `m.anomalies` in-memory map and (when M6.5 lease
replication lands separately in TS-LeaseReplication) replicated through
the same path as M3.6 anomalies. The kernel observation itself is
ephemeral per-node — by design, see § Non-goals.

**Shared anomaly storage:** the four new kinds reuse the existing
`Anomaly` struct in `lease.go`. `details` is populated with the new
fields (`mac_kernel`, `kernel_state`) — no schema change needed; the
M3.6 struct already has a `details map[string]string` slot.

## Metrics

Four new series in the `skoed_dhcp_arp_*` family (added to
`prometheus-metrics.md` § DHCP):

| Series                                             | Type    | Labels                            |
|----------------------------------------------------|---------|-----------------------------------|
| `skoed_dhcp_arp_sweeps_total`                      | counter | `result=ok\|skipped\|error`       |
| `skoed_dhcp_arp_anomalies_detected_total`          | counter | `kind=arp_mac_mismatch\|ndp_mac_mismatch\|ghost_lease\|unseen_by_kernel` |
| `skoed_dhcp_arp_kernel_entries`                    | gauge   | `family=v4\|v6`                   |
| `skoed_dhcp_arp_netlink_unavailable`               | gauge   | —                                 |

### Cardinality

- `skoed_dhcp_arp_sweeps_total`: 3 series (one per `result` value)
- `skoed_dhcp_arp_anomalies_detected_total`: 4 series (one per kind)
- `skoed_dhcp_arp_kernel_entries`: 2 series (v4, v6)
- `skoed_dhcp_arp_netlink_unavailable`: 1 series

**Total: 10 series per node**, bounded by the enums. No client IP, no
MAC, no hostname — all the high-cardinality identifiers stay in the
anomaly objects (served via the JSON API) and never reach metrics.
This is the same discipline as M3.6's `skoed_dhcp_anomalies_open`.

## Posture

### AuthN/AuthZ

- `GET /api/v1/clients/{ip}/arp-state` — requires bearer token (same
  middleware as M3.6's `GET /api/v1/clients/{ip}`). No public surface.
- `GET /api/v1/clients/anomalies` — unchanged from M3.6 (authenticated).

### Audit

The endpoint is **read-only** and **per-IP**. It is **not** added to
the M5.2 audit middleware (matching the M3.6 `clients/{ip}` exemption
in `audit_middleware.go` — read-only client lookups are not audited).

The four new anomaly kinds **are** audited the same way M3.6 anomalies
are: when `recordAnomaly` writes a new entry, the audit middleware
emits an `anomaly.detected` event with `kind`, `ip`, and the redacted
MAC pair. Acknowledgements continue to flow through the existing
`POST /api/v1/clients/anomalies/{id}/acknowledge` route which is already
audited as `anomaly.acknowledge`.

### PII

The endpoint exposes MAC addresses (existing M3.6 surface — no new
PII class). Hostnames are not added to this endpoint's response
(unlike `GET /api/v1/clients/{ip}`); the operator who already has the
hostname can look it up via the existing M3.6 route.

The structured-log events (`netlink_unavailable`, `arp_sweep_skipped`,
`arp_sweep_error`) do **not** include MAC addresses or IPs — they
describe sweep-loop health, not per-lease state. Per-lease anomaly
logs continue to use the M3.6 redaction policy.

### SSRF / external probe surface

None. The sweep talks only to the local kernel via netlink — there
is no outbound network call, no DNS lookup, no fetch from any URL.
Compare with M5.9 URL-tester: the only attack surface here is a
caller hitting `GET /api/v1/clients/{ip}/arp-state` for many `ip`
values to enumerate leases; that's already covered by the M3.6
bearer-token requirement on `clients/*` routes.

### Netlink capability requirements

| Capability     | Required for                                       | Behavior if missing                                                                 |
|----------------|----------------------------------------------------|-------------------------------------------------------------------------------------|
| `CAP_NET_ADMIN`| Opening the netlink socket and dumping neighbours  | Sweep disables itself, logs once, retries hourly. API returns `netlink_unavailable`.|
| `CAP_NET_RAW`  | Not required (we never send packets)               | n/a                                                                                 |

Container deployments that want this signal must either:
- run with `--cap-add NET_ADMIN` (docker / podman), or
- include `NET_ADMIN` in `securityContext.capabilities.add` (k8s).

The default Helm chart ships **without** these capabilities — operators
opt in via `values.yaml` (`security.netlink.enabled=true`). This
matches our existing posture of "no privilege we don't need".

### Failure mode summary

| Failure                       | Effect                                                | Recovery                                |
|-------------------------------|-------------------------------------------------------|-----------------------------------------|
| No CAP_NET_ADMIN              | Sweep disabled, `kernel_state="netlink_unavailable"` | Grant cap; sweep auto-re-probes hourly  |
| Transient netlink ENOBUFS     | One sweep skipped, log `arp_sweep_error`              | Next sweep tick                         |
| Sweep overlaps (slow netlink) | `arp_sweep_skipped` (1/min log)                       | Tune `sweep_interval` upward            |
| Quiet subnet, stale ARP       | May produce `unseen_by_kernel` false positive         | Operator tunes `unseen_grace` upward    |
| Kernel mac cache eviction     | `ghost_lease` may flap after 24 h quiet               | Documented limitation; not a bug        |

## Non-goals (echo functional spec § Non-goals)

- **Active mitigation.** No gratuitous-ARP, no RA-guard, no port
  shutdown. Alert only.
- **Cross-node ARP correlation.** Each node cross-checks its own
  kernel only; followers do NOT probe the leader's link or vice
  versa. This means a 3-node cluster will report three independent
  `arp_mac_mismatch` events for the same spoofer if all three nodes
  share a broadcast domain — which is the right behaviour (each is
  an independent witness).
- **ARP table seeding / poisoning.** skoed never writes the neighbour
  table.
- **Layer-2 switch CAM-table queries** (SNMP / LLDP). Out of scope.
- **Per-anomaly confidence scoring.** Kinds are binary, matching the
  M3.6 detector's semantics.
- **Replacing the M3.6 lease-history detector.** This is an
  *additional* signal layered on top; the M3.6 detector continues to
  run unchanged.

## Implementation map

```
apps/skoed/internal/dhcp/
  arp_check.go          (new: sweep loop + cross_check decision tree)
  arp_netlink_linux.go  (new: build-tagged; vishvananda/netlink dump wrapper)
  arp_netlink_stub.go   (new: build-tagged !linux; always returns netlink_unavailable)
  lease.go              (extend: 4 new AnomalyKind constants; details map fields)
  manager.go            (extend: launch arp_check goroutine in Start(); shutdown in Shutdown())
apps/skoed/internal/api/
  app.go                (route: GET /api/v1/clients/{ip}/arp-state)
  audit_middleware.go   (no change — read-only route, no audit)
apps/skoed/internal/api/handlers/
  client_arp_state.go   (new: GetClientArpState)
apps/skoed/internal/config/
  config.go             (extend: node.dhcp.arp_check block with defaults + validation)
apps/skoed/internal/metrics/
  metrics.go            (extend: 4 new series; ObserveArpSweep, ObserveArpAnomaly)
specs/technical/
  prometheus-metrics.md (extend § DHCP table with the 4 new series)
tests/acceptance/
  test_dhcp_arp_check_test.go  (all 10 FSIDs; uses a fake-netlink injector)
decisions/
  20260608-NetlinkLibrary.md   (vishvananda/netlink choice rationale)
```
