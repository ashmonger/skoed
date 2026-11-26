# Performance report — 5-node cluster in LXC (2026-11-26)

Build `skoed v0.5.0-15-gfd5fa72` (commit fd5fa72, go1.27.4, branch
`feature/mtls-cross-host`), five nodes in LXC containers on
`ns3081405.ip-147-135-254.eu`. Raw summaries are under `results/`; the
full outputs, the scripts and a 1 s per-container sampler CSV are on the
host in `/root/perf/`. `test-report.html` has the same results with
graphs.

## Summary

* **The DNS path runs out of memory under sustained load.** Above about
  5,000 qps per node, a 2 GB container fills its RAM and 512 MB swap within
  seconds to a minute and skoed is OOM-killed: 36 kills over the run. Each
  kill moves leadership if it hits the leader, and the restarted node
  answers queries unfiltered until its blocklists are loaded again. In the
  15-minute soak every node stopped answering after 2–5 minutes, and 45
  minutes after the load stopped the nodes still held 2.0 GB and used about
  one core each. This is the main finding and it blocks any capacity claim
  above a few thousand qps.
* **Throughput is capped at about one core per node.** A single node peaks
  at 17,000–24,000 qps for cached or blocked names and 5,500–7,500 qps for
  cache misses, using 1–1.3 of its 2 cores. At 5,000 qps p50 is 0.29 ms; at
  10,000 qps p99 is 76 ms and loss 1.9 %.
* **Raft writes slow down as custom DNS records accumulate**, and each
  write adds 45–56 KB to `raft-log.bolt` (0.5 GB per node after 12,000
  writes). A fresh cluster does 132 writes/s at concurrency 8; with 12,000
  records it does 11–14.
* **Blocklists scale well.** A 2M-domain list is loaded by the leader in
  2.4 s and blocking on all five nodes in 5.1 s; the content is copied as
  files, so Raft grows by 4–12 KB per list. Memory is about 200 MB per
  million domains (2.8M domains: 830–940 MB per node).
* **Failover works:** a new leader in 1.7–3.4 s, with no DNS loss on the
  followers. **mTLS costs nothing measurable** (about 4 % on leader writes,
  within noise).
* **A 1 core / 512 MB container is not viable** with realistic lists
  (808k domains): idle RSS 213–312 MB, 3,200 qps with 23 % loss for cached
  names, no answers at all for cache misses, OOM kills.
* Defects found on the way: the OISD list in auto format loads as 11 junk
  entries; nodes answer DNS before their blocklists are loaded; a deleted
  list's file stays on disk; skoed starts before the container has its IP
  after a reboot.

## Environment

| | |
|---|---|
| Host | Xeon E3-1230 v6 (4 cores / 8 threads, 3.5 GHz), 15 GB RAM, Proxmox 9.2.41, kernel 7.0.14-30-pve |
| Nodes | CT 201–205 (n1–n5, 10.10.10.11–15), unprivileged Debian 13.6, 2 cores, 2048 MB RAM, 512 MB swap |
| Load generator | CT 210 (10.10.10.200), 4 cores, 4 GB: dnsperf 2.14.0, hey 0.1.4, HTTP server for synthetic lists |
| Upstream | CT 211 (10.10.10.100), 2 cores, 1 GB: Knot 3.4.6, wildcard zone `perf.test`; ceiling 224,000 qps, p99 1.6 ms |
| Network | private NAT bridge `vmbr1` (10.10.10.0/24), no port forward from the public IP |
| skoed config | forwarding to 10.10.10.100:53, mTLS on, cache on (10,000 entries), query log default (in-memory ring of 10,000 entries) |

CPU is overcommitted: 16 vCPU on 8 threads, and the load generator shares
the physical CPU with the nodes. Absolute numbers are therefore lower
bounds; the memory behavior does not depend on it.

dnsperf: `-c 64 -T 4` for a single node, 5 × `-c 32 -T 1` for all nodes.
About 0.16 % of "loss" is the queries in flight when a run ends (Knot
alone shows the same).

## Results

### T1 — Idle footprint

| | n1 (leader) | n2–n5 |
|---|---|---|
| RSS | 38 MB | 32–33 MB |
| CPU | 1.9 % | 0.3 % |
| `/var/lib/skoed` | 200 KB | 192–200 KB |

### T2 — DNS, one node, 30 s at maximum rate (100k-domain list loaded)

| node | mix | qps | loss | p50 / p99 / max (ms) |
|---|---|---|---|---|
| follower n2 | cached | 19,021 | 4.7 % | 4.9 / 61 / 2,550 |
| follower n2 | blocked | 19,424 | 4.8 % | 4.5 / 56 / 129 |
| follower n2 | cache miss | 7,528 | 11.2 % | 27 / 164 / 745 |
| leader n1 | cached | 16,898 | 5.4 % | 7.3 / 60 / 164 |
| leader n1 | blocked | 13,027 | 6.9 % | 6.0 / 117 / 1,087 |
| leader n1 | cache miss | 5,525 | 14.9 % | 28 / 233 / 548 |

