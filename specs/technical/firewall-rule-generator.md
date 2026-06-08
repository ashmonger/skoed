---
x-tsid: TS-FwRule
x-fsid-links:
  - FS-FwRuleIptablesSubnetScope
  - FS-FwRuleNftablesSubnetScope
  - FS-FwRuleMikrotikSubnetScope
  - FS-FwRuleOpnsenseSubnetScope
  - FS-FwRuleUnifiSubnetScope
  - FS-FwRuleProfileScope
  - FS-FwRuleAllScope
  - FS-FwRuleRejectActionOptIn
  - FS-FwRuleRejectsUnknownPlatform
  - FS-FwRuleRejectsInvalidSubnet
  - FS-FwRuleRejectsUnknownProfile
  - FS-FwRuleRequiresAuth
  - FS-FwRuleHeaderCarriesSnapshotProvenance
  - FS-FwRuleStaleSnapshotStillServes
  - FS-FwRuleMetricsCounter
---

# TS-FwRule — paste-ready DoH/DoT block rules per platform

## One endpoint, five rendering backends

| Path                          | Auth | Method | Returns                  |
|-------------------------------|------|--------|--------------------------|
| `GET /api/v1/firewall-rules`  | yes  | GET    | `text/plain` rule blob   |

skoed never touches netfilter, nft, the MikroTik API, the OpnSense API,
or the UniFi controller. The endpoint is pure text rendering: it loads
the curated DoH/DoT resolver snapshot (see TS-DohResolverIpDatabase),
resolves the scope to a set of source addresses, and runs a per-platform
renderer that emits operator-pasteable text. The operator pastes; we
don't push.

The five rendering backends share a single in-memory model:

```
type rulePlan struct {
  scope        Scope            // all | subnet(CIDR) | profile(IDs→IPs)
  sources      []netip.Prefix   // resolved source set; empty == "any"
  resolvers    []resolverEntry  // {name, ipv4, ipv6} from snapshot
  action       Action           // drop (default) | reject
  snapshot     snapshotMeta     // id, fetched_at, stale flag, count
  generatedAt  time.Time
}
```

The renderer interface is one method, `Render(rulePlan) string`. Each
platform implementation lives next to the handler and is selected by a
small `map[platform]renderer` lookup.

## OpenAPI fragment

```yaml
paths:
  /api/v1/firewall-rules:
    get:
      operationId: generateFirewallRules
      summary: Emit a paste-ready rule blob blocking the curated DoH/DoT resolver IPs.
      security:
        - basicAuth: []
      parameters:
        - in: query
          name: platform
          required: true
          schema:
            type: string
            enum: [iptables, nftables, mikrotik, opnsense, unifi]
        - in: query
          name: scope
          required: true
          schema:
            type: string
            enum: [all, subnet, profile]
        - in: query
          name: subnet
          required: false
          description: Required when scope=subnet. IPv4 or IPv6 CIDR.
          schema:
            type: string
            example: "10.0.0.0/24"
        - in: query
          name: profile
          required: false
          description: Required when scope=profile. Profile id.
          schema:
            type: string
            example: "kids"
        - in: query
          name: action
          required: false
          schema:
            type: string
            enum: [drop, reject]
            default: drop
      responses:
        "200":
          description: Rule blob ready to paste into the operator's firewall.
          headers:
            X-Skoed-Snapshot-Id:
              schema: { type: string }
            X-Skoed-Snapshot-Fetched:
              schema: { type: string, format: date-time }
            X-Skoed-Snapshot-Stale:
              schema: { type: string, enum: ["true", "false"] }
          content:
            text/plain:
              schema:
                type: string
        "400":
          description: |
            Invalid input. Returned for unknown platform, missing/invalid
            subnet when scope=subnet, missing profile when scope=profile,
            or unknown action value.
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorEnvelope"
        "401":
          description: Missing or invalid Basic auth credentials.
        "404":
          description: |
            Profile id supplied with scope=profile does not exist in the
            current cluster config.
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorEnvelope"
        "503":
          description: |
            Resolver snapshot has never been fetched (cold cluster, no
            seed bundled, no successful refresh yet). Operator should
            POST /api/v1/doh-resolvers/refresh and retry.
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorEnvelope"
components:
  schemas:
    ErrorEnvelope:
      type: object
      required: [error]
      properties:
        error: { type: string }
        supported_platforms:
          type: array
          items: { type: string }
          description: Echoed back on "unsupported platform" only.
```

## Query parameter resolution

| `scope`   | Required also                  | Source set emitted in rules                 |
|-----------|--------------------------------|---------------------------------------------|
| `all`     | —                              | empty — rules apply to every source         |
| `subnet`  | `subnet=<CIDR>` (v4 or v6)     | the parsed `netip.Prefix`                   |
| `profile` | `profile=<id>`                 | every `client_ips` entry on that profile    |

Validation order (first failure short-circuits):

1. `platform` parses against the enum (else 400 `unsupported platform`,
   echoing supported list).
