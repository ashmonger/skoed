---
x-tsid: TS-FwRuleUi
x-fsid-links:
  - FS-FwRuleUiClientsRowActionVisible
  - FS-FwRuleUiClientsModalPlatformTabset
  - FS-FwRuleUiClientsCopyToClipboard
  - FS-FwRuleUiStatsSubnetCallout
  - FS-FwRuleUiStatsSubnetPreviewAndCopy
  - FS-FwRuleUiProfileScope
  - FS-FwRuleUiKeyboardNavigablePlatformTabs
  - FS-FwRuleUiStaleSnapshotBanner
  - FS-FwRuleUiEmptyResolverDatabase
  - FS-FwRuleUiUnauthorizedRedirect
---

# TS-FwRuleUi — Web UI surfaces for the firewall-rule generator

The browser is a thin renderer over **two** backend surfaces already
specified elsewhere:

| Backend                                | Source spec               |
|----------------------------------------|---------------------------|
| `GET /api/v1/firewall-rules`           | TS-FwRuleGen (M6 sibling) |
| `GET /api/v1/doh-resolvers`            | TS-DohResolverDb (M6)     |

This TS owns ONLY the UI plumbing: where the buttons live, how the modal
behaves, how the platform tabset handles keyboard input, how clipboard
copy is wired. **No new HTTP routes, no new bbolt namespace, no new Raft
command, no new scheduler job** is introduced by this spec — every state
mutation continues to flow through the existing M6 endpoints.

## Surfaces

| View              | Entry point                                  | Default scope           |
|-------------------|----------------------------------------------|-------------------------|
| `Clients.vue`     | Per-row "Copy DoH-gap rules" overflow action | `scope=subnet&subnet=<client/32>` |
| `Clients.vue`     | Per-row → "scope to subnet…" sub-action      | `scope=subnet&subnet=<derived CIDR>` |
| `Profiles.vue`    | Per-profile "Copy DoH-gap rules" action      | `scope=profile&profile=<id>` |
| `Stats.vue`       | "Closing the DoH gap" callout (above the fold)| `scope=subnet&subnet=<picker>` |

Every surface delegates to the same Vue component, `RuleCopyModal.vue`,
which is the only consumer of the generator API. Single component → single
place to enforce keyboard navigation, stale-snapshot banners, and clipboard
behaviour. This is the property the spec asks for in
FS-FwRuleUiKeyboardNavigablePlatformTabs and FS-FwRuleUiStaleSnapshotBanner.

## Modal contract (`RuleCopyModal.vue`)

Props:

```ts
type Scope =
  | { kind: 'client';  ip: string }      // expands to scope=subnet&subnet=<ip>/32 in the request
  | { kind: 'subnet';  cidr: string }
  | { kind: 'profile'; profileId: string }
  | { kind: 'all' }

interface Props {
  scope: Scope
  initialPlatform?: 'iptables' | 'nftables' | 'mikrotik' | 'opnsense' | 'unifi'
  action?: 'drop' | 'reject'   // default 'drop'
}
```

Internal state:

```ts
const activePlatform = ref<Platform>(props.initialPlatform ?? 'iptables')
const preview        = ref<string>('')
const loading        = ref(false)
const fetchError     = ref<string | null>(null)
const snapshot       = ref<{ id: string; fetched_at: string; stale: boolean; count: number } | null>(null)
```

On mount and on `activePlatform` change, the modal calls:

```
GET /api/v1/firewall-rules
  ?platform={activePlatform}
  &scope={derived from props.scope}
  &subnet=... | profile=... (conditional on scope kind)
  &action={props.action ?? 'drop'}
```

The response body is plain text; the modal renders it in a `<pre>` block
with monospace styling. Snapshot provenance for the stale banner is read
from the leading comment block in the response body (which TS-FwRuleGen
emits as `snapshot_id`, `snapshot_fetched`, `resolver_count`) — the modal
does NOT make a second call to `/api/v1/doh-resolvers` just to get the
fetched_at timestamp.

## Platform tabset

Implemented as an ARIA `tablist` (W3C APG pattern). Tabs are buttons with
`role="tab"`, the active tab carries `aria-selected="true"`, and the
preview region has `role="tabpanel"` + `aria-labelledby={active-tab-id}`.

Keyboard model (FS-FwRuleUiKeyboardNavigablePlatformTabs):

| Key         | Behaviour                                                     |
|-------------|---------------------------------------------------------------|
| `ArrowRight`| Focus next tab; wraps from last → first                       |
| `ArrowLeft` | Focus previous tab; wraps from first → last                   |
| `Home`      | Focus first tab                                               |
| `End`       | Focus last tab                                                |
| `Space`/`Enter` | Activate the focused tab (changes `activePlatform`)       |

