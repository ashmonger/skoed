# Webhook / Push Alerts — Technical Specification

x-tsid: TS-Webhooks
x-fsid-links: [FS-WebhookCreate, FS-WebhookDelete, FS-WebhookTestFire, FS-WebhookEventDeviceNew, FS-WebhookEventBlocklistFailed, FS-WebhookSignature]

---

## 1. Overview

skoed delivers push notifications to operator-configured HTTP endpoints when
specific cluster events occur. Endpoint configuration is Raft-replicated so
every node in the cluster has an identical view of the webhook registry. Each
node fires webhooks independently for events it observes locally; there is no
cross-node deduplication (non-goal).

---

## 2. Data Structures

### 2.1 WebhookEndpoint (config and Raft payload)

```go
type WebhookEndpoint struct {
    ID      string   `yaml:"id"      json:"id"`       // opaque, server-generated UUID v4
    URL     string   `yaml:"url"     json:"url"`      // HTTPS or HTTP; must be absolute
    Secret  string   `yaml:"secret"  json:"secret"`   // used for HMAC-SHA256 signing; never empty
    Events  []string `yaml:"events"  json:"events"`   // empty slice = subscribe to all event types
    Enabled bool     `yaml:"enabled" json:"enabled"`
}
```

### 2.2 Config location

`WebhookEndpoint` entries live in `Config.Webhooks []WebhookEndpoint` (config package).
The field is YAML-tagged `webhooks` and JSON-tagged `webhooks`.
`Config.Defaults()` treats a nil slice as an empty slice (no action needed).

### 2.3 Raft command

```
CmdWebhooksUpdate CommandKind = "webhooks.update"
```

Payload:

```go
type WebhooksUpdatePayload struct {
    Webhooks []config.WebhookEndpoint `json:"webhooks"`
}
```

All four API mutations (create, delete, enable/disable, update) rewrite the
full `Config.Webhooks` slice in a single `CmdWebhooksUpdate` apply so that no
partial state can exist between nodes.

---

## 3. API Endpoints

All endpoints require Bearer authentication (same as the rest of the
management API). Request and response bodies are `application/json`.

### 3.1 POST /api/v1/webhooks — Create endpoint

**Request body:**

```json
{
  "url":    "https://example.com/hook",
  "secret": "supersecret",
  "events": ["device.new", "cluster.node_down"]
}
```

- `url`: required, must be a valid absolute HTTP/HTTPS URL.
- `secret`: required, non-empty string.
- `events`: optional; if absent or empty, the endpoint receives all event types.

**Response 201 Created:**

```json
{
  "id":      "a3f1e2d4-...",
  "url":     "https://example.com/hook",
  "secret":  "supersecret",
  "events":  ["device.new", "cluster.node_down"],
  "enabled": true
}
```

**Error responses:**

| Status | Condition |
|--------|-----------|
| 400    | Missing or invalid `url`; missing or empty `secret`; unknown event type in `events` |
| 401    | Missing or invalid Bearer token |

### 3.2 GET /api/v1/webhooks — List endpoints

**Response 200 OK:**

```json
[
  {
    "id":      "a3f1e2d4-...",
    "url":     "https://example.com/hook",
    "secret":  "supersecret",
    "events":  ["device.new"],
    "enabled": true
  }
]
```

Returns an empty array when no endpoints are configured.

### 3.3 DELETE /api/v1/webhooks/{id} — Remove endpoint

**Response 204 No Content** — endpoint removed.

**Error responses:**

| Status | Condition |
|--------|-----------|
| 404    | No endpoint with the given id |
| 401    | Missing or invalid Bearer token |

### 3.4 POST /api/v1/webhooks/{id}/test — Fire test event

