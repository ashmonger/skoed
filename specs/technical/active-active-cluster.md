x-tsid: TS-ActiveActiveCluster
x-fsid-links:
  - FS-AaWriteAcceptedOnAnyNode
  - FS-AaFollowerWriteProducesSameStateAsLeaderWrite
  - FS-AaReadServedLocallyWithoutLeaderContact
  - FS-AaResponseSurfacesServingNodeAndCommitPosition
  - FS-AaWriteWithNoLeaderReturnsUnavailable
  - FS-AaPerNodeTelemetryIsLocal
  - FS-AaDistributedWritesConvergeOnAllNodes

---

# Technical Specification: Active-Active Cluster (TS-ActiveActiveCluster)

## 1. Overview — What Changes vs Current Behavior

### Current behavior (M5/M9)

Every mutating HTTP route in `app.go` is wrapped with `a.forward(handler)`, which resolves to
`apimw.LeaderForward`. When `IsLeader() == false` the middleware transparently proxies the entire
request body and headers to `c.LeaderAPIAddress()` and mirrors the leader's response verbatim.
Reads (GET/HEAD) are always served locally without any cluster round-trip.

### What M10 changes

| Dimension | Before M10 | After M10 |
|---|---|---|
| Write path on follower | Transparent HTTP proxy to leader | Forward write to leader via `WriteForwarder`; return leader's response to caller |
| Write path on leader | Execute locally | Execute locally (unchanged) |
| Read path | Always local (unchanged) | Always local (unchanged) |
| Response headers | None cluster-specific | `X-Served-By` and `X-Raft-Commit-Index` on every response |
| No-leader condition on write | 503 `{"error":"no leader"}` | 503 `{"error":"no leader elected"}` (same semantics, consistent key) |
| Audit guard | `if !c.IsLeader() { return }` skips audit on follower | Guard removed; each node audits its own committed writes only |
| Per-node telemetry | Mixed local/replicated | Metrics namespace split: local counters never enter Raft |

"Active-active" in this codebase means: any node accepts a write call and submits it into the
single Raft log via the current leader. There is always exactly one Raft leader. Followers are
not leaders. What changes is the **API contract**: no redirect or 409 is ever returned to the
caller for ordinary write operations; the follower handles forwarding internally and returns the
committed result.

The Raft FSM, consensus algorithm, bbolt storage layout, and `commands.go` / `store.go` / `fsm.go`
structures are **not** changed by M10 unless new `CommandKind` entries are needed for genuinely
new data objects introduced in this milestone. The forwarding infrastructure (`LeaderForward`) is
already complete; the M10 work is limited to response headers, the `WriteForwarder` contract
extension, telemetry namespace split, and audit guard cleanup.

---

## 2. WriteForwarder Interface

The existing `Cluster` interface surface exposed to `LeaderForward` middleware is extended with
one new method. The full interface used by the middleware becomes:

```go
// WriteForwarder is the cluster capability interface consumed by apimw.LeaderForward.
// It replaces the previous narrow Cluster interface in middleware/forward.go.
type WriteForwarder interface {
    // IsLeader reports whether this node is the current Raft leader.
    IsLeader() bool

    // LeaderAPIAddress returns the HTTP base URL (scheme://host:port) of the
    // current Raft leader, as resolved from the members bucket via
    // Cluster.LeaderAPIAddress(). Returns "" when no leader is known.
    LeaderAPIAddress() string

    // LeaderID returns the Raft ServerID (== NodeID string) of the current
    // leader. Returns "" when no leader is known.
    LeaderID() string

    // ForwardWrite sends a mutating request to the leader and returns the
    // leader's response for the middleware to mirror verbatim to the caller.
    //
    //   ctx        — carries the original request deadline; the implementation
    //                MUST respect ctx.Done() and abort the outbound call.
    //   method     — HTTP method of the original request (POST, PUT, PATCH, DELETE).
    //   path       — request URI path + raw query string (no scheme/host).
    //   body       — fully-buffered request body (may be nil for bodyless methods).
    //   inHeaders  — original request headers to forward (Authorization, Content-Type, …).
    //
    // Returns:
    //   statusCode — HTTP status code from the leader.
    //   respBody   — full response body from the leader (caller must not mutate).
    //   respHeaders — response headers from the leader (caller copies selectively).
    //   err        — non-nil only when the network call itself fails or ctx is done;
    //                HTTP-level errors (4xx/5xx) are surfaced via statusCode, not err.
    ForwardWrite(
        ctx context.Context,
        method string,
        path string,
        body []byte,
        inHeaders http.Header,
    ) (statusCode int, respBody []byte, respHeaders http.Header, err error)

    // NodeID returns the stable identifier of this node (== Raft ServerID).
    // Used to populate the X-Served-By response header.
    NodeID() string

    // CommitIndex returns the last Raft log index known to this node.
    // Wraps raftNode.CommitIndex() which calls raft.LastIndex().
    // Used to populate the X-Raft-Commit-Index response header.
    CommitIndex() uint64
}
```