Latency at a fixed rate (cached, follower n2):

| rate | p50 | p99 | loss |
|---|---|---|---|
| 5,000 qps | 0.29 ms | 23.6 ms | 0.15 % |
| 10,000 qps | 0.40 ms | 75.8 ms | 1.86 % |

The curve stops at 10,000 qps because loss passed 1 %. Every
maximum-rate run caused OOM kills (n2: 4 in 2 minutes, n1: 3). The first
kill of n1 moved leadership to n3, so most of the "leader" rows ran on a
follower.

### T3 — DNS, all five nodes at once, 30 s

| mix | per node | total | loss | p50 / p99 (ms) |
|---|---|---|---|---|
| cached | 9,186–10,641 qps | 49,780 qps | 3.4–3.8 % | 12–14 / 76–135 |
| 70 % cached, 20 % miss, 10 % blocked | 7,453–8,063 qps | 39,487 qps | 4.3–4.6 % | 19–24 / 84–147 |

The host CPU was saturated. 9 OOM kills: 4 during the cached run, 1 just
after it, 4 in the minute after the mixed run ended.

### T4 — Blocklist size (each list replaces the previous one)

| domains | leader loaded | all 5 nodes blocking | follower RSS | leader RSS / peak CPU | file per node |
|---|---|---|---|---|---|
| 100k | 0.13 s | 0.72–0.84 s | – | – | 3.1 MB |
| 500k | 0.58 s | 1.60–1.71 s | 84–90 MB | 125 MB / 77 % | 16 MB |
| 1M | 1.19 s | 2.76–2.86 s | 130–142 MB | 240 MB / 118 % | 30 MB |
| 2M | 2.42 s | 4.90–5.12 s | 228–262 MB | 424 MB / 191 % | 61 MB |

Public lists, added on top of the 2M list:

| list | domains | leader loaded | followers |
|---|---|---|---|
| HaGeZi Pro | 504,794 | 1.67 s | 2.6–2.8 s |
| OISD big (`format: askoed` set by hand) | 221,884 | 1.69 s | 2.4–3.1 s |
| StevenBlack hosts | 81,684 | 1.12 s | 1.8–2.1 s |

With all four (2.8M domains), RSS was 830–940 MB on every node. With the
2M list, cached and blocked lookups lost 15–40 % of their T2 throughput;
40.8 % of the "blocked" answers were REFUSED. These names reached
the upstream unfiltered, after OOM restarts.

### T5 — Raft writes (`POST /api/v1/local-dns`, 2,000 writes, 12,000 records at the end)

| concurrency | to the leader: writes/s, p50 / p99 | to a follower: writes/s, p50 / p99 |
|---|---|---|
| 1 | 41.7, 23.5 / 55 ms | 15.2, 63.6 / 122 ms |
| 8 | 13.5, 590 / 720 ms | 12.2, 658 / 808 ms |
| 32 | 11.5, 2,785 / 3,007 ms | 11.0, 2,922 / 3,160 ms |

Replication lag (2xx from the leader → the follower answers the new
record), 50 samples: p50 32–40 ms, p99 57–73 ms, no timeout. The first 20
writes took 4.7 ms each; the rate falls as records accumulate, and each
node uses 60–110 % CPU per write stream. `raft-log.bolt` reached
546–565 MB per node.

### T6 — mTLS cost (fresh cluster, concurrency 8)

| | leader: writes/s, p50 / p99 | follower: writes/s, p50 / p99 | lag p50 |
|---|---|---|---|
| mTLS off | 137.4, 58.7 / 112 ms | 60.3, 131 / 201 ms | 6.6–7.2 ms |
| mTLS on | 132.4, 61.3 / 131 ms | 59.3, 133 / 210 ms | 5.7–6.6 ms |

The Raft directory reached 192–235 MB after 4,050 writes (about 56 KB per
write).

### T7 — Leader failover (5,000 qps on two followers)

| run | stop → new leader | DNS on the two followers | writes sent to a follower |
|---|---|---|---|
| `systemctl stop` #1 | 1.7 s | 0 % loss, 1 s dip to ~4,200 qps | none succeeded within 33 s |
| `systemctl stop` #2 | 1.8 s | 0 % loss | none within 33 s |
| `systemctl stop` #3 | 2.3 s | ≤ 0.05 % loss | OK after about 3 s |
| `pct stop` | about 3.4 s | 0 % loss | none within 33 s |

In every run, a node was then OOM-killed under the 5,000 qps load, 28–64 s
after the stop, which caused a second election. That second election
probably explains the writes that failed for 33 s. A restarted node
answers DNS after 2.4 s, blocks after 2.3 s and has caught up after 10.8 s
(7.4 s for a container stop/start).

### T8 — Recovery with 2.8M domains and 12,000 records

* Wiped follower rejoined with a new token: first DNS answer at 1.05 s,
  caught up at 2.06 s, **blocking only at 4.10 s**. Between 1.05 s and
  4.10 s it answered the sample blocked name NOERROR. Snapshot 16 MB;
  leader RSS 579 → 782 MB, CPU 82 % at peak.
