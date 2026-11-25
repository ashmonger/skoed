# A fourth review cycle for the M50 implementation

## Context

The execution model in AGENTS.md caps a run at 3 cycles. After the third
cycle of the M50 implementation (2026-11-25), four of five branches passed
review and the full acceptance suite gave 599 passes, 32 skips and one
flake that passed 5 times out of 5 on its own, with `go test -race` clean
and no "address already in use" left.

One MAJOR finding was still open on the blocklist content branch: after
`seedLegacy` converts a list to the replicated-version format, the
scheduler keeps the attempt record of the earlier legacy refresh. If the
capability gate is then lost (a previous-version node joins, or a node is
rolled back), `dueLegacy` waits for the list's interval instead of forcing
a download, so previous-version nodes get no domains for that list until
then, and never with an interval of 0.

## Problem

Merging with an open MAJOR goes against the gate; a fourth cycle goes
against the cap.

## Options considered

1. One more cycle, limited to the open MAJOR and a few small safety fixes.
2. Merge and record the MAJOR as a known limitation.
3. Stop and leave the branches unmerged.

## Decision

Option 1, chosen by the UoR on 2026-11-25. The fourth cycle is limited to:

* `seedLegacy` clears the legacy attempt record, so a lost gate forces a
  download again (the MAJOR);
* node IDs written as bracketed or zoned IP literals (`[::1]`, `::1%lo`)
  count as unpinnable, and join refuses unpinnable IDs only on an mTLS
  cluster (UoR decision of the same day);
* the plain DNS server's boot handler is built from a fresh snapshot right
  before `Start`, so a rebuild before boot cannot leave a handler built
  before `main` finished wiring.

No other scope. If this cycle leaves a BLOCKER or MAJOR, the run stops
without merging.

## Consequences

The remaining MINOR findings of cycle 3 go to TODO.md.

## Related hypotheses

None.

## Affected features

M50 (blocklist content distribution, peer mTLS); M1 DNS startup.
