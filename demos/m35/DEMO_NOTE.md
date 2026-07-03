# M35 – Filtering Pause Enhancements: Demo Note

## Implemented Scope

### Per-Client Pause (`FS-PerClientPauseActivates`, `FS-PerClientPauseStateVisible`, `FS-PerClientPauseCancelledEarly`, `FS-PerClientPauseOtherClientsUnaffected`)
- `POST /api/v1/profiles/{id}/pause` accepts an optional `client_ips` array
- When `client_ips` is set, only those IPs bypass filtering; other clients in the profile remain filtered
- `GET /api/v1/profiles/{id}/pause` returns `client_ips` in the response
- `DELETE /api/v1/profiles/{id}/pause` cancels a per-client pause; filtering resumes for the paused IPs immediately after filter engine rebuild
- Scope is reflected as `"per-client"` in history entries; a global profile pause (no `client_ips`) is scoped as `"profile"`

### Pause History (`FS-PauseHistoryRecorded`, `FS-PauseHistoryCappedAt50`, `FS-PauseHistoryNotFoundForUnknownProfile`)
- `GET /api/v1/profiles/{id}/pause/history` returns a list of up to 50 pause history entries per profile
- Each entry records: `started_at`, `ended_at` (zero if still active), `scope`, `reason`, `client_ips`
- History is capped at 50 entries (oldest entries are dropped when the cap is exceeded)
- Returns 404 for unknown profile IDs
- History is Raft-replicated and consistent across all cluster nodes

### Pause-Expiry Webhook (`FS-PauseExpiryWebhookFired`)
- A background goroutine polls profiles every 5 seconds and fires a `filter.pause_expired` webhook event when a pause's `resumes_at` deadline passes
- The event payload includes `profile_id`, `reason`, and `client_ips` (if it was a per-client pause)

### New Dynamic Client Alert (`FS-NewDynamicClientAlertEndpoint`, `FS-NewDynamicClientAlertDismissed`)
- `GET /api/v1/clients/new-dynamic` returns a list of client IPs that have been seen for the first time (tracked per-node in bbolt, not Raft-replicated)
- `POST /api/v1/clients/new-dynamic/dismiss` with `{"client_ip": "..."}` removes an IP from the list (Raft-replicated dismiss, so all nodes stop showing the IP)

## Not Implemented in M35

- **Frontend UI**: No pause management UI (duration picker, countdown badge, per-client IP input, new-dynamic alert banner) — backend API only
- **Webhook retry / delivery confirmation**: Existing webhook dispatcher behavior; no new guarantees
- **Per-node new-dynamic client tracking**: Each node tracks clients it has served DNS queries to; a client that only queries follower nodes does not appear in leader's list unless that client queries the leader too

## Limitations

- The per-client pause check uses IP matching; CIDR ranges are not supported (exact IP match only)
- The `filter.pause_expired` webhook may fire slightly after the `resumes_at` deadline (up to ~5s delay from the 5s polling interval)
- New dynamic client entries are local to each node's bbolt store. The `dismiss` command is Raft-replicated so all nodes remove the entry, but each node only tracks IPs it observed locally.

## Proxmox Validation

- 3-node cluster: skoed-01 (leader), skoed-02, skoed-03
- Binary: v0.3.5-dev (commit 0e0b3c7)
- **14/14 functional API checks** pass against live cluster
- **9/9 replication checks** pass (per-client pause and history replicated to all 3 nodes)
- **10/10 local acceptance tests** pass (covering all 10 FSIDs)

## Screenshots

- `ss-35-01-profiles-list.png` — profiles list showing the M35 demo profile
- `ss-35-02-profile-detail-pause-active.png` — profile with an active per-client pause
- `ss-35-03-api-responses.png` — pause state, history, and new-dynamic API responses
- `ss-35-04-cluster-in-sync.png` — all 3 nodes in_sync on v0.3.5-dev
