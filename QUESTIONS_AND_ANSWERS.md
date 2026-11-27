# Questions and Answers

## Open questions

None.

## Confirmed answers

- **Q**: What technology stack?
  **A**: Go 1.22+. DNS: `miekg/dns`. Web UI: Vue.js compiled and embedded. Config: YAML. — 2026-05-29

- **Q**: Project name?
  **A**: skoed. — 2026-05-29

- **Q**: Multi-node sync model?
  **A**: Primary + replicas (push). Hybrid approach: split-brain avoidance via last-seen timestamps and health-check quorum. Full consensus (Raft) deferred to post-M2. — 2026-05-29

- **Q**: M2 — How does a replica become the new primary when the current primary fails?
  **A**: ~~Hybrid. Manual default, opt-in quorum auto-failover.~~ **SUPERSEDED 2026-05-29** by the Raft decision below — Raft handles leader election automatically; manual promotion and the opt-in quorum protocol are no longer needed.

- **Q**: M2 — How do config changes propagate from primary to replicas?
  **A**: ~~Replica pulls from primary via SSE.~~ **SUPERSEDED 2026-05-29** by the Raft decision below — config replication is now via Raft log; writes accepted on any node and forwarded to the leader; SSE is not used.

- **Q**: M2 — Is the Helm chart / Kubernetes DaemonSet part of M2 scope?
  **A**: Deferred to a later milestone (M2.5). M2 focuses on the cluster core (Raft replication, enrollment, cluster status). Plain `kubectl apply` of a Deployment remains supported. — 2026-05-29

- **Q**: M2 — What is the replication core for cluster config?
  **A**: hashicorp/raft + go.etcd.io/bbolt. Pure Go (no CGO), proven in production by Consul / Nomad / Vault / k3s. Any node accepts writes; non-leader nodes forward to the leader transparently. Raft handles leader election, split-brain prevention, log replication, and snapshots. Hypothesis H2 (manual quorum protocol) is replaced by H4 (hashicorp/raft is operationally suitable for ≤10 nodes on home/lab networks). — 2026-05-29

- **Q**: M2 — What is the on-disk source of truth for cluster config?
  **A**: bbolt is the source of truth. The YAML file from M1 becomes an import/export artifact only — useful for backup, migration, and human review, but never the live state. Editing config.yaml on disk after boot has no effect. Node-local settings (DNS listen port, API port) remain in a small `node.yaml` file. — 2026-05-29

- **Q**: M2 — How does the cluster expose query log data for the future dashboard?
  **A**: Hybrid. Raw entries stay per-node (volume too high to replicate). Each node writes hourly aggregate counters (totals, top-N domains, top-N clients) into a bbolt bucket that Raft replicates to all nodes — so cluster-wide stats are available from any node with zero extra protocol code. Individual-entry searches use an on-demand fan-out endpoint that queries every node in parallel. — 2026-05-29

- **Q**: M2 — Should the YAML config still be written to disk for filesystem-level backup tools (Proxmox Backup Server, restic, borg)?
  **A**: Yes. A write-through shadow YAML at `<data_dir>/config.yaml` is updated after every Raft commit (debounced ~1 s, atomic rename). It is the M1 export format byte-for-byte. It is NEVER read by a running node — bbolt remains source of truth — but PBS-style FS snapshots capture it automatically. On restore to a fresh node, the M1→M2 migration imports it. — 2026-05-29

- **Q**: Parental control scope?
  **A**: All four: category-based blocking, schedule-based blocking, per-device/client profiles, SafeSearch enforcement. — 2026-05-29

- **Q**: Primary deployment target for v1?
  **A**: Both simultaneously — Linux bare-metal/LXC binary + Docker image from Milestone 1. Helm chart in Milestone 2. — 2026-05-29

- **Q**: DNSSEC stance for Milestone 1?
  **A**: Transparent proxy — forward DNSSEC records (RRSIG, DNSKEY, DS, NSEC) as-is to clients that set the DO bit. skoed does not validate signatures. Zero complexity added; DNSSEC-validating clients handle validation themselves. — 2026-05-29

