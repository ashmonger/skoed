# M36 — Allowlist Scheduling + Per-Entry Metadata

## Implemented

- **Rich allowlist entries** — `AllowlistEntry` struct with `domain`, `expires_at` (optional RFC3339), `note` (free text), `schedule_id` (optional reference to a schedule).
- **Expiry enforcement** — expired entries are skipped at DNS resolution time; the domain falls back to blocklist evaluation.
- **Per-profile allowlist entries** — `POST /api/profiles/:id/allowlist/entries` / `GET /api/profiles/:id/allowlist/entries` / `DELETE /api/profiles/:id/allowlist/entries/:domain`.
- **Bulk import** — `POST /api/profiles/:id/allowlist/entries/import` accepts `{"domains": [...]}`, returns `{"added": N, "skipped": M}`.
- **Shared Allowlists (SAL)** — cross-profile reusable allowlists: `POST /api/shared-allowlists`, `GET /api/shared-allowlists/:id`, `PUT`, `DELETE`; assign to profiles via `profile.shared_allowlist_ids`.
- **UI** — Allowlist Entries tab shows note/expiry columns; Add Entry form exposes note + expiry fields; Shared Allowlists page with create modal and profile assignment.
- **Backward compatibility** — legacy `[]string` plain allowlists continue to work; plain entries stored as empty-value bbolt keys, rich entries stored as JSON.
- **Raft replication** — `SharedAllowlistUpsert` / `SharedAllowlistDelete` / `AllowlistEntryUpsert` / `AllowlistEntryDelete` Raft commands replicate to all nodes.

## Not Implemented

- Schedule-based enforcement (linking `schedule_id` to a time-gated allow window) — schedule binding logic is delivered in M37.
- Per-entry per-profile DNSSEC override — out of scope (handled by M38).
- Allowlist entry edit (PUT) — entries are replaced by delete + re-add.
- Bulk export of allowlist entries.

## Limitations

- Expiry is evaluated at query time against `time.Now()`. No background sweeper removes expired entries from the store; they remain visible in the API response with their `expires_at` field but are silently skipped during DNS resolution.
- `schedule_id` is stored and returned by the API but has no enforcement effect until M37 is active and the referenced schedule exists.
- The shared allowlist UI does not yet support editing the domain list inline; domains must be managed via API or per-profile entry endpoints.
