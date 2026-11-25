---
x-tsid: TS-BlocklistContentDistribution
x-fsid-links:
  - FS-BlocklistContentConfigReplicates
  - FS-BlocklistContentOnlyLeaderDownloads
  - FS-BlocklistContentFollowersCopyFromLeader
  - FS-BlocklistContentNotReplicated
  - FS-BlocklistContentInlineStillReplicates
  - FS-BlocklistContentLeaderFailover
  - FS-BlocklistContentNewLeaderWithoutCopy
  - FS-BlocklistContentDownloadFailureKeepsCopy
  - FS-BlocklistContentFollowerCannotReachLeader
  - FS-BlocklistContentSurvivesRestartOffline
  - FS-BlocklistContentNewNodeCopiesOnJoin
  - FS-BlocklistContentManualRefresh
  - FS-BlocklistContentUpgradeKeepsFiltering
  - FS-RaftLogStaysBounded
  - FS-RaftLogCompactOffline
  - FS-RaftLogCompactRefusedWhileRunning
---

# TS-BlocklistContentDistribution — leader downloads, followers copy

Functional spec: `specs/functional/blocklist-content-distribution.feature`.
Decision: `decisions/20261125-LeaderDistributedBlocklistContent.md`.
HTTP contract: `management-api.openapi.yaml` (operations tagged
`TS-BlocklistContentDistribution`).

Paths are relative to `apps/skoed`.

## 1. Current state (what changes and why)

| Today | Effect |
|-------|--------|
| URL list domains are stored in `config.Blocklist.Domains`, in bbolt bucket `config_blocklists` (`internal/cluster/store.go:23`). | Every snapshot carries them (35 MB on the home cluster). |
| Every REST write goes through `App.WithWriteLock` (`internal/api/app.go:631`), which commits the whole config as one `config.import` entry. | Editing an allowlist entry writes all 511k domains to the Raft log. |
| The scheduler calls `UpsertBlocklist` after every due refresh, including `error` and `unchanged` (`internal/refresh/scheduler.go:202-229`). | A full-domain entry per list per interval. |
| `TrailingLogs` is not set (`internal/cluster/raft.go:89-97`), so it is 10240. | The log is never truncated below 10240 entries. |
| `App.onApply` rebuilds the filter engine on every apply, stats and audit included (`internal/api/app.go:575-617`). | Repeated full rebuilds of a 511k-entry map. |
| `fsm.Restore` does not call `onApply` (`internal/cluster/fsm.go:48-84`). | After InstallSnapshot, a follower keeps its old engine. |
| `config.Export` drops URL domains, but nothing downloads them again after an import (`internal/config/export.go:39-62`). | Imported URL lists stay empty until their interval elapses. |

## 2. Data model

### 2.1 Replicated (Raft, bbolt `config_blocklists`)

`config.Blocklist` gains one field and keeps the others:

```go
type BlocklistContent struct {
    SHA256      string `yaml:"sha256"       json:"sha256"`        // hex, of the cache file bytes
    DomainCount int    `yaml:"domain_count" json:"domain_count"`
    FetchedAt   string `yaml:"fetched_at"   json:"fetched_at"`    // RFC3339, leader's download time
}

type Blocklist struct {
    // ... existing fields unchanged ...
    Content *BlocklistContent `yaml:"content,omitempty" json:"content,omitempty"`
}
```

Rules:

* `Source.Type == "url"`: `Domains` is empty in every entry the new leader
  writes. `Content` is the version every node must hold. `nil` means no
  successful download yet.
* `Source.Type == "inline"`: unchanged. `Domains` holds the entries,
  `Content` stays `nil` (FS-BlocklistContentInlineStillReplicates).
* `LastRefreshAt`, `LastRefreshStatus`, `LastRefreshError` stay replicated
  and describe the leader's last download, so they are identical on all
  nodes (FS-AutoRefreshUpdatesAllNodes,
  FS-BlocklistContentDownloadFailureKeepsCopy).
* A URL list with non-empty `Domains` and `Content == nil` is a **legacy
  entry** written by a previous version (§6).

### 2.2 Node-local cache

```
<data_dir>/blocklists/<id>.list   # one normalized domain per line, sorted, LF
```

* Normalization: the output of `filter.ParseStoredDomains` for the list's
  format, lower-cased, deduplicated, sorted. Two nodes that normalize the
  same input produce the same bytes, so the same SHA-256.
