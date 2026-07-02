# M42 — Query Log Stream Enhancements (Backfill + WebSocket)

## Implemented

**Backfill on connect:**
- `GET /api/v1/query-log/stream?backfill=N` — on connection, the server replays the last N log entries as `event: query` frames before going live. Default N is 0 (no backfill); maximum is 500 (larger values are silently capped).
- Entries are taken from the in-memory ring buffer under a snapshot lock before subscribing, so no query arrives twice.
- A synthetic `event: backfill_end` frame with `data: {}` marks the boundary between replayed history and live events.
- All active filters (`?profile_id=`, `?result=`, `?domain=`, `?dnssec_status=`) apply to backfill entries as well as live events.

**WebSocket transport:**
- `GET /api/v1/query-log/ws` — provides the same live DNS query stream as the SSE endpoint but over a WebSocket connection (`HTTP 101 Switching Protocols`).
- Authentication via `Authorization: Bearer` header on the upgrade request (same as SSE).
- Server sends text frames with the same JSON payload as SSE `data:` lines.
- Server sends `{"type":"keep-alive"}` text frames every 15 s when no queries arrive.
- Connection is read-only; incoming frames from the client are discarded.

## Not Implemented

- **Bidirectional WebSocket commands** — the WebSocket endpoint is a read-only stream.
- **Cluster-wide aggregation via WebSocket** — the M41 `?cluster=true` aggregation is SSE-only; the WebSocket endpoint is single-node only.
- **Persistent subscriptions** — neither SSE nor WebSocket subscriptions survive a server restart.
- **Backfill on cluster stream** — `?backfill=N` on `?cluster=true` replays the local node's ring buffer only.

## Limitations

- The backfill window is bounded by the in-memory ring buffer size (default 1000 entries); if the buffer holds fewer than N entries, all available entries are replayed.
- WebSocket authentication happens at HTTP upgrade time; if the Bearer token expires during a long-lived connection, the session continues (no mid-stream re-auth).
- The gorilla/websocket library (`github.com/gorilla/websocket v1.5.3`) is now a dependency.
