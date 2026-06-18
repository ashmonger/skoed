# Technical Specification — Rolling Cluster Upgrade + Read Load Balancing

**x-tsid: TS-RollingUpgrade**  
**x-fsid-links: [FS-RollingUpgradeOrchestrated, FS-RollingUpgradeStatus, FS-RollingUpgradeAbortOnFailure, FS-RollingUpgradeLeadershipTransfer, FS-FollowerReadsDirectly, FS-FollowerForwardsMutations]**

---

## 1. Read Load Balancing (already shipped in M10)

The `WriteForwardMiddleware` in `internal/api/write_forward_middleware.go` already implements FS-FollowerReadsDirectly and FS-FollowerForwardsMutations:

- **GET/HEAD requests** on any node → served locally, no forwarding.
- **POST/PUT/PATCH/DELETE requests** on a follower → forwarded to leader.

Headers set on every response:
- `X-Served-By: <node-id>` — ID of the node that served the response.
- `X-Raft-Commit-Index: <n>` — commit index at time of serving.

No code changes required for this sub-feature. Tests are added to verify the existing behavior.

---

## 2. Rolling Upgrade API

### POST /api/v1/cluster/upgrade/apply

Leader-only (forwarded by follower nodes via `WriteForwardMiddleware`).

**Request body:**
```json
{ "url": "https://github.com/ashmonger/skoed/releases/download/vX.Y.Z/skoed_linux_amd64.tar.gz" }
```

**Behavior:**
1. Validates the cluster has ≥ 2 healthy members (refuses if solo node — use `/api/v1/upgrade/start` instead).
2. Sets upgrade state to `in_progress`; starts background goroutine.
3. Returns **202 Accepted** immediately.

**Goroutine logic (sequential):**
1. Get peer list from `Store().Members()`, sorted by node ID, self excluded.
2. For each peer:
   a. POST `{url}` to `http://<peer.APIAddress>/api/v1/upgrade/start`.
   b. Wait up to `upgrade_node_timeout_seconds` (default 120 s) for peer health to return 200.
   c. Wait up to 30 s more for `GET /api/v1/cluster/status` to show peer as healthy member.
   d. Mark peer as completed; move to next.
3. Transfer Raft leadership to the first completed peer via `cluster.TransferLeadership(nodeID)`.
4. Wait up to 30 s for a new leader to be elected.
5. POST `{url}` to self's own `/api/v1/upgrade/start` (this calls `os.Exit(0)` after 200 ms, restarting this node).

**Failure handling:**
- If any step fails (HTTP error, timeout, leadership transfer failure): set `failed_node`, clear `in_progress`, stop. Remaining nodes are not upgraded.
- In `SKOED_TEST_MODE=1`: the final self-upgrade exits are skipped; the test binary survives.

**Response (202):**
```json
{ "accepted": true, "message": "rolling upgrade started; check /api/v1/cluster/upgrade/status" }
```

**Errors:**
- 400 — url missing or empty.
- 409 — another upgrade is already in progress.
- 422 — cluster has only 1 member; use `/api/v1/upgrade/start` instead.
- 503 — cluster unavailable.

---

### GET /api/v1/cluster/upgrade/status

Any node. Returns current upgrade state (in-memory; resets on process restart).

**Response (200):**
```json
{
  "in_progress": false,
  "pending_nodes": [],
  "completed_nodes": ["node-2", "node-3"],
  "failed_node": null
}
```

Fields:
- `in_progress` (bool) — true while the goroutine is running.
- `pending_nodes` ([]string) — node IDs not yet upgraded.
- `completed_nodes` ([]string) — node IDs successfully upgraded.
- `failed_node` (string|null) — node ID that caused the abort, or null.

---

## 3. State management

The upgrade state is stored in a process-level singleton (`upgradeState` struct, protected by `sync.Mutex`) in the handlers package. It resets to zero on process restart (which is expected — a restarted node has already been upgraded).

---

## 4. Upgrade node timeout

`upgrade_node_timeout_seconds` is a constant (120 s) in this milestone. It is not exposed in config or API.

---

## 5. Test mode

When `SKOED_TEST_MODE=1`:
- The self-upgrade step posts to self's upgrade endpoint but the `os.Exit(0)` inside `UpgradeStart` is suppressed.
- The goroutine marks self as "completed" without actually restarting.
- This lets acceptance tests exercise the full orchestration logic without killing the test process.
