# M10 Active-Active Cluster — Demo Note

## Implemented

- Transparent write forwarding: any node accepts write requests (POST/PUT/PATCH/DELETE); non-leader nodes proxy the request to the current Raft leader and return the result transparently to the caller.
- `X-Served-By` response header on all responses: contains the NodeID of the node that processed the request.
- `X-Raft-Commit-Index` response header on all responses: contains the decimal Raft commit index of the serving node.
- `WriteForwardMiddleware` wired in the router after `requireWrite`, before route handlers.
- Handler-level `307` leader-redirect responses removed from `CreateJoinToken`, `ClusterJoin`, `ClusterMTLSBootstrap`, `TransferLeadership`, `RemoveNode`.

## Not Implemented

- Per-namespace write sharding (telemetry vs config namespaces routed to different leaders).
- CRDT state types (conflict-free replicated data types for last-write-wins semantics).
- Client-side retry logic on 503 during leader election.

## Limitations

- Assumes cluster RTT <= 50 ms between nodes; higher latency degrades forwarded write latency.
- Leader unavailability returns 503 immediately with no internal retry.
- In-flight write requests during a leader election may return 503; callers must retry.

## Quick Demo

Start a 3-node cluster. Send a POST to any follower node API endpoint (e.g. `POST /api/v1/blocklists`). Observe:
1. Response is `200` (or the normal success code), not a `307` redirect.
2. Response header `X-Served-By` contains the follower's NodeID.
3. Response header `X-Raft-Commit-Index` shows the follower's current commit index.
4. The created resource is visible from all nodes (Raft-replicated).
