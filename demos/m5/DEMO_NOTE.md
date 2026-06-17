# DEMO NOTE — M5 Production Hardening

## Scope

Comprehensive production-readiness improvements across 9 sub-milestones. All sub-milestones ship as a single production hardening track.

### Sub-milestones

| Sub | Feature | Detail file |
|-----|---------|-------------|
| M5.1 | Prometheus `/metrics` exporter | `m5.1.md` |
| M5.2 | Audit log | `m5.2.md` |
| M5.3 | Encrypted cluster mesh (mTLS) | `m5.3.md` |
| M5.4 | Automated blocklist refresh | `m5.4.md` |
| M5.5 | Native packaging (deb/rpm/apk) | `m5.5.md` |
| M5.6 | Upgrade check endpoint | `m5.6.md` |
| M5.7 | Multi-arch builds (amd64/arm64/armv7) | `m5.7.md` |
| M5.8 | Documentation site | `m5.8.md` |
| M5.9.1 | CLI (`skoed-ctl`) | `m5.9.1.md` |
| M5.9.2 | Dev hot-reload | `m5.9.2.md` |
| M5.9.3 | Docker cache layer | `m5.9.3.md` |
| M5.9.4 | Config validation command | `m5.9.4.md` |
| M5.9.5 | Health probe endpoint | `m5.9.5.md` |
| M5.9.7 | Domain test endpoint | `m5.9.7.md` |

### Key deliverables

- **Prometheus metrics**: DNS counters, cache gauges, cluster state, DHCP gauges — all labelled and bounded at ≤60 series/node
- **Audit log**: every state-changing API call persisted with actor, action, target, result, timestamp; queryable via `GET /api/v1/audit`
- **mTLS mesh**: inter-node Raft traffic encrypted; certificate auto-rotation without cluster downtime
- **Blocklist refresh**: scheduled pull of blocklist URLs via cron-style config; refresh history and error surfaced in UI
- **Packaging**: .deb / .rpm / .apk for amd64+arm64; goreleaser pipeline; Docker multi-arch image
- **Domain tester**: `POST /api/v1/test-domain` — simulates query resolution for a given domain/client and returns the outcome with matched blocklist/allowlist

### Not implemented (M5 non-goals)

- Push-mode Prometheus (Pushgateway)
- High-cardinality per-domain / per-client metrics
- SNMP integration
- Windows packaging

## Limitations

See individual sub-milestone files for feature-specific limitations.
