---
x-tsid: TS-DnsOverloadProtection
x-fsid-links:
  - FS-DnsOverloadCapacityConfigurable
  - FS-DnsOverloadAnswersUpToCapacity
  - FS-DnsOverloadRefusesExcess
  - FS-DnsOverloadFilteringKept
  - FS-DnsOverloadMemoryBounded
  - FS-DnsOverloadRecoversWhenLoadDrops
  - FS-DnsOverloadNoRefusalBelowCapacity
  - FS-DnsOverloadVisibleToAdministrator
---

# TS-DnsOverloadProtection — DNS capacity and admission on the plain UDP listeners

Functional spec: `specs/functional/dns-overload-protection.feature`
(validated 2026-11-27). Target release: v0.6.0.

## 1. Context

In v0.5.1 nothing limits the queries a node accepts. miekg/dns
(v1.1.73) reads each UDP packet in one goroutine per listener and starts
one goroutine per packet (`go srv.serveUDPPacket(...)`), with no limit.
When packets arrive faster than the handler finishes them, goroutines
pile up: 274,239 goroutines and 1.5 GB of RSS after 24 s in the
5-node run of 2026-11-26 (demos/perf-5node-lxc/REPORT.md, Findings 1). v0.5.1 removed
the two serialized steps found there (the device-new hook now uses
`DeviceNewQueue`, the query log is a ring without `copy()`), which moved
the single-node limit to about 60,000 qps for cached names, 14,000 for
cache misses and 26,000–34,000 on the 70/20/10 mix (1 core, 2026-11-27).
Above that limit the node still accepts every packet: excess queries are
lost, never refused, and the backlog is unbounded.

This spec adds a per-node token bucket in front of the plain UDP
listeners. Queries above the DNS capacity receive REFUSED before a
goroutine is started for them.

## 2. Config and storage

### 2.1 Setting

| | |
|---|---|
| Name | `dns.capacity_qps` (JSON and YAML) |
| Go field | `config.DNSConfig.CapacityQPS int` with tags `yaml:"capacity_qps,omitempty" json:"capacity_qps,omitempty"` |
| Type | integer, queries per second |
| Stored values | `0` (or absent): not set, the node uses the shipped default. `100`–`1000000`: set by the administrator. |
| Shipped default | `DefaultDNSCapacityQPS` in `internal/config`: measured, see § 9 Default capacity. Until measured: 10,000 (provisional). |
| Effective value | `CapacityQPS` when it is not 0, otherwise `DefaultDNSCapacityQPS` (`DNSConfig.EffectiveCapacityQPS()`). |

`Config.Defaults()` does not fill the field: a cluster where the
administrator never set it follows the shipped default, including when a
later release changes the default (open question OQ-2).

`Config.Validate()` rejects a value that is not 0 and outside
100–1,000,000. The same check runs on `PATCH /api/v1/settings` and on
`POST /api/v1/config/import`. The lower bound keeps a typing error from
making a node refuse almost everything; it is also low enough for the
acceptance tests (§ 10). The upper bound is above anything a node
answers (§ 1).

### 2.2 Storage and replication

The setting is cluster-wide, like the other DNS settings
(`dns.mode`, `dns.upstream_timeout_seconds`, `dns.cache`): it is a field
of `config.DNSConfig`, stored in bbolt bucket `config_settings`, key
`dns` (`applySettingsPatch`, `internal/cluster/store.go`). It is written
through `App.WithWriteLock`, which commits the snapshot with one Raft
`ConfigImport` command. It is part of the shadow YAML and of
`GET /api/v1/config/export`. No new Raft command, bucket or key.

### 2.3 Applying a change

Every node applies the committed entry in its FSM, then runs
`App.onApply` → `rebuildDNS` (`cmd/skoed/main.go`) →
`ServerHolder.Rebuild`. `Rebuild` calls `Admission.SetRate(cfg.EffectiveCapacityQPS())`
before it swaps the handler or restarts the listeners. This is the path
every DNS setting already takes, within the 5-second propagation bound
of `cluster-config-sync.feature` (FS-DnsOverloadCapacityConfigurable).

The `Admission` value is created once by `ServerHolder.Start` and kept
for the life of the process. A rebuild (every Raft apply calls
`rebuildDNS`, including stats and lease writes) or a listener restart
reuses it, so its bucket is never refilled by a rebuild. `SetRate` with
an unchanged rate does nothing.

## 3. Admission

### 3.1 Where