Tabs use **automatic activation**: focus = active. This matches the spec's
"focus moves to the nftables tab AND the preview switches" expectation in
a single key press. Trade-off: each key press fires a fresh
`GET /api/v1/firewall-rules` call. Mitigation: the modal keeps a
per-platform cache keyed by `(scope, platform, action)` so a second visit
to a tab is free.

## Copy-to-clipboard

```ts
async function copy() {
  await navigator.clipboard.writeText(preview.value)
  copied.value = true
  setTimeout(() => (copied.value = false), 1500)
}
```

The button is `disabled` whenever:

- `loading === true`, OR
- `fetchError !== null`, OR
- `preview === ''` (FS-FwRuleUiEmptyResolverDatabase — empty resolver
  database produces an empty body from the API; modal renders the
  empty-state instead of a `<pre>`).

When `navigator.clipboard` is unavailable (HTTP origin, ancient browsers),
the modal falls back to selecting the `<pre>` text and showing
"Press Ctrl+C to copy" — no `document.execCommand('copy')` fallback
(deprecated, flaky, and the dashboard is HTTPS-only in any modern deploy).

## Stale-snapshot banner

Source of truth: the `WARNING: snapshot is stale` line in the leading
comment block of the generator response (TS-FwRuleGen,
FS-FwRuleStaleSnapshotStillServes). The modal parses the first ~10 lines
of the body for that exact marker plus the `snapshot_fetched` field.

When detected:

- a yellow `<aside role="status">` banner sits ABOVE the `<pre>` preview,
- text reads: `Resolver snapshot last refreshed {{ fetched_at }} — rules may miss new resolvers.`,
- the Copy button remains enabled (FS-FwRuleUiStaleSnapshotBanner is
  explicit on this).

## Empty-state

When the API returns 200 with an effectively empty body (no resolvers in
the snapshot — e.g. brand-new cluster that has never run a refresh):

- the `<pre>` is replaced by a centered empty-state block,
- copy is disabled,
- admins (role check via the existing `auth.isAdmin` flag) see a
  "Refresh resolver database" button that POSTs `/api/v1/doh-resolvers/refresh`
  and re-fetches on success,
- non-admins see "Ask your admin to refresh the resolver database."

(FS-FwRuleUiEmptyResolverDatabase.)

## Stats page callout

`Stats.vue` gains a top-of-page card ABOVE the existing M5.4 stats tiles:

```
┌───────────────────────────────────────────────────────────────────────────┐
│  Closing the DoH gap                                              [Copy]  │
│                                                                           │
│  Subnet: [ 10.0.0.0/24 ▾ ]   Platform: [ iptables | nftables | … ]        │
│                                                                           │
│  -A FORWARD -s 10.0.0.0/24 -d 1.1.1.1 -j DROP                             │
│  -A FORWARD -s 10.0.0.0/24 -d 8.8.8.8 -j DROP                             │
│  …                                                                        │
└───────────────────────────────────────────────────────────────────────────┘
```

The subnet picker is pre-populated from the existing
`GET /api/v1/clients` response — every distinct IP is grouped into a `/24`
candidate set; the picker shows the unique CIDRs sorted by client-count
descending. Operator can also type a free-form CIDR (validated client-side
with a regex; server-side validation per FS-FwRuleRejectsInvalidSubnet
takes precedence).

The callout reuses `RuleCopyModal.vue`'s body in inline form (no modal
wrapper) — same tabset, same preview pane, same copy button.

## Clients page row action

`Clients.vue` gets a new column "Actions" with a single overflow menu
(`<button aria-haspopup="menu">⋯</button>`). The menu items are:

| Item                          | Action                                              |
|-------------------------------|-----------------------------------------------------|
| Copy DoH-gap rules (this IP)  | Open `RuleCopyModal` with `scope={kind:'client'}`   |
| Copy DoH-gap rules (this /24) | Open `RuleCopyModal` with `scope={kind:'subnet'}`   |
| View query log for this IP    | Existing M3.5 deep-link (unchanged)                 |

The menu is a `Popover` (Headless UI Vue) — keyboard accessible by default
(`ArrowDown`/`ArrowUp`/`Esc`).

## Profiles page action

`Profiles.vue` gains a per-row "Copy DoH-gap rules" button that opens
`RuleCopyModal` with `scope={kind:'profile', profileId}`. This is the
surface for FS-FwRuleUiProfileScope: the modal renders the rules whose
`-s` lists every IP currently bound to that profile.

