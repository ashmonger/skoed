# M37 — Schedule Binding Web UI

## Implemented

- **Schedule CRUD API** — `POST /api/schedules`, `GET /api/schedules`, `GET /api/schedules/:id`, `DELETE /api/schedules/:id`.
- **Time windows** — each schedule holds an array of `{day_of_week, start_hour, end_hour}` windows; `day_of_week` is 0–6 (Sunday=0).
- **Allowlist schedule enforcement** — when an `AllowlistEntry` has a `schedule_id`, the domain is only allowed during active windows; outside windows the blocklist wins.
- **Conflict validation** — `POST /api/schedules/:id/bindings` returns 409 if a binding for the same profile + day already exists.
- **Visual 7×24h grid editor** — click-to-toggle hour cells across all seven days; drag to fill multiple cells.
- **Template presets** — Weekdays (Mon–Fri 08:00–22:00), Weekends (Sat–Sun 08:00–22:00), Bedtime (daily 21:00–07:00), School Hours (Mon–Fri 08:00–15:00); presets overwrite the current grid selection.
- **Raft replication** — `ScheduleUpsert` / `ScheduleDelete` commands replicate schedules to all cluster nodes.
- **Config export** — schedules are included in the `/api/export` JSON dump.

## Not Implemented

- **Recurring override** — one-off date exceptions (e.g. school holiday) are not yet supported.
- **Schedule-level profile assignment** — schedules are linked per `AllowlistEntry.schedule_id`; there is no top-level "apply this schedule to entire profile" binding.
- **Visual conflict indicators** in the grid (overlapping windows across two entries on the same profile).
- **Schedule import** — bulk creation of schedules from JSON/CSV.

## Limitations

- Window enforcement is evaluated at DNS query time using the server's local clock (the leader's timezone if nodes are in different TZs).
- Dragging across the grid requires a mouse (no touch/mobile support); keyboard navigation toggles individual cells via Enter/Space.
- The Weekdays preset does not adjust for locale-specific first-day-of-week; Monday is always treated as day 1.
