# M14 Demo Note — Block Dynamic-Lease Clients

## Implemented scope

- `block_dynamic_clients` boolean field on the `Profile` struct, accepted by
  `POST /api/v1/profiles` and `PATCH /api/v1/profiles/{id}`.
- Filter engine: when a DNS query arrives from a client whose DHCP lease has
  `origin = "dhcp_dynamic"`, every profile that sets `block_dynamic_clients =
  true` is added to the candidate set (OR semantics with existing matchers —
  IP/CIDR/MAC/hostname/Client-ID).
- Match priority is preserved: Client-ID (tier 1) > MAC (tier 2) > hostname
  (tier 3) > IP/CIDR/block_dynamic_clients (tier 4). A higher-tier match
  prevents the tier-4 dynamic rule from applying.
- Only `origin = "dhcp_dynamic"` triggers the rule. `dhcp_static`,
  `router_advertised`, `manual_admin`, and unknown/empty origins do not.
- Setting `block_dynamic_clients = true` on the `default` profile is rejected
  with 400 (error message names both "default profile" and
  "block_dynamic_clients").
- `GET /api/v1/clients/{ip}` surfaces the `origin` and `profile_ids` fields,
  so the operator can confirm which profiles a dynamic client matches.
- The `http_json` DHCP connector honours the `origin` wire field, allowing the
  rule to work without a native DHCP integration.

## Not implemented (non-goals per spec)

- Auto-creating an "untrusted" profile on first boot — the operator configures
  it explicitly.
- Dashboard alerts for new dynamic-client arrivals.
- DHCPv6 DUID as a match criterion.
- Per-blocklist application — the boolean applies to all of the profile's
  blocklists.
- Time-bounded variants — combine with M3 schedule-rules instead.
- `block_static_clients` inverse rule.

## Limitations

- The rule depends on the DHCP connector reporting an `origin` field. Connectors
  that do not supply it leave `origin` empty, which the engine treats as
  not-dynamic (conservative default).
- Real-time origin changes (a device moving from dynamic to static reservation)
  take up to one connector refresh interval to propagate (default 60 s,
  configurable).

## Test results

10/10 acceptance tests pass (`TestBlockDyn*`), full suite green.

### Real-environment tests (Proxmox 3-node LXC cluster)

- Cluster: CT301/302/303 (10.0.0.11–13), Raft quorum, dnsmasq on CT301
- DHCP: 3 `dhcp_static` + 3 `dhcp_dynamic` clients verified via `GET /api/v1/clients`
- DNS filtering: 15/15 tests pass with `SKOED_TEST_MODE=1` + EDNS0 option 65500
- DNS load: **14,583 QPS** peak, 0% loss (dnsperf 10s, 10 clients)
- API load: **391 req/s**, p95 1.94 ms, 0% errors (k6 20 VUs × 20s)

Issue found: `upstream_resolvers` must use plain DNS addresses (`1.1.1.1:53`) —
the forwarder uses raw UDP/TCP, not DoH. DoH URLs cause SERVFAIL after
`normaliseUpstream()` appends `:53` to the HTTPS URL.

Full report: `demos/m14/test-report.html`
