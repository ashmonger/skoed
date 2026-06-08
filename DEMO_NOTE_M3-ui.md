# DEMO NOTE — M3 Web UI (Profiles · Schedules · Categories · DoH widget)

## Scope

Surfaces the M3 backend (parental control, schedules, category catalog,
DoH/DoT detection) in the embedded Web UI. Closes the M3 UI gap that
the M3 backend merge left for follow-up.

### Implemented

- **/profiles** — full CRUD against `/api/v1/profiles`:
  - Table: id (mono) | name | blocklists (badge per id) | clients (IPs · CIDRs) | SafeSearch (provider badges) | actions.
  - Inline edit modal: name, blocklist checklist (from `/api/v1/blocklists`), allowlist textarea, SafeSearch toggles (google / bing / youtube / duckduckgo), client IPs textarea, client CIDRs textarea.
  - Create modal exposes the `id` field and the same body.
  - `default` profile row exposes no Delete button (UI-level protection; server also returns 409).
  - PATCH submits only dirty fields (deep array compare).

- **/schedules** — full CRUD against `/api/v1/schedules`:
  - Table: id | name | mode badge (block-in-window / allow-in-window) | window summary.
  - Mode radio toggle (`block_only_inside` / `allow_only_inside`).
  - Window editor: list of rows, each with day checklist (Sun-Sat), start/end `<input type="time">`, remove. "+ Add window" appends.
  - Per-row Bindings panel: add binding by selecting (profile, blocklist) → `POST .../bindings`; delete via DELETE.
  - Known gap (M3.1): no GET endpoint to list existing bindings, so the panel only shows bindings created in the current session. Flagged in-file.

- **/categories** — catalog browser against `/api/v1/categories`:
  - 3-column responsive card grid.
  - Per card: name badge + format badge, description, effective URL (annotated `(default)` or `(override)`), inline override editor (URL + format select + Reset to default), subscribed-profile badges (each with × → disable confirmation), Enable button → modal to pick a target profile (filtered to profiles not yet subscribed).

- **Stats.vue — DoH widget** (FS-WebUiDohWidget):
  - "DoH attempts today" card rendered as a sibling of the aggregate stats block, so it surfaces even before the first hourly flush.
  - Sources data from the cluster query log (`outcome=blocked` + `blocklist_id === 'cat:doh'` + today-only).
  - Top 10 clients by probe count, each linking to `/query-log?client=<ip>&category=doh-probe`.
  - Empty state when no probes; inline error + Retry on fetch failure.

- **Sidebar** — three new entries between "Local DNS" and "Query log": Profiles (UsersIcon), Schedules (ClockIcon), Categories (TagIcon). Page titles wired into the breadcrumb.

### Not implemented (intentional)

- Drag-and-drop schedule editor (static weekly grid is enough at M3.1).
- Visual flow chart of profile-rule precedence (operator reads the table).
- I18n for the new strings (English only).
- "List bindings" GET endpoint — bindings can be added/removed via UI but pre-existing bindings aren't fetched (see the in-file M3.1 note in `Schedules.vue`).

### Limitations

- The `doh` category card shows no `Upstream URL` because the bundled DoH-resolver list is in-binary (no remote URL). Card still functional.
- The "View log" link from the DoH widget passes `category=doh-probe` but the query-log view currently ignores unknown filters — surface-only navigation; doesn't actually narrow the result.
- Theme matrix coverage limited to **monokai-solarized** (light + dark). The full 4-palette × 2-mode matrix is already covered by the cluster screenshot set; M3 UI screenshots use solarized as the canonical theme.

## Build artifacts

| Component | Size |
|-----------|------|
| SPA bundle (gzipped) | 88 KB |
| New view bytes (gzipped) | +12 KB (Profiles 3.8 + Schedules 4.9 + Categories 3.2) |
| skoed binary (Alpine static, CGO=0) | 12 MB |

## Demo recipe (single node)

```bash
# 1. Build SPA + binary
make -C apps/skoed build

# 2. Write a single-node config (~/.skoed/test.yaml)
cat > /tmp/test-config.yaml <<EOF
node:
  id: demo
  raft_address: 127.0.0.1:17000
  api_address:  127.0.0.1:18080
  data_dir:     /tmp/skoed-m3-ui-demo/data
  dns:
    listen: { port: 5353, ipv4: true, ipv6: false }
EOF
mkdir -p /tmp/skoed-m3-ui-demo/data

# 3. Boot (test mode unlocks EDNS0 client-IP spoofing)
SKOED_TEST_MODE=1 ./apps/skoed/skoed -config /tmp/test-config.yaml &

# 4. Set admin password (one-shot)
curl -sX POST http://127.0.0.1:18080/api/v1/auth/setup \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"demopass123"}'

# 5. Seed a profile, a schedule, enable two categories
AUTH="-u admin:demopass123"; URL="http://127.0.0.1:18080"
curl -s $AUTH -X POST $URL/api/v1/profiles \
  -H 'Content-Type: application/json' \
  -d '{"id":"kids","name":"Kids","client_ips":["192.168.1.42","192.168.1.43"],"safesearch":["google","youtube"]}'
curl -s $AUTH -X POST $URL/api/v1/schedules \
  -H 'Content-Type: application/json' \
  -d '{"id":"evening-clamp","name":"Evening clamp","mode":"block_only_inside","windows":[{"days":["Mon","Tue","Wed","Thu","Fri"],"start":"20:00","end":"23:59"}]}'
curl -s $AUTH -X POST $URL/api/v1/categories/social/enable \
  -H 'Content-Type: application/json' -d '{"profile_id":"kids"}'

# 6. Probe a DoH hostname from a spoofed client (192.168.1.42)
dig @127.0.0.1 -p 5353 +ednsopt=65500:c0a8012a dns.google
dig @127.0.0.1 -p 5353 +ednsopt=65500:c0a8012a cloudflare-dns.com

# 7. Open http://127.0.0.1:18080 in a browser, login as admin/demopass123
#    Navigate to /profiles, /schedules, /categories, /stats
```

## Screenshots

8 PNGs under `docs/screenshots/m3ui-*.png` — Profiles, Schedules, Categories, Stats(DoH) in the monokai-solarized palette (light + dark). Reproduced via the standalone Playwright script `web/shoot-m3-ui.mjs` (assumes the node from the recipe above plus the same seed).

## Tests

- `npm run type-check` passes on the full SPA (446 modules).
- `npm run build` succeeds; embedded `apps/skoed/internal/api/static/dist` refreshed.
- Go backend unchanged — existing `go test ./...` remains green from the M3 merge.

## What's next

- **K8s validation** of the M2.5 Helm chart on kind/k3s (next task).
- **M3.5** — per-client DoH surfacing (`/api/v1/clients/{ip}/doh-status` endpoint + dashboard alert). Firewall-recipe track skipped per UoR.
- **M4** — skoed as DoH/DoT server (RFC 8484, port 853, optional ACME).
