# M16 Demo Note — In-place Upgrade Binary Swap

## Implemented scope

### Binary swap on POST /api/v1/upgrade/start

`POST /api/v1/upgrade/start` now performs a real binary swap when an upgrade is available:

1. Reads `Assets` from the cached feed — a `map[string]string` keyed by `runtime.GOOS + "_" + runtime.GOARCH` (e.g. `"linux_amd64"`).
2. Downloads the `.tar.gz` at the matching asset URL (120 s HTTP timeout).
3. Extracts the `skoed` entry from the archive (capped at 256 MiB).
4. Writes the extracted binary to a temp file beside the executable (`skoed_new_*.tmp`), `chmod 0755`.
5. Atomically replaces the running executable via `os.Rename` (single syscall, no window where the file is absent).
6. Returns HTTP 202 with `{"accepted": true, "target_version": "...", "message": "binary swap initiated; process will restart"}`.
7. Schedules `os.Exit(0)` after 200 ms so the supervisor (systemd / OpenRC) restarts the process with the new binary.

**Feed format** — the feed JSON now carries an `assets` field:

```json
{
  "version": "0.1.4",
  "published_at": "2026-06-16T10:00:00Z",
  "release_notes_url": "https://github.com/ashmonger/skoed/releases/tag/v0.1.4",
  "assets": {
    "linux_amd64": "https://github.com/ashmonger/skoed/releases/download/v0.1.4/skoed_0.1.4_linux_amd64.tar.gz",
    "linux_arm64": "https://github.com/ashmonger/skoed/releases/download/v0.1.4/skoed_0.1.4_linux_arm64.tar.gz"
  }
}
```

**Error responses:**

| Condition | Status |
|-----------|--------|
| No feed configured | 503 |
| Already on latest | 409 |
| No asset for current arch | 422 |
| Download or extraction failure | 500 |

**Test mode** — `SKOED_TEST_MODE=1` skips `os.Exit(0)` so the test process survives. `SKOED_TEST_SWAP_DEST=<path>` redirects the rename target so acceptance tests don't overwrite the binary used by the rest of the suite.

### Bug fixed: binary swap corrupted test suite

`TestUpgradeBinarySwap` was calling `upgrade.Swap()` without redirecting the target, which overwrote `apps/skoed/skoed` with the fake sentinel shell script. All subsequent tests that spawned skoed processes failed with a 90 s timeout (`skoed API did not become ready`).

Fix: added `SKOED_TEST_SWAP_DEST` env var to all three tests that call `POST /api/v1/upgrade/start`. The env var is checked in the handler (not in `Swap()`) so production code paths are unaffected.

## Not implemented (non-goals for M16)

- Cosign signature verification of downloaded assets.
- Rolling cluster upgrade (nodes upgrade independently; coordinated rolling is M5.6.1).
- Channel selection (stable / beta / nightly) — single feed URL only.
- Rollback on failed restart (requires supervisor integration beyond scope).
- Progress streaming over SSE during download.

## Limitations

- The swap is atomic at the filesystem level (`os.Rename`), but the process restarts unconditionally after 200 ms. If the new binary fails to start, systemd/OpenRC must be configured to restart on failure (the default `Restart=on-failure` unit is sufficient).
- The feed URL is polled on a 6-hour interval. An operator trigger for an immediate feed refresh is not exposed; operators can restart skoed to force a first-poll.
- Asset URL is trusted as-is (no TLS certificate pinning beyond the system trust store).

## Test results

### Acceptance test suite

New tests added for M16:

| Test | FSID | Result |
|------|------|--------|
| `TestUpgradeBinarySwap` | FS-UpgradeBinarySwap | PASS |
| `TestUpgradeStartRecordedInAudit` | FS-UpgradeStartRecordedInAudit | PASS (skip removed) |
| `TestUpgradeStartForwardedToLeader` | FS-UpgradeStartRequiresLeader | PASS (skip removed) |
| `TestUpgradeCheckEndpoint` | FS-UpgradeCheckEndpoint | PASS |
| `TestUpgradeCheckRequiresAuth` | FS-UpgradeCheckRequiresAuth | PASS |

Full suite: `ok skoed/acceptance 138.427s` — 409 tests, 0 failures, 0 new skips.
