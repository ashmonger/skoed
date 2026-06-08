# Problem Statement

## One-sentence problem

Home network administrators and parents experience uncontrolled ad traffic, trackers, and unrestricted content access on their home or lab networks, which causes privacy risks, slower browsing, bandwidth waste, and exposure of children to inappropriate content — without a self-hosted DNS filtering solution that is easy to deploy on multiple nodes and keeps all nodes in sync automatically.

## Current flow and failure points

1. A user installs Pi-Hole or AdGuard Home on a single host.
2. The tool blocks ads and trackers for all clients using that host as DNS resolver.
3. If the user wants a second node (for redundancy or a second network segment), they must manually duplicate the entire configuration.
4. Any configuration change (new blocklist, new local DNS entry) must be applied to each node manually.
5. If the filtering node goes down, clients lose DNS resolution (no automatic failover).
6. Parental control features (per-device rules, schedules) are absent, limited, or require external tooling.

**Failure points:**
- No built-in multi-node config sync → configuration drift between nodes.
- No per-client profiles → uniform blocking policy for all devices.
- No schedule-based rules → no time-of-day access control.
- Configuration is not easily portable → import/export is manual and fragile.
- Container-native deployment requires manual orchestration not provided by the tool.

## Success outcomes

| Outcome | Observable measure |
|---------|-------------------|
| Single node operational | skoed serves DNS and blocks ads within 10 minutes of install on a fresh Linux host |
| Second node joins cluster | A replica enrolls and receives full config in ≤ 5 manual steps; config changes appear on replica within 10 s |
| Config is portable | A full export imported on a fresh node restores identical behavior |
| Parental control is active | A child device is blocked from adult categories on a schedule, verified in the query log |
| Container deployment works | A single `docker run` or `helm install` starts a functional skoed node |

## Scope

### In scope
- DNS-level ad blocking, tracker blocking, malware domain blocking
- Local DNS entry management for home and lab networks
- Root DNS recursive resolution (no third-party upstream dependency)
- Multi-node configuration sync (primary + replicas)
- Per-client profiles with category-based and schedule-based rules
- SafeSearch DNS rewriting
- Web UI for all management operations
- Config import/export
- Docker image and Helm chart

### Out of scope
- DHCP server
- Network topology management

### Under reconsideration
Previously listed as out of scope; now being re-evaluated. See
`ROADMAP.md` "Non-goals under reconsideration" for the live list and
TODO.md for the tracking entries.

- Deep packet inspection / HTTP filtering
- Transparent proxy mode (was: "VPN or proxy functionality")
- Mobile application
- Cloud-hosted SaaS offering
