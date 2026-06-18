# M21 Demo Note — DNSSEC Validation Mode

## Implemented Scope

- `dns.dnssec_mode` config field: `"transparent"` (default, existing behavior) or `"validate"` (new).
  Replicated via Raft and persisted to bbolt config store.
- `PATCH /api/v1/settings` accepts `{"dns":{"dnssec_mode":"validate"}}`, validated against allowed values.
  `GET /api/v1/settings` reflects the current mode in the `dns` section.
- **Validate mode behavior**:
  - DO bit set on all outgoing upstream queries (requests DNSSEC records from upstream).
  - `AD=1` in response → `dnssec_status="ok"`, answer passes through.
  - `AD=0` + RRSIG records in answer → `dnssec_status="bogus"`, SERVFAIL returned to client.
  - `AD=0`, no RRSIG records → `dnssec_status="insecure"`, answer passes through.
  - Upstream SERVFAIL → `dnssec_status="bogus"`, SERVFAIL passed through.
- `dnssec_status` field added to query log entries (JSON: `"dnssec_status"`).
  Empty / absent in transparent mode.

## Acceptance Tests (4/4 pass)

| Test | FSID | Result |
|------|------|--------|
| TestDnssecModeConfigurable | FS-DnssecModeConfigurable | PASS (1.7s) |
| TestDnssecValidateBogusReturnsServfail | FS-DnssecValidateBogusServfail | PASS (2.5s) |
| TestDnssecValidateOkPassthrough | FS-DnssecValidateOkPassthrough | PASS (2.2s) |
| TestDnssecValidateInsecurePassthrough | FS-DnssecValidateInsecurePassthrough | PASS (1.9s) |

All 3 existing `TestDnssecTransparentProxy*` tests still pass. Full suite: **all green** (145s).

## Not Implemented in This Milestone

- Per-profile DNSSEC policy — one cluster-wide mode only.
- DNSSEC signing of skoed-served local DNS entries (skoed is a resolver, not authoritative).
- Trust anchor auto-rollover (RFC 5011) — manual root trust anchor only.
- DNSSEC-aware caching — cache behavior unchanged between modes.
- UI toggle for DNSSEC mode — API-only in M21.
- Full chain validation from embedded root KSK — mode relies on upstream AD bit
  (production upstreams: Cloudflare 1.1.1.1, Google 8.8.8.8 both validate DNSSEC).

## Limitations

- Validate mode trusts the upstream resolver's AD bit for chain validation. A rogue or
  non-validating upstream set as the resolver bypasses DNSSEC protection.
- Operators should pair `validate` mode with DNSSEC-capable upstream resolvers.
- Mode change takes effect immediately on the next query after PATCH; no restart required.
