# M38 — Per-Profile DNSSEC Policy

## Implemented

- **Global DNSSEC mode** — `settings.dnssec_mode` accepts `"transparent"` (pass through; default), `"validate"` (enforce AD=1 + valid RRSIG; return SERVFAIL on bogus), or `"off"` (strip DNSSEC records).
- **Per-profile override** — `ProfileConfig.dnssec_mode` accepts `"inherit"` (use global), `"validate"`, or `"transparent"`; stored in the profile JSON in bbolt.
- **Cache key isolation** — DNS response cache keys include `DnssecMode` to prevent cross-mode cache pollution between a validate-profile and a transparent-profile resolving the same domain.
- **Engine routing** — `DNSSECModeForClient(clientIP)` resolves the effective mode by looking up the client's profile then falling back to global.
- **UI dropdown** — Profile edit form shows "DNSSEC Mode" select (Inherit / Validate / Transparent); Settings page shows the global DNSSEC mode control.
- **Raft replication** — `ProfileUpdate` Raft command carries `dnssec_mode`; replicates to all nodes via existing profile sync.
- **Acceptance tests** — `TestPerProfileDnssecInherit`, `TestPerProfileDnssecValidate`, `TestPerProfileDnssecTransparent` (all in `per_profile_dnssec_test.go`).

## Not Implemented

- **`"off"` per-profile override** — global `off` is supported but per-profile `off` (strip DNSSEC for specific profiles) is not exposed in the UI or API profile schema.
- **DNSSEC statistics** — no per-profile metrics for bogus vs. secure vs. insecure response counts.
- **DNSSEC key pinning** — trusting only specific DS records for specific domains.

## Limitations

- Validation requires an upstream resolver that returns the `AD` flag and `RRSIG` records. On the Proxmox cluster (recursive mode) DNSSEC validation is end-to-end; in forwarding mode the upstream must be DNSSEC-aware.
- Cache isolation doubles memory footprint for the same domain if two profiles use different modes and both query the same name in a short window.
- The `"validate"` mode returns SERVFAIL to the client regardless of the underlying failure reason (network error vs. actual DNSSEC bogus); no EXTENDED-DNS-ERROR (RFC 8914) codes are returned yet.
