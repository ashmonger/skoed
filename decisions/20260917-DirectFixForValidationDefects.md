# Direct fix for the defects found in v0.4.3 cluster validation

## Context

A 3-node cluster was built on a clean Proxmox host from the published v0.4.3
release and exercised end to end on 2026-09-17. DNS forwarding, blocklist
replication and leader failover all passed, and the `/var/lib/skoed/bin`
relocation shipped in v0.4.3 was confirmed in the `.deb`.

Two defects were found that are not covered by any existing specification or
by the 2026-07 security audit. Both were reproduced on the running cluster.

1. **An enabled blocklist that blocks nothing is accepted.**
   `apps/skoed/internal/api/handlers/blocklists.go:69` validates only that
   `name` is non-empty, and that `source.url` is present when
   `source.type` is `url`. An empty or unrecognized `source.type` falls
   through to the else branch, `domains` takes the value of `req.Domains`
   (nil when absent), and the blocklist is committed to Raft enabled with
   zero domains. It then reads as active through the API and the web UI
   while filtering nothing. Measured on the cluster: a POST carrying neither
   `id` nor `source` returned 201 with `domain_count: 0`.

2. **Alpine creates an unprivileged user and then runs as root.**
   `scripts/proxmox-create-alpine.sh:138-141` creates the `skoed` system
   user and group and chowns `/var/lib/skoed` to it; line 196 sets
   `command_user="root"` in the OpenRC service. The isolation the Debian
   packaging provides is therefore set up and discarded on Alpine, and
   nothing records that as a decision. Measured on the cluster: on CT 201
   the binary is owned `root:root` and the process runs as root, while CT
   200 and CT 202 run as `skoed`.

A third observation, that the OpenAPI contract declares `id` and `source`
required while the handler generates an `id` and tolerates a missing
`source`, is part of defect 1: the contract and the implementation have to
be reconciled in one direction or the other.

## Problem

Rule 5 makes functional specifications, and the UoR validation of them, a
precondition for tests and code. Both defects are small, already understood,
and already reproduced against a real cluster, so the specification step
would document behavior that is not in question rather than resolve an open
one. M50 is the cost evidence: its first cycle took four implement-and-review
cycles and is still waiting at the Rule 7 gate, and the defect it addresses
has not moved.

## Options considered

1. **Full BDD-First pipeline.** Functional specs, UoR validation, technical
   specs, tests, code. No exception needed and consistent with how M50 runs.
   Rejected for these two defects: it parks a verified, reproduced fail-open
   behind a gate whose purpose is to settle what the behavior should be,
   which is not in doubt here.
2. **Fold both into M50.** Thematically right — the blocklist defect belongs
   to the fail-closed input sweep and the Alpine gap to the secure-by-default
   posture. Rejected because M50 cycle 1 is frozen awaiting validation, so
   this would park the fix behind that gate and widen a slice the UoR has not
   yet approved.
3. **Direct fix with tests, under this exception.** Acceptance tests that
   reproduce both defects first, then the fix. Chosen.

## Decision

Option 3, approved by the UoR on 2026-09-17.

The acceptance tests carry the behavior contract in place of a functional
specification: each one states the observable outcome it pins, and is written
before the fix so it is seen to fail first. A functional specification with
`@fsid` scenarios is owed retroactively and is recorded as an open item in
`TODO.md`; until it exists these tests have no FSID and do not appear in the
traceability matrix.

This exception also covers running two implementation tasks rather than the
three the Execution Model's Phase 1 requires. The objective contains exactly
two independent defects, in disjoint parts of the tree — a Go handler with
its tests, and a provisioning script — and splitting either one further would
produce tasks that are not independent.

## Consequences

* Two defects are closed without a functional specification, so neither is
  represented in the FSID catalog until the retroactive spec is written.
  Rule 15's traceability expectation is not met for them in the meantime.
* The reconciliation of the OpenAPI contract with the handler is decided by
  whoever implements defect 1 rather than by a validated specification. The
  direction chosen must be recorded in the commit.
* Changing the Alpine service to run unprivileged is a behavior change for
  anyone running an Alpine node today: a process that could write anywhere
  becomes one that cannot. If it turns out the daemon needs a capability it
  only had as root, the fix is to grant that capability, not to revert to
  root silently.
* The exception is for these two defects only. It does not lift Rule 5 for
  M50 or for any later work.

## Related hypotheses

* That both defects are as narrow as the measurements suggest. If closing
  defect 1 turns out to require deciding what an operator means by a
  blocklist with no source — rather than simply refusing one — the objective
  has become a specification question and the work should stop and return to
  Rule 5.
* That the Alpine daemon can bind its DNS port unprivileged the way the
  systemd unit does. This is untested; the systemd unit uses
  `CAP_NET_BIND_SERVICE` and OpenRC has no direct equivalent in the current
  service file.

## Affected features

* `apps/skoed/internal/api/handlers/blocklists.go` — the create handler
* `specs/technical/management-api.openapi.yaml` — `BlocklistCreate`
* `tests/acceptance/` — new coverage for both defects
* `scripts/proxmox-create-alpine.sh` — the OpenRC service definition
* `AGENTS.md` Rule 5 and Execution Model Phase 1 — the rules excepted here