2. `action` parses if present (else 400 `unknown action`).
3. `scope` parses against the enum (else 400 `unknown scope`).
4. `subnet` parses with `netip.ParsePrefix` when `scope=subnet` (else
   400 `invalid subnet`). FSID: FS-FwRuleRejectsInvalidSubnet.
5. `profile` exists in the cached config snapshot when `scope=profile`
   (else 404 `profile not found`). FSID: FS-FwRuleRejectsUnknownProfile.
6. Resolver snapshot is loadable (else 503 `resolver snapshot unavailable`).

## Output anatomy (all platforms)

Every blob starts with a header comment block in the platform's native
comment syntax (`#` for iptables/nftables/mikrotik/opnsense, `//` inside
the JSON string-values for UniFi — UniFi emits a top-level `_comment`
key instead). The header carries the six fields FS-FwRuleHeaderCarriesSnapshotProvenance
requires:

```
# skoed firewall-rule generator
# snapshot_id:       <snapshot.ID>
# snapshot_fetched:  <snapshot.FetchedAt RFC3339>
# resolver_count:    <len(snapshot.Resolvers)>
# generated_at:      <time.Now().UTC() RFC3339>
# scope:             all | subnet=10.0.0.0/24 | profile=kids
# WARNING: snapshot is stale (older than 7d)    ← only when stale
```

The same fields are also emitted as `X-Skoed-Snapshot-*` response
headers so callers that strip comments can still read provenance.

### iptables (FS-FwRuleIptablesSubnetScope)

```
# resolver: cloudflare
-A FORWARD -s 10.0.0.0/24 -d 1.1.1.1 -j DROP
# resolver: google
-A FORWARD -s 10.0.0.0/24 -d 8.8.8.8 -j DROP
```

IPv6 resolvers are emitted as a separate `ip6tables` block below, with
a `# === ip6tables ===` divider. `scope=all` omits the `-s` clause
entirely (`-A FORWARD -d 1.1.1.1 -j DROP`). `action=reject` swaps the
target to `-j REJECT --reject-with icmp-admin-prohibited`
(`icmp6-adm-prohibited` for the v6 block). FSID: FS-FwRuleRejectActionOptIn.

### nftables (FS-FwRuleNftablesSubnetScope)

Single `table inet skoed_doh_gap` covering both address families via
inline sets:

```
table inet skoed_doh_gap {
  chain forward {
    type filter hook forward priority 0;
    ip  saddr 10.0.0.0/24 ip  daddr { 1.1.1.1, 8.8.8.8, 9.9.9.9 } drop
    ip6 saddr 10.0.0.0/24 ip6 daddr { 2606:4700:4700::1111, 2001:4860:4860::8888, 2620:fe::fe } drop
  }
}
```

`scope=all` drops the `saddr` clause. `scope=profile` expands to one
rule per source IP since nftables sets can't mix v4 and v6 sources.
`action=reject` swaps `drop` for `reject with icmpx admin-prohibited`.

### MikroTik (FS-FwRuleMikrotikSubnetScope)

One `/ip firewall filter add` per IPv4, one `/ipv6 firewall filter add`
per IPv6, each with a `comment="skoed doh-gap: <resolver>"`:

```
/ip firewall filter add chain=forward action=drop src-address=10.0.0.0/24 \
  dst-address=1.1.1.1 comment="skoed doh-gap: cloudflare"
```

`scope=all` omits `src-address`. `action=reject` sets
`action=reject reject-with=icmp-admin-prohibited`.

### OpnSense (FS-FwRuleOpnsenseSubnetScope)

Two artefacts in one blob, separated by `# ---`:

1. An importable alias snippet (XML-ish but consumable by the OpnSense
   "Firewall > Aliases > Import" form):

   ```
   skoed_doh_resolvers
     1.1.1.1
     8.8.8.8
     9.9.9.9
     2606:4700:4700::1111
     ...
   ```

2. A rule descriptor in the OpnSense UI's "add rule" form fields:

   ```
   Action:           Block
   Interface:        LAN  (operator picks)
   Direction:        out
   Source:           10.0.0.0/24
   Destination:      Single host or alias: skoed_doh_resolvers
   ```

The leading comment block documents the paste flow (FS-FwRuleOpnsenseSubnetScope
"the body's header documents how to paste the alias into the OpnSense UI").

### UniFi (FS-FwRuleUnifiSubnetScope)