### Implementation mapping

| Interface method | Implemented by |
|---|---|
| `IsLeader()` | `cluster.Cluster.IsLeader()` → `raftNode.IsLeader()` → `r.State() == raft.Leader` |
| `LeaderAPIAddress()` | `cluster.Cluster.LeaderAPIAddress()` (already exists, cluster.go:234) |
| `LeaderID()` | `cluster.Cluster.LeaderID()` (already exists) |
| `ForwardWrite(...)` | New method on `cluster.Cluster`; wraps the HTTP proxy logic currently inline in `LeaderForward` |
| `NodeID()` | New method on `cluster.Cluster`; returns `string(c.raft.nodeID)` |
| `CommitIndex()` | `cluster.Cluster.CommitIndex()` → `raftNode.CommitIndex()` → `r.LastIndex()` |

The `clusterAdapter` in `app.go` is updated to implement `WriteForwarder` by delegating to the
above `Cluster` methods.

---

## 3. Write-Forwarding Flow

### 3a. Follower receives write — leader is known

```
Client          Follower (node-2)                  Leader (node-1)
  |                    |                                  |
  |--POST /api/v1/x -->|                                  |
  |                    | LeaderForward:                   |
  |                    |  IsLeader() == false             |
  |                    |  LeaderAPIAddress() == "http://node-1:8080"
  |                    |                                  |
  |                    |--ForwardWrite(POST /api/v1/x)--> |
  |                    |  (Authorization header copied)   |
  |                    |                                  |
  |                    |          applyAsLeader()         |
  |                    |          raft.Apply(cmd)         |
  |                    |          FSM commits to bbolt    |
  |                    |                                  |
  |                    |<--200 OK + body + headers -------|
  |                    |                                  |
  |                    | middleware appends:              |
  |                    |   X-Served-By: node-2            |
  |                    |   X-Raft-Commit-Index: <N>       |
  |<--200 OK + body----|                                  |
```

### 3b. Follower receives write — no leader elected

```
Client          Follower (node-2)
  |                    |
  |--POST /api/v1/x -->|
  |                    | LeaderForward:
  |                    |  IsLeader() == false
  |                    |  LeaderAPIAddress() == ""
  |                    |
  |<--503 {"error":"no leader elected"} --|
  |    Retry-After: 5                     |
```

### 3c. Leader receives write directly

```
Client          Leader (node-1)
  |                    |
  |--POST /api/v1/x -->|
  |                    | LeaderForward:
  |                    |  IsLeader() == true → serve locally
  |                    |  applyAsLeader()
  |                    |  raft.Apply(cmd)
  |                    |  FSM commits to bbolt
  |                    |
  |                    | middleware appends:
  |                    |   X-Served-By: node-1
  |                    |   X-Raft-Commit-Index: <N>
  |<--200 OK + body----|
```

### 3d. Read — always local regardless of role

```
Client          Any node
  |                   |
  |--GET /api/v1/x -->|
  |                   | LeaderForward:
  |                   |  isReadMethod() == true → serve locally
  |                   |  bbolt read
  |                   |
  |                   | middleware appends:
  |                   |   X-Served-By: <this-node-id>
  |                   |   X-Raft-Commit-Index: <N>
  |<--200 OK + body---|
```

---

## 4. Response Headers

Both headers are injected by `LeaderForward` middleware **after** the inner handler (or forwarded
response) has been written, on **every** response regardless of method or node role.

### X-Served-By

| Field | Value |
|---|---|
| Header name | `X-Served-By` |
| Value | The `NodeID` of the node that received and returned the HTTP response to the caller. On a follower write, this is the **follower's** node ID (the node the client actually spoke to), not the leader's. |
| Source | `WriteForwarder.NodeID()` → `string(c.raft.nodeID)` |
| Type | Non-empty opaque string; matches the `node_id` field in `Member` bbolt records. |

### X-Raft-Commit-Index

| Field | Value |
|---|---|
| Header name | `X-Raft-Commit-Index` |
| Value | Decimal string representation of `WriteForwarder.CommitIndex()` at the time the response is sent. On a forwarded write the value reflects the **follower's** view of the commit index after the forward has returned (the follower will have applied the leader's commit by then within Raft's normal replication window). |
| Source | `WriteForwarder.CommitIndex()` → `raftNode.CommitIndex()` → `r.LastIndex()` |
| Type | `uint64` serialized as base-10 ASCII string, e.g. `"42"`. |

