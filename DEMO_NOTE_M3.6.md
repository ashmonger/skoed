# DEMO NOTE — M3.6 Read-only DHCP integration

## Scope

skoed learns hostnames, MAC addresses, and DHCP Client-IDs from one
of three upstream DHCP sources (Kea control-agent, dnsmasq lease file,
generic HTTP-JSON). The query log and Dashboard surface enriched
client identities; profiles match clients by stable identity instead
of fragile IP; an anti-spoof detector flags lease changes that look
like spoofing.

### Implemented

- **Three read-only connectors** in `apps/skoed/internal/dhcp/`:
  - **Kea** — POSTs `lease4-get-all` to the control-agent. Supports
    Basic Auth.
  - **dnsmasq** — reads the lease file (default
    `/var/lib/misc/dnsmasq.leases`). Skips expired + malformed lines.
    Honors the `*` hostname / client-id markers (= absent).
  - **Generic HTTP-JSON** — pulls `[{ip, mac, hostname, client_id,
    expires_at}, …]` from any operator-configured endpoint. Useful
    for OPNsense / UniFi / custom backends.
- **Lease cache** with periodic refresh (default 60 s). Node-local —
  every node polls its own configured source. Recommended deployment:
  point every skoed node at the same central DHCP server.
- **Anti-spoof detector** (Layers 1 + 2):
  - Prefers DHCP Client-ID for identity (Layer 1)
  - Tracks `(client_id, mac, hostname)` history; flags three anomaly
    kinds: `mac_changed_for_client_id`, `client_id_changed_for_mac`,
    `new_device_steals_hostname` (Layer 2)
  - Anomalies persist for 7 days, then evict. Acknowledged ones
    linger but don't show in the alert card.
- **API surface**:
  - `GET /api/v1/clients/{ip}` — enriched record + recent anomalies
  - `GET /api/v1/clients/anomalies` — list
  - `POST /api/v1/clients/anomalies/{id}/acknowledge`
  - `GET /api/v1/clients/export-reservations?format=dnsmasq|kea|json` —
    operator-pasteable static-reservation syntax derived from the
    current lease snapshot
  - `GET /api/v1/clients/_leases` — debug/harness snapshot
- **Profile matching** by `client_ids` / `client_macs` /
  `client_hostnames` (priority: Client-ID > MAC > hostname > IP/CIDR).
  Legacy IP/CIDR matching unchanged.
- **Query-log enrichment**: every entry gains optional
  `client_hostname`, `client_mac`, `client_id` fields.
- **Web UI**:
  - New **Clients** page (`/clients`): sortable lease table, filter,
    spoof-alert card, "Export reservations" dropdown (dnsmasq / Kea /
    JSON).
  - **Dashboard** alert card at the top for unacknowledged spoof
    anomalies, danger-toned.
  - **Profiles** edit modal gets a "DHCP-stable identity" expandable
    section with three textareas: Client-IDs, MACs, hostnames.

### Acceptance tests

23 acceptance tests across three files:

- `dhcp_connectors_test.go` (8) — Kea, dnsmasq, generic HTTP round-trip
  and edge cases.
- `dhcp_client_identity_test.go` (9) — enrichment + Client-ID > MAC >
  hostname > IP/CIDR profile-match priority.
- `dhcp_spoof_detection_test.go` (8) — three anomaly kinds + rename =
  info / acknowledge.

**20 PASS, 3 intentional SKIP, 0 FAIL.** Full suite (M1 → M3.6) green in
Docker at 482 s.

### Not implemented (intentional / deferred)

- **ISC `dhcpd` lease file** — ISC declared EOL in 2022. Migrate to Kea.
- **DHCPv6** — defer.
- **ARP/NDP cross-check** (Layer 3 anti-spoof) — backlog.
- **Auto-remediation** — alert only.
- **"Block dynamic-lease clients" category** — TODO entry; needs
  per-connector static-vs-dynamic detection that dnsmasq's lease file
  alone doesn't surface.
- **Raft-replicated lease cache** — node-local for now. Same effect
  achieved by pointing every node at the same DHCP source.
- **Lease list virtualization** — fine for typical home/small-office
  scale (≤500 leases); larger deployments may want pagination later.

## Demo recipe (dnsmasq + skoed side-by-side)

This recipe runs **dnsmasq as the DHCP server** and **skoed as the DNS
resolver** in two containers, with skoed reading dnsmasq's lease file
via a shared volume.

