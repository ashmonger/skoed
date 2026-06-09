---
x-tsid: TS-Dhcpv6Lease
x-fsid-links:
  - FS-Dhcpv6LeaseKeaReadsLease6
  - FS-Dhcpv6LeaseKeaMergesIaNaAndIaPd
  - FS-Dhcpv6LeaseDnsmasqParsesLease6File
  - FS-Dhcpv6LeaseDnsmasqSkipsExpired
  - FS-Dhcpv6LeaseDualStackMerge
---

# TS-Dhcpv6Lease — DHCPv6 lease parsing (Kea + dnsmasq)

## What changes vs M3.6

M3.6 shipped IPv4-only lease ingestion (Kea `lease4-get-all`,
dnsmasq `/var/lib/misc/dnsmasq.leases`, generic `http_json`). M6.5
adds **read-only** DHCPv6 ingestion to the same two real-world
connectors, plus a dual-stack merge step that collapses one device's
v4 and v6 records into a single `Lease`.

DUID is **observational only** at M6.5 — it travels through the cache
and is rendered by the SPA, but profile matching priority
(`ClientID > MAC > Hostname > IP/CIDR`) is unchanged. A future
milestone may add `client_duids` to the Profile schema; not in scope
here.

## Lease struct — additive, wire-compatible

The canonical `dhcp.Lease` gains three optional fields. All carry
`omitempty` so an old M3.6 follower deserializing a new leader's
JSON ignores them, and a new follower receiving an old leader's JSON
sees the Go zero value (nil slice, empty string, false) — which is
exactly the "no v6 known for this client" semantics.

```go
type Lease struct {
    // Existing M3.6 fields — unchanged.
    IP        string    `json:"ip"`
    MAC       string    `json:"mac"`
    Hostname  string    `json:"hostname"`
    ClientID  string    `json:"client_id"`
    Source    string    `json:"source"`
    ExpiresAt time.Time `json:"expires_at"`

    // M6.5 additions — all optional, all backwards-safe.
    IPv6Addresses []string `json:"ipv6_addresses,omitempty"`
    DUID          string   `json:"duid,omitempty"`
    IsDualStack   bool     `json:"is_dual_stack,omitempty"`
}
```

Invariants:

- `IPv6Addresses` is never `nil` for v6-only leases (always at least
  one element). For v4-only leases it is either absent (`nil`) or
  empty `[]`.
- `IPv6Addresses` may carry IA_PD prefixes (CIDR form like
  `2001:db8:abcd::/56`); consumers must accept both literal addresses
  and CIDR notation.
- `IsDualStack` is set during the merge step (see below), never by a
  connector directly. A connector returning only v6 records always
  emits `IsDualStack=false`.
- `IP` may be empty for v6-only leases. The Manager's snapshot indexes
  v6-only leases under their first `IPv6Addresses` entry — see
  "Snapshot indexing" below.

Versioning: no Raft schema bump. The Lease is not currently
Raft-replicated at M6.5 (still node-local polling). Sibling spec
TS-LeaseReplication will introduce replication and define the
command format that carries this struct; both specs land in M6.5 and
must agree on the field set above.

## Connector changes

### Kea — `lease6-get-all`

The `keaConn` gains a second poll per refresh cycle. After the
existing `lease4-get-all` call returns, the connector issues:

```json
{"command":"lease6-get-all","service":["dhcp6"]}
```

The control-agent response shape is the same envelope as v4 but the
inner lease record differs:

```go
type keaLease6 struct {
    IPAddress string `json:"ip-address"` // IA_NA address OR IA_PD prefix
    Type      string `json:"type"`       // "IA_NA" | "IA_PD" | "IA_TA"
    DUID      string `json:"duid"`
    Hostname  string `json:"hostname"`
    HWAddress string `json:"hw-address"` // may be empty for v6-only
    ValidLft  int64  `json:"valid-lft"`
    Cltt      int64  `json:"cltt"`
    PrefLen   int    `json:"prefix-len,omitempty"` // IA_PD only
}
```

