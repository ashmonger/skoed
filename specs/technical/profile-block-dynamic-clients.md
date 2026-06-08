---
x-tsid: TS-BlockDyn
x-fsid-links:
  - FS-BlockDynPureBlockDynamicProfileMatchesAllDynamicClients
  - FS-BlockDynMixedCriteriaIsOrNotAnd
  - FS-BlockDynEmptyMatchSetIsFine
  - FS-BlockDynRejectedOnDefaultProfile
  - FS-BlockDynRouterAdvertisedAndManualAdminCountAsNotDynamic
---

# TS-BlockDyn — block-dynamic-clients profile rule

## Premise

A single boolean knob on `Profile` turns the rule on:

```go
type Profile struct {
    // ... existing M3.6 fields (ID, Name, Blocklists, Allowlist,
    //     SafeSearch, ClientIPs, ClientCIDRs, ClientIDs,
    //     ClientMACs, ClientHostnames) ...

    // M6.5: when true, every client whose Lease.Origin is exactly
    // "dhcp_dynamic" matches this profile. Composes as OR with the
    // existing client_* matchers. Rejected on the "default" profile —
    // see Validation below.
    BlockDynamicClients bool `yaml:"block_dynamic_clients,omitempty" json:"block_dynamic_clients,omitempty"`
}
```

The semantics are deliberately narrow: only the literal string
`"dhcp_dynamic"` triggers the match. `"dhcp_static"`,
`"router_advertised"`, `"manual_admin"`, the empty/unknown string, and
"no lease at all" are all treated as **not dynamic**. This is the
conservative reading FS-BlockDynRouterAdvertisedAndManualAdminCountAsNotDynamic,
FS-BlockDynUnknownClientIsNotDynamic, and
FS-BlockDynUnknownOriginTreatedAsNotDynamic all lock in.

`Lease.Origin` and `Lease.OriginConfidence` are owned by TS-LeaseOrigin;
this spec consumes them read-only.

## Profile resolution — where the rule plugs in

`filter.Engine.profilesMatchingLockedWithIdentity` (M3.6) already does
the 4-tier walk:

1. Client-ID (exact) — returns a singleton if matched, else falls through.
2. MAC (exact) — same.
3. Hostname (exact) — same.
4. IP / CIDR — returns the union of every matching profile.

M6.5 keeps that priority untouched. `block_dynamic_clients` joins the
**tier-4 union** as a sibling matcher:

```
tier4 = profilesByExactIP(ip)
      ∪ profilesByCIDRContains(ip)
      ∪ profilesByBlockDynamicClients(leaseOrigin(ip))
```

Concretely: at tier 4, after computing the IP/CIDR-matched set, the
engine iterates every profile with `BlockDynamicClients=true` and adds
it to the set when the resolved lease's `Origin == "dhcp_dynamic"`. If
tiers 1–3 already produced a match, the engine returns early and
**never consults** `BlockDynamicClients` — that's the property
FS-BlockDynPriorityHigherTierStillWins enforces.

Lookup table (effective profile set, given a dynamic-lease client at
192.168.1.77):

| Profile state                                                 | In set? | Why                                       |
|---------------------------------------------------------------|---------|-------------------------------------------|
| no `block_dynamic_clients`, no client_*                       | no      | nothing matches                           |
| `block_dynamic_clients=true`, no client_*                     | yes     | tier-4 union — dynamic origin matches     |
| `block_dynamic_clients=true`, `client_ips=[other.ip]`         | yes     | OR with the IP rule                       |
| `block_dynamic_clients=false`, `client_ips=[192.168.1.77]`    | yes     | tier-4 union — IP matches                 |
| `block_dynamic_clients=true`, profile id = "default"          | n/a     | rejected at validation; cannot be stored  |

## Lease lookup — single call site

The engine queries the existing `dhcp.Manager.LookupByIP(ip)` accessor.
No new manager API. The engine reads `lease.Origin` and treats `(lease,
ok==false)` and `lease.Origin != "dhcp_dynamic"` identically — both
mean "not a dynamic-lease client". This is how
FS-BlockDynUnknownClientIsNotDynamic and
FS-BlockDynUnknownOriginTreatedAsNotDynamic share one code path.