In `internal/dns/server.go`, `Server.Start` sets `DecorateReader` on the
`dns.Server` of each UDP listener (`udp`, `udp6`, and the `udp` fallback
when both address families are off). The decorated reader
(`admissionReader`) wraps miekg/dns's packet read, before miekg/dns
starts a goroutine and before it unpacks the message:

```
loop:
  n, session, err := dns.ReadFromSessionUDP(conn, buf)   // buf owned by the reader
  if err: return err                                     // miekg/dns handles it as today
  if n < 12 or QR bit set: hand the packet to miekg/dns as today (not counted)
  if admission.Allow(): copy buf[:n] into a new slice, return it to miekg/dns
  else: write REFUSED in place in buf (§ 3.4), dns.WriteToSessionUDP(conn, buf[:m], session),
        admission.refused.Add(1), continue loop
```

The reader sets the read deadline from the timeout miekg/dns passes, as
the default reader does. Shutdown is unchanged: the read returns an
error once the connection is closed.

Not counted and not limited: plain DNS over TCP, DoH (HTTP/2 and
HTTP/3), DoT and DNSCrypt. They are separate servers
(`EncryptedServer`, `DNSCryptServer`, and the `tcp`/`tcp6`
`dns.Server`) and the functional spec makes them a non-goal. They do not
consume tokens.

Counted: every UDP packet of at least 12 bytes with QR = 0 received on
the plain DNS port, whatever its opcode, question or client address
(trusted subnets are checked later, in the handler). Packets with QR = 1
are never answered, so skoed cannot be made to answer responses.

### 3.2 Token bucket

One bucket per node, shared by the IPv4 and IPv6 listeners. Rate C =
effective `dns.capacity_qps`, burst B = C (one second's worth, decided
2026-11-27).

State, in integer units of 1/10⁹ token so no floating point is used:

```
mu       sync.Mutex
rate     int64   // C, tokens per second
level    int64   // current tokens × 1e9, 0 ≤ level ≤ rate × 1e9
last     int64   // ns since base, at the last refill
base     time.Time  // time.Now() at creation; carries the monotonic clock
```

`Allow()`:

```
now := int64(time.Since(base))        // monotonic, not affected by clock changes
mu.Lock()
elapsed := now - last; last = now
if elapsed > 1e9 { elapsed = 1e9 }    // a full second refills the bucket; also avoids overflow
level = min(level + elapsed × rate, rate × 1e9)
ok := level ≥ 1e9
if ok { level -= 1e9 }
mu.Unlock()
return ok
```

`SetRate(c)`: under `mu`, refill at the old rate up to now, set
`rate = c`, then `level = min(level, c × 1e9)`.

At creation the bucket is full (`level = rate × 1e9`). With
C ≤ 1,000,000, `rate × 1e9` ≤ 10¹⁵ and `elapsed × rate` ≤ 10¹⁵, far
below the int64 limit.

One short lock: `Allow` runs only on the reader goroutines (one per UDP
listener, so at most two contend), holds the lock for a few arithmetic
operations and allocates nothing.

Properties used by the scenarios:

* Over any interval of T seconds, at most C × T + C queries are admitted
  (FS-DnsOverloadCapacityConfigurable: 60 × 2,000 + 2,000 = 122,000).
* A load at a rate r ≤ C never empties a full bucket, so nothing is
  refused (FS-DnsOverloadNoRefusalBelowCapacity).
* At 2 × C from a full bucket, refusals start after 1 second, then C
  queries per second are admitted (FS-DnsOverloadAnswersUpToCapacity).

### 3.3 What a refused query costs and what is recorded

A refused query gets no goroutine, is not unpacked by miekg/dns and does
not reach `Handler.ServeDNS`. So it:

* is not written to the query log, the live query stream or the
  query-log aggregates;
* does not call the device-new hook;
* is not counted in `skoed_dns_queries_total` or
  `skoed_dns_query_duration_seconds`.

Why: the query log append and the per-query work of `ServeDNS` are what
limits the answer rate (§ 1). Recording refused queries there would spend
the CPU the refusal is meant to save, and at 2 × C half of the 10,000
query-log entries would be refusals, hiding the answered queries. The
refused queries are visible through the counter and the log line of § 6.

Cost: one `recvmsg` and one `sendmsg` system call and a few byte
operations on the reader goroutine. The only allocations are those
`dns.ReadFromSessionUDP` makes for every packet (the session and its
control-message buffer, under 100 bytes, garbage at once); nothing is
kept after the next read.

### 3.4 REFUSED response format

Built in place in the request buffer, so it is never longer than the
request:

| Part | Value |
|---|---|
| ID | copied from the query |
| QR | 1 |
| Opcode | copied |
| AA, TC, AD | 0 |
| RD, CD | copied |
| RA | 1 (as every other skoed response) |
| Z | 0 |
| RCODE | 5 (REFUSED) |
| QDCOUNT | 1 and the question copied byte for byte, when the query has QDCOUNT = 1 and its name is a sequence of uncompressed labels inside the packet; otherwise 0 and no question |
| ANCOUNT, NSCOUNT | 0 |
| ARCOUNT | 1 when the query carries an OPT record (found by walking the additional section when ANCOUNT = NSCOUNT = 0), else 0 |
| OPT | root name, type 41, UDP payload size 1232, extended RCODE 0, version 0, DO copied from the query, no options |

No Extended DNS Error option and no cookie. The startup readiness
feature (dns-startup-readiness.feature, pending) decided to send "the
same response"; its technical spec should call the same function.

## 4. Memory bound

FS-DnsOverloadMemoryBounded allows 200 MB of growth over the level with
lists loaded and no DNS load. The bound comes from three points:

1. **Refused queries hold no memory.** They are answered on the reader
   goroutine from a buffer the reader reuses (§ 3.3). Packets the
   reader cannot read in time wait in the kernel socket buffer
   (`net.core.rmem_default`, outside the process) and the kernel drops
   them when it is full; the functional spec allows 2 % of the queries
   sent to get no response.
2. **Admitted queries are at most C per second**, so the goroutines in
   flight are about C × the mean handling time. With the Background (70 %
   cached, 10 % blocked, 20 % cache misses answered upstream within
   10 ms), the mean is about 0.2 × 11 ms + 0.8 × 0.05 ms ≈ 2.3 ms: about
   25 goroutines in flight at C = 10,000, a few hundred with jitter,
   each with an 8–32 KB stack — under 10 MB.
3. **The other per-query structures are bounded**: DNS cache and query
   log at 10,000 entries each (about 10 MB and 3 MB when full), the
   device-new queue at 1,024 IPs and its checked map at 65,536.

Outside the Background, point 2 depends on the upstream: with an
upstream that times out (3 s by default), C × 3 s goroutines wait, e.g.
30,000 at C = 10,000, several hundred MB. A cap on admitted queries in
flight is open question OQ-1.

