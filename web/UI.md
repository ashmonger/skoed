# skoed Web UI

Embedded Vue 3 SPA served from the skoed binary. Reachable at
`http://<node>:8080/` once admin credentials are configured.

Look-and-feel mixes:

- **Pi-hole**: persistent left sidebar grouping all admin sections.
- **AdGuard Home**: top bar with theme toggle + account menu, big stat
  tiles on the dashboard, accent-on-neutral palette.

## Themes

Two palettes, both with a light/dark toggle:

- **Monokai-Solarized** (default): solarized base3/base2 surfaces with
  solarized blue/green/yellow/red accents. Easier on the eyes for long
  admin sessions.
- **Monokai** (vivid): canonical Monokai colors — bg `#272822`, green
  `#A6E22E`, cyan `#66D9EF`, pink `#F92672`, orange `#FD971F`.

Theme controls live in the top-right of the header. Choice persists in
`localStorage`; mode also follows OS `prefers-color-scheme` on first
load.

## Layout

```
┌────────┬─────────────────────────────────────────────┐
│        │ <page title>     [☀/🌙] [Palette ▾]  user  ⏻│ ← Header (h-12)
│ Side-  ├─────────────────────────────────────────────┤
│ bar    │                                             │
│ (w-60) │              <RouterView>                   │ ← Main
│        │                                             │
│ status │                                             │
│ block  │                                             │
└────────┴─────────────────────────────────────────────┘
```

The sidebar collapses below the `md:` Tailwind breakpoint; a hamburger
button in the header toggles it on mobile.

## Pages

Each section below describes the route, what it shows, what it lets the
admin do, and which API endpoints it calls.

---

### `/login` — Login

**When**: requested by the router guard when credentials are missing and
the server has admin credentials configured.

**What it shows**: centered card with username + password fields, a "Sign
in" button, and the skoed logo + tagline.

**What it does**:

- Submit → `GET /api/v1/blocklists` with the supplied Basic Auth header
  (cheap auth probe; 200 = creds good, 401 = creds bad).
- On success: store `{user, pass}` in `sessionStorage`; redirect to the
  original target (`?redirect=...`) or `/`.
- On 401: show inline "Invalid credentials." error.
- On other errors: show generic "Login failed." error.

---

### `/setup` — First-run setup

**When**: requested by the router guard when the server has no admin
credentials yet (the auth probe returned 200 without auth or never had a
valid `auth.username`).

**What it shows**: centered card with username (≥ 2 chars), password
(≥ 8 chars), and password confirmation fields.

**What it does**:

