---
x-tsid: TS-GettingStarted
x-fsid-links:
  - FS-GettingStartedShownWhenEmpty
  - FS-GettingStartedAutoHidesAfterFirstBlocklist
  - FS-GettingStartedDismissPersists
  - FS-GettingStartedDocsChapter
---

# TS-GettingStarted — Dashboard onboarding card + docs chapter

## Scope

A new operator's first authenticated view of skoed is the Dashboard.
Before M5.9.4 it's empty (no queries, no blocklists, no alerts) and
gives no breadcrumb. This spec adds:

1. An inline **Getting Started** card on `/` (Dashboard.vue).
2. A new docs chapter `docs/src/first-run/getting-started.md`
   reachable from the SUMMARY left-nav.

No new backend code: the card reads `listBlocklists()` +
`listProfiles()` via the existing M3 / M5.4 endpoints, and persists
dismissal in `localStorage`.

## Visibility predicate

The card renders iff **all** of:

```
userBlocklists.length === 0
  AND userProfiles.length === 0
  AND localStorage["skoed.gettingStarted.dismissed"] !== "true"
```

Where:

- `userBlocklists` = `listBlocklists().filter(b => !b.id.startsWith("cat:"))`
- `userProfiles`   = `listProfiles().filter(p => p.id !== "default")`

A fresh node ships pre-seeded with the bundled `cat:doh` category
blocklist and a `default` profile that uses it — neither counts as
"the operator has done something useful." Counting them would mean
the card is never visible on a real fresh install.

| Condition                       | Card visible? |
|---------------------------------|---------------|
| Fresh node, never dismissed     | yes           |
| Has any blocklist               | no            |
| Has any profile (manual M3 add) | no            |
| Operator clicked [x]            | no (persists) |
| Operator cleared localStorage   | yes (again)   |

Auto-hide on first blocklist means: an operator who follows step 1 of
the checklist watches the card disappear on their next page load — a
small but pleasing confirmation that they did the right thing.

The dismissal is sticky on purpose: once the operator has clicked
[x] we trust them and never re-show the card. Re-showing after later
blocklist deletions would be nagware.

## Component shape

Inline in `web/src/views/Dashboard.vue`, placed **first in template
order** — above the spoof / upgrade / stale-blocklist / DoH alert
cards. The existing alert ordering is preserved beneath it.

Visual:

```
┌─ ▍ (accent left border, like the M5.6 upgrade banner) ────────[x]─┐
│                                                                    │
│   Getting started                                                  │
│   A few minutes to a working setup.                                │
│                                                                    │
│   ① Add a blocklist                              → /blocklists     │
│   ② (optional) Bootstrap a cluster               → docs            │
│   ③ Point a client at skoed                     → dig snippet     │
│                                                                    │
│   See the full walk-through →                    → docs            │
└────────────────────────────────────────────────────────────────────┘
```

- Card class: `card p-4 border-l-4 border-accent` (same as M5.6).
- Numbered steps are an `<ol>` with `text-fg-strong` numbers and
  `text-accent hover:underline` links to:
  - `{ name: 'blocklists' }` for step 1
  - `/docs/cluster/bootstrap.html` (mdBook output path) for step 2
  - a `<details>` toggle revealing a copy-pasteable
    `dig @<skoed-host> example.com` for step 3
- [x] dismiss is a `<button>` with an `XMarkIcon` in the top-right;
  sets the localStorage flag and flips a local `dismissed = true`
  ref so the card unmounts immediately.

## Reactivity

`onMounted` calls `Promise.all([listBlocklists(), listProfiles()])`
and computes `showGettingStarted` from the results plus the
localStorage flag. The Dashboard's existing polling timers do **not**
need to re-evaluate this — the card is for first-run state only, so
a single fetch at mount is enough. If the operator adds a blocklist
on another tab, they'll see the card disappear on next navigation /
reload, which is the documented behavior in
`FS-GettingStartedAutoHidesAfterFirstBlocklist`.

## localStorage key

```
key:   skoed.gettingStarted.dismissed
value: "true"            (string, sentinel)
unset: card honors visibility predicate
```

Kept under the `skoed.` namespace — same convention as
`skoed.theme` (Pinia theme store, M3) and `skoed.creds`
(session-only auth helper, login flow).

## Docs chapter

`docs/src/first-run/getting-started.md` — a single page that walks
the operator through the same three steps, with copy-pasteable bash
that links to:

- `install/debian-ubuntu.md` (M5.5 .deb path)
- `first-run/auth-setup.md` (already exists)
- `first-run/first-blocklist.md` (already exists; M5.4 refresh-interval
  shape)
- `cluster/bootstrap.md` (already exists)

`SUMMARY.md` lists it as the **first** entry under "First run" — it's
the umbrella overview, with the existing auth / blocklist pages still
listed underneath as deeper dives.

## Out of scope

- A server endpoint that records dismissal — per-browser is enough.
- Showing the card to non-admin users — there's only one auth role
  in v1 (the admin).
- A11y polish beyond `aria-label="Dismiss"` on the [x] button — the
  card is keyboard-navigable via the underlying `<router-link>` and
  `<a>` elements.
- Telemetry on dismissal / step clicks — we don't have analytics
  infrastructure and we don't want it.

## Traceability

| FSID                                              | Surface                                   |
|---------------------------------------------------|-------------------------------------------|
| FS-GettingStartedShownWhenEmpty                   | Dashboard.vue render predicate            |
| FS-GettingStartedAutoHidesAfterFirstBlocklist     | Dashboard.vue + listBlocklists()          |
| FS-GettingStartedDismissPersists                  | Dashboard.vue + localStorage key          |
| FS-GettingStartedDocsChapter                      | docs/src/first-run/getting-started.md     |

UI-only feature — no Go acceptance test added. Validated by the M5.9.4
screenshot capture + the operator demo flow in `DEMO_NOTE_M5.9.4.md`.
