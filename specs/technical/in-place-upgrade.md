---
x-tsid: TS-InPlaceUpgrade
x-fsid-links:
  - FS-UpgradeCheckEndpoint
  - FS-UpgradeCheckRequiresAuth
  - FS-UpgradeStartRequiresLeader
  - FS-UpgradeStartRecordedInAudit
  - FS-UpgradeBinarySwap
  - FS-UpgradeBannerOnDashboard
  - FS-UpgradeNoBannerWhenCurrent
---

# TS-InPlaceUpgrade — release feed, binary swap, UI banner

## Config

```yaml
node:
  upgrade:
    feed_url: https://releases.skoed.io/feed.json
    poll_interval_seconds: 21600     # 6h
    require_signature: false         # M5.6 v1; M5.6.1 flips default
    cosign_pub_key: ""               # PEM; required when require_signature
```

The release feed is a tiny JSON document published alongside each
release. Asset URLs point to GitHub Releases for the `ashmonger/skoed`
repository:

```json
{
  "version": "0.1.3",
  "published_at": "2026-06-16T12:00:00Z",
  "release_notes_url": "https://github.com/ashmonger/skoed/releases/tag/v0.1.3",
  "assets": {
    "linux_amd64": "https://github.com/ashmonger/skoed/releases/download/v0.1.3/skoed_0.1.3_linux_amd64.tar.gz",
    "linux_arm64": "https://github.com/ashmonger/skoed/releases/download/v0.1.3/skoed_0.1.3_linux_arm64.tar.gz"
  }
}
```

The asset key is `runtime.GOOS + "_" + runtime.GOARCH` (e.g. `linux_amd64`).
Each `.tar.gz` archive contains a single `skoed` binary at the archive root.

## Service

`internal/upgrade/checker.go` — single goroutine per node, polls
`feed_url` on `poll_interval_seconds`. Caches the latest feed snapshot
in memory; the API and UI read from the cache (no per-request fetch).

`internal/upgrade/swapper.go` (M16) — executes the binary swap:

1. Look up `assets[runtime.GOOS+"_"+runtime.GOARCH]` from the cached feed.
2. Download the tarball to a temp file in the same directory as the running binary.
3. Extract the `skoed` binary entry from the tarball.
4. Write extracted bytes to `<exe_path>.new` in the same directory.
5. `chmod 0755` on `<exe_path>.new`.
6. `os.Rename("<exe_path>.new", exe_path)` — atomic on Linux (same filesystem).
7. Return `SwapResult{OK: true, TargetVersion: feed.Version}`.
8. Caller schedules `os.Exit(0)` after the HTTP response is flushed; the
   supervisor (systemd / OpenRC) restarts the process with the new binary.

When `SKOED_TEST_MODE=1`, step 8 is skipped — the process stays up so acceptance
tests can verify the swap without crashing the harness.

## API

| Path                          | Method | Auth | Behaviour                                  |
|-------------------------------|--------|------|--------------------------------------------|
| `/api/v1/upgrade/check`       | GET    | yes  | Returns the cached feed + `upgrade_available`. |
| `/api/v1/upgrade/start`       | POST   | yes  | Downloads + swaps binary, returns 202, schedules exit(0). Forwarded to leader. |

Response shape for `/check`:

```json
{
  "current_version":   "0.5.0",
  "available_version": "0.5.1",
  "upgrade_available": true,
  "release_notes_url": "https://github.com/skoed/skoed/releases/tag/v0.5.1",
  "published_at":      "2026-07-01T09:00:00Z",
  "checked_at":        "2026-07-01T10:23:11Z"
}
```

When the feed is unreachable, `available_version` is empty and
`upgrade_available` is false; `checked_at` reflects the last
successful poll.

## Version comparison

Strict semver via `golang.org/x/mod/semver`. Pre-releases (`v0.5.0-rc1`)
are NEVER considered upgrades from a stable; the operator opts into
pre-release tracks via a future `node.upgrade.channel` field
(M5.6.1).

## Web UI

- **Dashboard** banner card at the top:
  - Visible iff `upgrade_available` and operator is admin.
  - "Upgrade available: <new-version>" + "Release notes" link.
  - "Upgrade now" button → calls `/upgrade/start`, then shows a
    spinner + the audit-log-style result.

## Audit + metrics

- Audit middleware (M5.2) tags `POST /api/v1/upgrade/start` as
  `upgrade.start`.
- No new Prometheus series for v1 — the feed cache + the audit
  counter cover operational observability.

## M16 response shape for `/start` (202)

```json
{
  "accepted":       true,
  "target_version": "0.1.4",
  "message":        "binary swap initiated; process will restart"
}
```

Error cases:

| Condition | Status | body `error` |
|-----------|--------|--------------|
| No feed configured | 503 | `upgrade feed is not configured` |
| Already on latest | 409 | `no upgrade available (running latest)` |
| No asset for this arch | 422 | `no asset for linux_amd64 in feed` |
| Download or swap failed | 500 | `swap failed: <detail>` |

## Non-goals for M16

- Cosign/signature verification (`require_signature` stays false; M5.6.1)
- Rolling cluster upgrade (nodes upgrade independently; coordinated rolling is M5.6.1)
- Rollback (`--rollback` flag is M5.6.1)