Immediately delivers a single `webhook.test` event payload to the endpoint URL.
The call is synchronous (from the API handler's perspective): the handler waits
for the first delivery attempt and returns whether it succeeded.

**Response 200 OK:**

```json
{ "delivered": true }
```

**Response 200 OK (delivery failed):**

```json
{ "delivered": false, "error": "connection refused" }
```

HTTP 200 is returned in both cases so the caller can distinguish between
"API request failed" and "delivery to the remote endpoint failed".

**Error responses:**

| Status | Condition |
|--------|-----------|
| 404    | No endpoint with the given id |
| 401    | Missing or invalid Bearer token |

---

## 4. Event Payload

Every delivery (including test events) uses the same JSON envelope:

```json
{
  "event":     "device.new",
  "timestamp": "2026-06-18T12:34:56Z",
  "node_id":   "node-1",
  "data":      { ... }
}
```

| Field       | Type   | Description |
|-------------|--------|-------------|
| `event`     | string | One of the event type strings listed in §5 |
| `timestamp` | string | RFC 3339 UTC timestamp of when the event occurred on the local node |
| `node_id`   | string | The node that generated the event |
| `data`      | object | Event-specific fields; see §5 |

---

## 5. Event Type Registry

| Event type                  | Trigger | `data` fields |
|-----------------------------|---------|---------------|
| `device.new`                | First DNS query from an IP address not previously seen in the query log | `{"client_ip": "192.168.1.42"}` |
| `blocklist.download_failed` | A URL-source blocklist refresh returns an HTTP error or network failure | `{"blocklist_id": "ads", "error": "connection refused"}` |
| `cluster.node_down`         | A cluster peer is detected as unreachable by the Raft leader | `{"node_id": "node-2"}` |
| `cluster.node_rejoined`     | A previously down peer reconnects to the cluster | `{"node_id": "node-2"}` |
| `filter.pause_started`      | A global or profile filtering pause is activated | `{"scope": "global", "profile_id": "", "resumes_at": "2026-06-18T13:00:00Z"}` |
| `filter.pause_expired`      | A filtering pause reaches its scheduled end time | `{"scope": "profile", "profile_id": "kids"}` |
| `webhook.test`              | Operator triggered POST /{id}/test | `{"message": "test event from skoed"}` |

`scope` in pause events is either `"global"` or `"profile"`. When `scope` is
`"global"` the `profile_id` field is an empty string.

---

## 6. Signature

Every outbound POST request carries:

```
X-Skoed-Signature: sha256=<lowercase hex>
```

The hex value is `HMAC-SHA256(key=endpoint.Secret, message=raw_request_body)`.

The receiver MUST NOT treat a request as authentic unless the computed HMAC
matches the header value. Constant-time comparison is recommended.

Signing is applied after JSON serialisation and before any transport
encoding, so the signed bytes are exactly the bytes that appear in the HTTP
request body.

---

## 7. Delivery Semantics

- **At-least-once**: each event triggers one delivery attempt per matching
  endpoint. Retries are applied for transient failures.
- **Retry schedule**: up to 3 attempts with exponential backoff — 1 s, 4 s,
  16 s between successive attempts (attempt 1 is immediate).
- **Timeout per attempt**: 10 seconds.
- **Failure handling**: when all 3 attempts fail, the error is written to the
  audit log (`action: "webhook.delivery_failed"`). Delivery failures never
  block the caller or the Raft FSM.
- **Non-blocking**: webhook delivery runs in a goroutine spawned after the
  event-producing operation completes. It must never delay query processing,
  Raft commits, or API responses.
- **Disabled endpoints**: endpoints with `enabled: false` receive no deliveries.
- **Event filtering**: an endpoint with a non-empty `events` list only receives
  events whose type appears in that list.

---

## 8. Sequence Diagram

```
[Event source]  →  skoed node
                      │
                      ├─ match enabled webhook endpoints subscribed to event type
                      │
                      └─ goroutine per endpoint
                              │
                              ├─ attempt 1 (immediate)
                              │     ├─ success → done
                              │     └─ failure
                              │           ├─ wait 1s
                              │           ├─ attempt 2
                              │           │     ├─ success → done
                              │           │     └─ failure
                              │           │           ├─ wait 4s
                              │           │           ├─ attempt 3
                              │           │           │     ├─ success → done
                              │           │           │     └─ failure → audit log
                              │           │           └─ done
                              │           └─ done
                              └─ done
```

---

## 9. Non-Goals

- Email or SMS delivery.
- Per-client device alerts beyond `device.new`.
- Durable message queue or guaranteed exactly-once delivery.
- Fan-out deduplication across cluster nodes (each node fires independently).
- Webhook payload history or replay API.
- Webhook endpoint ordering or priority.
