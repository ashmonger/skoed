---
x-tsid: TS-QueryLogCluster
x-fsid-links:
  - FS-QueryLogAggregatesPerNodePerHour
  - FS-QueryLogAggregatesClusterStats
  - FS-QueryLogAggregatesAvailableDuringLeaderLoss
  - FS-QueryLogAggregatesFanOutForRawEntries
  - FS-QueryLogAggregatesFanOutPartialFailure
  - FS-QueryLogAggregatesRetention
  - FS-ClusterConfigSyncQueryLogRawIsPerNode
---

# TS-QueryLogCluster — Cluster-Wide Query Log

Splits the query log into two layers:

```
                          ┌─────────────────────────────────┐
                          │ Cluster-replicated (Raft)        │
                          │  stats/{node}/{hour} = aggregate │
                          └─────────────────────────────────┘
                                         ▲
                                         │ stats.commit_hour FSM cmd
                                         │ (one per node per hour)
┌────────────┐  raw entries  ┌──────────────────────┐
│ DNS query  │──────────────▶│ querylog.bbolt        │ (per node)
└────────────┘               │  bounded ring buffer  │
                             └──────────────────────┘
                                         ▲
                                         │ GET /api/v1/query-log (local)
                                         │ GET /api/v1/cluster/query-log (fan-out)
```

## HourlyAggregate

The unit committed once per node per hour. Schema (matches the OpenAPI
`HourlyAggregate`):

```json
{
  "node_id": "node-1",
  "hour_start": "2026-05-29T10:00:00Z",
  "total": 12345,
  "blocked": 4321,
  "forwarded": 7890,
  "cached": 134,
  "local": 0,
  "top_domains": [
    {"domain": "example.com", "count": 312},
    ...
  ],
  "top_clients": [
    {"client": "192.168.1.50", "count": 482},
    ...
  ]
}
```

- `top_domains` and `top_clients` hold the top 20 entries per hour per node.
  Cluster stats sums these; precision is bounded but adequate for a
  dashboard. (Exact cluster-wide top-N would require streaming raw entries,
  which we explicitly avoid.)
- Aggregates are flushed:
  - on the hour boundary, OR
  - 60 seconds after the bucket opens (so a freshly-started node doesn't
    look idle until the next boundary), whichever comes first.

## Commit cadence

```
T+0:00  hour bucket opens locally
T+0:00..T+1:00  in-memory counters accumulate
T+1:00  flush: encode HourlyAggregate → raft.Apply(stats.commit_hour)
        once committed, in-memory counters reset
```

If the leader is unreachable when a flush is due, the node retains the
counters and retries every 30 seconds. Counters survive process restart by
re-reading the local raw log.

## Cluster stats query

`GET /api/v1/cluster/stats?from&to&top_n` is served from local bbolt:

1. Iterate `stats/*/*` with `from <= hour_start < to`.
2. Sum `total`, `blocked`, `forwarded`, `cached`, `local` across all nodes
   and all hours in the window.
3. Merge `top_domains` and `top_clients` per node, sum by key, return the
   top N.
4. Build `per_node` list directly from the iterated entries.

Because the data is replicated, this works on any node even when the leader
is down. Stale by at most one flush interval (~60 s).

## Fan-out for raw entries

`GET /api/v1/cluster/query-log` is the on-demand path for searches the
aggregates cannot answer (specific client history, recent activity in the
last few minutes, full-text domain search).

```
admin --GET /api/v1/cluster/query-log?client=X--> node-A
                                                  │
                                                  ├── self lookup (local bbolt)
                                                  ├── GET /api/v1/query-log @ node-B  (timeout_ms)
                                                  └── GET /api/v1/query-log @ node-C  (timeout_ms)
                                                  ▼
                                                  merge → sort by timestamp desc
                                                       → return ClusterQueryLog
```

- Parallel fan-out, one HTTP request per peer.
- Each peer call carries a `timeout_ms` (default 2 s).
- Authentication: the receiving node uses a short-lived signed token
  (signed by the cluster's stored auth credentials) so peers do not need
  the admin's basic auth.
- Unreachable peers do NOT fail the call. The response's `per_node` list
  reports `status: timeout` or `status: error` for them.
- Pagination is best-effort: each peer returns up to `limit + offset`
  entries; the caller paginates the merged result. For deep pagination
  the dashboard SHOULD prefer narrower filters (specific client, smaller
  time window) over large offsets.

## Retention

- Raw `querylog.bbolt` retention: bounded by `query_log.max_entries` (M1
  setting, unchanged).
- Aggregate retention: configurable `query_log.aggregate_retention_days`
  (default: 30 days). A `stats.prune` FSM command runs daily on the leader
  and removes all `stats/*/<hour>` keys older than the cutoff. Replicated
  via Raft like any other write.

## Privacy notes

- Raw entries stay on the node that served the query. A node never sees
  raw entries from another node unless an admin explicitly invokes
  `/api/v1/cluster/query-log`.
- Top-N aggregates ARE replicated to every node and visible to any admin
  on any node. If this is unacceptable in some deployments, the
  `query_log.aggregate_top_n` setting MAY be set to 0 to skip top-N
  collection entirely while still publishing counters.

## Non-goals

- Time-series storage (no per-minute granularity for cluster stats).
- Exact cluster-wide top-N (per-node top-20 merge is best-effort).
- Streaming push of new aggregates to dashboards.
- Cross-node deduplication (a query handled by node-A and not by node-B
  is, by definition, counted once).