```bash
# 1. Build skoed image
docker build -t skoed:m3.6 -f apps/skoed/Dockerfile apps/skoed

# 2. Shared volume for the lease file
docker volume create skoed-leases

# 3. Start dnsmasq with DHCP enabled, writing leases to /var/lib/misc.
#    The static reservations seed three "known" devices we'll later
#    verify skoed has learned.
docker run -d --name dnsmasq --cap-add=NET_ADMIN --network host \
  -v skoed-leases:/var/lib/misc \
  -e DNSMASQ_USER=root \
  dockurr/dnsmasq:latest \
    --no-daemon \
    --dhcp-range=192.168.99.50,192.168.99.150,12h \
    --dhcp-host=aa:bb:cc:dd:ee:42,192.168.99.42,kid-tablet \
    --dhcp-host=aa:bb:cc:dd:ee:10,192.168.99.10,home-laptop \
    --dhcp-leasefile=/var/lib/misc/dnsmasq.leases \
    --log-dhcp

# 4. Per-node skoed config pointing at the shared lease file
mkdir -p /tmp/skoed-m3.6-demo/data
cat > /tmp/skoed-m3.6-demo/config.yaml <<'EOF'
node:
  id: "demo"
  raft_address: "127.0.0.1:7000"
  api_address: "127.0.0.1:8080"
  data_dir: "/var/lib/skoed"
  dns:
    listen:
      port: 53
      ipv4: true
      ipv6: false
  dhcp:
    enabled: true
    kind: "dnsmasq"
    file_path: "/var/lib/misc/dnsmasq.leases"
    refresh_seconds: 5
EOF

# 5. Start skoed with the shared lease volume mounted read-only
docker run -d --name skoed -p 18080:8080 -p 15353:53/udp \
  -v skoed-leases:/var/lib/misc:ro \
  -v /tmp/skoed-m3.6-demo/config.yaml:/etc/skoed/config.yaml:ro \
  -v /tmp/skoed-m3.6-demo/data:/var/lib/skoed \
  -e SKOED_TEST_MODE=1 \
  skoed:m3.6 --config /etc/skoed/config.yaml

# 6. Set the admin password
curl -sX POST http://127.0.0.1:18080/api/v1/auth/setup \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"demopass123"}'

# 7. Trigger a DHCP exchange (in a separate test container with the
#    target MAC).  Skip this if your LAN is already feeding dnsmasq.

# 8. Verify skoed sees the lease
curl -s -u admin:demopass123 http://127.0.0.1:18080/api/v1/clients/192.168.99.42 | jq
#   → { ip, mac, hostname: "kid-tablet", client_id, source: "dnsmasq", … }

# 9. Open the UI at http://127.0.0.1:18080  →  Clients page
#    Confirm the lease table shows your seeded devices.
```

### Anti-spoof drill

```bash
# 10. Manually corrupt the lease file to simulate a MAC change for the
#     same Client-ID (Layer-2 detector input)
docker exec dnsmasq sh -c "sed -i 's/aa:bb:cc:dd:ee:42/ff:00:00:00:00:99/' /var/lib/misc/dnsmasq.leases"

# 11. Within one refresh interval (5 s above), skoed raises an anomaly
curl -s -u admin:demopass123 http://127.0.0.1:18080/api/v1/clients/anomalies | jq
#   → [{kind: "mac_changed_for_client_id", ip: "192.168.99.42", …}]

# 12. The Dashboard top-of-page shows "Possible identity spoofing"
#     Open http://127.0.0.1:18080  →  Dashboard.

# 13. Acknowledge from the Clients page (button on each anomaly row),
#     OR via API:
curl -s -u admin:demopass123 -X POST \
  http://127.0.0.1:18080/api/v1/clients/anomalies/ANOM-XXX/acknowledge
```

### Reservation export

```bash
# 14. Export the current lease snapshot as dnsmasq dhcp-host=... lines
curl -s -u admin:demopass123 \
  'http://127.0.0.1:18080/api/v1/clients/export-reservations?format=dnsmasq'
# Drop into /etc/dnsmasq.d/skoed-reservations.conf and reload dnsmasq.
```

### Cleanup

```bash
docker rm -f skoed dnsmasq
docker volume rm skoed-leases
```

## What's next

- **M4.5** — Swagger UI bundled under `/api/docs` (next milestone).
- **M5** — Production hardening (Prometheus, audit log, multi-arch,
  in-place upgrade).
- **HTTPS for the management API** — TODO on the backlog.