* Written with tmp file + fsync + rename (same helper as
  `cluster/shadow_yaml.go:270-290`, moved to a shared package).
* The hash of a cache file is computed once when it is written or loaded
  and kept in memory (`blocklistcache.Cache`, map id → {sha256, count}).
* A cache file whose list ID is no longer in the replicated state is
  deleted at the next sync pass.
* Directory mode 0700, files 0600, owned by the daemon user.

The cache is not part of the FSM and not part of Raft snapshots. Writing it
never happens inside `Store.Apply` (audit M-1).

## 3. Raft commands

### 3.1 New: `blocklist.content_set`

```go
type BlocklistContentSetPayload struct {
    ID        string            `json:"id"`
    Content   *BlocklistContent `json:"content,omitempty"` // nil on error/unchanged
    Status    string            `json:"status"`            // ok | unchanged | error
    Error     string            `json:"error,omitempty"`
    RefreshAt string            `json:"refresh_at"`        // RFC3339
    URL       string            `json:"url"`               // source the download used
    Format    string            `json:"format"`
}
```

Apply (`store.go`): read-modify-write of the stored blocklist. It sets the
three `LastRefresh*` fields. When `Status == "ok"`, it also sets
`Content`, sets `LastUpdated = Content.FetchedAt` and clears `Domains`.
An unknown ID is a no-op, and so is a payload whose `URL` or `Format`
differs from the stored source: a PATCH that changed the source while the
download was in flight wins, and the list stays due. Entry size is under
1 KB.

### 3.2 Changed: `config.import` and `blocklist.upsert`

`importM1Config` (`store.go:1028-1055`) and the `blocklist.upsert` case
merge `Content` from the stored record: when the incoming URL list has
`Content == nil` and the stored record has the same `Source.URL` and
`Source.Format`, the stored `Content` and `LastRefresh*` are kept. This
prevents a `WithWriteLock` commit built from a stale snapshot from erasing
a concurrent `blocklist.content_set`. A changed URL or format clears
`Content`, so the list is downloaded again.

### 3.3 Changed: `member.upsert` (capability gate)

`Member` (`store.go:1358`) gains `Capabilities []string`. At startup, after
the Raft node is up, each node upserts its own member record with
`["blocklist-content-v1"]` (through the leader, the same path as
`cluster.go:964`). A node running a previous version upserts without the
field, so it has no capability.

The leader uses the new behavior (§4, §6) only when **every** member lists
`blocklist-content-v1`. Until then it keeps writing `Domains`, so old
followers keep filtering during a rolling upgrade. A previous-version node
that receives `blocklist.content_set` rejects it with
`unknown command kind` (`store.go:875`) and keeps its state.

## 4. Leader: downloading

`internal/refresh/scheduler.go` keeps its leader-only loop
(FS-AutoRefreshLeaderOnly, FS-BlocklistContentOnlyLeaderDownloads). Changes:

* **Due lists.** A URL list is due when any of these holds:
  * `RefreshIntervalSeconds > 0` and `LastRefreshAt + interval <= now`
    (unchanged rule);
  * `Content == nil` (never downloaded, or imported: fixes the import gap),
    except for a legacy entry (§2.1): the leader seeds it from its local
    copy as described in §6 step 3 and does not download it;
  * the leader's cache does not hold `Content.SHA256`
    (FS-BlocklistContentNewLeaderWithoutCopy). This rule applies whatever
    the interval, including 0.
* **`refreshOne`:**
  1. Download with `filter.Download` (30 s timeout, unchanged).
  2. On error, apply `content_set{Status: error}` only if the stored status
     or error message differs, so a list that keeps failing writes one
     entry, not one per tick. Keep the previous cache file. Fire the
     `OnDownloadFailed` hook as today.
  3. On success, normalize the domains and compute the SHA-256. If it
     equals `Content.SHA256`, apply `content_set{Status: unchanged}` (one
     small entry per interval, as `LastRefreshAt` must move). Otherwise
     write `<id>.list` first, then apply `content_set{Status: ok, Content}`.
* **Leader change.** The loop already checks `IsLeader()` on every tick
  (10 s, 1 s in test mode). A node that becomes leader evaluates the due
  rules on its first tick, so failover needs no new hook
  (FS-BlocklistContentLeaderFailover). `LeadershipCh` keeps its single
  consumer (`cmd/skoed/main.go:505`).