- **Q**: Block policy for blocked domains?
  **A**: Configurable per blocklist, with a global default. Supported response types: NXDOMAIN, NULL (A=0.0.0.0 / AAAA=::), NODATA (NOERROR with empty answer). — 2026-05-29

- **Q**: IPv6 support scope for Milestone 1?
  **A**: Full dual-stack — DNS listener on both IPv4 and IPv6 (port 53), AAAA records in local DNS entries, IPv6 client identification in query log and client profiles, NULL block returns 0.0.0.0 for A queries and :: for AAAA queries. — 2026-05-29

- **Q**: Wildcard domain syntax and matching semantics for blocklists and allowlists?
  **A**: `*.example.com` matches the apex domain (`example.com`) and all subdomains at any depth (`sub.example.com`, `a.b.example.com`). Applies to both blocklist entries and allowlist entries. — 2026-05-29

- **Q**: Client group membership model (M3)?
  **A**: A client may belong to multiple groups. Effective rules are the union of all group blocklists and the union of all group allowlists. Ungrouped clients fall into a built-in default group. — 2026-05-29

- **Q**: Client identification by MAC address — how to resolve IP → MAC (M3)?
  **A**: Primary = DHCP integration (supported sources: Kea DHCP REST API, dnsmasq lease file, ISC DHCP lease file, generic configurable HTTP API returning leases as JSON). Fallback = IP-only identification for clients not in the DHCP lease table or when no DHCP source is configured. — 2026-05-29

- **Q**: M50 — Raft replicates only the blocklist configuration, not the downloaded domains. Where does each node get the content of a URL blocklist?
  **A**: Each node downloads the URL itself and caches it under `data_dir`, as AdGuard Home and Pi-hole do. No peer fallback, no leader-side download. See `decisions/20261125-NodeLocalBlocklistContent.md`. — 2026-11-25

- **Q**: M50 — revisited the same day: where does each node get the content of a URL blocklist, and what triggers a leader change?
  **A**: Supersedes the previous answer. The leader downloads the full list; followers copy it from the leader over the cluster API. The leader changes only when the leader node goes down (Raft election); a failed download on a live leader keeps the previous copy and is retried at the next refresh. See `decisions/20261125-LeaderDistributedBlocklistContent.md`. — 2026-11-25

- **Q**: M50 — how should the blocklist copy between nodes be encrypted, and how should the acceptance test cut one follower off from the leader?
  **A**: Encrypt all peer HTTP with the existing cluster mTLS (a dedicated peer listener using the cluster CA and node certificates), implemented as its own M50 item for FS-MtlsInternalApiHTTPS; the copy endpoint uses it. Cut the follower with a real `iptables` owner-match rule in the acceptance test (root + NET_ADMIN in the Docker runner, skipped otherwise) instead of a test-only variable, and repeat the cut on Proxmox. — 2026-11-25

- **Q**: M50 — the validated tests count NXDOMAIN as "blocked", but test nodes resolve recursively with internet access, so a made-up name also gets NXDOMAIN. How should the tests be fixed?
  **A**: Create the test URL lists with `block_policy: "null"` and count only the 0.0.0.0 / `::` answer as a block in the M50 tests. — 2026-11-25

- **Q**: M50 — with mTLS on, should a peer with no recorded peer address be reached over plain HTTP (sending the cluster secret in clear)?
  **A**: Never plain HTTP. Use https on the recorded peer address, or on the default (Raft host, Raft port + 1). During a rolling upgrade, writes forwarded to an older leader fail with 503 until the leader is upgraded. — 2026-11-25

- **Q**: M50 — the run hit the 3-cycle cap with one MAJOR left (a lost capability gate does not force a fresh download of converted lists). How to proceed?
  **A**: One more focused cycle under a Rule 14 exception, limited to that fix and a few small safety fixes; see `decisions/20261125-M50FourthCycle.md`. — 2026-11-25

