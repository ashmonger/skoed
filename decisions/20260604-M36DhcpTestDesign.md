# M3.6 — DHCP integration test design

## Context

The roadmap entry for M3.6 requires read-only connectors to four DHCP
sources (Kea REST, dnsmasq lease file, ISC DHCP lease file, generic
HTTP). The dev host has no live DHCP server and no realistic way to
run one in CI either. This decision records the test strategy that
exercises the connectors without standing up a real DHCP service.

## Problem

End-to-end-with-real-daemon is the gold standard but:

- ISC DHCP server is deprecated, hard to containerize, and refuses to
  start without a real network interface.
- Kea works in containers but adds ~80 MB of image and a control-agent
  to configure for each test.
- dnsmasq runs as DHCP only with `--bind-interfaces` on a real NIC,
  which can't co-exist with the host's resolver on `localhost`.
- A real lease lifecycle (DISCOVER → OFFER → REQUEST → ACK) needs a
  client too, multiplying complexity.

Even if we could run them, the test would mostly verify that the
daemon behaves — not that our connector correctly **reads** what the
daemon **wrote**.

## Options considered

### Option A — Fixture files, no live daemon (RECOMMENDED)

Each connector has a parser. Tests provide static fixture files /
synthetic HTTP responses that match what each source produces in the
wild. Black-box: feed fixture → assert parsed `Lease[]`.

- **Kea connector**: spin a tiny `httptest.NewServer` that returns the
  documented `command-response` JSON shape for the `lease4-get-all`
  command, with a few synthetic leases. Connector hits `/`, asserts on
  the parsed result.
- **dnsmasq connector**: fixture file at `tests/fixtures/dhcp/dnsmasq.leases`
  with realistic entries (`<expiry-epoch> <mac> <ip> <hostname> <client-id>`).
- **ISC DHCP connector**: fixture file at `tests/fixtures/dhcp/dhcpd.leases`
  with the full `lease … { starts …; ends …; hardware ethernet …; … }` form.
- **Generic HTTP connector**: same `httptest` pattern as Kea but
  returning the doc's JSON array shape.

What this catches: parsing correctness, expiry handling, encoding
issues (dnsmasq vendor extensions, ISC quoted hostnames with `"`),
duplicate-IP resolution policy, empty/short files, malformed lines
(should skip not crash).

What it misses: connector-server protocol bugs (auth header missing,
wrong Content-Type accepted, network timeouts). Mitigate with a small
set of "edge-case" subtests on the `httptest` connectors: 500 / 401 /
slow-response / partial-response.

### Option B — Live dnsmasq in Docker

Add a `dnsmasq` test container next to the skoed test container,
share a docker network, run dnsmasq as DHCP server on a private
subnet, attach a synthetic client container that DHCP-leases an IP,
then point skoed's dnsmasq-file connector at the shared lease file.

- Pros: real lifecycle, real file format edge cases.
- Cons: tied to docker availability in CI (we don't have it for the
  go test suite — only via `make demo`). Time per test: 5-10 s.
  Authz-plugin / userns issues on this host (already seen with kind).

### Option C — Vendor a tiny in-process DHCP server (`pebble`-style)

Mini DHCP server pinned for tests, runs in-process, emits whatever
lease format we need.

- Pros: no Docker, fast.
- Cons: writing a DHCP server is out of scope; no off-the-shelf Go
  library that does this minimally; we'd own the maintenance.

## Recommendation: Option A

Plus one "smoke" demo recipe in `DEMO_NOTE_M3.6.md` that walks an
operator through `docker run dnsmasq` + `skoed` for hands-on
verification before they trust it on their LAN. The demo is not in CI.

### Test inventory (proposed)

| Test file                              | FSIDs                                       |
|----------------------------------------|---------------------------------------------|
| `tests/acceptance/dhcp_kea_test.go`    | FS-DhcpKeaReadsLeases, FS-DhcpKeaHandlesAuth, FS-DhcpKea500 |
| `tests/acceptance/dhcp_dnsmasq_test.go`| FS-DhcpDnsmasqParsesLeaseFile, FS-DhcpDnsmasqSkipsExpired   |
| `tests/acceptance/dhcp_isc_test.go`    | FS-DhcpIscParsesLeaseFile, FS-DhcpIscHandlesQuotedHostnames |
| `tests/acceptance/dhcp_generic_test.go`| FS-DhcpGenericJsonRoundtrip, FS-DhcpGenericRetry            |
| `tests/acceptance/dhcp_lookup_test.go` | FS-ClientLookupReturnsHostname, FS-ClientLookupFallsBackToIp, FS-QueryLogShowsHostname |
| `tests/acceptance/dhcp_profile_test.go`| FS-ProfileMatchesByMac, FS-ProfileMatchesByHostname        |

Fixtures under `tests/fixtures/dhcp/`:
- `dnsmasq.leases` (5-6 entries: active, expired-just-now, expired-long-ago, malformed line, vendor-extension line)
- `dhcpd.leases` (2 leases in full ISC syntax, one with quoted hostname containing a `"` escape)
- `kea-lease4-get-all.json` (Kea command-response wrapper with 3 leases)
- `generic-leases.json` (the documented `[{ip,mac,hostname,expires_at}, …]` shape)

### Spec coverage

Two `.feature` files in `specs/functional/`:

- `dhcp-client-identity.feature` — observable behavior: query log
  shows hostname, profile matches by MAC, `/api/v1/clients/{ip}` shape,
  fallback to IP-only when no lease, refresh interval honoured.
- `dhcp-connectors.feature` — per-connector contract: each source
  produces the same canonical `Lease` shape.

Both files explicitly list non-goals (no DHCPv6, no active probing,
no writes).

## What I want you (UoR) to confirm before I touch code

1. **Option A is acceptable** — fixtures-only for CI, demo recipe for
   hands-on. No live DHCP daemon in the test suite.
2. **Connector list is right** — Kea / dnsmasq / ISC / generic-HTTP.
   Anything to add or drop?
3. **Profile-matching scope** — should `client_macs` and
   `client_hostnames` be added in M3.6, or kept as M4-or-later?
4. **Fallback semantics** — when no DHCP source is configured OR a
   client's IP isn't in any lease table, query log shows the raw IP
   in the `client` field and `client_hostname` is omitted. Confirm.
5. **Cache backing** — bbolt (so it survives restarts, replicates via
   Raft) **vs** in-memory only (faster, per-node, lost on restart).
   Recommend bbolt; lease data is tiny and benefits from cluster-wide
   consistency.

Once these are confirmed I'll write the feature files, fixtures, and
tests per the existing BDD-First flow — then ask again before
implementation.
