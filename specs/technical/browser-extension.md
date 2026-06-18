# Technical Specification — Browser Extension Push Notification Bridge

x-tsid: TS-BrowserExtension
x-fsid-links:
  - FS-BrowserExtSseConnect
  - FS-BrowserExtSseReconnect
  - FS-BrowserExtNotifyDeviceNew
  - FS-BrowserExtNotifyClusterDown
  - FS-BrowserExtNotifyBlocklistFailed
  - FS-BrowserExtNotifyFilterPause
  - FS-BrowserExtBadgeConnected
  - FS-BrowserExtPopupStatus
  - FS-BrowserExtPopupSettings
  - FS-BrowserExtFirefoxMv2
  - FS-BrowserExtChromeMv3

---

## 1. Server-Side: SSE Endpoint

### GET /api/v1/events

**Authentication**: Bearer token (same `Authorization: Bearer <token>` as the rest of the management API).

**Response**:
- Status: `200 OK`
- Content-Type: `text/event-stream`
- Cache-Control: `no-cache`
- Connection: `keep-alive`
- Body: newline-delimited SSE frames, indefinitely

**SSE frame format**:
```
event: <EventType>
data: <JSON payload>
id: <event-id>

```

The JSON payload is identical to the webhook event body (fields: `id`, `event`, `timestamp`, `node_id`, `data`).

**Keepalive**: server sends a comment line every 15 seconds to prevent proxies from closing the connection:
```
: keepalive

```

**Broadcast**: all events fired by the local node's webhook dispatcher are also broadcast to all open SSE clients on that node. Events from remote nodes are NOT forwarded (the extension connects to its configured node only).

**Concurrency**: the endpoint supports multiple simultaneous SSE clients per node. Each client gets its own goroutine; the node uses a fan-out broadcaster protected by a sync.RWMutex.

**Error**: if the token is invalid, the endpoint returns `401 Unauthorized` (JSON, not SSE) immediately before upgrading.

---

## 2. Server-Side: Implementation

### internal/api/sse/broadcaster.go

```
type Broadcaster struct {
    mu      sync.RWMutex
    clients map[chan []byte]struct{}
}

func (b *Broadcaster) Subscribe() (ch chan []byte, cancel func())
func (b *Broadcaster) Publish(payload []byte)
```

- `Subscribe()` allocates a buffered channel (32 events); `cancel()` removes it and closes the channel.
- `Publish()` fans out the payload to all subscribers under read lock; slow clients are dropped (non-blocking send).

### internal/api/handlers/events.go

```go
// GET /api/v1/events
func (h *EventsHandler) StreamEvents(w http.ResponseWriter, r *http.Request)
```

- Sets SSE headers.
- Calls `broadcaster.Subscribe()`.
- Writes each message from the channel as an SSE frame.
- Sends `: keepalive\n\n` on a 15 s ticker.
- Returns when the client disconnects (`r.Context().Done()`).

### Wiring in dispatcher.go

The webhook dispatcher's `Fire()` method is extended: after queuing to endpoint workers, it also calls `broadcaster.Publish(eventJSON)` so SSE clients receive events in real time.

---

## 3. Browser Extension Package Structure

```
web/extension/
├── manifest-firefox.json     # Manifest V2
├── manifest-chrome.json      # Manifest V3
├── background/
│   ├── background.js         # Firefox: persistent background page
│   └── service-worker.js     # Chrome: MV3 service worker
├── popup/
│   ├── popup.html
│   ├── popup.js
│   └── popup.css
├── icons/
│   ├── icon-16.png
│   ├── icon-32.png
│   └── icon-128.png
└── build.sh                  # produces skoed-firefox.zip + skoed-chrome.zip
```

No npm/bundler dependency — plain ES modules, no external libraries.

---

## 4. Extension: Storage Schema

Stored in `browser.storage.local` (Firefox) / `chrome.storage.local` (Chrome):

```json
{
  "skoed_url": "https://skoed.home:8443",
  "api_token": "<bearer-token>",
  "notify_events": ["device.new", "cluster.node_down", "blocklist.download_failed",
                    "filter.pause_started", "filter.pause_expired"],
  "recent_events": [ /* last 10 events, newest first */ ]
}
```

---

## 5. Extension: SSE Client (background script)

```
openConnection()
  → EventSource(skoed_url + "/api/v1/events", {headers: {Authorization: "Bearer " + token}})
  → on message: updateBadge(), storeRecentEvent(), maybeNotify()
  → on error: scheduleReconnect() with back-off [1s, 2s, 4s, 8s, 16s, 30s, 30s, …]
```

**Chrome MV3 keep-alive**: the service worker uses `chrome.alarms` (1-minute interval) to ping the SSE connection and prevent the worker from being suspended.

---

## 6. Extension: Notifications

Uses `browser.notifications.create()` / `chrome.notifications.create()` with:

| Field             | Value                                    |
|-------------------|------------------------------------------|
| type              | `"basic"`                                |
| iconUrl           | `icons/icon-128.png`                     |
| title             | Event-specific (see functional spec)     |
| message           | Event-specific                           |
| requireInteraction| `true` for `cluster.node_down` only      |

Clicking a notification opens the skoed web UI at the relevant page via `browser.tabs.create()`.

---

## 7. Extension: Toolbar Badge

| State         | Badge text | Badge background |
|---------------|-----------|-----------------|
| Connected     | (empty)   | `#27ae60` green |
| Disconnected  | `!`       | `#e74c3c` red   |
| Unconfigured  | `?`       | `#95a5a6` grey  |

---

## 8. Acceptance Test Strategy

The server-side SSE endpoint is testable in Go acceptance tests:
- Start a node, open an SSE connection (HTTP chunked reader), fire a test event via `POST /api/v1/webhooks/{id}/test`, verify the SSE frame arrives within 2 s.

The browser extension UI (popup, badge) is not testable in the Go acceptance suite. It is validated manually against Firefox and Chrome in the demo environment.

---

## 9. Build Artifacts

`make build-extension` produces:
- `dist/skoed-firefox.zip` — Firefox add-on (MV2), installable via `about:debugging`
- `dist/skoed-chrome.zip` — Chrome extension (MV3), installable via developer mode

Both zips contain only the files in `web/extension/` with the appropriate manifest file renamed to `manifest.json`.