**GOMEMLIMIT.** When the `GOMEMLIMIT` environment variable is not set,
`main` sets `debug.SetMemoryLimit` at start to 80 % of the cgroup memory
limit: the smallest numeric `memory.max` (cgroup v2) or
`memory.limit_in_bytes` (cgroup v1) on the path from the process's
cgroup (`/proc/self/cgroup`) up to the root of its cgroup namespace.
When none is numeric, no limit is set. The value and its source are
logged once at start. The other 20 % is left for memory the Go runtime
does not manage (bbolt's mmap, Raft log files in the page cache). This
does not create the 200 MB bound (points 1–3 do); it makes the garbage
collector run harder before the cgroup kills the process if something
else grows. When the operator sets `GOMEMLIMIT`, it is used as is.

`GOMAXPROCS` is left to the Go runtime, which reads the cgroup CPU limit
since Go 1.25 (go.mod: Go 1.27).

## 5. Recovery

**Refusals stop within 5 seconds of the drop.** When the arrival rate
falls to 25 % of C, the bucket gains 0.75 × C tokens per second, so the
first query after the drop that finds an empty bucket is at most 1/C
second after it. No backlog is left to drain: admitted queries were at
most C per second (§ 4), and the kernel socket buffer holds a few
hundred packets, read in milliseconds. The 5 seconds of
FS-DnsOverloadRecoversWhenLoadDrops are a margin for the measurement.

**Memory within 50 MB of the level before the load, within 5 minutes.**
The memory that stays in use after the load is the DNS cache and query
log, about 13 MB when full (§ 4). The rest is free heap and stack
memory, which the Go runtime's background scavenger returns to the
system once a garbage collection has lowered the heap goal; with no
load, the runtime forces a collection at least every 2 minutes. We rely
on the scavenger and do not call `debug.FreeOSMemory`.

In Findings 1 the node still held 458 MB 120 s after the load stopped.
That came from the 300,000 goroutines: the Go runtime never frees a
goroutine's descriptor, it keeps it for reuse. Once refused queries get
no goroutine and admitted ones are limited to C per second, the number
of goroutines does not grow with the overload.

If the bench run (§ 9) shows the memory above the 50 MB bound after
5 minutes, the fallback is one `debug.FreeOSMemory()` call 60 s after
the last refusal, at most once per 5 minutes.

## 6. Visibility

### 6.1 Metric

| Series | Type | Labels |
|---|---|---|
| `skoed_dns_overload_refused_total` | counter | — |

Help: "DNS queries refused on the plain UDP listeners because the node
was above its DNS capacity." It counts only refusals for overload, not
those for clients outside the trusted subnets or (later) during startup.
It is read from the `Admission` atomic counter at scrape time by a
collector registered in `metrics.New` (same pattern as the cache
collector), so the counter keeps counting across rebuilds. The counter
is incremented when the refusal is decided, before the send, so its
increase is at least the number of REFUSED responses clients receive,
and exceeds it only by responses lost on the way back
(FS-DnsOverloadVisibleToAdministrator). Listed in prometheus-metrics.md.

### 6.2 Log line

A goroutine started with the `Admission` reads the counter every 60 s
(`time.Ticker`). When it increased since the previous tick, it logs one
line with the standard logger (stderr, the journal under systemd):

```
dns: overloaded: refused 61234 queries in the last 60 s (DNS capacity 2000 queries per second)
```

At most one line per 60-second interval, and none while nothing is
refused. During a 1-minute overload starting at t0, one tick falls
between t0 + 1 s (when refusals start) and t0 + 61 s, so the line is in
the log 70 s after t0.

## 7. API

No new operation. The setting is a property of the existing DNS settings
(`components/schemas/Settings` and `SettingsPatch` in
management-api.openapi.yaml, with `x-tsid: TS-DnsOverloadProtection`):

* `GET /api/v1/settings` returns `dns.capacity_qps` (the effective value,
  never 0) and `dns.capacity_qps_default` (read-only, the shipped
  default). Scope `read`, as today.
* `PATCH /api/v1/settings` with `{"dns": {"capacity_qps": 2000}}` sets
  it; `0` clears it so the shipped default applies. Scope `write`, as
  today. Forwarded to the leader like every write.
* Validation: a value that is not an integer gives 400 `invalid JSON: …`
  (existing decoder); a value that is not 0 and outside 100–1,000,000
  gives 400 with `{"error": "dns.capacity_qps must be 0 (default) or
  between 100 and 1000000"}`. Nothing is committed on a 400.
* The PATCH response, like the GET response, carries the effective
  value.

## 8. Failure modes and non-goals

* **Above what the reader can refuse.** One reader goroutine per
  listener refuses at the rate of one core's system calls (on the order
  of 100,000 packets per second). Above that, packets are dropped by the
  kernel without a response. Memory stays bounded (§ 4); the functional
  spec only covers 2 × C.
* **Unprotected listeners.** TCP, DoH, DoT and DNSCrypt are not limited
  (non-goal). A flood on them can still slow the node, and their queries
  are not counted against C.
* **Capacity set too high.** If the administrator sets C above what the
  node answers, admitted queries pile up as in v0.5.1 (bounded only by
  OQ-1 if accepted). The default is measured so this does not happen on
  2 cores / 2 GB.
* **Rolling upgrade.** A v0.5.1 node does not know `capacity_qps`; a
  settings write it serializes (for instance a `PATCH /api/v1/settings`
  while it is the leader) writes `dns` without the field, which resets
  the cluster to the default. Set the capacity again once every node
  runs v0.6.0. A v0.5.1 node ignores the field.
* **No per-client fairness, no priority, no forwarding of the excess to
  another node, no change to the answer rate itself** (non-goals). A
  client that sends most of the queries takes most of the capacity.
* **Clients with one configured server** see REFUSED as a failure; the
  documentation tells administrators to give clients at least two nodes
  (decided 2026-11-27).

## 9. Default capacity

### 9.1 Result

| Date | Commit | Bench | C (qps) | Scenarios |
|---|---|---|---|---|
| — | — | — | not measured yet | — |

Until it is measured, `DefaultDNSCapacityQPS` = **10,000 (provisional)**.
Rationale: on 1 core, v0.5.1 answered 26,000–34,000 qps on the 70/20/10
mix before losing queries, with no refusal work. At 2 × C the node also
refuses C queries per second (two system calls each), the leader also
runs Raft, and the bench host shares physical cores; 10,000 is about a
third of the 1-core figure and twice the 5,000 qps above which memory
grew in the 2026-11-26 run. The release ships the measured value (IMPLEMENTATION_PLAN.md).

### 9.2 Procedure

