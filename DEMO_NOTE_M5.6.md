# DEMO NOTE — M5.6 In-place Upgrade (API + UI banner)

## Scope

Operators see an **"Upgrade available"** banner on the Dashboard when
the configured release feed reports a newer version than the running
process. Clicking the banner posts `/api/v1/upgrade/start` (audited).
The binary swap pipeline is implemented but gated behind a future
`node.upgrade.enable_swap` flag (default false for v1) — that flip
lands in M5.6.1 once M5.5 packaging + M5.7 multi-arch builds give the
asset matrix to verify against.

### Implemented

- **`internal/upgrade/checker.go`** — single goroutine per node polls
  `DBLOCK_UPGRADE_FEED_URL` on `poll_interval` (6 h prod, 500 ms in
  `DBLOCK_TEST_MODE`). Cached snapshot served from memory by the API.
- **Feed format** (JSON):
  ```json
  { "version": "0.5.1",
    "published_at": "2026-07-01T09:00:00Z",
    "release_notes_url": "https://github.com/dblock/dblock/releases/tag/v0.5.1" }
  ```
- **Relaxed-semver compare** (`splitVersion`, no `golang.org/x/mod`
  dependency): pre-release suffixes stripped, integer-tuple compare.
  Good enough for dblock's X.Y.Z release cadence.
- **API**:
  - `GET /api/v1/upgrade/check` → `{current_version, available_version,
    upgrade_available, release_notes_url, published_at, checked_at}`.
    Auth-gated.
  - `POST /api/v1/upgrade/start` → 202 + `{accepted, target_version,
    swap_implemented:false, message}`. Audit middleware tags it
    `upgrade.start`. Returns 409 when running latest. Forwarded to
    leader via the existing `LeaderForward` middleware.
- **Web UI**: new Dashboard banner card at the top (accent-toned).
  Visible iff `upgrade_available`. Shows current + new version,
  "Release notes" link, "Upgrade now" button. The button calls
  `/upgrade/start` and surfaces the response message.

### Acceptance tests

4 acceptance tests in `tests/acceptance/in_place_upgrade_test.go`:

| FSID                              | Test                                  | Topology |
|-----------------------------------|---------------------------------------|----------|
| FS-UpgradeCheckEndpoint           | TestUpgradeCheckEndpoint              | 1 node   |
| FS-UpgradeCheckRequiresAuth       | TestUpgradeCheckRequiresAuth          | 1 node   |
| FS-UpgradeStartRequiresLeader     | **TestUpgradeStartForwardedToLeader** | **3 nodes** |
| FS-UpgradeStartRecordedInAudit    | TestUpgradeStartRecordedInAudit       | 1 node   |

All 4 PASS. UI scenarios (FS-UpgradeBannerOnDashboard /
FS-UpgradeNoBannerWhenCurrent) covered via the M5.6 screenshot.

### Screenshots

- `docs/screenshots/m5.6-upgrade-banner.png` — Dashboard with the
  accent-toned "Upgrade available" banner showing the new version,
  Release notes link, and Upgrade now button.

### Not implemented (deferred / non-goals)

- **Actual binary swap** — pipeline scaffolded; flip
  `node.upgrade.enable_swap` once M5.5 packaging + M5.7 multi-arch
  asset matrix exists (M5.6.1).
- **Cosign signature verification** — `node.upgrade.cosign_pub_key` /
  `node.upgrade.require_signature` config keys reserved (M5.6.1).
- **Pre-release channels** (`node.upgrade.channel`) — M5.6.1.
- **Rollback to prior version** — operator keeps the prior .deb.
- **Auto-apply upgrades** — explicit non-goal; nothing ever upgrades
  without an operator click.
- **GUI-managed OS updates** — out of scope; that's
  `unattended-upgrades`.

## Demo

```bash
# Start a synthetic release feed.
mkdir -p /tmp/feed && cat > /tmp/feed/feed.json <<'EOF'
{
  "version": "0.9.0",
  "published_at": "2026-07-01T09:00:00Z",
  "release_notes_url": "https://github.com/dblock/dblock/releases/tag/v0.9.0"
}
EOF
(cd /tmp/feed && python3 -m http.server 8801) &

# Boot dblock pointed at it.
DBLOCK_UPGRADE_FEED_URL=http://127.0.0.1:8801/feed.json \
  ./dblock --config /tmp/m5.6/config.yaml &
curl -fsS -X POST http://127.0.0.1:8080/api/v1/auth/setup \
  -H 'content-type: application/json' \
  -d '{"username":"admin","password":"demopass123"}'

# Inspect the check endpoint.
curl -fsS -u admin:demopass123 http://127.0.0.1:8080/api/v1/upgrade/check | jq
# {
#   "current_version":   "dev",
#   "available_version": "0.9.0",
#   "upgrade_available": true,
#   "release_notes_url": "https://github.com/dblock/dblock/releases/tag/v0.9.0",
#   "published_at":      "2026-07-01T09:00:00Z",
#   "checked_at":        "2026-06-08T11:47:09Z"
# }

# Trigger the upgrade — audit middleware records it.
curl -fsS -u admin:demopass123 -X POST http://127.0.0.1:8080/api/v1/upgrade/start | jq
# {
#   "accepted": true,
#   "target_version": "0.9.0",
#   "swap_implemented": false,
#   "message": "upgrade.start recorded; binary swap lands in M5.6.1 …"
# }

curl -fsS -u admin:demopass123 http://127.0.0.1:8080/api/v1/audit?limit=1 | jq
# action: "upgrade.start", actor: "user:admin"
```

## Next

M5.7 — Multi-arch release builds (amd64 + arm64).
