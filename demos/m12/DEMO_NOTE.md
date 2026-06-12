# M12 Demo Note — Cluster Join via Web UI + Config Backup/Restore

## Implemented scope

### Cluster join via web UI

An operator with an existing single-node skoed installation can join it to a
cluster entirely from the browser — no SSH, no CLI.

**Leader side (Cluster page):**
- "Generate join token" button calls `POST /api/v1/cluster/tokens` and displays
  the resulting payload block:
  ```
  token: <one-time token>
  leader_address: http://<leader-ip>:<port>
  ```
- The payload is displayed with an expiry timestamp and a "Copy" button.

**Follower side (Cluster page):**
- A "Join an existing cluster" panel is visible when the node is in
  `single-node` mode (hidden once it becomes a cluster member).
- Operator pastes the payload block from the leader into the textarea and
  clicks **Join**.
- The new `POST /api/v1/node/join-cluster` endpoint:
  1. Parses `token` and `leader_address` from the request body.
  2. Returns `409 Conflict` if the node is already a cluster member.
  3. Reads the node's own identity (node ID, Raft address, API address).
  4. Calls `Cluster.ResetRaftForJoin()`: shuts down running Raft, deletes
     `raft-log.bolt`, `raft-stable.bolt`, and the `snapshots/` directory,
     restarts Raft with `Bootstrap=false`.
  5. Posts the node's identity + token to the leader's
     `POST /api/v1/cluster/join`.
  6. Returns `403` if the leader rejects the token (already consumed or
     expired), `200` on success.
- The UI polls cluster health and the join panel disappears once
  `mode: cluster` is reported.

**Acceptance tests (all green):**
- `TestClusterJoinWebUiTokenDisplay` — token, leader_address, expires_at all
  present and expires_at is RFC-3339.
- `TestClusterJoinWebUiFollowerDialog` — two independent single-node clusters;
  fresh node joins via the API; both nodes report `mode: cluster` within 30 s;
  cluster status lists 2 members.
- `TestClusterJoinWebUiAlreadyMember` — join on an already-joined node returns
  `409`.
- `TestClusterJoinWebUiExpiredToken` — consuming a token then trying it again
  returns `403`.
- `TestClusterJoinWebUiInvalidPayload` — missing fields return `400`.

### Config backup/restore (Settings page)

**Download:** "Download backup" link in Settings → Configuration backup calls
`GET /api/v1/config/export`. The tar.gz archive contains `config.yaml` with:
- All DNS, filtering, local DNS, profile, schedule, and binding settings.
- `password_hash` and all `auth.*` fields **absent** — enforced by the
  `exportShape` struct which omits the `Auth` field entirely (yaml.v3 does not
  honour `omitempty` on struct fields; a separate struct type is the correct
  approach).

**Restore:** A file picker + "Restore" button opens a confirmation modal.
On confirm, the archive is uploaded to `POST /api/v1/config/import`. The
handler merges the archive into the current config while **preserving the
current node's admin credentials** (`newCfg.Auth = cfg.Auth` in the
`WithWriteLock` callback).

**Acceptance tests (all green):**
- `TestConfigExportDoesNotIncludeCredentials` — the exported archive YAML
  contains no `password_hash` key.
- `TestConfigImportPreservesCredentials` — after importing a backup, the
  previously-set password still works.
- `TestConfigBackupWebUiRoundTrip` — export then import reproduces the same
  domain count in the blocklist; credentials survive the round-trip.

## Not implemented in this milestone

- Cluster **leave** / node removal via web UI (use `DELETE /api/v1/cluster/nodes/{id}` directly).
- **Scheduled or automatic backups** — manual download only.
- **Backup encryption** — the archive is plaintext tar.gz; operator is
  responsible for storage security.
- **Merge / diff** between two backup archives.

## Known limitations

- The join flow requires the follower's Raft listener address to be reachable
  from the leader at the time `AddVoter` is called. Firewall or NAT between
  nodes will prevent the Raft handshake from completing even if the API call
  returns 200.
- `ResetRaftForJoin` is irreversible once called: if the subsequent join API
  call fails (network error, not a 4xx), the follower is left with no Raft
  state and no cluster membership. The operator must restart skoed on that node
  (it will re-bootstrap as a single-node cluster automatically because
  `Bootstrap=false` + no peers = a fresh election with only itself).

## Demo steps

### Prerequisites

Two skoed instances already running:
- **Leader**: started with valid config, admin password set.
- **Follower**: fresh single-node, admin password set.

### Step 1 — Generate join token (leader)

1. Open the leader's web UI → **Cluster** page.
2. Click **Generate join token**.
3. Copy the displayed payload block.

### Step 2 — Join (follower)

1. Open the follower's web UI → **Cluster** page.
   - Confirm the "Join an existing cluster" panel is visible.
2. Paste the payload into the textarea.
3. Click **Join**.
4. Wait ~5 s; the panel disappears and the cluster topology shows both nodes.

### Step 3 — Config backup (leader)

1. Open the leader's web UI → **Settings** → Configuration backup.
2. Click **Download backup** — verify the `.tar.gz` downloads.
3. Extract and inspect `config.yaml` — confirm no `password_hash` key present.

### Step 4 — Config restore (second follower or fresh node)

1. Open the target node's web UI → **Settings** → Configuration backup.
2. Click **Choose file**, select the `.tar.gz` from step 3.
3. Click **Restore**, confirm in the modal.
4. Verify the same blocklist domains appear; verify the previous password still works.