The engine does **not** call the connector directly, does not hold a
reference to bbolt, and does not look at `OriginConfidence`. Confidence
is presentational only; for the filter rule, "the only thing that
matches is the literal `dhcp_dynamic`" is the whole spec.

## Validation rules (POST / PATCH /api/v1/profiles)

Single new rule, enforced in the API handler before the
`profile.upsert` FSM command is issued:

```
if body.id == "default" && body.block_dynamic_clients == true {
    400 {"error":"the default profile cannot set block_dynamic_clients
                  — create a dedicated profile (e.g. \"untrusted\") for
                  this rule instead"}
}
```

PATCHes that omit the field don't change it. PATCHes that explicitly
set `"block_dynamic_clients": false` on the default profile are
accepted (no-op, idempotent). FS-BlockDynRejectedOnDefaultProfile is
the contract.

Every other profile (including freshly-created ones) may freely set
the field on or off. A profile that sets ONLY this field is valid;
FS-BlockDynEmptyMatchSetIsFine and
FS-BlockDynPureBlockDynamicProfileMatchesAllDynamicClients both rely
on that.

## API surface

```yaml
# additions to specs/technical/management-api.openapi.yaml

components:
  schemas:
    Profile:
      type: object
      properties:
        # ...existing fields...
        block_dynamic_clients:
          type: boolean
          default: false
          description: |
            When true, this profile matches any client whose DHCP
            lease has origin exactly "dhcp_dynamic". Composes (OR)
            with the client_ips / client_cidrs / client_ids /
            client_macs / client_hostnames matchers. The "default"
            profile is rejected with 400 if this is set true —
            create a dedicated profile instead.

    ClientDetail:                 # body returned by GET /clients/{ip}
      type: object
      properties:
        ip:           { type: string }
        mac:          { type: string }
        hostname:     { type: string }
        client_id:    { type: string }
        origin:
          type: string
          enum: ["dhcp_static","dhcp_dynamic","router_advertised","manual_admin",""]
          description: forwarded from Lease.Origin; empty when no lease or origin not reported
        profile_ids:
          type: array
          items: { type: string }
          description: |
            every profile that currently matches this client, in
            priority-resolved form. When BlockDynamicClients caused
            the match, "untrusted" (or whatever the operator named
            it) appears here.

paths:
  /api/v1/profiles:
    post:
      requestBody:
        content:
          application/json:
            schema: { $ref: '#/components/schemas/Profile' }
      responses:
        "201": { description: created }
        "400":
          description: |
            Validation failure. Notable case:
            `block_dynamic_clients=true` on profile id "default".
          content:
            application/json:
              schema:
                type: object
                properties:
                  error: { type: string }

  /api/v1/profiles/{id}:
    patch:
      parameters:
        - in: path
          name: id
          schema: { type: string }
          required: true
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                block_dynamic_clients: { type: boolean }
                # ...other patchable fields...
      responses:
        "200": { description: updated }
        "400": { description: validation failure (see POST) }

  /api/v1/clients/{ip}:
    get:
      parameters:
        - in: path
          name: ip
          schema: { type: string }
          required: true
      responses:
        "200":
          content:
            application/json:
              schema: { $ref: '#/components/schemas/ClientDetail' }
```

`GET /api/v1/clients/{ip}` already exists (M3.6) — the schema gains
`origin` (from TS-LeaseOrigin) and the `profile_ids` array now
includes block-dynamic-matched profiles. That's the round-trip
FS-BlockDynClientLookupSurfacesMatchedProfile asserts.

## Raft / FSM impact

No new command kind. The existing `profile.upsert` and `profile.delete`
commands carry the full `Profile` payload (JSON-encoded), so adding
`BlockDynamicClients` to the struct flows through transparently:

| Command          | Payload change                                             |
|------------------|------------------------------------------------------------|
| `profile.upsert` | extra optional `block_dynamic_clients: bool` field         |
| `profile.delete` | unchanged                                                  |