**Note on AppliedIndex:** `raftNode` currently exposes only `CommitIndex()` (via `r.LastIndex()`),
not `AppliedIndex()` (via `r.AppliedIndex()`). For M10 the commit index is sufficient. If a
future milestone requires lag reporting (commit vs applied divergence), a separate
`AppliedIndex() uint64` method wrapping `r.AppliedIndex()` must be added to `raftNode`.

---

## 5. Error Handling

### Leader unknown (no election)

- Condition: `WriteForwarder.LeaderAPIAddress()` returns `""`.
- HTTP status: `503 Service Unavailable`.
- Response body:
  ```json
  {
    "error": "no leader elected",
    "retry_after_seconds": 5
  }
  ```
- `Retry-After: 5` header is set.
- `X-Served-By` and `X-Raft-Commit-Index` are still set.
- No state mutation occurs on any node.

### Leader unreachable or forward timeout

- Condition: `WriteForwarder.ForwardWrite(...)` returns a non-nil `err` (network failure,
  connection refused, context deadline exceeded).
- HTTP status: `503 Service Unavailable`.
- Response body:
  ```json
  {
    "error": "forward to leader failed: <err.Error()>",
    "leader_id": "<LeaderID()>",
    "leader_address": "<LeaderAPIAddress()>"
  }
  ```
- `X-Served-By` and `X-Raft-Commit-Index` are still set.
- No state mutation occurs (the Raft Apply call was never issued).

### Forward timeout value

The forward HTTP client timeout remains `10s` (the existing `forwardTimeout` constant in
`middleware/forward.go`). This value is not changed by M10.

### Cluster-membership operations (join, remove, transfer)

The five handlers in `handlers/cluster.go` that currently contain manual `!c.IsLeader()` →
`writeLeaderRedirect(409)` guards (`CreateJoinToken`, `ClusterJoin`, `ClusterMTLSBootstrap`,
`TransferLeadership`, `RemoveNode`) retain their `a.forward()` wrapping. The 409 guard inside
each handler is kept as a defensive belt-and-suspenders check. From the API contract perspective
these operations are still sequenced through the leader and the 409 response is only reached if
`LeaderForward` is bypassed, which is not a supported path.

---

## 6. Leader API Address Resolution

When a non-leader node needs to forward a write, it resolves the leader's HTTP endpoint as
follows:

1. `raftNode.Leader()` calls `r.LeaderWithID()` (hashicorp/raft) which returns
   `(raft.ServerAddress, raft.ServerID)`.  
   `ServerID` is the node's stable `NodeID` string.

2. `Cluster.LeaderAPIAddress()` (cluster.go:234) uses that `ServerID` to call
   `c.store.MemberByID(string(id))` which looks up the `Member` record in the `members`
   bbolt bucket.

3. The `Member` struct (store.go:838) holds:
   ```go
   type Member struct {
       NodeID      string `json:"node_id"`
       RaftAddress string `json:"raft_address"`
       APIAddress  string `json:"api_address"`  // host:port, no scheme
       JoinedUnix  int64  `json:"joined_unix"`
   }
   ```

4. `apiBaseURL(m.APIAddress)` (cluster.go) prepends `http://` to produce the full base URL.

5. `ForwardWrite` appends the original request path (including raw query string) to this base
   URL to build the outbound request URI.

**Scheme limitation:** `apiBaseURL()` hard-codes `http://`. If M10 or a future milestone
introduces HTTPS-only cluster APIs, either `Member.APIAddress` must be extended to store the
full URL (including scheme), or the mTLS code path must derive the scheme from
`Cluster.mtlsEnabled`. This is a known gap, not addressed by M10 unless the milestone
explicitly requires HTTPS cluster API forwarding.

---

## 7. Per-Node Telemetry Namespace

### Invariant

Counters that are **per-node observations** (DNS query counts, cache hit/miss ratios, forward
latency histograms) MUST NOT be submitted to Raft. They are local to the node that observed
them and are never replicated.

### Split

| Metric class | Storage | Source | Replication |
|---|---|---|---|
| DNS query counter (total, blocked, allowed, cached) | In-process atomic (metrics.go) | Local DNS resolver | None — never enters Raft |
| DHCP lease state | bbolt via Raft FSM | `CmdLeaseSet` / `CmdLeaseDelete` | Raft-replicated |
| Blocklist/allowlist/profile/config mutations | bbolt via Raft FSM | Write handlers | Raft-replicated |
| Cluster membership | bbolt via Raft FSM | `CmdMemberSet` | Raft-replicated |
| Forwarding latency histogram | In-process (future) | LeaderForward middleware | None |