Bench: the Proxmox host of demos/perf-5node-lxc (Xeon E3-1230 v6, 4
cores / 8 threads).

* 3 skoed nodes in unprivileged Debian 13 LXC containers, each with 2
  CPU cores, 2048 MB of memory and no swap, running the v0.6.0
  candidate. The node under test is pinned (`lxc.cgroup2.cpuset.cpus`)
  to 2 physical cores whose sibling threads no other container uses.
  The other nodes, the upstream and the load generator share the other
  2 physical cores.
* Upstream: Knot answering the `perf.test` wildcard zone (p99 1.6 ms in
  the 2026-11-26 run, within the 10 ms of the Background).
* skoed config: forwarding to the upstream, cache and query log at
  10,000 entries, list "ads" (100,000 domains including
  `tracker.example.com`, block policy NXDOMAIN).
* Query mix over UDP at an even rate: 70 % from a set of 1,000 names
  queried once before the run so they are in the cache, 20 % unique
  names under `perf.test` (cache misses), 10 % names drawn from "ads".
  The run is valid when `skoed_dns_cache_hits_total` /
  (hits + misses) over the run is at least 65 %.
* Load generator: the Go client of the acceptance tests, built as a
  standalone binary, recording for every query the RCODE and the
  latency (dnsperf does not separate the latency of answers from that
  of refusals). Timeout 5 s; a query with no response within 5 s is
  lost.

Scenarios at a candidate C, each at 2 × C:
FS-DnsOverloadAnswersUpToCapacity and FS-DnsOverloadRefusesExcess on a
follower, FS-DnsOverloadMemoryBounded on the leader, with the pass
criteria of the functional spec.

Search: binary search on C between 5,000 and 50,000 qps with a
resolution of 1,000 qps, each step running the three scenarios for
2 minutes instead of 15. The highest passing C is then confirmed with
the three scenarios at their full 15 minutes; if a confirmation fails, C
is lowered by 1,000 and confirmed again. The confirmed C is recorded in
§ 9.1 with the date, commit and per-scenario results, and becomes
`DefaultDNSCapacityQPS`.

## 10. Test hooks

None. The acceptance tests set a low capacity through the real API
(`PATCH /api/v1/settings` with `dns.capacity_qps`, minimum 100), read
`skoed_dns_overload_refused_total` from `/metrics` and the log line from
the node's standard log as existing tests do. No `SKOED_TEST_MODE`
behavior is added. How the 15-minute and memory scenarios run in CI is
open question OQ-3.

## 11. Open questions for the UoR

* **OQ-1 — Cap on admitted queries in flight.** The 200 MB bound holds
  with the Background's 10 ms upstream (§ 4). With a slow or unreachable
  upstream, C × upstream timeout goroutines wait (30,000 at C = 10,000
  and 3 s).
  * A: no cap; the bound holds only under the Background.
  * B: a fixed cap of 4,096 admitted queries in flight (4,096 × 32 KB =
    128 MB at most); above it the reader sends REFUSED and counts it in
    `skoed_dns_overload_refused_total`. Below capacity with a normal
    upstream the cap is never reached (§ 4: a few hundred).
  * C: as B, with the cap as a second setting.
  * Recommendation: B. It makes the memory bound independent of the
    upstream, and needs no new setting.
* **OQ-2 — Clusters that never set the capacity follow the shipped
  default when it changes.** The provisional 10,000 will be replaced by
  the measured value, and later releases may change it.
  * A: a stored 0 means "use the shipped default", and the GET shows
    the effective value and `capacity_qps_default` (this spec).
  * B: the default is written into the cluster settings at bootstrap or
    upgrade and never changes afterwards.
  * Recommendation: A.
* **OQ-3 — Scenario durations in CI.** Five scenarios run 1 to 15
  minutes at 2 × C on a 3-node cluster with 2 cores per node, which CI
  cannot provide.
  * A: CI runs all eight FSIDs at C = 200 with the durations of the
    functional spec (about 50 minutes).
  * B: CI runs all eight FSIDs at C = 200, with the 15-minute and
    5-minute runs shortened to 1 minute; the full-length runs at the
    default C are the bench runs of § 9, kept as the demo evidence.
  * C: only the bench runs.
  * Recommendation: B.
* **OQ-4 — Web UI.** The functional spec says the administrator reads
  and sets the capacity in the DNS settings, without naming the web UI.
  * A: API only in v0.6.0.
  * B: also a "DNS capacity (queries per second)" field on the DNS
    settings page.
  * Recommendation: B, so the setting is visible where the other DNS
    settings are.
