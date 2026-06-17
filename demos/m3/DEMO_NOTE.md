# DEMO NOTE — M3 Parental Control + DoH/DoT Detection

## Scope

Adds the full parental control feature set: per-device profiles, time-based schedules, category-based blocklists, SafeSearch enforcement, and DoH/DoT bypass detection.

### Implemented

**Backend:**
- Profiles (`GET/POST/PATCH/DELETE /api/v1/profiles`): per-subnet or per-IP client grouping, blocklist assignments, SafeSearch enforcement (Google, Bing, YouTube, DuckDuckGo)
- Schedules (`GET/POST/PATCH/DELETE /api/v1/schedules`): weekly time windows with `block_only_inside` or `allow_only_inside` mode
- Schedule bindings (`POST/DELETE /api/v1/schedules/{id}/bindings`): attach schedules to (profile, blocklist) pairs
- Categories (`GET /api/v1/categories`, `/enable`, `/disable`): curated blocklist catalog (social, ads, adult, gaming, news, etc.) per profile
- SafeSearch enforcement: DNS rewrite for google.com → safeSearch.google.com, etc.
- DoH/DoT detection via embedded resolver IP database: `GET /api/v1/clients/{ip}/doh-status`

**Web UI:**
- `/profiles` — full CRUD, blocklist checklist, SafeSearch toggles, client IP assignment
- `/schedules` — CRUD with time-window editor and inline binding panel
- `/categories` — catalog browser with per-profile enable/disable
- Dashboard stats: DoH attempt counter widget
- 4 UI themes (Monokai Solarized, Vivid, Blue, Pro) with dark/light toggle

### Not implemented (M3 non-goals)

- Drag-and-drop schedule editor
- GET endpoint to enumerate existing bindings (added in M17 as the "M3.1 gap")
- Per-domain granular audit trail for SafeSearch redirects
- I18n

## Detail files

See `web-ui.md` for Web UI implementation notes and `themes-cluster.md` for the 5-node theme validation cluster.

## Limitations

- Binding list in UI only shows bindings created in the current session (no server-side enumeration until M17)
- DoH category card shows no upstream URL (embedded in binary)
