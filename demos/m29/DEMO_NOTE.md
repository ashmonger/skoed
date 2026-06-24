# M29 — Live Query Stream

## Implemented

- `GET /api/v1/query-log/stream` — authenticated SSE endpoint (Bearer token)
- Each DNS query processed by the node is pushed immediately as an `event: query` SSE frame
- JSON payload per event: `domain`, `type`, `client_ip`, `profile_id`, `result`, `duration_ms`, `timestamp`
- Query-string filters: `?profile_id=<id>`, `?result=blocked|forwarded|cached|local|nxdomain`, `?domain=<substring>`
- Keep-alive SSE comment (`: keep-alive`) sent every 15 seconds to prevent proxy timeouts
- Clean client-disconnect handling via `r.Context().Done()`
- Subscriber fan-out in `QueryLog` is non-blocking: slow consumers drop events rather than stalling `Append`

## Not implemented

- Cluster-wide fan-out (each node streams its own queries only; no cross-node aggregation)
- Historical backfill of queries made before the connection opened
- Per-query DNSSEC chain details on the stream
- WebSocket alternative transport

## Validation

7/7 acceptance tests pass (Docker harness):
- `TestLiveQueryStreamConnect` — 200 text/event-stream
- `TestLiveQueryStreamEventShape` — all required JSON fields present
- `TestLiveQueryStreamBlockedQuery` — blocked result streamed correctly
- `TestLiveQueryStreamFilterByResult` — forwarded queries hidden from `?result=blocked` stream
- `TestLiveQueryStreamUnauthenticated` — 401 without token
- `TestLiveQueryStreamHeartbeat` — keep-alive comment within 20s
- `TestLiveQueryStreamDisconnect` — node continues serving DNS after disconnect

## Proxmox Enterprise Validation (2026-06-24)

3-node Raft cluster redeployed from scratch: CT200/201/202 (skoed-1/2/3).
3 client containers: CT204 (kids, 10.0.0.50), CT205 (adults, 10.0.0.51), CT206 (IoT, 10.0.0.52).
Binary: `skoed v0.2.3-4-ga822919` (M29 commit).

Real SSE events captured:
- `example.com` from CT204 → `cached`
- `spotify.com` from CT204 → `forwarded`
- `dns.google` from CT204 → `blocked` (trackers blocklist)
- `github.com` from CT204 → `forwarded`
- `netflix.com` from CT205 → `forwarded`
- `doubleclick.net` from CT205 → `blocked`
- `iot-hub.example.com` from CT206 → `forwarded`
- `telemetry.vendor.com` from CT206 → `blocked`

Demo: `ss-29-01-enterprise-sse.html` — animated terminal replay of real SSE stream.
