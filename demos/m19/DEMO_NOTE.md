# M19 — Query Log Aggregates Demo Note

## Implemented Scope

- **Hourly aggregates per node**: each node accumulates query/blocked counts and top-N domains/clients in memory, then commits a `CmdAggregateFlush` Raft FSM command at the top of each hour (or after 60 s of data for testing)
- **Cluster-wide stats endpoint**: `GET /api/v1/cluster/stats` sums per-hour buckets from all nodes, returns merged top-N domains/clients and per-hour totals — response is identical on any node
- **Fan-out for raw query log**: `GET /api/v1/query-log` with `cluster=true` fans out in parallel to all peer nodes and merges results sorted by timestamp descending; each entry carries the `node_id` that served it
- **Partial fan-out tolerance**: unreachable nodes are skipped within the configured timeout; response includes a `partial` flag and lists which nodes were unreachable
- **Aggregate retention**: aggregates older than the configured window are pruned when a new aggregate is committed (pruning goes through Raft)
- **Leader-loss read resilience**: aggregate reads are served by any follower from local bbolt storage — no leader required

## Not Implemented / Out of Scope

- Sub-hour granularity for cluster-wide stats
- Streaming push of new aggregates (poll-only)
- Time-series histograms or percentile tracking

## Limitations

- Top-N is computed per-node and then merged; the global top-N may not be strictly accurate if a domain is split across many nodes with small per-node counts
- Fan-out timeout is fixed at 3 s; not yet configurable via API

## Test Results

6/6 acceptance tests pass:

| Test | FSID | Result |
|------|------|--------|
| TestQueryLogAggregatesPerNodePerHour | FS-QueryLogAggregatesPerNodePerHour | PASS |
| TestQueryLogAggregatesClusterStats | FS-QueryLogAggregatesClusterStats | PASS |
| TestQueryLogAggregatesAvailableDuringLeaderLoss | FS-QueryLogAggregatesAvailableDuringLeaderLoss | PASS |
| TestQueryLogAggregatesFanOutForRawEntries | FS-QueryLogAggregatesFanOutForRawEntries | PASS |
| TestQueryLogAggregatesFanOutPartialFailure | FS-QueryLogAggregatesFanOutPartialFailure | PASS |
| TestQueryLogAggregatesRetention | FS-QueryLogAggregatesRetention | PASS |
