# dnsload

DNS load generator for the default-capacity measurement of
TS-DnsOverloadProtection § 9.2
(`specs/technical/dns-overload-protection.md`). It sends UDP queries to
one server at an even rate and records, for every query, whether it
received an answer (NOERROR or NXDOMAIN, UBIQUITOUS_LANGUAGE.md
"Answer"), a REFUSED response, another response (e.g. SERVFAIL) or no
response within the timeout, and the latency of the response. It reports
per second, and per phase at the end, the figures the functional spec
(`specs/functional/dns-overload-protection.feature`) checks:

| Functional-spec check | Where in the output |
|---|---|
| answers per second vs C (≥ 90 % of C, every minute) | `answered/s: mean …, lowest minute …` |
| ≥ 98 % answered or REFUSED | `answered or REFUSED … %` |
| < 0.1 % with no response | `lost … %` |
| every response that is not an answer is REFUSED | `other RCODE` is 0 |
| p99 of the answers < 50 ms | `answers … p99 … ms` |
| p99.9 of all responses < 1 s | `all responses p99.9 … ms` |

dnsperf is not used because its latency histogram mixes answers and
refusals.

## Build

It is its own Go module (`skoed/dnsload`), outside `apps/skoed`, so that
the bench tool does not become part of the product module and its
client package `loadgen` can be imported by the acceptance module
(`tests/acceptance`) with a `replace` directive; `apps/skoed/internal`
cannot be imported from another module. `loadgen/doc.go` describes its
API.

    cd tools/dnsload
    CGO_ENABLED=0 go build -o dnsload .

## Query mix

* 70 % cached names: `c0.<zone>` … `c999.<zone>` (`-cached-count`), or
  the names of `-cached-file`. Each is queried once before the run
  (`-warm`, on by default) so it is in the cache.
* 20 % never-asked names: `u<run id>-<query number>.<zone>`, unique per
  query and per run (`-zone`, default `perf.test`).
* 10 % blocked names, drawn from `-blocked-file` (one name per line or a
  hosts file; the last field of a line is the name).

`-mix 70,20,10` changes the shares.

## Counting

A query counts in the second it was sent, whenever its response comes,
and in exactly one column:

* `answered`: NOERROR or NXDOMAIN;
* `refused`: REFUSED;
* `other`: any other RCODE (SERVFAIL, FORMERR, NOTIMP, …); not an
  answer, so not in the answer latencies, but in the p99.9 of all
  responses;
* `lost`: no response within `-timeout` (default 5 s, as in § 9.2);
* `send err`: the local send failed (e.g. ENOBUFS); the query never
  reached the network, so it is not counted lost.

A response is matched to its query by DNS ID and question name; one
that matches no query in flight (duplicate, after the timeout, other
question) is counted in the final warning line, not in the rows. A
second's row is printed once the timeout of its last query has passed,
so rows lag the run by the timeout. These printed rows are for watching
the run: a query of a second that a busy worker sends, receives or
times out after its row is printed is missing from that row. The
`-csv` file and the phase summaries are written at the end from the
final counts and are the figures to record.

After Ctrl-C, dnsload stops sending, waits for the outcome of the
queries sent and prints the summaries with a line saying the run was
cancelled: they cover only the queries sent, and the seconds after the
cancellation are empty.

Each phase summary covers the whole phase, except the answers per
second, which leave out the first `-settle` of the phase (default 1 s,
the burst second the functional spec excludes at 2 × C). A phase can set
its own, e.g. `5000:300s:5s` for the 5 seconds after a load drop. The
shares answered or REFUSED, other and lost, and the latencies, are over
the whole phase.

Latencies are kept in buckets at most 3 % wide; a percentile is the
upper bound of its bucket.

## Usage

On the bench of § 9.2, dnsload runs pinned to the 2 physical cores it
shares with Knot, not to those of the node under test. Find the CPU ids
with `lscpu -e`, then e.g. `taskset -c 2,3,6,7 ./dnsload …` (one thread
on each of the 2 physical cores and their siblings; adapt to the
`lscpu -e` output). With each result, record the node's
`cpuset.cpus.effective` (`cat /sys/fs/cgroup/cpuset.cpus.effective` in
the container) and the GOMAXPROCS skoed logs at start.

`10.0.0.12` below is a follower and `10.0.0.11` the leader; check which
node is the leader in the cluster status before each run.

FS-DnsOverloadAnswersUpToCapacity and FS-DnsOverloadRefusesExcess run
on a follower; at a candidate C = 20,000, 2 minutes per binary-search
step:

    taskset -c 2,3,6,7 ./dnsload -server 10.0.0.12:53 \
      -blocked-file ads.txt -phases 40000:2m -csv step-20000-follower.csv

FS-DnsOverloadMemoryBounded runs on the leader:

    taskset -c 2,3,6,7 ./dnsload -server 10.0.0.11:53 \
      -blocked-file ads.txt -phases 40000:2m -csv step-20000-leader.csv

The 15-minute confirmation is the same with `-phases 40000:15m`.

FS-DnsOverloadRecoversWhenLoadDrops, on a follower: 2 × C for 5
minutes, then 25 % of C for 5 minutes, the second phase's answer rate
counted from 5 s after the drop:

    taskset -c 2,3,6,7 ./dnsload -server 10.0.0.12:53 \
      -blocked-file ads.txt -phases 40000:5m,5000:5m:5s

Its REFUSED criterion is from 5 s after the drop, while the phase
summary counts every REFUSED of the phase: read the `refused` column of
the CSV rows from 5 s after the drop.

FS-DnsOverloadNoRefusalBelowCapacity, at 75 % of C, where every REFUSED
of the run counts (`-settle 0`; the phase summary counts REFUSED over
the whole phase in any case):

    taskset -c 2,3,6,7 ./dnsload -server 10.0.0.12:53 \
      -blocked-file ads.txt -qps 15000 -duration 15m -settle 0

Run validity (§ 9.2): `skoed_dns_cache_hits_total` / (hits + misses)
over the run, read from the node's `/metrics`, is at least 65 %; dnsload
does not read it.

## Sockets

Each worker has its own UDP socket and gives its queries consecutive
DNS IDs. By default dnsload uses enough workers that a socket reuses an
ID no sooner than twice the timeout (16 at 100,000 qps with a 5 s
timeout), and refuses a `-workers` value that would need to reuse an ID
still in flight.

## Measuring the load generator

`-serve` turns dnsload into a UDP responder that answers every query
with an empty NOERROR (or REFUSED every `-refuse-every`-th query)
without any DNS work. The final line gives the CPU dnsload used and how
late it sent against its schedule:

    ./dnsload -serve 127.0.0.1:5300 -refuse-every 2 &
    ./dnsload -server 127.0.0.1:5300 -mix 70,30,0 -qps 50000 -duration 30s

    load generator: 8 workers, 30.01 s elapsed, CPU … s (… cores,
    … µs per query), send lag p99 … ms, max … ms

The elapsed time includes waiting for the timeout of the last queries,
so compare rates by CPU per query. A send lag p99 above a few
milliseconds means dnsload could not keep the rate. On a 2-core
Pentium G4400T test container, with dnsload pinned to one core and the
responder to the other, dnsload kept 70,000 qps on that core (0.94
core, send lag p99 2.6 ms) and 40,000 qps at 0.45 core.
