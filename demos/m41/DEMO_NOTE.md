# M41 — Cluster-wide Live Query Stream

## Implemented

- **Fan-in aggregation** — `GET /api/v1/query-log/stream?cluster=true` opens a server-sent events connection to any cluster node and receives real-time DNS query events from **all** nodes in the Raft cluster.
- **`node_id` field** — every SSE event carries a `node_id` field identifying which cluster node processed the DNS query (e.g. `"node_id":"skoed-02"`).
- **Graceful degradation** — if a peer node's stream subscription fails (node down, network partition), the aggregator logs the failure and continues streaming from the remaining nodes. A synthetic `event: node_unavailable` frame is sent to the client with `{"node_id":"<unreachable-node>"}`.
- **Deduplication** — a 100 ms window keyed on `(node_id, payload)` prevents duplicate events during momentary double-subscription caused by leader re-elections.
- **Filters applied after fan-in** — `?profile_id=`, `?result=`, `?domain=`, `?dnssec_status=` are all honoured on the aggregated stream.
- **Inter-node authentication** — peer subscriptions use the replicated cluster secret (`X-Cluster-Secret` header), not user credentials.
- **Keep-alive** — `: keep-alive` comments are sent every 15 s as in the single-node stream (M29).
- **Backward compatibility** — the existing `GET /api/v1/query-log/stream` (without `?cluster=true`) is unchanged.

## Not Implemented

- **Backfill on cluster stream** — `?backfill=N` replays the local node's ring buffer only; peer nodes are not backfilled (covered by M42 for single-node only per roadmap).
- **Cross-cluster (multi-cluster) aggregation** — out of scope.
- **WebSocket transport for cluster stream** — the M42 WebSocket endpoint is single-node only.

## Limitations

- Events are merged in **arrival order** (first received by the aggregating node, not by original DNS resolution time). Under high load or unequal network latency, events from different nodes may arrive slightly out of timestamp order.
- If the aggregating node itself restarts, the client must reconnect and will miss events that occurred during the reconnection gap.
- The `event: node_unavailable` frame is a one-shot notification; the aggregator does not retry failed peer connections within a single client session.
