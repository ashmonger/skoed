# M6.5 Demo Note — DHCP Layer-3 Anti-Spoof + Replicated Leases

Branch: `dblock-m6.5`  
Commit: `d14f3de`  
Tests: 337 / 337 pass (178 s, −5.3× vs sequential)

---

## Implemented scope

| Feature | TSID | Tests |
|---------|------|-------|
| Lease origin tagging — http_json connector honours the `origin` wire field; maps to `dhcp_static` / `dhcp_dynamic` / `router_advertised` / `manual_admin` with `high` confidence; blank wire field → `dhcp_dynamic` / `unknown` (safe default) | TS-LeaseOrigin | 12 |
| DHCPv6 lease parsing — `ip6leases.conf` parser; IPv6-only clients appear in `/api/v1/clients`; dual-stack clients merged; legacy v4-only shape unchanged | TS-Dhcpv6Lease | 19 |
| Raft-replicated lease cache — leader polls DHCP; snapshot replicated through Raft; followers serve `/api/v1/leases` from their local bbolt replica without extra polling | TS-LeaseRepl | 11 |
| ARP/NDP cross-check — rtnetlink detects IP→MAC mismatches between DHCP and kernel; `SKOED_TEST_ARP_TABLE` injects a fake table in tests; degrades gracefully when `NET_ADMIN` cap is absent | TS-ArpCheck | 10 |
| `block_dynamic_clients` profile rule — a profile with `block_dynamic_clients: true` matches every client whose DHCP lease has `origin=dhcp_dynamic` AND `confidence=high`; `unknown` origin is treated as not-dynamic; `default` profile rejects the flag (HTTP 400) | TS-BlockDyn | 10 |
| `GET /api/v1/clients/{ip}` — exposes `origin`, `origin_confidence`, `ipv6_addresses`, `duid`, `is_dual_stack`, `profile_ids` | TS-BlockDyn / TS-LeaseOrigin | (covered above) |

---

## Not implemented in M6.5

- **Active mitigation** — detect-only; operator decides the response (block / redirect / alert). No automated enforcement beyond profile matching.
- **DHCP failover protocol awareness** — no understanding of ISC DHCP failover state; both peers treated as equivalent sources.
- **`block_dynamic_clients` on the default profile** — intentionally rejected with HTTP 400 to prevent accidental lockout.
- **DHCPv6 over http_json** — the http_json connector is IPv4-only; DHCPv6 leases require the `kea_json` or `isc_dhcpd_v6` connector.

---

## Limitations

- ARP cross-check requires `NET_ADMIN` capability (Linux). Without it the endpoint is live but `anomaly_source` is `dhcp_only` — no kernel ARP data. Not a blocker; degrades gracefully.
- EDNS0 option 65500 (client-IP injection for DNS enforcement tests) is only active when `SKOED_TEST_MODE=1`. Production DNS enforcement uses the real source IP.
- The `origin_confidence=unknown` clients (blank wire field) are tagged `dhcp_dynamic/unknown` and deliberately excluded from `block_dynamic_clients` to avoid false positives on connectors that don't expose origin data.

---

## Demo environment

```sh
# From repo root — requires the rebuilt binary (CGO_ENABLED=0)
cd apps/skoed && CGO_ENABLED=0 go build -ldflags="-s -w" -o skoed ./cmd/skoed/
./demos/m6.5/demo.sh apps/skoed/skoed
```

Starts a 3-node Raft cluster + a Python http_json DHCP stub; runs all 7 demo sections; tears down on exit.
