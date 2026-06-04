---
x-tsid: TS-WebUi
x-fsid-links:
  - FS-WebUiServedAtRoot
  - FS-WebUiFirstRunSetup
  - FS-WebUiLoginFlow
  - FS-WebUiDashboardSurfaces
  - FS-WebUiBlocklistsCrud
  - FS-WebUiAllowlistCrud
  - FS-WebUiLocalDnsCrud
  - FS-WebUiQueryLog
  - FS-WebUiStatsDashboard
  - FS-WebUiClusterOps
  - FS-WebUiSettings
  - FS-WebUiAccount
  - FS-WebUiThemes
---

# TS-WebUi — Web UI architecture

A Vue 3 + Vite single-page app, compiled into a static bundle and embedded
into the dblock Go binary via `//go:embed`. Served from `GET /` and
`GET /assets/*`; every other unmatched route falls back to `index.html` so
the SPA's history router can take over. The API surface is unchanged from
M1+M2.

## Tech stack

| Layer | Choice | Rationale |
|---|---|---|
| Framework | Vue 3 (Composition API, `<script setup>`) | Smaller bundle than React; first-class TS support; comfortable for component-heavy admin UIs |
| Bundler | Vite 6 | Fast HMR in dev; rollup-based prod build |
| Language | TypeScript ~5.7 | Type-safety on the API client; catches misspellings against `internal/api/handlers/*` JSON shapes |
| Routing | Vue Router 4 | History mode (clean URLs); guards for auth gating |
| State | Pinia 2 | Small, idiomatic Vue 3 store; less ceremony than Redux |
| HTTP | axios 1.x | Interceptors make Basic Auth header injection trivial |
| Icons | `@heroicons/vue` (24/outline) | Bundled per-icon (~0.5 KB each) |
| Styling | Tailwind 3 + custom design tokens | Lets us swap palettes via CSS variables / dark class without rewriting components |

## Repo layout

```
web/
├── package.json                # npm scripts: dev, build, type-check
├── vite.config.ts              # alias @/* → src/*; dev proxy /api → :8080
├── tsconfig.json
├── tailwind.config.js          # design tokens: Monokai-Solarized (light) + Monokai (dark)
├── postcss.config.js
├── index.html                  # SPA shell <div id="app">
├── public/
│   └── favicon.svg
└── src/
    ├── main.ts                 # createApp + pinia + router; applies theme before first paint
    ├── App.vue                 # <RouterView/>
    ├── style.css               # @tailwind directives + dark-mode token overrides
    ├── router.ts               # routes + auth guard
    ├── api/
    │   ├── client.ts           # axios instance, Basic Auth interceptor, creds in sessionStorage
    │   ├── endpoints.ts        # typed wrappers per resource
    │   └── types.ts            # mirrors of management-api.openapi.yaml JSON shapes
    ├── stores/
    │   ├── auth.ts             # login/setup/logout; isAuthenticated, ready, isSetup
    │   └── theme.ts            # mode (light/dark) + palette; persisted in localStorage
    ├── layouts/
    │   └── Shell.vue           # sidebar + topbar + RouterView (the "logged in" layout)
    ├── views/                  # one file per route
    │   ├── Login.vue
    │   ├── Setup.vue
    │   ├── Dashboard.vue
    │   ├── Blocklists.vue
    │   ├── Allowlist.vue
    │   ├── LocalDNS.vue
    │   ├── QueryLog.vue
    │   ├── Stats.vue
    │   ├── Cluster.vue
    │   ├── Settings.vue
    │   └── Account.vue
    └── components/             # small shared widgets
        ├── StatTile.vue
        └── Breakdown.vue
```

## Build pipeline

```
cd web
npm install                                      # 156 packages, no security advisories
npm run build                                    # vue-tsc --noEmit + vite build → web/dist/
cp -r web/dist apps/dblock/internal/api/static/dist
cd apps/dblock && CGO_ENABLED=0 go build -o dblock ./cmd/dblock/
```

The dblock binary's `internal/api/static/embed.go` declares
`//go:embed all:dist` so `go build` ingests whatever has been copied into
that path. Production releases run the build pipeline end-to-end; Go-only
contributors who don't touch the UI can leave `apps/dblock/internal/api/static/dist`
empty — the router gracefully degrades to `404 Not Found` on `/` if no
`index.html` is found (`static.HasIndex()` short-circuits the SPA handler).

## Routing and auth gate