- **Q**: M50 — should join refuse node IDs a certificate cannot pin (localhost, IP literals) when mTLS is off?
  **A**: No. Refuse them only on an mTLS cluster; mtls-bootstrap and the start-time check keep refusing them with mTLS on. — 2026-11-25

- **Q**: DNS overload — who sets the DNS capacity of a node?
  **A**: A cluster-wide setting the administrator can change, with a default that skoed ships. Not derived from the node's CPU and memory. — UoR, 2026-11-27

- **Q**: DNS overload — what is the default DNS capacity?
  **A**: The highest C for which the 2 × C scenarios of `dns-overload-protection.feature` pass on a node with 2 cores and 2 GB running the fixed code with the spec's query mix, measured under that overload and recorded in the technical spec. Not 4,000 qps (in T10 nodes failed at 4,000 with the current code). — UoR, 2026-11-27

- **Q**: DNS overload — how far above its capacity is a node loaded in the scenarios?
  **A**: 2 × C. — UoR, 2026-11-27

- **Q**: DNS overload — what tolerance applies to UDP loss?
  **A**: Above capacity, the node answers at least 90 % of C per second and at least 98 % of the queries sent receive an answer or a REFUSED response; below capacity, less than 0.1 % of the queries sent receive no response. Derived from T2. — UoR, 2026-11-27

- **Q**: DNS overload — what latency bound applies during overload?
  **A**: p99 of the answers below 50 ms, and p99.9 of all responses (answers and refusals) below 1 second. — UoR, 2026-11-27

- **Q**: DNS overload — what does a client receive for a query above the capacity?
  **A**: A REFUSED response, the same as from a node that is not ready (decided together with the startup question below). — UoR, 2026-11-27

- **Q**: DNS overload — are clients expected to fall back to another node?
  **A**: Yes. The administrator gives clients at least two nodes, and skoed documents it. — UoR, 2026-11-27

- **Q**: DNS overload — how much may the memory of an overloaded node grow?
  **A**: At most 200 MB over its level with no DNS load (the 160 MB of heap in use in the perf report's Findings 1, plus a margin). — UoR, 2026-11-27

- **Q**: DNS overload — how soon after the load drops must a node be back to normal?
  **A**: From 5 seconds after the drop it refuses no query, and within 5 minutes its memory is back within 50 MB of its level before the load. — UoR, 2026-11-27

- **Q**: DNS overload — how is the DNS capacity counted?
  **A**: Per second, with a burst allowance of one second's worth of queries. — UoR, 2026-11-27

- **Q**: DNS overload — how does the administrator see that a node is overloaded?
  **A**: A count of refused queries per node in the metrics endpoint, and a log line at most once per minute while the node refuses queries. — UoR, 2026-11-27

- **Q**: DNS startup readiness — what does a client receive from a node that has not loaded its lists yet?
  **A**: A REFUSED response, the same as for a query above the capacity. — UoR, 2026-11-27

- **Q**: DNS startup readiness — what does a node do when it cannot load one of its lists at startup?
  **A**: It waits at most 60 seconds for that list, then answers with the lists it could load, shows DNS state "ready" and reports the missing list as not loaded. — UoR, 2026-11-27

- **Q**: DNS startup readiness — is a restart of the whole cluster at once covered?
  **A**: Yes. A node that holds a copy of every list loads it from disk and answers without the leader and without internet access. — UoR, 2026-11-27

- **Q**: DNS startup readiness — how does the administrator see that a node is not ready yet?
  **A**: The cluster status shows the node with DNS state "starting" until it answers queries, then "ready", and the node logs when it starts answering. — UoR, 2026-11-27

- **Q**: DNS overload protection — is the functional specification (specs/functional/dns-overload-protection.feature) validated, and is it the next feature to implement?
  **A**: Yes: "add the overload protection", for the next release (v0.6.0). The startup readiness specification stays pending. — UoR, 2026-11-27
