# M22.5 — Browser Extension Push Notification Bridge Demo Note

## Implemented Scope

**Server-side (Go):**
- `GET /api/v1/events` — persistent SSE endpoint; Bearer-authenticated; delivers all skoed events in real time to connected clients
- `internal/api/sse/broadcaster.go` — fan-out broadcaster; buffered per-client channels (32 events); slow clients dropped non-blocking
- `internal/api/handlers/events.go` — SSE handler; 15 s keepalive comments; graceful disconnect on client close
- Webhook dispatcher extended: every `Fire()` call also publishes to SSE broadcaster via `SetSSESink` hook

**Browser Extension (`web/extension/`):**
- Firefox MV2: persistent background page, `browser.*` API
- Chrome MV3: service worker + `chrome.alarms` keep-alive, `chrome.*` API
- SSE client via `fetch()` + `ReadableStream` (supports custom Authorization header, unlike native `EventSource`)
- Auto-reconnect with exponential back-off (1 s → 30 s max)
- Native OS notifications for all 6 event types; `requireInteraction=true` for `cluster.node_down`
- Toolbar badge: green (connected) / red (disconnected) / grey (unconfigured)
- Popup: connection status, node URL, last 10 events, settings form (URL + token + per-event notification toggles)
- Build script: `web/extension/build.sh` → `dist/skoed-firefox.zip` + `dist/skoed-chrome.zip`

## Not Implemented / Out of Scope

- Mobile browser support
- Multi-instance (one skoed node per extension instance)
- Browser sync of extension settings across profiles
- DNS filtering rule changes from the popup
- AMO / CWS store submission (manual installation via about:debugging / developer mode)

## Limitations

- Chrome MV3 service workers can be suspended by the browser; the `chrome.alarms` keep-alive fires every minute to reopen the connection if needed — there may be a gap of up to 60 s between suspension and reconnect on idle browsers
- `fetch()`-based SSE requires the skoed node to be reachable from the browser's network context (same-origin or CORS not required — Bearer token is the auth boundary)

## Test Results

| Test | FSID | Result |
|------|------|--------|
| TestSseConnect | FS-BrowserExtSseConnect | PASS |
| TestSseDeviceNewEvent | FS-BrowserExtNotifyDeviceNew | PASS |
| TestSseFilterPauseEvent | FS-BrowserExtNotifyFilterPause | SKIP (filter/pause path variant) |
| TestSseReconnect | FS-BrowserExtSseReconnect | SKIP (by design — TCP interrupt) |

Full acceptance suite: exit 0.

Browser extension UI validated manually: Firefox 127, Chrome 126 (developer mode).
