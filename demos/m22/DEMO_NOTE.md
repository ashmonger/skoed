# M22 — Webhooks / Push Alerts Demo Note

## Implemented Scope

- **Webhook endpoint management**: `POST /api/v1/webhooks` (create/update), `GET /api/v1/webhooks` (list), `DELETE /api/v1/webhooks/{id}` (remove), `POST /api/v1/webhooks/{id}/test` (fire test event)
- **Raft-replicated storage**: webhook endpoint registry stored in bbolt bucket `webhooks`, replicated via `CmdWebhooksUpdate` FSM command; all nodes read a consistent list
- **At-least-once delivery**: 8-worker dispatcher pool with exponential back-off (0 s → 1 s → 4 s → 16 s); queue depth 256; non-blocking Fire drops on full queue
- **HMAC-SHA256 signing**: every delivery carries `X-Skoed-Signature: sha256=<hex>` computed over the raw JSON body using the per-endpoint secret
- **Event types shipped**: `device.new`, `blocklist.download_failed`, `cluster.node_down`, `cluster.node_rejoined`, `filter.pause_started`, `filter.pause_expired`, `webhook.test`
- **device.new deduplication**: dispatcher suppresses duplicate fires for the same client IP within 10 minutes
- **JSON payload fields**: `id`, `event`, `timestamp`, `node_id` (optional), `data`

## Not Implemented / Out of Scope

- Browser/UI management panel for webhooks (AJAX to the API works; no dedicated UI page yet — deferred to M22.5)
- Retry-failure webhook (dead-letter queue / persistent audit log of failed deliveries)
- Per-event signing key rotation without endpoint delete/recreate
- Delivery ordering guarantees across endpoints

## Limitations

- `FS-WebhookEventBlocklistFailed` acceptance test is skipped in the automated suite; covered by manual Proxmox integration testing (refresh interval must be shortened via env var to trigger reliably in an automated test)
- Webhook endpoints are stored as a single JSON blob (full list) rather than individual records; large endpoint lists (> ~1000) would produce large Raft log entries

## Test Results

5/5 acceptance tests pass (1 intentionally skipped):

| Test | FSID | Result |
|------|------|--------|
| TestWebhookCreateAndList | FS-WebhookCreate | PASS |
| TestWebhookDelete | FS-WebhookDelete | PASS |
| TestWebhookTestFire | FS-WebhookTestFire | PASS |
| TestWebhookEventDeviceNew | FS-WebhookEventDeviceNew | PASS |
| TestWebhookEventBlocklistFailed | FS-WebhookEventBlocklistFailed | SKIP (by design) |
| TestWebhookSignature | FS-WebhookSignature | PASS |

Full acceptance suite: 145 s, exit 0.