For `IA_PD`, the connector renders `IPAddress + "/" + PrefLen` into
the `IPv6Addresses` slice. The IA_NA address is appended as-is.
Multiple records sharing the same DUID merge into one `Lease`
in-connector (see "Merge step" below). `lease6-get-all` failures are
logged at WARN and the v4 result is still returned — partial-success
is preferable to dropping the whole poll.

The combined v4+v6 poll fits inside the existing 5s `http.Client`
timeout for typical home/SMB Kea deployments (hundreds of leases);
extreme catalogues would already have hit timeout on v4 alone.

### dnsmasq — `/var/lib/misc/dnsmasq.leases6`

A new optional config field `FilePathV6` (defaults to
`<FilePath>6` when unset) points at the DHCPv6 lease file. dnsmasq
writes one line per IA, whitespace-separated:

```
<expiry-epoch> <iaid> <ipv6-or-prefix> <hostname> <duid>
```

Behaviour matches M3.6 v4:

- `<5` fields → log WARN once per poll, skip the line, continue.
- `expiry-epoch < now` → drop the lease.
- `hostname == "*"` → empty hostname (uses the same `normalize`
  path).
- The same `<iaid>` may appear twice (IA_NA + IA_PD); both addresses
  go into the same `Lease.IPv6Addresses` via the merge step keyed on
  DUID.

If `FilePathV6` is set but the file does not exist, the connector
logs `dnsmasq v6 file not present: <path>` at INFO **once per
process** (suppressed thereafter via `sync.Once`) and continues with
v4-only results. This is the most common misconfiguration and we
refuse to log-spam it.

### http_json — passthrough

The generic connector gains `ipv6_addresses` and `duid` fields on
the wire shape:

```go
type httpJSONLease struct {
    IP            string   `json:"ip"`
    MAC           string   `json:"mac"`
    Hostname      string   `json:"hostname"`
    ClientID      string   `json:"client_id"`
    ExpiresAt     string   `json:"expires_at"`     // RFC3339
    IPv6Addresses []string `json:"ipv6_addresses"` // M6.5
    DUID          string   `json:"duid"`           // M6.5
}
```

Missing fields are treated as empty — no validation beyond "if
`ipv6_addresses` is set, every element must parse as `net.IP` or a
CIDR".

## Merge step (dual-stack collapse)

After each connector's `Fetch()` returns, the Manager runs a
deterministic merge over the combined v4+v6 lease set. Strategy:

1. Build two maps: `byClientID[ClientID]` (v4 records) and
   `byDUID[DUID]` (v6 records).
2. For each v6 record, attempt to find a matching v4 record using
   this heuristic ladder, first hit wins:
   - **DUID-LLT/LL MAC suffix**: if the DUID ends in a 6-byte MAC
     that equals an existing v4 `Lease.MAC`, merge.
   - **Hostname equality**: case-insensitive non-empty hostname
     match.
   - **No match**: emit the v6 record as a stand-alone v6-only
     `Lease` (empty `IP`, empty `MAC`).
3. When a merge happens:
   - The v4 record absorbs `IPv6Addresses` (deduped, sorted
     lexicographically for stable diffing).
   - The v4 record absorbs `DUID`.
   - `IsDualStack = true` on the merged record.
   - The v6 record is dropped from the output set.
4. Merge is **read-only** w.r.t. the connector — connectors return
   the raw record set; the Manager owns the merge.

The merge is `O(n_v4 + n_v6)` with two hash lookups per v6 record,
which keeps poll-time work proportional to lease count.

## Snapshot indexing

`Manager.byIP` currently maps `string → Lease`. M6.5 keeps the same
map but extends the indexing rule:

- v4 lease → indexed by `Lease.IP`.
- v6-only lease (empty `Lease.IP`) → indexed by
  `Lease.IPv6Addresses[0]` (after sort; the lexicographically
  smallest address).
