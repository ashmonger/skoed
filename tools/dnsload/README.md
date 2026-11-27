# dnsload

DNS load generator for the default-capacity measurement of
TS-DnsOverloadProtection § 9.2
(`specs/technical/dns-overload-protection.md`). It sends UDP queries to
one server at an even rate and records, for every query, the RCODE and
the latency, or that no response came within the timeout. It reports
per second, and per phase at the end, the figures the functional spec
(`specs/functional/dns-overload-protection.feature`) checks:

| Functional-spec check | Where in the output |
|---|---|
| answers per second vs C (≥ 90 % of C, every minute) | `answered/s: mean …, lowest minute …` |
| ≥ 98 % answered or REFUSED | `answered or REFUSED … %` |
| < 0.1 % with no response | `lost … %` |
| every non-answer is REFUSED | `lost` and `other RCODE` |
| p99 of the answers < 50 ms | `answers … p99 … ms` |
| p99.9 of all responses < 1 s | `all responses p99.9 … ms` |

dnsperf is not used because its latency histogram mixes answers and
refusals.

## Build

It is its own Go module (`skoed/dnsload`), outside `apps/skoed`, so that
the bench tool does not become part of the product module and its
client package `loadgen` can be imported by the acceptance module
(`tests/acceptance`) with a `replace` directive; `apps/skoed/internal`
cannot be imported from another module.

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

A query counts in the second it was sent, whenever its response comes.
An answer is any response other than REFUSED, as in the functional
spec; `other` counts the answers that are neither NOERROR nor NXDOMAIN
(e.g. SERVFAIL). A query with no response within `-timeout` (default
5 s, as in § 9.2) is lost. A second's row is printed once the timeout of
its last query has passed, so rows lag the run by the timeout.

Each phase summary leaves out the first `-settle` of the phase (default
1 s, the burst second the functional spec excludes at 2 × C); a phase
can set its own, e.g. `5000:300s:5s` for the 5 seconds after a load
drop.

Latencies are kept in buckets at most 3 % wide; a percentile is the
upper bound of its bucket.

## Usage

The 2 × C scenarios of § 9.2 (FS-DnsOverloadAnswersUpToCapacity,
FS-DnsOverloadRefusesExcess, FS-DnsOverloadMemoryBounded) at a
candidate C = 20,000, 2 minutes per binary-search step:

    ./dnsload -server 10.0.0.12:53 -blocked-file ads.txt \
      -phases 40000:2m -csv step-20000.csv

The 15-minute confirmation:

    ./dnsload -server 10.0.0.12:53 -blocked-file ads.txt \
      -phases 40000:15m -csv confirm-20000.csv

FS-DnsOverloadRecoversWhenLoadDrops: 2 × C for 5 minutes, then 25 % of
C for 5 minutes, the second phase summarized from 5 s after the drop:

    ./dnsload -server 10.0.0.12:53 -blocked-file ads.txt \
      -phases 40000:5m,5000:5m:5s

FS-DnsOverloadNoRefusalBelowCapacity, at 75 % of C:

    ./dnsload -server 10.0.0.12:53 -blocked-file ads.txt \
      -qps 15000 -duration 15m

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
