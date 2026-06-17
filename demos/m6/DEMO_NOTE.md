# DEMO NOTE — M6 Closing the DoH Gap

## Scope

Closes the DoH detection gap identified in M3. Adds per-client DoH status surfacing, firewall rule generation for blocking encrypted DNS bypass, and a DoH resolver IP database for accurate detection.

### Implemented

- `GET /api/v1/clients/{ip}/doh-status` — per-client DoH probe detection
- `GET /api/v1/firewall-rules?platform=<platform>&scope=<scope>` — generates iptables/nftables/MikroTik/OPNsense/UniFi rules to block encrypted DNS bypass
  - Platforms: `iptables`, `nftables`, `mikrotik`, `opnsense`, `unifi`
  - Scopes: `subnet`, `profile`, `all`
- **DoH resolver IP database**: embedded list of known DoH/DoT resolver IPs updated from the `doh-resolvers` dataset; used to detect bypass attempts
- Web UI: `/dashboard/clients` shows per-client DoH probe badge; `/dashboard/firewall-rules` page (inline firewall rule generator)

### Not implemented

- Automated firewall rule push (generate-only, no SNMP/API push to network gear)
- DNS-over-QUIC (DoQ) detection

## Demo

```bash
# Get DoH status for a client
curl -u admin:pass http://localhost:8080/api/v1/clients/192.168.1.42/doh-status

# Generate iptables rules to block DNS bypass for all clients
curl -u admin:pass "http://localhost:8080/api/v1/firewall-rules?platform=iptables&scope=all"
```

## Limitations

Firewall rules are generated as text for the operator to apply manually. The DoH resolver IP database is embedded at build time and updated with each skoed release.
