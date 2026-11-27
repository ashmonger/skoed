# Direct fix for the stats aggregator map race

## Context

On 2026-11-27, the DNS overload acceptance tests (branch
`work/2/overload-tests`) sent 400–3,000 qps to a cluster, and a node
exited twice in those runs (node-2 in the one whose stack is cited
below) with:

```
fatal error: concurrent map iteration and map write
```

The stack went through `tryFlush`, `buildAggregate`
(`apps/skoed/internal/log/aggregates.go:223`) and `topN`
(`aggregates.go:235`). The chain, in `aggregates.go`:

1. Once a minute (`FlushInterval`) or at the hour boundary, `tryFlush`
   takes `a.mu`, copies the `a.bucket` pointer and releases the lock.
2. `buildAggregate` then calls `topN` on the bucket's `domains` and
   `clients` maps, which iterates them without the lock.
3. Every DNS query calls `Observe`, which takes `a.mu` and increments
   the same maps of the same bucket.
4. The Go runtime detects the concurrent iteration and write and aborts
   the process. It is a fatal error, not a panic, so no `recover`
   catches it: DNS, DHCP and the API go down together on that node, until
   the service manager restarts it (`packaging/skoed.service`:
   `Restart=on-failure`, `RestartSec=3`).

The more queries per second, the more likely an `Observe` falls into the
`topN` window, so the crash shows under load. The code has this shape
since the aggregator was added in c89e008 (M2, 2026-06-01); the first
release with it is v0.1.0, and every release up to v0.5.1 has it.

A second defect follows from the same steps: queries observed between
step 1 and the reset after a successful commit (`a.bucket = nil`) are
written into the bucket that was already built, so they are dropped from
the hourly stats.

## Problem

Rule 5 requires a validated functional specification before code. The
defect is internal to the aggregator, its cause is known from the stack,
and it makes a node exit under load. The hourly stats contract
(`FS-QueryLogAggregatesPerNodePerHour`) does not change; the fix makes
the code meet it.

## Options considered

1. **Detach the bucket under the lock.** `tryFlush` takes the bucket and
   sets `a.bucket` to nil while holding `a.mu`, so the next `Observe`
   starts a new bucket. The aggregate is built from the detached bucket,
   which no other goroutine can reach. Queries during the build and the
   commit go into the new bucket. On a successful commit, the new bucket
   stays current for the next flush. On commit failure, `restore` adds
   the new bucket's counters to the detached bucket under the lock and
   makes the detached bucket current again. It keeps its `hourStart` and
   `openedAt`, so the retry after `retryBackoff` (30 s) happens as
   before, and all the counters, including those recorded since the
   detach, are committed for the hour of the bucket's first observation,
   as before the fix.
2. **Build the aggregate while holding the lock.** Simpler, but every
   query's `Observe` then waits for `topN`, which copies and sorts both
   maps, once a minute per node; the lock is on the DNS hot path. It
   also keeps the second defect unless the reset is moved too.
3. **Copy the maps under the lock.** Removes the race, but allocates a
   copy of both maps at every flush while holding the lock, and still
   drops the queries observed between the copy and the reset.

## Decision

Option 1, approved directly by the UoR on 2026-11-27, who asked for a
hotfix release v0.5.2. The fix is made on `fix/aggregator-race` (task
branch `work/3/aggregator-race`).

No retroactive functional scenario is owed. The two precedents owe one
because no scenario covered their behavior at all: the DNS server
started twice at boot and crashed a joining node, and the owed scenario
states that a joining node starts its DNS listener once
(`decisions/20261125-DirectFixDnsServerDoubleStart.md`); the late
listen address had no scenario either
(`decisions/20261127-WaitForUnassignedListenAddress.md`). Here the
counting behavior is already specified by
`FS-QueryLogAggregatesPerNodePerHour`
(`specs/functional/query-log-aggregates.feature`): a node that handled
100 queries, 30 of them blocked, commits an aggregate with total=100 and
blocked=30. The crash and the dropped queries both break that scenario,
and the fix restores it.

## Consequences

* A node no longer exits when a flush overlaps a query.
* Queries observed during a flush are no longer dropped: after a
  successful commit, they are in the next aggregate; after a failed
  commit, they are added to the retried bucket and committed for that
  bucket's hour.
* Every node running v0.5.1 or earlier should be upgraded to v0.5.2.
* The exception is for this fix only. It does not lift Rule 5 for any
  other work.

## Related hypotheses

None.

## Affected features

* `apps/skoed/internal/log/aggregates.go` — `tryFlush` and the merge on
  commit failure
* M19 query log aggregates (`specs/functional/query-log-aggregates.feature`);
  the aggregator itself dates from M2
* `AGENTS.md` Rule 5 — the rule excepted here