`router.ts` declares two top-level routes outside the shell (`/login`,
`/setup`) and a `Shell` parent that wraps every authenticated view in the
sidebar+topbar chrome. A `beforeEach` guard:

1. Lazily calls `auth.probe()` once per session to check whether the
   backend already has admin credentials configured. The probe is a simple
   `GET /api/v1/blocklists`: a 401 means credentials are required (auth
   complete), any error means the server is unreachable or never set up.
2. If the user lacks credentials AND auth is required → redirect to
   `/login` (or `/setup` if the server is unconfigured).
3. If the user is on `/setup` after credentials have been set → bounce to
   `/login`.

Credentials live in `sessionStorage` so they're lost on tab close. The
axios request interceptor reads them per call and writes
`Authorization: Basic <base64>`. The response interceptor exposes 401s for
views to handle (no auto-logout, since a 401 during setup race is benign).

## Theme system

Two palettes:

- **Monokai-Solarized** (light default): solarized base3/base2 surfaces,
  solarized blue/green/yellow/red accents — softer, easier on the eyes,
  closer to AdGuard Home's look.
- **Monokai** (vivid dark): canonical Monokai colors — `#272822` bg,
  `#A6E22E` green, `#66D9EF` cyan, `#F92672` pink, `#FD971F` orange.

Tokens live in `tailwind.config.js` under `theme.extend.colors`. Components
NEVER inline hex; they reference design tokens like `bg-bg-card`, `text-fg`,
`text-success`. The `dark:` variant is class-based (`class="dark"` on
`<html>`) and overrides token colors via `style.css` `@layer base` blocks.

Theme state lives in `stores/theme.ts`: `mode` (`light` | `dark`) and
`palette` (`monokai` | `monokai-solarized`). Both persist to
`localStorage`. `applyOnStartup()` is invoked from `main.ts` BEFORE the
Vue app mounts so the first paint already has the right palette.

## Backend integration

```
internal/api/static/
├── embed.go               # //go:embed all:dist + HasIndex/FS helpers
└── dist/                  # web/dist/ copied here at build time (gitignored if we want; currently committed)
```

`internal/api/app.go::Router` now ends with `r.NotFound(serveSPA)`. The
handler:

- Returns 404 if `static.HasIndex()` is false (Go-only build with no UI).
- Returns 404 for any path under `/api/` (defensive: routes are first
  match, so this only triggers for genuinely unrouted API paths).
- For `GET /` and `GET /<asset path>`: tries to open the file; if found
  serves it via `http.ServeFileFS`.
- Otherwise (SPA route like `/dashboard`, `/login`): serves `index.html`
  so the client router can render.

## Tests

- HTTP-level: `TestAuthUnauthenticatedUiRedirect` confirms `GET /` returns
  either 200 with the SPA shell (`<div id="app">`) when the bundle is
  embedded, or 401/302/404 when it isn't. All 103 M1+M2 acceptance tests
  continue to pass against a binary with the SPA embedded.
- TypeScript: `npm run type-check` (a.k.a. `vue-tsc --noEmit`) gates the
  build; misshapen API calls fail to compile.
- Manual: Chromium-based screenshot pass per view documented in
  `web/UI.md`. Browser-driven E2E in CI is out of scope at M2.6.

## Bundle budget

After tree-shaking + gzip (vite build):

| Bundle | Raw | gzip |
|---|---:|---:|
| `app.js` (Vue core + router + pinia + axios) | 145 KB | 56 KB |
| `style.css` (Tailwind, purged) | 24 KB | 4.5 KB |
| Per-view chunks (lazy-loaded) | 2–12 KB each | 1–4 KB each |
| Total embedded | ~260 KB | ~90 KB |

Binary growth: `dblock` went from ~10.5 MB to ~11 MB after embed — well
inside the 25 MB roadmap budget.

## Known limitations

- No real-time push: views poll on a 10-second interval. Future work could
  switch to SSE on `/api/v1/cluster/events` when M3 adds events.
- No i18n; English only.
- Theme palette CSS overrides live in `style.css` rather than CSS variables;
  this is fine for two palettes but doesn't scale to N. Refactor to CSS
  vars when a third palette appears.
- The static handler doesn't set long-lived `Cache-Control` headers on
  `/assets/*`. Vite's content-hashing is also disabled (asset names stay
  stable across rebuilds) — operators are expected to hard-reload after
  upgrades. Acceptable for an admin UI.