- Dual-stack lease → indexed by `Lease.IP` (v4). The Manager also
  maintains a `byV6 map[string]*Lease` so that
  `GET /api/v1/clients/2001:db8::10` resolves the same `Lease`.

`LookupByIP` accepts both v4 and v6 string literals; it tries the
v4 map first, then the v6 map. The two maps share `*Lease`
pointers (never two copies), so an update applied through the v4
key is observable via the v6 key without extra wiring.

## HTTP contracts

### `GET /api/v1/clients/{ip}` — v4 or v6 literal

The path parameter accepts either form. Routing must use a wildcard
that tolerates the colons in IPv6 literals (the existing
`gorilla/mux` `{ip:.*}` style is sufficient; `{ip}` with default
slash separator is fine because we have no further path segments).

```yaml
paths:
  /api/v1/clients/{ip}:
    get:
      summary: Lookup a client by IPv4 or IPv6 literal
      parameters:
        - in: path
          name: ip
          required: true
          schema: { type: string }
          description: IPv4 dotted-quad or IPv6 literal (no brackets).
      responses:
        '200':
          description: Lease record (M3.6 shape + optional M6.5 fields)
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Lease'
        '404':
          description: No lease for that address.
components:
  schemas:
    Lease:
      type: object
      required: [source]
      properties:
        ip:             { type: string, description: "Empty for v6-only leases." }
        mac:            { type: string }
        hostname:       { type: string }
        client_id:      { type: string }
        source:         { type: string, enum: [kea, dnsmasq, http_json] }
        expires_at:     { type: string, format: date-time }
        ipv6_addresses:
          type: array
          items: { type: string }
          description: "IA_NA addresses and IA_PD prefixes. Omitted when none."
        duid:
          type: string
          description: "DHCPv6 client DUID. Observational only at M6.5."
        is_dual_stack:
          type: boolean
          description: "True when this client has both v4 and v6 leases."
```

### `GET /api/v1/clients` — list shape unchanged

The list endpoint returns the merged set. Operators with no v6
source configured continue to see the M3.6 wire shape because all
three new fields are `omitempty`. FS-Dhcpv6LeaseV6DisabledLegacyShapeUnchanged
asserts this contract.

## Raft commands / bbolt keys

**None at M6.5.** Lease state remains node-local in this milestone.
The sibling spec TS-LeaseReplication will introduce a
`CmdLeasesReplace` command carrying `[]Lease` payloads — the field
additions above are pre-positioned for that wire format.

There are no new bbolt buckets.

## Scheduler jobs

The existing Manager poll loop covers both v4 and v6. No new
scheduled job is introduced. The `RefreshSeconds` config applies to
the combined cycle (v4 fetch + v6 fetch + merge), so operators
should not need to retune intervals when enabling v6.

## Metrics

Two existing series gain a label value; one new series is added.

| Series                              | Type    | Labels             | M6.5 change                                              |
|-------------------------------------|---------|--------------------|----------------------------------------------------------|
| `skoed_dhcp_leases`                 | gauge   | source             | unchanged (counts merged Leases)                         |
| `skoed_dhcp_leases_v6`              | gauge   | source             | **new** — counts Leases with non-empty `IPv6Addresses`   |
| `skoed_dhcp_leases_dual_stack`      | gauge   | —                  | **new** — counts Leases with `IsDualStack=true`          |
| `skoed_dhcp_poll_errors_total`      | counter | source, family     | label `family` ∈ {`v4`, `v6`} **added**                  |

### Cardinality bounds

- `source` ∈ {`kea`, `dnsmasq`, `http_json`} — bounded set, max 3.
- `family` ∈ {`v4`, `v6`} — bounded, max 2.
- `skoed_dhcp_leases_v6` series: max 3 (one per source).
- `skoed_dhcp_leases_dual_stack` series: 1.
- `skoed_dhcp_poll_errors_total` series: max 6 (3 × 2).