### Wire compatibility

- **New node → old node** (rolling upgrade, leader is new): old
  follower deserialises `Profile` with `encoding/json` standard
  decoder. Unknown field `block_dynamic_clients` is silently dropped.
  The follower keeps an in-memory `Profile` with the field unset
  (`false`), so its filter engine just doesn't apply the rule until
  the follower itself is upgraded. Worst case: a few seconds of
  inconsistent matching during the rollout window; spec accepts this
  (the operator is already mid-upgrade).
- **Old node → new node**: new node decodes the wire format with the
  field defaulting to `false` — identical to "rule disabled".
  Backward compatible.

No `SchemaVersion` bump is required by this change alone (the field is
additive and optional). TS-LeaseOrigin bumps `SchemaVersion` for its
`Origin`/`OriginConfidence` additions; this spec piggybacks on that
bump and asserts no additional migration.

### bbolt keys

| Bucket            | Key                         | Value                                  |
|-------------------|-----------------------------|----------------------------------------|
| `config_profiles` | `<profile_id>` (existing)   | JSON of the updated `Profile` struct   |

No new buckets. No new keys. Storage growth per profile is exactly one
extra boolean field in the JSON-encoded body (≤ 28 bytes:
`,"block_dynamic_clients":true`). With a realistic upper bound of
~100 profiles per cluster, that's ≤ 3 KB additional bucket footprint —
noise.

### Scheduler jobs

None. The rule is evaluated per-query in-line with profile resolution;
there is no background scan, no precomputed match set, and no
debouncer. `dhcp.Manager`'s existing 60s poll loop (TS-DhcpConnectors)
is the only source of `Lease.Origin` freshness — when a lease flips
from `dhcp_static` to `dhcp_dynamic` (e.g., reservation removed in
Kea), the next poll tick rebuilds `m.byIP` and the very next DNS query
sees the new origin. No additional scheduler work.

## Metrics

Single new counter, low cardinality, registered once at startup:

```
skoed_profile_block_dynamic_matches_total{profile_id="<id>"}
```

- **Label cardinality bound**: one series per profile that has
  `block_dynamic_clients=true`. Operator-controlled, capped in
  practice by the per-cluster profile count (typical ~5–20, hard
  ceiling ~100). No high-cardinality labels (no `client_ip`, no
  `domain`).
- **When incremented**: each DNS query whose final
  effective-profile set was extended by the block-dynamic rule (i.e.,
  the tier-4 union grew because of this rule). Counted once per
  query, even if multiple block-dynamic profiles matched.
- **What it answers**: "is the untrusted profile actually catching
  anyone today, or is the rule dormant?" — a flat counter at zero
  for a week is the operator's signal to verify the connector is
  reporting `Origin` at all.

Existing series `skoed_dhcp_leases_total{source,origin}` (owned by
TS-LeaseOrigin) already breaks down lease populations by origin, so
this spec does not duplicate that view.

## Posture

### Authentication
- All profile mutation endpoints (`POST`, `PATCH`, `DELETE /api/v1/profiles[/...]`)
  require an authenticated admin token, identical to the M3 profiles
  baseline. No public/guest exposure.
- `GET /api/v1/clients/{ip}` requires the same admin auth as the
  M3.6 endpoint it extends.

### Audit
- `profile.upsert` and `profile.delete` are already routed through
  the M5.2 audit middleware as state-changing operations. The new
  `block_dynamic_clients` field is included in the JSON payload the
  audit log records — operators can see "alice@host enabled
  block_dynamic_clients on profile 'untrusted' at T".
- Per-query matches do NOT emit an audit event. The audit log is for
  configuration changes, not per-query traces (that's the query log,
  which already records `profile_id` and `blocklist_id` per query —
  FS-BlockDynPureBlockDynamicProfileMatchesAllDynamicClients reads
  back from there).

### Metrics privacy
- The single counter carries `profile_id` only (operator-named, not
  user PII). No client IP, no MAC, no DUID, no hostname surfaces in
  Prometheus output via this rule.