### API surface for metrics

`GET /api/v1/metrics` (or Prometheus scrape endpoint) is registered **without** `a.forward()`.
It is a read-only, node-local endpoint. It MUST NOT be wrapped with `a.forward()` either now
or in future refactoring. Every node returns only its own counters.

### No new CommandKind needed for telemetry

Because per-node metrics are local-only, M10 introduces no new `CommandKind` entries in
`commands.go` for telemetry purposes. Any new `CommandKind` entries introduced by M10 are
limited to genuinely cluster-replicated state (e.g. a new data object requiring consensus).

---

## 8. What Does NOT Change

The following components are **outside the M10 change surface** and must not be modified:

| Component | Location | Reason |
|---|---|---|
| Raft FSM apply logic | `cluster/fsm.go` | Consensus algorithm unchanged |
| `CommandKind` enum and payload structs | `cluster/commands.go` | No new replicated data objects in M10 unless explicitly scoped |
| bbolt bucket layout | `cluster/store.go` | Storage schema unchanged |
| `applyAsLeader` gate | `cluster/cluster.go:279` | Leader-only write gate is correct and must be kept |
| Raft transport (TCP + mTLS) | `cluster/raft.go` | Peer-to-peer layer unchanged |
| `Member` registry fields | `cluster/store.go:838` | `APIAddress` format unchanged (`host:port`) |
| Snapshot/restore path | `cluster/fsm.go` | Snapshotting logic unchanged |
| `ErrNotLeader` sentinel | `cluster/cluster.go` | Returned by `requireLeader()`; keeps its meaning |
| Consensus algorithm | hashicorp/raft | Single Raft leader, majority quorum — non-negotiable |
| "Active-active" definition | — | Means any node may forward a write; does NOT mean multiple Raft leaders or eventual consistency |

---

## 9. Acceptance Test Approach

Each scenario in `specs/functional/active-active-cluster.feature` is covered by a black-box
HTTP acceptance test in `tests/acceptance/active_active_cluster_test.go`.

The test harness launches a 3-node cluster via Docker (per project feedback: cluster tests run
in containers, not bare processes). Each test:

1. Waits for leader election (polls `GET /api/v1/cluster/status` on all nodes until one reports
   `"role":"leader"`).
2. Sends the mutating HTTP call to the non-leader node under test.
3. Asserts the response HTTP status and body from the **follower** (no 307, no 409, no 503).
4. Asserts `X-Served-By` is present and matches the follower's node ID.
5. Asserts `X-Raft-Commit-Index` is present and is a valid decimal uint64.
6. Polls all three nodes for state convergence within the scenario's timeout (5 s or 10 s).
7. For `FS-AaWriteWithNoLeaderReturnsUnavailable`: partitions the leader (Docker network
   disconnect), sends write to a remaining node, asserts 503 with `"no leader elected"`.
8. For `FS-AaPerNodeTelemetryIsLocal`: scrapes metrics from all three nodes, asserts that
   per-node query counters differ and are not visible on other nodes.
9. For `FS-AaReadServedLocallyWithoutLeaderContact`: partitions the leader, verifies GET
   succeeds on follower, inspects follower's access log to confirm no outbound call to leader.

Reference FSIDs each test asserts: see `x-fsid-links` at the top of this document.

---

## 10. Delivery Sequence

| Step | Artifact | Blocks |
|---|---|---|
| 1 | Add `NodeID() string` method to `cluster.Cluster` (returns `string(c.raft.nodeID)`) | Step 3 |
| 2 | Add `ForwardWrite(...)` method to `cluster.Cluster` (extracts inline proxy logic from `LeaderForward`) | Step 3 |
| 3 | Update `clusterAdapter` in `app.go` to implement `WriteForwarder` | Step 4 |
| 4 | Update `LeaderForward` in `middleware/forward.go` to use `WriteForwarder` interface and inject `X-Served-By` + `X-Raft-Commit-Index` on every response | Step 5 |
| 5 | Remove `if !c.IsLeader() { return }` guard from `audit_middleware.go`; add write-origin deduplication if needed | Step 6 |
| 6 | Verify metrics endpoint is not wrapped with `a.forward()`; confirm per-node counter isolation | Step 7 |
| 7 | Write acceptance tests in `tests/acceptance/active_active_cluster_test.go` covering all 7 FSIDs | Step 8 |
| 8 | Run acceptance tests in Docker 3-node harness; iterate until all pass | Step 9 |
| 9 | Run `tools/spec-lint/spec_lint.sh` and `tools/traceability/traceability_check.sh` | Step 10 |
| 10 | Refactoring phase (readability, no behavior change); re-run full test suite | Done |
