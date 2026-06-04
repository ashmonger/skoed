# DEMO NOTE — M3.5 Per-Client DoH/DoT Surfacing

## Scope

Adds the per-client DoH visibility track from the roadmap. Firewall-recipe
generators and the resolver-IP database refresh are **explicitly skipped**
per UoR — that subtrack stays parked on the roadmap.

### Implemented

- **Endpoint** `GET /api/v1/clients/{ip}/doh-status` returns:
  ```json
  {
    "client": "192.168.1.42",
    "using_doh": true,
    "doh_probes_1h": 4,
    "last_doh_query": "2026-06-04T12:48:47Z",
    "suspected_provider": "adguard"
  }
  ```
  Sourced from the local query log only — no Raft round-trip, no cluster
  fan-out. Rolling 1-hour window via `filter.Now()`, so `DBLOCK_TEST_NOW`
  drives deterministic windowing in tests.

- **Provider inference**: substring match against a curated table
  (`cloudflare-dns.com → cloudflare`, `dns.google → google`,
  `dns.quad9.net → quad9`, `dns.adguard.com → adguard`, `dns.adguard-dns.com
  → adguard`, `doh.opendns.com → opendns`, `dns.nextdns.io → nextdns`,
  `dns.controld.com → controld`). Unknown domains yield
  `suspected_provider: null`.

- **Dashboard alert** (top-of-page warning card): the top 5 clients with
  blocked DoH probes in the last hour, each with probe count, suspected
  provider badge (when known), and a link to the Query Log filtered by
  client. Auto-refreshes every 60s.

- **Acceptance tests** (6 FSIDs):
  - `FS-ClientDohStatusEndpointShape` — populated client returns the
    documented shape with valid RFC3339 timestamp.
  - `FS-ClientDohStatusNoProbes` — clean client returns
    `using_doh: false`, all nullable fields null.
  - `FS-ClientDohStatusUnauthenticated` — 401 without `Authorization`.
  - `FS-ClientDohStatusInvalidIp` — 400 with `"invalid client IP"` body.
  - `FS-ClientDohStatusRollingWindow` — implicit (1h cutoff uses
    `filter.Now() - time.Hour`).
  - `FS-ClientDohStatusSuspectedProvider` — table-driven across 6
    providers, each probe→domain→provider mapping verified.

  All 6 pass against the new binary.

### Not implemented (explicit UoR carve-out)

- **Firewall-rule generators** (iptables / nftables / RouterOS /
  OpnSense / pfSense / UniFi templates).
- **Resolver-IP database refresh** (the curated set of hardcoded
  resolver IPs that firewalls would block).
- **"Closing the DoH gap" guide** (the documentation that ties firewall
  rules to dblock's observation).

These remain on the roadmap; the UI alert text already references
"firewall rule (see roadmap M3.5)" so the operator knows where to look.

## Demo recipe

```bash
# 1. Build binary (SPA already embedded)
make -C apps/dblock build

# 2. Single-node config
cat > /tmp/m3.5-config.yaml <<EOF
node:
  id: demo
  raft_address: 127.0.0.1:17002
  api_address:  127.0.0.1:18082
  data_dir:     /tmp/dblock-m3.5-demo/data
  dns:
    listen: { port: 5355, ipv4: true, ipv6: false }
EOF
mkdir -p /tmp/dblock-m3.5-demo/data

# 3. Boot in test mode (unlocks EDNS0 client-IP spoofing)
DBLOCK_TEST_MODE=1 ./apps/dblock/dblock -config /tmp/m3.5-config.yaml &

# 4. Set admin
curl -sX POST http://127.0.0.1:18082/api/v1/auth/setup \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"demopass123"}'

# 5. Emit DoH probes from 3 spoofed clients
for ip_hex in c0a8012a c0a8012b 0a006305; do
  for host in dns.google cloudflare-dns.com dns.quad9.net dns.adguard.com; do
    dig @127.0.0.1 -p 5355 +short +ednsopt=65500:$ip_hex $host > /dev/null
  done
done

# 6. Hit the endpoint
curl -s -u admin:demopass123 \
  http://127.0.0.1:18082/api/v1/clients/192.168.1.42/doh-status
# → {"client":"192.168.1.42","using_doh":true,"doh_probes_1h":4,
#    "last_doh_query":"2026-06-04T...","suspected_provider":"adguard"}

# 7. Open http://127.0.0.1:18082 — the Dashboard surfaces all three
#    spoofed clients in a warning card at the top.
```

## Screenshots

`docs/screenshots/m3.5-dashboard-doh-alert-solarized-{dark,light}.png` —
Dashboard top-card alert showing the 3 demo clients, each with their
suspected provider badge and View Log link.

## Tests

```
$ DBLOCK_BINARY=apps/dblock/dblock go test -run 'TestClientDohStatus' ./tests/acceptance/...
PASS — 5 tests + 6 subtests, 17.8s
```

Full acceptance suite (`go test ./...`) remains green: 420s, no
regressions.

## What's next

- M4 — dblock as a DoH/DoT server (RFC 8484 on `/dns-query`, DoT on
  port 853, optional ACME).