### SSRF / PII
- No outbound HTTP fetches introduced by this rule. The engine reads
  in-memory lease state from `dhcp.Manager`; the manager's connector
  is the one network actor and is owned by TS-DhcpConnectors with
  its own SSRF posture (allow-list of connector URLs, no
  caller-controlled URLs).
- The Clients endpoint already exposes `mac`, `hostname`, `client_id`
  to authenticated admins (M3.6). Adding `origin` reveals only the
  connector's static-vs-dynamic classification — no new PII surface.

### Netlink capability
- **Not applicable to this rule.** TS-BlockDyn does not touch the
  kernel ARP/NDP tables; that's TS-ArpCrossCheck's responsibility.
  The block-dynamic rule reads only the DHCP-reported `Origin`
  field, which the DHCP connector populates from a userspace source
  (Kea control-agent JSON, dnsmasq leases file, or HTTP-JSON
  connector). No CAP_NET_ADMIN is required, and the rule continues
  to function on hosts where the ARP cross-check probe has degraded
  to `netlink_unavailable`.

### Failure modes
- **Connector down**: `dhcp.Manager` keeps its prior snapshot
  (TS-DhcpConnectors resilience rule). Existing leases retain their
  `Origin` value, so the rule keeps matching correctly until the
  cached entries expire (`Lease.ExpiresAt`). After expiry, the
  manager drops them and the engine starts treating the IP as "no
  lease" — the conservative path (not dynamic, falls through to
  default profile). No false positives.
- **Connector reports empty `Origin`**: rule does NOT match. See
  FS-BlockDynUnknownOriginTreatedAsNotDynamic. Operator can grep
  `skoed_dhcp_leases_total{origin=""}` to spot this.
- **Profile config corrupt**: the M2 FSM validation gate rejects the
  command before it lands in bbolt; the broken state never reaches
  the engine.

## Implementation map

```
apps/skoed/internal/config/
  config.go              (add BlockDynamicClients bool to Profile)
apps/skoed/internal/cluster/
  commands.go            (no kind change; payload is auto-encoded)
apps/skoed/internal/api/handlers/
  profiles.go            (validation: reject default + block_dynamic=true)
  clients.go             (extend GET /clients/{ip} response with origin)
apps/skoed/internal/filter/
  engine.go              (extend profilesMatchingLockedWithIdentity
                          tier-4 union with the block-dynamic rule;
                          consult dhcp.Manager.LookupByIP for origin)
apps/skoed/internal/metrics/
  metrics.go             (register skoed_profile_block_dynamic_matches_total
                          + ObserveBlockDynamicMatch(profileID))
web/src/views/
  Profiles.vue           (add a "Block dynamic clients" checkbox to the
                          profile editor; disable + warn on the default
                          profile row)
tests/acceptance/
  profile_block_dynamic_test.go  (all 10 FSIDs)
```

## Non-goals (explicit)

- **DUID-based matching** — the M6.5 DHCPv6 spec keeps DUID
  observational. This rule does not branch on DUID and does not
  consult `Lease.DUID`.
- **Per-blocklist application of the rule** — the boolean lives on
  the profile, not on the (profile, blocklist) pair. When the rule
  matches, all of the profile's blocklists apply to the dynamic
  client, identical to any other matcher in the M3.6 set.
- **Time-bounded variants** ("block dynamic clients only during
  bedtime") — combine this rule with the existing M3 schedule-rules
  surface; no new schedule semantics in this spec.
- **An inverse `block_static_clients` rule** — not requested; the
  trust model assumes static = trusted. Adding the inverse would
  invert it and is out of scope.
- **Auto-creating an "untrusted" profile on first boot** — operators
  configure it explicitly. The default profile bootstrap
  (TS-ProfilesAndSchedules) is unchanged.
- **Dashboard alerting when a brand-new dynamic client first matches
  the rule** — that's the M3.6 anomaly surface's job (see
  TS-DhcpSpoofDetection); this rule is filtering only.
