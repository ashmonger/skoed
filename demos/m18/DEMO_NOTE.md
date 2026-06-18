# M18 Demo Note — Rolling Cluster Upgrade + Read Load Balancing

## Milestone summary

M18 delivers zero-downtime rolling upgrades for multi-node skoed clusters. A single API call
on the leader upgrades all nodes sequentially, preserving quorum throughout.

## Implemented scope

### Rolling upgrade (FS-RollingUpgradeOrchestrated, FS-RollingUpgradeStatus, FS-RollingUpgradeAbortOnFailure, FS-RollingUpgradeLeadershipTransfer)

- `POST /api/v1/cluster/upgrade/apply` — triggers rolling upgrade from the leader; accepts `{"url": "https://…/skoed.tar.gz"}`
- `GET /api/v1/cluster/upgrade/status` — returns `{in_progress, pending_nodes, completed_nodes, failed_node}`
- `POST /api/v1/upgrade/node-start` — cluster-internal per-node trigger; authenticated by `X-Cluster-Secret`; intentionally outside `WriteForwardMiddleware` so followers self-upgrade instead of forwarding to the leader
- Sequential upgrade: followers first (sorted by node ID), then leadership transfer, then self-upgrade
- Health polling: each node is polled on `GET /api/v1/health` for up to 120 s after the swap; upgrade aborts if the node does not return `200 OK` in time
- Leadership transfer: `Raft.LeadershipTransfer()` to the first successfully upgraded follower before the leader upgrades itself
- Adblock format fix: `parseByFormat` now maps `"adblock"` to `ParseAskoed` (same parser as `"askoed"`), fixing blocklist sync for feeds using the `adblock` format label

### Read load balancing (FS-FollowerReadsDirectly, FS-FollowerForwardsMutations)

- All `GET`/`HEAD` requests are served locally on any node (no forwarding)
- `X-Served-By` identifies the responding node on every response
- Mutating `POST`/`PUT`/`PATCH`/`DELETE` requests on followers are still forwarded to the leader via `WriteForwardMiddleware`

### Real-environment validation (2026-06-18, Proxmox 3-node cluster)

```
Cluster:  CT 200 (skoed-1, Debian, leader)
          CT 201 (skoed-2, Alpine, follower)
          CT 202 (skoed-3, Debian, follower)

Upgrade timeline:
  10:32:55 → node-start on 10.0.0.100:8080 (CT 200) → 202 Accepted → healthy in 6 s
  10:33:01 → node-start on 10.0.0.102:8080 (CT 202) → 202 Accepted → healthy in 9 s
  10:33:10 → node-start on 10.0.0.101:8080 (CT 201, self) → 202 Accepted → healthy
  10:33:16 → cluster fully recovered; skoed-1 elected leader

Post-upgrade DNS filtering verified:
  CT 204 (kids profile):   pornhub.com → NXDOMAIN ✓, google.com → resolved ✓
  CT 205 (adults profile): doubleclick.net → NXDOMAIN ✓, google.com → resolved ✓
  CT 206 (iot profile):    oisd-small blocked domains → NXDOMAIN ✓, google.com → resolved ✓
```

## Not implemented

- Canary-style partial rollouts (all-or-nothing per spec non-goal)
- Automated rollback on version mismatch (operator keeps the prior binary)
- Blue-green node replacement
- Upgrade progress via WebSocket/SSE (HTTP polling only)
- UI for upgrade (API-only)

## Limitations

### Systemd (Debian/Ubuntu nodes)

`ProtectSystem=strict` makes `/usr/bin` read-only inside the service sandbox. The binary
must live in a directory included in `ReadWritePaths`.

Setup required on Debian nodes:

```sh
mkdir -p /var/lib/skoed/bin
cp /usr/bin/skoed /var/lib/skoed/bin/skoed
chown skoed:skoed /var/lib/skoed/bin/skoed
ln -sf /var/lib/skoed/bin/skoed /usr/bin/skoed
```

The systemd unit must have `Restart=always` (not `Restart=on-failure`) because the binary
swap exits with code 0 and `on-failure` does not restart on clean exit.

### OpenRC (Alpine nodes)

The init script must use `supervisor="supervise-daemon"` so that `os.Exit(0)` after the
swap triggers an automatic restart. The default `command_background=true` marks the
service as "crashed" on clean exit.

### Archive format

The upgrade tar.gz must contain a binary entry named exactly `skoed` (the `filepath.Base`
of the tar header path). Archives named differently (e.g. `skoed-latest`) are rejected by
the extractor.

### Conflict guard

Only one rolling upgrade may run at a time. A second `POST /api/v1/cluster/upgrade/apply`
returns `409 Conflict` while the first is in progress.

## Screenshots

| File | Description |
|------|-------------|
| `01-dashboard.png` | Dashboard overview post-upgrade |
| `02-cluster-status-post-upgrade.png` | All 3 nodes healthy after rolling upgrade |
| `03-query-log-post-upgrade.png` | DNS query log showing blocked + allowed traffic |
| `04-profiles-overview.png` | Kids / Adults / IoT profiles with client IPs |
| `05-blocklists-with-counts.png` | Blocklists with resolved domain counts |
| `06-api-docs-overview.png` | API documentation browser |
| `08-follower-cluster-status.png` | Follower node cluster view |