Create, PATCH and manual refresh (`internal/api/handlers/blocklists.go`)
already reach the leader through `WriteForwardMiddleware`. They stop
writing `Domains` for URL lists:

* **Create** (`url`) downloads on the leader as today (502 on failure),
  writes the cache file, then commits the list with `Content` set and
  `LastRefreshStatus: ok` via `WithWriteLock`.
* **PATCH** that changes `source.url` or `source.format` clears `Content`,
  so the scheduler downloads on its next tick.
* **`POST /blocklists/{id}/refresh`** runs `refreshOne` synchronously on
  the leader and returns the updated list (FS-BlocklistContentManualRefresh).
  It now sets `LastRefresh*` like the scheduler.
* **Category enable** (`handlers/categories.go:148-160`) follows the
  create path.

## 5. Followers: copying

### 5.1 Endpoint

```
GET /api/v1/cluster/_internal/blocklists/{id}/content?sha256=<hex>
X-Cluster-Secret: <secret>
Accept-Encoding: gzip
```

* Registered next to `/api/v1/cluster/_internal/aggregates`
  (`internal/api/app.go:960`), outside the auth group. The handler checks
  the secret with `ValidateClusterSecret`, and the audit middleware already
  skips `/_internal/`.
* `200`: body is the cache file, `Content-Type: text/plain; charset=utf-8`,
  gzip-encoded when accepted, `X-Skoed-Content-SHA256` set.
* `404`: unknown list or no cache file. `409`: the cache holds another
  hash than requested (the leader moved on). `401`: bad or missing secret.
* Served by any node that holds the file. Followers only ask the leader
  (non-goal: copying from a follower).
* With mTLS enabled, followers call it on the leader's peer mTLS listener:
  `Cluster.LeaderAPIAddress()` returns `https://<PeerAPIAddress>` and the
  shared peer client presents the node's certificate
  (`encrypted-cluster-mesh.md`, "Internal API").
* Response streamed from the file; no full read into memory.

### 5.2 Sync worker

New package `internal/blocklistsync`, started on every node:

* Runs on a 10 s tick (1 s in test mode) and after each cluster
  notification, debounced by 500 ms.
* For each enabled URL list with `Content != nil` whose hash differs from
  the local cache: if this node is the leader, do nothing (§4 covers it);
  otherwise fetch from `Cluster.LeaderAPIAddress()` (`cluster.go:304`).
  At most 2 fetches at a time, 60 s timeout per fetch.
* The body is written to a tmp file while it is hashed. On hash mismatch
  the tmp file is deleted and the fetch counts as failed. On match it is
  renamed over `<id>.list` and the engine is rebuilt (§7).
* On failure (no leader, network error, 404, 409, mismatch) the previous
  file is kept and the next tick retries
  (FS-BlocklistContentFollowerCannotReachLeader). With a 10 s tick and a
  60 s timeout, a follower that regains the leader copies within 30 s of
  the next tick that reaches it, as long as the transfer itself takes
  under 20 s (a 16 MB list gzips to about 4 MB).
* A node that joins the cluster receives the state through Raft and then
  copies every list through the same loop (FS-BlocklistContentNewNodeCopiesOnJoin).

### 5.3 Per-node status in the API

`GET /api/v1/blocklists[/{id}]` adds, for URL lists:

| field | meaning |
|-------|---------|
| `content_sha256` | replicated `Content.SHA256` |
| `local_content_sha256` | hash of this node's cache file, "" when none |
| `content_status` | `current` (equal), `out_of_date` (both set, different), `missing` (no local file) |

The three fields are omitted when the list has no replicated version yet
(`Content == nil`: never downloaded, or a legacy entry during a rolling
upgrade), so a rolling upgrade does not report every list as out of date.

`domain_count` becomes the count of this node's cache file for URL lists
(the replicated `Content.DomainCount` when the file is current), and
`len(Domains)` for inline lists. On a current node it equals the leader's
count (FS-BlocklistContentFollowersCopyFromLeader).

## 6. Upgrade from a version that replicated the domains

A legacy URL entry (`Domains` set, `Content == nil`) is handled as follows:

1. **Every node, at startup** (before the Raft node starts, after the store
   opens): for each legacy URL entry, normalize `Domains`, write
   `<id>.list` if no file with that hash exists, and record the hash. This
   is local disk work outside the FSM, run again after every
   `fsm.Restore` (§7).
2. **Engine fallback.** For a URL list with no current cache file, the
   engine uses the legacy `Domains` when they are present. This keeps
   filtering on nodes that have not yet seeded.
3. **The leader**, once the capability gate (§3.3) is met, applies for each
   legacy entry
   `content_set{Status: ok, Content: {sha256, count, FetchedAt: LastUpdated}}`
   computed from its own cache file. That clears `Domains` in the
   replicated state without a download
   (FS-BlocklistContentUpgradeKeepsFiltering).
4. Followers already hold a file with the same hash from step 1, so the
   sync worker finds nothing to copy.

The upgrade writes one small entry per URL list. Old entries in the Raft log
still carry the domains until the log is truncated (§8).

## 7. Engine and memory

* `filter.New` / `filter.NewProfiled` (`internal/filter/engine.go:270-319`)
  take a `ContentSource` interface: `Domains(id, sha256 string) ([]string, bool)`.
  For URL lists the engine reads the cache file through it; for inline
  lists it uses `bl.Domains` as today.
* `App.onApply` (`internal/api/app.go:575`) rebuilds the engine only when a
  fingerprint changes. The fingerprint is a hash over the filtering
  configuration (blocklists without `LastRefresh*`, allowlists, custom
  rules, profiles, schedules, categories) and the local cache hashes.
  Stats, audit, lease and member entries no longer rebuild it.
* The sync worker and the leader's `refreshOne` trigger the same rebuild
  after writing a cache file.
* `fsm.Restore` calls `onApply` after `store.init()`, so subscribers (engine,
  shadow YAML, sync worker, legacy seeding) run after an InstallSnapshot.
* With URL domains out of the config, the in-memory copies are the engine's
  `domainSet` and, during a rebuild, the parsed slice of the list being
  loaded.

## 8. Raft log

* `internal/cluster/raft.go`: `cfg.TrailingLogs = 256` next to
  `SnapshotThreshold = 64` and `SnapshotInterval = 30s`
  (FS-RaftLogStaysBounded).
* Visibility for the test and operators: `GET /api/v1/cluster/status`
  gains, for the answering node, `raft.first_log_index`
  (`logStore.FirstIndex()`), `raft.last_log_index` and
  `raft.last_snapshot_index` (`raft.Stats()`). The scenario holds when
  `last_snapshot_index - first_log_index <= 256`.
* `fsm.Persist` keeps the raw file copy for now. Moving it to a bbolt read
  transaction (`tx.WriteTo`, audit M-2) is separate M50 work.

## 9. `skoed raft compact`

New offline subcommand in `internal/cli`:

```
skoed raft compact [--config /etc/skoed/config.yaml | --data-dir /var/lib/skoed]
```

1. Resolve `data_dir` with `cluster.LoadConfig` (`cluster/node.go:225`), or
   use `--data-dir`.
2. Open `<data_dir>/raft/raft-log.bolt` with
   `bolt.Options{Timeout: 1 * time.Second, ReadOnly: true}`. A timeout
   means the daemon holds the lock: print
   `raft-log.bolt is locked: stop the skoed daemon first` and exit 1
   without touching any file (FS-RaftLogCompactRefusedWhileRunning).
3. If `raft-log.bolt.pre-compact` already exists, refuse (exit 1) so an
   earlier backup is never overwritten. Remove a leftover
   `raft-log.bolt.compact` from an interrupted run (only after the lock is
   held).
4. Create `raft-log.bolt.compact` with the original's owner and mode, then
   `bolt.Compact(dst, src, 64<<20)` into it, and fsync it.
5. Hard-link the original to `raft-log.bolt.pre-compact` (`os.Link`, which
   also fails if the backup appeared meanwhile), then rename the compacted
   file over `raft-log.bolt`, then fsync the directory. `raft-log.bolt`
   exists at every point, so a crash never leaves a node without its log.
   On a link failure, remove `.compact`; on a rename failure, remove the
   link so the next run is not refused. Print both sizes.
6. The operator deletes `raft-log.bolt.pre-compact` after the node has
   restarted and rejoined (FS-RaftLogCompactOffline).

