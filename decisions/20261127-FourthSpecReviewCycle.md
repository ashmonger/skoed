# A fourth review cycle for the DNS overload and startup specs

## Context

The execution model in AGENTS.md caps a run at 3 cycles. The third review
of the draft functional specs for DNS overload protection and DNS startup
readiness (`work/1/overload-specs` at dba02c2) left 2 MAJOR findings, both
about wording that makes a scenario impossible to test as written:

* `FS-DnsOverloadCapacityConfigurable` gives no run length or measurement
  window, and its upper bound conflicts with the recommended burst
  allowance;
* the recommended default capacity is measured with no excess load, while
  the scenarios load a node at twice its capacity on the same 2 cores, so
  a default measured that way can fail them by definition.

The specs are drafts: no technical spec, test or code depends on them yet.

## Problem

Handing the specs to the UoR with open MAJOR findings goes against the
gate; a fourth cycle goes against the cap.

## Options considered

1. One more cycle, limited to the cycle-3 findings.
2. The UoR decides the DECISION points first, and the findings are fixed
   in the same pass as those decisions.
3. Pause the specs and leave the branch unmerged.

## Decision

Option 1, approved by the UoR on 2026-11-27. The fourth cycle fixes the
cycle-3 findings only. The UoR then decides the DECISION points of both
specs before any further review cycle.

## Consequences

The specs reach the UoR with no known MAJOR wording finding. Any finding
left after this cycle is handled together with the UoR's decisions.

## Related hypotheses

None.

## Affected features

DNS overload protection (`specs/functional/dns-overload-protection.feature`);
DNS startup readiness (`specs/functional/dns-startup-readiness.feature`).