- Submit → `POST /api/v1/auth/setup` `{username, password}`.
- On success: store credentials, route to `/` (Dashboard).
- On client validation failure (passwords don't match): inline error.
- On server error (e.g., already configured): inline error from the
  response body.

---

### `/` — Dashboard

**When**: the default landing page after login.

**What it shows** (3 sections, top-down):

1. **4 stat tiles** — Cluster status (badge tone reflects ok/degraded),
   Mode (`single-node` | `cluster`), Members (reachable / total), Total
   queries in the current stats window.
2. **Query breakdown** + **Top blocked domains** (side-by-side, stack on
   small screens):
   - Breakdown is a stack of 4 horizontal bars (Blocked / Forwarded /
     Cached / Local) each with absolute count and percentage of total.
   - Top blocked domains is a table sorted descending by count, top 8
     rows.
3. **Cluster nodes** table (Node id / Role / Sync state / Commit index).

**Polling**: 10 s. Concurrent `Promise.allSettled` over
`/cluster/health`, `/cluster/stats`, `/cluster/status` so one slow
endpoint never blocks the others.

---

### `/blocklists` — Blocklists

**What it shows**: a `.card` table with one row per blocklist:

| Column | Render |
|---|---|
| Enabled | Toggle switch (Pi-hole-style sliding pill) — flips inline |
| ID | Mono, small |
| Name | Plain text |
| Source | `badge-accent` = URL or `badge` = Inline |
| Domain count | Right-aligned, mono |
| Last updated | Relative time (`"3h ago"`) |
| Actions | Refresh (URL-source only), Delete (with confirm) |

**What it does**:

- **List**: `GET /api/v1/blocklists` on mount; re-fetched after every
  successful mutation.
- **Toggle**: optimistic UI flip → `PATCH /api/v1/blocklists/{id}` with
  `{enabled}`. Rolls back on error and shows the row's error message.
- **New** (`+ New blocklist` button top-right): modal with fields:
  - **ID** (optional)
  - **Name** (required)
  - **Source type**: radio "From URL" or "Manual entries"
  - URL mode: URL field + Format select (auto / hosts / domainlist /
    askoed)
  - Manual mode: textarea, one domain per line, whitespace-split
  - **Block policy** override (optional): inherit / nxdomain / null /
    nodata
  - **Enabled** checkbox (default on)
  - Submit → `POST /api/v1/blocklists` with the parsed payload.
- **Refresh**: `POST /api/v1/blocklists/{id}/refresh`; updates
  domain_count + last_updated in place.
- **Delete**: confirmation modal showing the domain count; on confirm →
  `DELETE /api/v1/blocklists/{id}`.

Empty state: centered explainer + "Create your first blocklist" CTA.

---

### `/allowlist` — Allowlist

**What it shows**:

- Header + helper sentence ("Domains here are never blocked, even if a
  blocklist matches.").
- Add input: single text field + "Add" button. Pressing Enter submits.
- Search input: filters the visible list client-side
  (case-insensitive substring).
- A `.card` with a `.table` listing every domain (sorted alphabetically),
  one row each, with a Remove button.
- "Bulk add" expander: opens a textarea; submit splits on whitespace,
  dedups, then serially calls `addAllowlist` for each. Reports
  `Added N, M already present, K failed` with a scrollable per-failure
  list.

**What it does**:

- `GET /api/v1/allowlist` on mount.
- `POST /api/v1/allowlist` `{domain}` on add.
- `DELETE /api/v1/allowlist/{domain}` on remove (with confirm).
- Domain regex: `^\*?\.?[A-Za-z0-9-_.]+$` (one optional leading wildcard).

---

### `/local-dns` — Local DNS

**What it shows**: a `.card` table:

| Column | Render |
|---|---|
| Hostname | Mono |
| Type | `A` / `AAAA` / `CNAME` (badge) |
| Value | Mono — IP or target FQDN |
| TTL | Right-aligned, mono, in seconds |
| Actions | Edit (inline), Delete (with confirm) |

**What it does**:

- `GET /api/v1/local-dns` on mount.
- **Edit inline**: clicking Edit swaps the row into editable fields
  (hostname / type / value / ttl). Save → `PUT /api/v1/local-dns/{id}`.
  Cancel reverts.
- **Delete**: confirmation modal; `DELETE /api/v1/local-dns/{id}`.
- **New** (`+ New entry` button top-right): modal with fields:
  - Hostname (required, FQDN or short name)
  - Type (A / AAAA / CNAME)
  - Value (validated against type — IPv4 / IPv6 / FQDN)
  - TTL (number, default 300)
  - Submit → `POST /api/v1/local-dns`.

Escape closes any open modal or cancels an inline edit.

---

### `/query-log` — Query log

**What it shows**:

- Filter bar (top): client IP input, outcome select
  (any / forwarded / blocked / cached / local), limit select
  (50 / 100 / 250 / 500), pause/resume toggle, **Cluster-wide** switch.
- A `.card` table:

| Column | Render |
|---|---|
| Time | Relative ("2s ago") + absolute on hover |
| Client | Mono IP |
| Domain | Mono |
| Type | A / AAAA / MX / etc. |
| Outcome | `badge-danger` blocked, `badge-success` forwarded, `badge-accent` cached, `badge-warning` local |
| Node | Mono node id, only when cluster-wide is on |

**What it does**:

- `GET /api/v1/query-log` (or `/cluster/query-log` when the switch is on)
  every 2 s while not paused; merges into the list and dedups by `id`.
- Filters re-fire on change.
- Pause stops the auto-refresh — useful for inspecting a row without
  scroll jitter.

---

### `/stats` — Cluster stats

**What it shows**: 4 stat tiles for the current window:

- Total queries / Blocked / Forwarded / Cached / Local (with %).

Plus two side-by-side cards:

- **Top blocked domains** (top 20, sortable column header).
- **Top clients** (top 20).

And a **per-node** table:

| Column |
|---|
| Node id |
| Hour start |
| Total |
| Blocked |
| Forwarded |
| Cached |
| Local |

**What it does**:

- `GET /api/v1/cluster/stats` on mount + 10 s polling.

---

### `/cluster` — Cluster

**What it shows**:

- Header card: cluster_id (mono, truncated with copy button), raft_term,
  leader_id, mode (single-node / cluster), status badge.
- Nodes table: every node with node_id / role / raft_address /
  api_address / last_contact (relative) / commit_index / sync_state.
  Per-row actions:
  - **Transfer leadership** (only for non-leader rows; only enabled on
    the leader). Confirms then `POST /api/v1/cluster/leadership/transfer`
    `{target_node_id}`.
  - **Remove** (only for non-leader rows; only on the leader). Confirms
    then `DELETE /api/v1/cluster/nodes/{node_id}`.
- **Join token** card: `+ Generate token` button. On generation, modal
  pops up showing token + expiration + leader address with a "Copy"
  button. Closing the modal does NOT re-fetch — the token is single-use
  and the operator must paste it into the new node's config now.

**What it does**:

- `GET /api/v1/cluster/status` + `GET /api/v1/cluster/health` polled every
  5 s.
- `POST /api/v1/cluster/tokens` on demand.

---

### `/settings` — Settings

**What it shows**: three independent `.card` sections, each with its own
Save button:

1. **DNS**
   - Mode (forwarding / recursive)
   - Upstream resolvers (textarea, one per line) when forwarding
   - Trusted subnets (textarea, one CIDR per line) when recursive
   - Upstream timeout (s)
   - Cache enabled + max entries
2. **Filtering**
   - Default block policy (NXDOMAIN / NULL / NODATA) with helper text
3. **Query log**
   - Max entries (per-node raw log)
   - Aggregate retention (days)

**What it does**:

- `GET /api/v1/settings` on mount.
- Each Save button: `PATCH /api/v1/settings` with ONLY that section's
  keys; disabled when the section is clean; shows "Saved." flash that
  fades after 2 s on success.

---

### `/account` — Account

**What it shows**: a single `.card` with three fields:

- Current password
- New password
- Confirm new password

**What it does**:

- Submit → `PUT /api/v1/auth/password`
  `{current_password, new_password}`.
- On success: log out and bounce to `/login` so the user re-enters the
  new password.
- On 401: "Current password is incorrect."
- On validation: inline message ("Passwords do not match.", "Password
  must be at least 8 characters.").

---

## Manual screenshot pass

Per the project's testing protocol, multi-instance UI validation uses
real browsers. The current capture flow:

```sh
docker run -d --name skoed-demo \
  --network skoed-demo \
  -v /tmp/skoed-demo/node1:/var/lib/skoed \
  -p 8080:8080 skoed:m2.6

# point a headless Chromium at http://localhost:8080/ for each page
chromium --headless --disable-gpu --screenshot=/tmp/skoed-dashboard.png \
  --window-size=1400,900 http://localhost:8080/
```

Screenshots aren't checked into the repo (binary content; rot fast)
but live in `docs/screenshots/` for releases.

## Build artifacts

`web/dist/` (gitignored) is the Vite output. The build pipeline copies
it into `apps/skoed/internal/api/static/dist` before `go build`, where
it's picked up by `//go:embed`. Bundle sizes after tree-shaking + gzip:

| Asset | Raw | gzip |
|---|---:|---:|
| `app.js` | 145 KB | 56 KB |
| `style.css` | 24 KB | 4.5 KB |
| Lazy view chunks (10 of them) | 2–12 KB each | 1–4 KB each |
| Total | ~260 KB | ~90 KB |
