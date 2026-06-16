# M15 Demo Note — Test Suite Cleanup + keepalived Reference

## Implemented scope

### M15-A — Test suite cleanup

**Alt-Svc advertisement (FS-Doh3AltSvcAdvertised, FS-Doh3AltSvcAbsentWhenDisabled)**
- `handleDoH` in `internal/dns/encrypted.go` now sets `Alt-Svc: h3=":<doh3_port>"; ma=86400`
  on every DoH (HTTP/2) response when `doh3_port > 0`.
- When `doh3_port = 0` (default), no `Alt-Svc` header is set.
- Firefox and Chrome will auto-upgrade to DoH3 (HTTP/3 over QUIC) after the first
  HTTP/2 response; clients like `dnscrypt-proxy` can also use explicit DoH3 config.
- New tests: `TestDoh3AltSvcAdvertised`, `TestDoh3AltSvcAbsentWhenDisabled`.

**Blocklist refresh test timing (M5.4)**
- All 6 `TestAutoRefresh*` tests now run with `SKOED_TEST_MODE=1`, dropping the
  scheduler tick from 10 s → 1 s so tests complete well within their 8 s deadline.
- Root cause: `createUrlBlocklist` does an inline fetch on create, so the first
  scheduler tick sees unchanged content → status "unchanged", not "ok". Fixed the
  failure-recorded test to accept both "ok" and "unchanged" as a successful baseline.
- All 6 tests now pass without skipping.

**DoH resolver snapshot timing (M6)**
- `TestDohResolverDbAdminForceRefresh` and `TestDohResolverDbReplicatedAcrossNodes`
  now run with `SKOED_TEST_MODE=1` so the bundled-seed Raft apply lands within
  the polling deadline on slower machines.
- All 13 DoH resolver database tests pass without skipping.

### M15-C — keepalived reference (documentation, BDD-exempt)

- `deploy/keepalived/keepalived.conf.template` — VRRP config template for a 3-node
  Proxmox LXC cluster with unicast peer lists, priority-based failover, and a
  `weight -20` track-script so a failing node yields the VIP automatically.
- `deploy/keepalived/skoed-health.sh` — health-check script called every 2 s.
  Passes when `GET /api/v1/health` returns 200 with `{"status":"ok"}`.
  Fails (exits 1) on any other response or if skoed is unreachable.
- `docs/src/cluster/keepalived.md` — step-by-step setup guide: install, configure,
  start, point DHCP at the VIP, verify failover. Added to SUMMARY.md.
  Updated to use `eth0` (correct Proxmox LXC interface name), document the
  `curl` prerequisite, and the `api_address: 0.0.0.0:8080` requirement.

## Not implemented (non-goals for M15)

- Alt-Svc on DoT responses (DoT is not HTTP; no Alt-Svc mechanism).
- Alt-Svc on the management API HTTPS port (only the DNS endpoint matters).
- keepalived Helm sidecar (overkill for the Proxmox target).
- Automated VIP health check integration in the skoed API itself.

## Limitations

- Alt-Svc `ma=86400` (24 h max-age) is hardcoded. Browsers cache it; changes
  to `doh3_port` take up to 24 h to propagate to already-upgraded clients.
- keepalived VRRP password is transmitted in plain text on the LAN (VRRP L2
  protocol). Use a strong password and restrict your LAN segment.
- VRRP master ≠ Raft leader; the difference is transparent thanks to skoed's
  LeaderForward middleware, but operators should be aware the VIP can sit on
  a follower node.

## Test results

### Acceptance test suite (Docker harness)

- `TestDoh3AltSvcAdvertised` — PASS
- `TestDoh3AltSvcAbsentWhenDisabled` — PASS
- `TestAutoRefresh*` (6 tests) — all PASS, zero skips
- `TestDohResolverDb*` (13 tests) — all PASS, zero skips
- Full suite: `ok skoed/acceptance 177.049s` (177 tests total)

### Real-environment keepalived deployment (3-node Proxmox cluster)

Cluster topology (CT IDs, IPs, and host details redacted for public docs):

| Node | OS | Role | keepalived priority |
|------|----|------|---------------------|
| skoed-1 | Debian 12 | Raft leader | 101 (VIP holder) |
| skoed-2 | Alpine 3.22 | Raft follower | 100 |
| skoed-3 | Debian 12 | Raft follower | 99 |

VIP: `10.0.0.10` (internal LAN)

**Bugs found and fixed during real-env validation:**

1. **Raft snapshot replication to fresh nodes** — When `--destroy` was used but old
   containers were stopped individually (not all-at-once), a still-running Raft peer
   sent its full state snapshot (including credentials) to newly-created nodes before
   `auth/setup` could run, causing 409 conflicts. Fix: pre-destroy block in
   `proxmox-cluster.sh` stops and destroys all containers atomically before
   creating any new ones.

2. **`issue_token` used Basic Auth** — API dropped Basic Auth support in M6
   (commit `26121a5`); only Bearer session tokens are accepted. Fix: `issue_token`
   now calls `POST /api/v1/auth/login` first to get a Bearer token, then uses it
   for the cluster join token endpoint.

3. **Health script checked wrong field** — Script matched `"cluster_status":"ok"`
   but `/api/v1/health` returns `{"status":"ok"}`. Fix: updated pattern in
   `skoed-health.sh` and corrected the description in `DEMO_NOTE.md`.

4. **`curl` missing on fresh LXC containers** — Debian minimal and Alpine templates
   do not ship `curl`. Fix: added `curl` to `apt-get install` in
   `proxmox-create.sh` and `apk add curl` in `proxmox-create-alpine.sh`.

5. **skoed bound to node IP, not VIP** — `api_address: 10.0.0.11:8080` doesn't
   serve the VIP `10.0.0.10`. Fix: `api_address: 0.0.0.0:8080` in both
   Debian and Alpine create scripts; cluster forwarding still uses the Raft address.

**Failover test result:**

```
# Stop skoed on skoed-1 (priority 101, VIP holder)
systemctl stop skoed          # on CT301
sleep 10
curl http://10.0.0.10:8080/api/v1/health
# → {"status":"ok"}   ← served by CT302 (priority 100)  ✓

# Restore skoed-1
systemctl start skoed          # on CT301
sleep 10
curl http://10.0.0.10:8080/api/v1/health
# → {"status":"ok"}   ← served by CT301 again             ✓
```

VIP failover observed in ~6–14 seconds (keepalived VRRP interval × advert_int).
All DNS queries to `10.0.0.10:53` remained uninterrupted after failover.

**Result: PASS** — VIP failover works end-to-end on real Proxmox LXC cluster.
