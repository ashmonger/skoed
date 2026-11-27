# Releases & test reports

Real-environment test reports are published for each milestone that includes
a Proxmox cluster validation run.

| Release | Feature | Test report |
|---------|---------|-------------|
| v0.5.2 | Hotfix: stats aggregator crash under DNS load | — |
| v0.5.1 | DNS hot path, mTLS across hosts, startup address wait | [Performance report](https://github.com/ashmonger/skoed/blob/master/demos/perf-5node-lxc/REPORT.md) |
| [v0.5.0](https://github.com/ashmonger/skoed/releases/tag/v0.5.0) | M50 — Blocklist content distributed by the leader, bounded Raft log, peer HTTP over mTLS | [Demo note](https://github.com/ashmonger/skoed/blob/master/demos/m50/DEMO_NOTE.md) |
| — (pending) | M15 — Test suite cleanup + keepalived VIP | [Test report](m15-test-report.html) |
| [v0.1.2](https://github.com/ashmonger/skoed/releases/tag/v0.1.2) | M14 — Block dynamic-lease clients | [Test report](m14-test-report.html) |
| [v0.1.1](https://github.com/ashmonger/skoed/releases/tag/v0.1.1) | M13 — Filtering pause | — |
| [v0.1.0](https://github.com/ashmonger/skoed/releases/tag/v0.1.0) | M11 — Distribution & packaging | — |

## v0.5.2 — Hotfix: stats aggregator crash under DNS load

- **Daemon crash under DNS load, fixed**: under sustained DNS load, the
  daemon could exit with `fatal error: concurrent map iteration and map
  write`, which stopped DNS, DHCP and the API on that node. The hourly
  stats aggregator built its top domains and top clients from the current
  hour's counters without holding their lock, while every DNS query kept
  updating them. It now detaches the counters under the lock before
  building the aggregate, and keeps them, with the queries counted since,
  for the next attempt if the commit fails.
- **Query counts**: queries counted while an aggregate was being committed
  were dropped from the hourly stats; they are now kept for the next one.
- Every node running v0.5.1 or earlier has this defect (it is present
  since v0.1.0) and should be upgraded.

## v0.5.1 — DNS hot path, mTLS across hosts, startup address wait

- **DNS memory growth under load, fixed**: in the 5-node LXC run, each
  query waited for the query-log mutex and a bbolt write transaction, so
  goroutines piled up: 274,239 goroutines and 1.5–1.9 GB of RSS at +24 s
  at 10,000 qps (`demos/perf-5node-lxc/results/diag/timeline.txt`); the two changes below
  remove both waits. Measured afterwards on a single node with 1 core
  (512 MB and 1 GB): 17,500–25,000 qps on the 70/20/10 mix with RSS at most
  262 MB and no OOM kill (results on the perf host, not in the repo) ([performance report](https://github.com/ashmonger/skoed/blob/master/demos/perf-5node-lxc/REPORT.md))
- **Query log**: the query log is a ring buffer; `QueryLog.Append` takes
  about 90–100 µs → 38 ns per call (`BenchmarkAppendFull`, 10,000 entries)
- **New-device tracking**: a query from a known client no longer opens a
  bbolt write transaction, and no longer starts a goroutine; `device.new`
  events go through a bounded queue, and dropped events are counted in
  `skoed_device_new_dropped_total`
- **Query stream**: a live query-stream client that disconnects can no
  longer crash the daemon (send on closed channel)
- **mTLS across hosts**: the Raft dial verifies the peer by its node ID
  (`ServerName`), `ResetRaftForJoin` keeps Raft TLS, certificates are
  reloaded on join, and a web-UI join installs the leader's mTLS bundle;
  validated with three Proxmox containers on separate LAN addresses
  ([demo note](https://github.com/ashmonger/skoed/blob/master/demos/mtls-cross-host/DEMO_NOTE.md))
- **Startup**: when a configured listen address is not assigned yet
  ("cannot assign requested address"), skoed waits up to 60 s instead of
  exiting, logging when it starts waiting and when the address appears
- **Roadmap**: M40, M48 and M49 removed

[→ 5-node LXC performance report](https://github.com/ashmonger/skoed/blob/master/demos/perf-5node-lxc/REPORT.md)

## v0.5.0 — M50: Blocklist content distributed by the leader

Validated on a 3-node Proxmox cluster, rolling upgrade from v0.3.2:

- **Blocklist content**: the leader downloads URL blocklists into a local
  cache and followers copy them from the leader; Raft carries only the list
  definition and version. Shadow `config.yaml` 2.85 MB → about 3 KB, RSS
  per node 140–168 MB → about 54 MB with an 81,684-domain list
- **Bounded Raft log**: `TrailingLogs` is 256, and `skoed raft compact`
  shrinks an oversized `raft-log.bolt` offline (4 MB → 1 MB)
- **Peer HTTP over mTLS**: with `mtls.enabled`, peer HTTP calls use the
  cluster CA and node certificates. Known limitation: mTLS did not work
  between real hosts (Raft failed certificate verification on the peer's
  IP address); fixed in v0.5.1
- **Go 1.27.4** and updated Go modules

[→ Demo note](https://github.com/ashmonger/skoed/blob/master/demos/m50/DEMO_NOTE.md)

## M15 — Test suite cleanup + keepalived VIP

409/409 acceptance tests pass. Real-environment keepalived VIP deployment and failover
test on a 3-node Proxmox LXC cluster (CT301/302/303, Raft quorum):

- **Alt-Svc header**: DoH/HTTP2 responses now include `Alt-Svc: h3=":port"; ma=86400`; browsers auto-upgrade to HTTP/3
- **Test timing**: 19 previously flaking tests (6 `TestAutoRefresh*` + 13 `TestDohResolverDb*`) now pass reliably with `SKOED_TEST_MODE=1`
- **keepalived VIP failover**: VIP `10.0.0.10` fails over in <14 s; health check passes throughout
- **5 provisioning bugs fixed**: Raft snapshot replication, Basic Auth removal, wrong health field, missing curl, wrong api_address

[→ Full test report](m15-test-report.html)

## M14 — Block dynamic-lease clients

10/10 acceptance tests pass. Real-environment validation on a 3-node Proxmox LXC
cluster (CT301/302/303, Raft quorum, dnsmasq DHCP on CT301):

- **DNS filtering**: 15/15 tests pass with `SKOED_TEST_MODE=1` + EDNS0 option 65500
- **DNS load (forwarding)**: 14,583 QPS peak, 0% loss
- **DNS load (recursive)**: 5,998 QPS peak, 0.10% loss
- **API load**: 391 req/s, p95 1.94 ms, 0% errors (k6 20 VUs × 20s)

[→ Full test report](m14-test-report.html)
