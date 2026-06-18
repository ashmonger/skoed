# Technical Specification — Query Log Aggregates + Cluster Fan-Out

**x-tsid: TS-QueryLogAggregates**  
**x-fsid-links: [FS-QueryLogAggregatesPerNodePerHour, FS-QueryLogAggregatesClusterStats, FS-QueryLogAggregatesAvailableDuringLeaderLoss, FS-QueryLogAggregatesFanOutForRawEntries, FS-QueryLogAggregatesFanOutPartialFailure, FS-QueryLogAggregatesRetention]**

---

## 1. Overview

Each node independently accumulates per-hour query counters and top-N domain/client lists in memory. On each flush (hourly in production, configurable in test mode), the node commits its aggregate as a Raft log entry, making it available on every peer. The replicated bbolt bucket `query_aggregates` holds all committed aggregates across the cluster for up to `aggregate_retention_days` days.

Two read endpoints surface this data:
- `GET /api/v1/cluster/stats` — merged cluster-wide totals from replicated aggregates.
- `GET /api/v1/cluster/query-log` — fan-out raw query-log entries from all live nodes.

---

## 2. Data Model

### 2.1 NodeHourAggregate (bbolt value, replicated via Raft)

```json
{
  "node_id":    "skoed-1",
  "hour_start": "2026-06-18T10:00:00Z",
  "total":      100,
  "blocked":    30,
  "forwarded":  65,
  "cached":     4,
  "local":      1,
  "top_domains": [
    {"domain": "tracker.example.com", "count": 28}
  ],
  "top_clients": [
    {"client": "192.168.1.10", "count": 42}
  ]
}
```

bbolt bucket: `query_aggregates`  
Key: `<node_id>/<hour_start_unix>` (e.g. `skoed-1/1750244400`)

### 2.2 Settings field

`aggregate_retention_days` (int, default `30`) — aggregates older than this are pruned on the next commit via Raft. Exposed in `GET /api/v1/settings` response body and writable via `PATCH /api/v1/settings`.

---

## 3. Flush Behaviour

| Env var | Default | Test override |
|---------|---------|---------------|
| `SKOED_TEST_AGGREGATE_FLUSH_SECONDS` | — | Set to `1` to flush every second instead of every hour |

The flush goroutine runs on every node. On each flush:
1. Snapshot the current in-memory counters and reset them.
2. Build a `NodeHourAggregate` for the completed hour window.
3. Submit it as a Raft command (`OpWriteAggregate`). The leader applies it; followers receive it via Raft replication.
4. After applying, prune entries where `hour_start < now - retention_days`.

---

## 4. API Endpoints

### GET /api/v1/cluster/stats

Returns merged cluster-wide statistics from the replicated bbolt store. Served locally from any node (no forwarding, no fan-out).

**Query params:**
- `range` — `1h` (default) | `24h` | `7d` — how far back to sum aggregates.

**Response 200:**
```json
{
  "window_from": "2026-06-17T10:00:00Z",
  "window_to":   "2026-06-18T10:00:00Z",
  "cluster_totals": {
    "total":     1250,
    "blocked":   312,
    "forwarded": 890,
    "cached":    40,
    "local":     8
  },
  "per_node": [
    {
      "node_id":    "skoed-1",
      "hour_start": "2026-06-18T10:00:00Z",
      "total":      450,
      "blocked":    110,
      "forwarded":  320,
      "cached":     15,
      "local":      5
    }
  ]
}
```

Route: registered outside `WriteForwardMiddleware` (read-only, served by any node).

### GET /api/v1/cluster/query-log

Fans out `GET /api/v1/query-log` to every known cluster member in parallel, merges results sorted by timestamp descending. Each entry is tagged with `node_id`.

**Query params:**
- `limit` — max entries to return (default `100`, max `1000`).
- `timeout_ms` — per-node timeout in milliseconds (default `2000`).

**Response 200:**
```json
{
  "entries": [
    {
      "node_id":   "skoed-1",
      "timestamp": "2026-06-18T10:32:01Z",
      "client":    "192.168.1.10",
      "domain":    "tracker.example.com",
      "type":      "A",
      "outcome":   "blocked"
    }
  ],
  "total":   42,
  "per_node": [
    {"node_id": "skoed-1", "status": "ok",      "entry_count": 30},
    {"node_id": "skoed-2", "status": "ok",      "entry_count": 12},
    {"node_id": "skoed-3", "status": "timeout", "entry_count": 0, "error": "deadline exceeded"}
  ]
}
```

Partial failure: unreachable nodes appear in `per_node` with `status: "timeout"` or `status: "error"`. The response still returns `200` with the entries from reachable nodes.

---

## 5. Raft Command

New command type `OpWriteAggregate` alongside existing cluster store commands. Payload is the `NodeHourAggregate` JSON. Applied by `ClusterStore.applyWriteAggregate()`.

---

## 6. In-memory Accumulator

`internal/log/aggregator.go` — `Aggregator` struct:
- `Add(outcome string, domain, client string)` — called by the query log on every DNS query.
- `Flush() NodeHourAggregate` — snapshots and resets counters; called by the flush goroutine.
- Top-N tracking: min-heap of size 10 for domains and clients.
- `SKOED_TEST_AGGREGATE_FLUSH_SECONDS` env var: if set, the flush goroutine uses this interval (seconds) instead of the hour boundary.

The `Aggregator` is wired into the DNS query handler via the existing `QueryLog` path.

---

## 7. Non-goals

- Sub-hour granularity for cluster-wide stats (per-node raw logs cover that).
- Streaming push of new aggregates (polling only via `GET /api/v1/cluster/stats`).
- Per-profile aggregates (whole-cluster view only).
- Persistent aggregate storage beyond `aggregate_retention_days` days.