The raft-boltdb stores opened by the daemon (`raft.go:56`) use
`raftboltdb.New(raftboltdb.Options{Path: ..., BoltOptions:
&bolt.Options{Timeout: 5 * time.Second}})` (raft-boltdb v2), so a daemon
started while a compaction holds the lock fails with
`<file> is locked by another process (skoed raft compact?)` instead of
blocking. The `raft/raft-log.bolt` path is exported from `internal/cluster`
and used by the daemon and the command alike.

Compaction only reclaims space freed by truncation, so the runbook for the
home cluster is: deploy the new version on both nodes, wait until
`last_snapshot_index - first_log_index <= 256`, then stop, compact and start
each node in turn.

## 10. Metrics and webhooks

* `skoed_blocklist_last_refresh_seconds{id}` keeps reading the replicated
  `LastRefreshAt`. `skoed_blocklist_refresh_failures_total{id}` stays a
  per-node counter, only incremented on the leader.
* New gauge `skoed_blocklist_content_current{id}`: 1 when the local cache
  matches `Content.SHA256`, 0 otherwise.
* New counter `skoed_blocklist_content_fetch_failures_total{id}` on
  followers.
* `EventBlocklistDownFailed` keeps firing from the leader only.

## 11. Error handling

| Condition | Behavior |
|-----------|----------|
| Leader download fails | previous file kept, one `content_set{error}` entry while the error stays the same |
| Cache write fails on the leader (disk full) | no `content_set`, error logged, list retried next tick |
| Follower cannot reach the leader, or no leader | previous file kept, `content_status: out_of_date`, retried every tick |
| Fetched body hash mismatch | tmp file deleted, fetch counted as failed |
| Cache file corrupt at startup (unreadable) | file deleted, list treated as `missing` |
| Capability gate not met | leader keeps the legacy behavior, logs once per leadership term which members lack the capability |

## 12. Security

* The content endpoint requires the cluster secret, like the other
  `/_internal/` routes. With `node.cluster.mtls.enabled: true` it is served
  and called over the peer mTLS listener (`encrypted-cluster-mesh.md`,
  "Internal API"), so the content and the secret are encrypted and both
  ends are authenticated by the cluster CA. With mTLS disabled it is plain
  HTTP, like all peer HTTP; the SHA-256 check then catches a corrupted or
  truncated transfer only.
* `{id}` is matched against the replicated list IDs before any file path is
  built; an unknown ID returns 404, so no path traversal through `id`.
* Cache files are 0600 under the 0700 data directory.

## 13. Test hooks

No new test-only switch in the binary. The acceptance tests run without
the internet and use:

* A local `httptest` server that serves the list. `filter.Download` sends
  `User-Agent: skoed/<version> (node <node_id>)`, so the server counts
  requests per node even though all test nodes connect from 127.0.0.1
  (FS-BlocklistContentOnlyLeaderDownloads). The header also tells list
  hosts which software is downloading.
* `SKOED_TEST_MODE=1`, which already shortens ticks.
* **A real network cut** for FS-BlocklistContentFollowerCannotReachLeader:
  * The harness starts the follower to cut off under its own uid
    (`SysProcAttr.Credential`, uid 65534), with a data dir it owns.
  * The test adds
    `iptables -A OUTPUT -m owner --uid-owner 65534 -p tcp -d 127.0.0.1 --dport <leader API port> -j REJECT`,
    so only that follower loses the leader's API while Raft keeps
    replicating. It removes the rule to restore access.
  * `tests/acceptance/run-in-docker.sh` adds `--cap-add NET_ADMIN` and
    installs `iptables` in the container.
  * Without root or without `iptables`, the test is skipped with a message
    saying so; it never passes silently.
* `GET /api/v1/cluster/status` (§8) and `GET /api/v1/blocklists` (§5.3)
  expose everything the assertions read.

Real-environment validation repeats the cut on a Proxmox 3-node cluster:
on one follower CT, `iptables` rejects TCP to the leader's API port (and its
peer API port with mTLS on) while the Raft port stays open.

## 14. Out of scope

* Changing `WithWriteLock` to send per-entity commands instead of a full
  `config.import`. Once URL domains are out of the config, each such entry
  is a few KB.
* Encrypting peer HTTP.
* Compacting `cluster.bbolt`.
