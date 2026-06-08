---
x-tsid: TS-InPlaceUpgrade
x-fsid-links:
  - FS-UpgradeCheckEndpoint
  - FS-UpgradeCheckRequiresAuth
  - FS-UpgradeStartRequiresLeader
  - FS-UpgradeStartRecordedInAudit
  - FS-UpgradeBannerOnDashboard
  - FS-UpgradeNoBannerWhenCurrent
---

# TS-InPlaceUpgrade — release feed + UI banner

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
release:

```json
{
  "version": "0.5.1",
  "published_at": "2026-07-01T09:00:00Z",
  "release_notes_url": "https://github.com/skoed/skoed/releases/tag/v0.5.1",
  "assets": {
    "linux_amd64": "https://releases.skoed.io/0.5.1/skoed_0.5.1_linux_amd64.tar.gz",
    "linux_arm64": "https://releases.skoed.io/0.5.1/skoed_0.5.1_linux_arm64.tar.gz"
  },
  "signatures": {
    "linux_amd64": "https://releases.skoed.io/0.5.1/skoed_0.5.1_linux_amd64.tar.gz.cosign",
    "linux_arm64": "https://releases.skoed.io/0.5.1/skoed_0.5.1_linux_arm64.tar.gz.cosign"
  }
}
```

## Service

`internal/upgrade/checker.go` — single goroutine per node, polls
`feed_url` on `poll_interval_seconds`. Caches the latest feed snapshot
in memory; the API and UI read from the cache (no per-request fetch).

## API

| Path                          | Method | Auth | Behaviour                                  |
|-------------------------------|--------|------|--------------------------------------------|
| `/api/v1/upgrade/check`       | GET    | yes  | Returns the cached feed + `upgrade_available`. |
| `/api/v1/upgrade/start`       | POST   | yes  | Triggers the upgrade. Forwarded to leader. M5.6 v1: returns 202 and writes audit entry. |

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

## v1 scope vs. v1.5

For M5.6 v1 the `/upgrade/start` endpoint accepts the request, writes
the audit entry, and returns 202 with a synthetic message — it does
NOT actually swap the binary. The download + verify + swap pipeline is
implemented in `internal/upgrade/swap.go` but gated behind
`node.upgrade.enable_swap = true` (default false). The reason: live
binary-swap on every operator's prod cluster needs an integration test
matrix we haven't built yet (M5.7 multi-arch + M5.5 packaging are
prerequisites). The banner + check + audit path lands now so we can
ship the swap flip later with zero UI/API churn.
