# DEMO NOTE — M5.9.4 Getting Started card + docs page

## Scope

A new operator who just set the admin password (`POST /api/v1/auth/setup`)
used to land on an empty Dashboard with no breadcrumb. M5.9.4 adds:

1. A dismissible **Getting Started** card on `/`, visible only while
   the cluster has no operator-added blocklists or profiles.
2. A new docs chapter `docs/src/first-run/getting-started.md`
   reachable from the SUMMARY left-nav (listed first under "First run").

No new API endpoints, no new backend code — the card reads the
existing `/blocklists` and `/profiles` lists and persists dismissal in
`localStorage`.

### Implemented

- **`web/src/views/Dashboard.vue`** — new card placed **first in
  template order**, above the M3.6 spoof / M5.6 upgrade / M5.4 stale /
  M3.5 DoH alert cards. The existing alert ordering is preserved.
- **Card visual**: `card p-4 border-l-4 border-accent` (same accent
  left border as the M5.6 upgrade banner). Rocket icon header,
  numbered ① ② ③ steps:
  1. **Add a blocklist** → router-link to `/blocklists`.
  2. **Bootstrap a cluster (optional)** → `/docs/cluster/bootstrap.html`,
     plus inline router-link to `/cluster` for the join-token UI.
  3. **Point a client at skoed** → inline `<details>` toggle revealing
     a copy-pasteable `dig @<skoed-host> example.com` (the host is
     auto-filled from `window.location.hostname`).
  Plus a footer link to the new docs chapter.
- **Dismiss [x] button**: top-right, `XMarkIcon`, `aria-label="Dismiss
  Getting Started card"`. Sets
  `localStorage["skoed.gettingStarted.dismissed"] = "true"` and
  unmounts the card immediately.
- **Visibility predicate**:
  ```
  userBlocklists.length === 0  AND
  userProfiles.length === 0    AND
  localStorage[...dismissed] !== "true"
  ```
  Where:
  - `userBlocklists = listBlocklists().filter(b => !b.id.startsWith("cat:"))`
  - `userProfiles   = listProfiles().filter(p => p.id !== "default")`

  This carve-out matters because a fresh node already ships with the
  bundled `cat:doh` category + `default` profile — counting them would
  mean the card is never visible on a real fresh install.
- **`docs/src/first-run/getting-started.md`** — single-page walk-through
  mirroring the card's three steps with copy-pasteable bash, plus
  links to the M5.5 .deb install path, the existing
  `auth-setup.md` / `first-blocklist.md` references, and the M5.6
  in-place upgrade page.
- **`docs/src/SUMMARY.md`** — adds the new chapter as the first
  entry under "First run".

### Acceptance tests

UI-only feature — no Go acceptance test added (consistent with the
documentation-site precedent in M5.8). Validated by three browser
smoke checks driven from `web/shoot-m5.9.4.mjs` + ad-hoc Playwright
scripts during this PR:

| FSID                                              | Result |
|---------------------------------------------------|--------|
| FS-GettingStartedShownWhenEmpty                   | PASS (screenshot) |
| FS-GettingStartedAutoHidesAfterFirstBlocklist     | PASS (smoke)      |
| FS-GettingStartedDismissPersists                  | PASS (smoke)      |
| FS-GettingStartedDocsChapter                      | PASS (file exists + linked from SUMMARY) |

### Screenshots

- `docs/screenshots/m5.9.4-getting-started-card.png` — Dashboard on
  a fresh node (Lipgloss dark, 1440×900) showing the accent-bordered
  Getting Started card above the empty stat tiles.

Re-capture: boot a fresh skoed node, set password, then:

```sh
cd web && node shoot-m5.9.4.mjs
# (requires playwright + chromium — `npm install --no-save playwright
# && npx playwright install chromium` once)
```

### Not implemented (deferred / non-goals)

- **Server-side dismissal state** — per-browser is sufficient.
  Operators who clear localStorage see the card again, which is a
  feature.
- **Re-showing the card after dismissal once new blocklists are added** —
  dismissal is sticky on purpose; we don't want to nag.
- **A multi-step wizard / modal** — explicit non-goal per ROADMAP.
- **Pop-up toasts** — zero pop-ups added.
- **Per-admin-user state** — single-org product; one admin role.
- **Telemetry on dismissal / step clicks** — no analytics infrastructure
  and we don't want it.

### Files added / changed

```
specs/functional/getting-started.feature       (new — 4 FSIDs)
specs/technical/getting-started.md             (new — TS-GettingStarted)

web/src/views/Dashboard.vue                    (added card + state)
web/shoot-m5.9.4.mjs                           (new — screenshot script)

docs/src/first-run/getting-started.md          (new — 3-step walk-through)
docs/src/SUMMARY.md                            (added entry)
docs/screenshots/m5.9.4-getting-started-card.png   (new)

DEMO_NOTE_M5.9.4.md                            (this file)
```

## Demo

```bash
# 1. Boot a fresh single-node skoed.
mkdir -p /tmp/m5.9.4/data
cat > /tmp/m5.9.4/config.yaml <<'EOF'
node:
  id: skoed-1
  raft_address: 127.0.0.1:18994
  api_address: 127.0.0.1:18995
  data_dir: /tmp/m5.9.4/data
  dns:
    listen: { port: 18996, ipv4: true, ipv6: false }
EOF
./apps/skoed/skoed --config /tmp/m5.9.4/config.yaml &

# 2. Set the admin password (first-run flow).
curl -fsS -X POST http://127.0.0.1:18995/api/v1/auth/setup \
  -H 'content-type: application/json' \
  -d '{"username":"admin","password":"demopass123"}'

# 3. Open http://127.0.0.1:18995/ — Dashboard shows the Getting
#    Started card at the top, above the (empty) stat tiles.

# 4. Add a blocklist (any non-"cat:*" id) — refresh the Dashboard
#    and the card auto-hides.
curl -fsS -u admin:demopass123 -X POST http://127.0.0.1:18995/api/v1/blocklists \
  -H 'content-type: application/json' \
  -d '{"id":"demo","name":"Demo","enabled":true,
       "source":{"type":"manual","format":"domainlist"},
       "domains":["example.test"]}'

# 5. Alternatively, on a fresh node click the [x] — refresh — card
#    stays gone (localStorage["skoed.gettingStarted.dismissed"]="true").
```

## Next

M5.9.5 — URL tester (CLI + public landing page).