Total net new series ≤ 7. No domain, IP, MAC, hostname, or DUID is
ever used as a label — those are unbounded user data.

## Posture

### AuthN/AuthZ

- `GET /api/v1/clients/{ip}` and `GET /api/v1/clients` already
  require admin auth (M3.6). DHCPv6 changes do not alter the auth
  policy: DUID + v6 addresses are admin-visible only.
- The public landing endpoints (M5.9.5 / TS-DomainTester) are
  unchanged and remain unaware of DUID.

### Audit

- Lease ingestion is not audited (read-only, polled). Same policy
  as M3.6 — adding 6× more lines per poll because of v6 would only
  bloat the audit log.
- The Anomaly subsystem (M3.6) remains v4-keyed at M6.5; v6-only
  anomalies are out of scope and explicitly belong to the sibling
  spec TS-DhcpArpCrossCheck (which gains v6 NDP coverage).

### PII

- DUID can encode the device's MAC (DUID-LL, DUID-LLT). It is
  therefore **as sensitive as MAC**. Treat it equivalently in logs,
  exports, and the audit redaction layer. The audit middleware's
  existing MAC-redaction allowlist gains `duid` as a sibling field.
- `GET /api/v1/clients` body is admin-only and exempt from the
  redaction filter (same as MAC today).

### SSRF

- Kea connector targets the operator-configured control-agent URL
  only. No user-supplied input flows into the Kea HTTP request.
  Adding `lease6-get-all` does not change the target host or
  scheme — same URL, second POST body.
- http_json connector unchanged. The new `ipv6_addresses` field is
  parsed as data, not used to issue further requests.
- No new outbound calls. No new SSRF surface.

### Netlink capability

Not applicable to this spec. ARP/NDP cross-check (which **does**
need `CAP_NET_ADMIN`) lives in the sibling TS-DhcpArpCrossCheck
spec. DHCPv6 lease parsing reads only:

- a control-agent HTTP endpoint (Kea),
- a lease file on local disk (dnsmasq),
- a generic HTTP endpoint (http_json).

None require elevated capabilities.

### Failure modes

| Failure                                  | Behaviour                                                                |
|------------------------------------------|--------------------------------------------------------------------------|
| Kea `lease6-get-all` returns 500/timeout | log WARN with source label, keep v4 result, increment v6 error counter   |
| dnsmasq `.leases6` file missing          | INFO once per process, continue with v4-only, no metric increment        |
| dnsmasq `.leases6` file malformed line   | WARN once per poll, skip the line, parse the rest                        |
| Merge ambiguity (two v4 candidates)      | first deterministic hit wins (hostname comparison is lexical), log DEBUG |
| Expired v6 lease                         | dropped during parse (same path as v4)                                   |

The Manager already keeps the prior snapshot on a fetch error
(M3.6 behaviour). v6 errors are wrapped in the same retry-quietly
contract — a flapping v6 source does not blank the v4 view.

## Implementation map

```
apps/skoed/internal/dhcp/
  lease.go        (extend: IPv6Addresses, DUID, IsDualStack fields + normalize())
  connectors.go   (extend: keaConn does lease6-get-all; dnsmasqConn reads FilePathV6;
                   httpJSONConn passes through new fields)
  manager.go      (new: mergeDualStack(); extend byIP indexing with byV6 map)
apps/skoed/internal/api/handlers/
  clients.go      (extend: LookupByIP accepts v6 literals via Manager.LookupByIP)
apps/skoed/internal/audit/
  redact.go       (extend: DUID joins MAC in the redaction allowlist)
apps/skoed/internal/metrics/
  metrics.go      (extend: register skoed_dhcp_leases_v6,
                   skoed_dhcp_leases_dual_stack, add family label to
                   poll_errors_total)
web/src/views/
  Clients.vue     (extend: IPv6 column + dual-stack chip)
tests/acceptance/
  dhcpv6_lease_test.go   (all FSIDs in this spec)
```