## Auth gating (FS-FwRuleUiUnauthorizedRedirect)

No new auth wiring. All three host views (`Clients.vue`, `Stats.vue`,
`Profiles.vue`) are already under the `Shell` parent route, which carries
the existing `requiresAuth` guard from `router.ts`. An unauthenticated
session navigating to `/dashboard/clients` is bounced to `/login` BEFORE
the component mounts → the "Copy DoH-gap rules" action is never rendered,
and `RuleCopyModal` never makes an unauthenticated API call.

## Error handling

| Backend status | Modal behaviour                                                |
|----------------|----------------------------------------------------------------|
| 200            | Render body in `<pre>`; parse stale banner; enable Copy         |
| 400            | Replace `<pre>` with inline error: `Invalid request: {{body.error}}` |
| 401            | Trigger the global axios 401 handler → bounce to `/login`       |
| 404            | (profile scope, unknown profile) Inline: `Profile not found.`   |
| 5xx            | Inline: `Generator unavailable. Retry?` with a Retry button     |

No retry-with-backoff in the client; one click = one request.

## Implementation map

```
web/src/components/
  RuleCopyModal.vue       (new: tabset + preview + copy + stale banner + empty-state)
  RuleCopyInline.vue      (new: same body without modal chrome; used by Stats callout)
  PlatformTabset.vue      (new: ARIA tablist + keyboard model)
  ResolverSnapshotBanner.vue (new: yellow stale banner)
web/src/views/
  Clients.vue             (extend: add Actions column with overflow menu)
  Stats.vue               (extend: add "Closing the DoH gap" callout above fold)
  Profiles.vue            (extend: add per-row "Copy DoH-gap rules" button)
web/src/api/
  endpoints.ts            (extend: getFirewallRules(scope, platform, action) → text)
  types.ts                (extend: Platform, FwRuleScope union types)
tests/acceptance/
  firewall_rules_web_ui_test.go (all FSIDs — HTTP-level assertions on
                                 GET /api/v1/firewall-rules; Vue rendering
                                 is exercised via embedded-SPA presence
                                 checks per the M2.6 convention)
```

## Non-changes (explicit)

To keep this TS surgical:

- **No new bbolt key namespace.** UI state (active tab, copied flag,
  cached previews) is in-memory in the Vue component and dies with the
  tab. Operator preferences (default platform per-user) are NOT
  persisted in M6.
- **No new Raft command type.** Every mutation the UI can trigger
  (`POST /api/v1/doh-resolvers/refresh`) is already specified in
  TS-DohResolverDb.
- **No new scheduler job.** The leader-only daily refresh is owned by
  TS-DohResolverDb; the UI is a pure consumer.

## Posture

**Auth gating.** All three host views are inside the `Shell` parent with
`requiresAuth`. Unauthenticated callers are bounced to `/login` at the
router guard, before any UI element renders. The backend
`GET /api/v1/firewall-rules` enforces auth independently
(FS-FwRuleRequiresAuth), so a logged-out tab attempting the call gets a
401 which the global axios interceptor converts to a `/login` redirect.

**Audit behaviour.** `GET /api/v1/firewall-rules` is a read-only endpoint
and is **not** audited (the M5.2 audit middleware's GET exemption applies;
no one-liner add needed). `POST /api/v1/doh-resolvers/refresh` IS audited
because it triggers a mutation — that auditing is owned by TS-DohResolverDb,
not this spec. The UI surfaces never POST anywhere else.

**Metrics series introduced.** None by this spec. Per-platform usage is
already counted by `skoed_firewall_rules_generated_total{platform="…"}`
(owned by TS-FwRuleGen, 5 series — one per supported platform). The UI
does not introduce any client-side telemetry, error-reporting, or
view-instrumentation pixel.

**SSRF concern.** None introduced by this spec. The UI calls only
same-origin skoed endpoints; no URL field is operator-controllable.
The subnet picker accepts free-form CIDR text but never fetches that
CIDR — it goes only as a query-string argument to skoed, which validates
and rejects non-CIDR input (FS-FwRuleRejectsInvalidSubnet).

**PII concern.** The generated rule blobs contain client IP addresses
(the LAN-side, RFC1918-style addresses skoed already exposes on the
Clients page). The clipboard is local to the operator's workstation; no
new exfiltration path. The modal never sends client IPs back to skoed
beyond what `GET /api/v1/firewall-rules` already receives as a query
argument. No browser-side telemetry, no third-party fonts, no CDN —
consistent with the M2.6 self-contained-bundle posture.