A single JSON document compatible with the UniFi controller's firewall
ruleset payload (the operator pastes into the gateway's firewall API or
the controller's advanced JSON editor):

```json
{
  "_comment": "skoed firewall-rule generator (see X-Skoed-Snapshot-* headers)",
  "name": "skoed_doh_gap",
  "ruleset": "WAN_OUT",
  "action": "drop",
  "src_address_group": { "type": "address-group", "value": "10.0.0.0/24" },
  "dst_address_group": {
    "type": "address-group",
    "name": "skoed_doh_resolvers",
    "members": ["1.1.1.1", "8.8.8.8", "9.9.9.9",
                "2606:4700:4700::1111", "2001:4860:4860::8888", "2620:fe::fe"]
  }
}
```

`scope=all` sets `src_address_group` to `"any"`. `action=reject` sets
`"action": "reject"`. The body remains `text/plain; charset=utf-8`
(per spec) — the JSON is just the payload string.

## Snapshot loading

Resolver IPs come from the curated snapshot owned by TS-DohResolverIpDatabase:

```go
snap, err := app.resolverSnapshot.Current()  // sync.RWMutex'd in-memory cache
if err != nil { /* 503 */ }
```

The handler treats the snapshot as immutable for the duration of one
request — re-reading it mid-render would risk inconsistent counts in the
header vs. the rule body. The snapshot includes:

- `ID string` — content hash of the upstream feed
- `FetchedAt time.Time`
- `Resolvers []ResolverEntry` (sorted by `Name` for stable output)
- `Stale bool` — derived `time.Since(FetchedAt) > 7*24h`

No new bbolt key namespace is introduced by THIS spec; the snapshot
lives under the namespace owned by TS-DohResolverIpDatabase
(`doh_resolvers/snapshot`).

## Raft / cluster

This endpoint is **read-only**. It does NOT route through Raft. It
reads:

- the in-memory config snapshot (for profile→client_ips lookup) via
  `app.GetCfg()` (already RWMutex-guarded)
- the in-memory resolver snapshot (RWMutex'd, refreshed by the
  TS-DohResolverIpDatabase scheduler)

No new `cluster.CommandKind` is introduced. Followers serve the request
locally — there's nothing leader-specific about reading two in-memory
caches. This matches the existing pattern for read-only endpoints like
`/api/v1/test-domain` and `/api/v1/cluster/stats`.

## Error responses

| Status | When                                              | Body                                                              |
|--------|---------------------------------------------------|-------------------------------------------------------------------|
| 400    | unknown `platform`                                | `{"error":"unsupported platform","supported_platforms":[...]}`    |
| 400    | unknown `action`                                  | `{"error":"unknown action; expected drop or reject"}`             |
| 400    | unknown `scope`                                   | `{"error":"unknown scope; expected all, subnet, or profile"}`     |
| 400    | `scope=subnet` with missing/invalid `subnet`      | `{"error":"invalid subnet: <parse err>"}`                         |
| 400    | `scope=profile` with missing `profile` param      | `{"error":"profile parameter required when scope=profile"}`       |
| 401    | missing/invalid Basic auth                        | (handled by `BasicAuth` middleware; no JSON envelope)             |
| 404    | `scope=profile` with unknown id                   | `{"error":"profile not found: <id>"}`                             |
| 503    | resolver snapshot unavailable (cold cluster)      | `{"error":"resolver snapshot unavailable; trigger a refresh"}`    |

## Posture

**Auth gating.** Mounted inside the authenticated route group
(`r.Group(r.Use(a.BasicAuth, a.auditMiddleware))`). No public surface —
unlike the M5.9.5/M5.9.7 testers, there is no `/api/v1/_public/...`
counterpart. Rationale: the output enumerates the operator's network
topology (which subnets exist, which clients are on which profile) and
fingerprints the curated resolver list. FSID: FS-FwRuleRequiresAuth.

**Audit behaviour.** This is a GET that mutates nothing, so it falls
through the `auditExempt` prefix check (same as `/api/v1/test-domain`).
The M5.2 audit middleware's existing GET-exemption logic covers it;
no one-liner add is needed. Operators who want to know "who pulled the
firewall rules" can read the access log instead.

**Metric series introduced.**

```
skoed_firewall_rules_generated_total{platform="iptables|nftables|mikrotik|opnsense|unifi"}
```

Single `CounterVec`, **5 series total** — cardinality bound is the
enum width on `platform`. `scope` and `action` are deliberately NOT
labels (would push cardinality to 5 × 3 × 2 = 30 with no analysis
value). FSID: FS-FwRuleMetricsCounter. The counter is bumped after
successful render (200 path only); 4xx/5xx do not increment.

**SSRF / PII concern.** No SSRF surface: the handler makes zero
outbound requests. The upstream-fetching is owned by the
TS-DohResolverIpDatabase scheduler, which has its own SSRF posture
(allowlisted source URL). PII: profile→client_ips lookup exposes the
operator's LAN map to the authenticated caller, which is fine (admin
sees this in the Clients page anyway). The generated rules contain
public DoH/DoT resolver IPs only — no private data leaks via the body.

## Implementation map

```
apps/skoed/internal/api/handlers/
  firewall_rules.go      (new: GenerateFirewallRules + renderers)
apps/skoed/internal/api/handlers/firewall/
  renderer.go            (new: renderer interface + rulePlan struct)
  iptables.go            (new)
  nftables.go            (new)
  mikrotik.go            (new)
  opnsense.go            (new)
  unifi.go               (new)
apps/skoed/internal/metrics/
  metrics.go             (extend: ObserveFirewallRulesGenerated + register counter)
apps/skoed/internal/api/
  app.go                 (route registration inside the auth group)
tests/acceptance/
  firewall_rules_test.go (all FSIDs)
```