* `pct reboot` of a node: blocking again after 9.0 s. The first start
  failed because the container had no IP yet, and systemd restarted it 3 s
  later:

  ```
  tls listen 10.10.10.14:7000: bind: cannot assign requested address
  ```

### T9 — Small container (1 core, 512 MB, no swap)

* With 2.8M domains: 16 OOM kills within 4 s, including the container's
  systemd, journald and sshd. After a restart, three nodes stayed at
  511 MB with memory pressure (PSI some avg10) at 75–91 % and did not
  respond.
* With the public lists only (808k domains, 12,000 records): idle RSS
  213–312 MB; cached names 3,191 qps with 23 % loss, p99 217 ms; cache
  misses got no answer (node at 508 MB, PSI 62 %); writes 5.0/s, p99
  2.0 s; 1 OOM kill.

### T10 — Soak, 15 min (public lists, target 4,000 qps per node)

Answered queries per second, per minute (dnsperf `-S 60`):

| node | min 1 | min 2 | min 3 | min 4 | min 5–14 |
|---|---|---|---|---|---|
| n1 | 3,853 | 3,623 | 1,792 | 144 | 0 |
| n2 | 3,785 | 539 | 16 | 76 | 0–1 |
| n3 | 3,773 | 1,028 | 0 | 0 | 0 |
| n4 | 3,718 | 1,229 | 0 | 46 | 0 |
| n5 | 3,815 | 1,395 | 43 | 10 | 0 |

Every node served the target in the first minute, then stopped answering.
dnsperf's averages for the run (295–628 qps, loss 2.4–5.5 %) hide this,
because it sends few queries once they all time out. The nodes reached
1.9 GB of RSS in the first minute and stayed at 2.0 GB with swap full and
100–120 % CPU for the whole run.

The memory is not released when the load stops (13:36:20). n2 was killed
at 13:46:49, 10 minutes later. The four other nodes kept 2.0 GB of RSS,
swap full and still slowly growing, and used 88–112 % CPU with no DNS load
until the cluster was rebuilt for T6, 45 minutes later:

```
time  ct   skoed unit memory  skoed RSS
13:50 201  2019 MiB           2,042,740 kB
14:28 201  2032 MiB           2,065,636 kB
```

Disk did not grow (`/var/lib/skoed` 19.6 MB, the query log is in memory
only). The blocked share of this mix and the write loop are not valid
(list deleted before the run, script error).

## Findings

1. **Memory grows without limit on the DNS path under load.** RSS climbs
   at 50–200 MB/s from 5,000–10,000 qps and is not released when the load
   stops (T10: 2.0 GB and about one core per node, 45 minutes later):

   ```
   Memory cgroup out of memory: Killed process 33539 (skoed) total-vm:3876392kB, anon-rss:2053504kB
   ```

   The cause is not confirmed (the goroutine dump was truncated by
   journald and no heap profile was taken). The most likely explanation is
   that every query goes through `QueryLog.Append`
   (`apps/skoed/internal/log/query.go:84`), which shifts the 10,000-entry
   ring with `copy()` under one global mutex. miekg/dns runs each query in
   its own goroutine, so when queries arrive faster than that mutex
   lets them through, goroutines pile up. This would also explain the
   ceiling of about one core. `Aggregator.Observe`
   (`apps/skoed/internal/log/aggregates.go:108`) also keeps every distinct
   domain of the hour in a map, which grows with cache misses.
2. **Nodes answer DNS before their blocklists are loaded** — after a
   rejoin (3 s in T8) and after every OOM restart — so blocked names are
   resolved during that time.
3. **Each `local-dns` write adds 45–56 KB to the Raft log** and writes
   slow down as records accumulate (132 → 11 writes/s). It looks like each
   entry carries the whole record set.
4. **OISD in auto format loads 11 junk entries.** `detectFormat`
   (`apps/skoed/internal/filter/engine.go:253`) decides on the first line
   that is not a comment; in OISD that is `[Adblock Plus]`, which contains
   a space, so the list is parsed as a hosts file and every `||domain^`
   rule is dropped.
5. A deleted URL list's file stays on disk, and after a restart
   `blocklistsync` logs `copy "syn1m" … leader answered 404`.
6. After a container reboot, skoed starts before the container has its IP
   (Raft bind fails once). The stock unit has the same
   `After=network-online.target`.
7. Raft logs at DEBUG level in the journal.

## Not covered

* The latency curve above 10,000 qps (stopped by loss), and the write
  loop of T10.
* DoH, DoT and DNSCrypt; DHCP; the web UI (the build has none).
* A host where the load generator does not share the CPU with the nodes.

## Reproduce and clean up

Scripts are in `/root/perf/` on the host; the cluster (CT 201–205, mTLS,
leader n1) and CT 210/211 are still running. To remove everything:

```
for ct in 201 202 203 204 205 210 211; do pct stop $ct; pct destroy $ct --purge; done
ifdown vmbr1
cp /root/interfaces.bak-20261126 /etc/network/interfaces
sysctl -w net.ipv4.ip_forward=0
```
