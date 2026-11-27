# Wait for a listen address that is not assigned yet at start

## Context

skoed failed to start three times after a reboot of a Proxmox LXC
container, because systemd started the daemon before the container's IP
was set, although the unit has `After=network-online.target` and
`Wants=network-online.target`. A listener bound to that IP failed, the
daemon exited, and systemd restarted it 3 s later.

* Once during the 5-node performance run on 2026-11-26, after
  `pct reboot` of a node with cluster mTLS
  (`demos/perf-5node-lxc/REPORT.md`, T8, lines 174-178):

  ```
  tls listen 10.10.10.14:7000: bind: cannot assign requested address
  ```

* Twice on 2026-11-27 during the AdGuard Home comparison runs, in the
  journal of CT 221 (a single node at 10.10.10.21), each time after
  `pct reboot`: once for the setup of the 512 MB run and once for the
  1 GB run. The results of those runs are on the perf host in
  `/root/perf/results/adguard-compare/`, not in the repository.

  ```
  start raft: raft transport: listen tcp 10.10.10.21:7000: bind: cannot assign requested address
  ```

The same failure applies to every listener skoed binds to a specific IP:
Raft (plain and mTLS), the peer API, the main API and the dual-port
HTTPS listener. No functional specification covers what skoed does when
a configured local address is missing at start.

## Problem

Rule 5 makes a functional specification, and its UoR validation, a
precondition for tests and code. The defect is reproduced, its cause is
known, and the expected behavior was stated by the UoR, so a
specification step would not settle anything that is in question.

## Options considered

1. **Retry the bind in skoed for a bounded time.** On
   `EADDRNOTAVAIL`, retry every 500 ms for up to 60 s, and fail startup
   after 60 s. skoed logs one line when it starts waiting and one when
   the address appears, with no line per retry. Works whatever the init
   system or container runtime.
2. **systemd unit changes only.** Does not work: the unit already
   orders skoed after `network-online.target`, and the failures above
   happened with that ordering.
3. **Leave it to systemd restarts.** Works, but logs a failed start on
   every boot and delays the node by the restart interval.

## Decision

Option 1, approved directly by the UoR on 2026-11-27, for every
listener bound to a specific IP: Raft (plain and mTLS), the peer API,
the main API and the dual-port HTTPS listener.

An acceptance test carries the behavior contract in place of a
functional specification:
`tests/acceptance/startup_unassigned_address_test.go`
(`TestStartupWaitsForUnassignedAddress`, with a plain and an mTLS Raft
case). It is written before the fix and was seen to fail against the
current code. A retroactive functional scenario with an `@fsid` ("a
node whose listen address is assigned after start waits for it") is
owed, recorded in `TODO.md`; until it exists the test has no FSID and
does not appear in the traceability matrix.

The contract:

* On `EADDRNOTAVAIL`, skoed retries every 500 ms for up to 60 s. It logs
  one line when it starts waiting, containing
  `waiting for local address <host> to be assigned (listener <addr>)`.
* When the address appears, it logs one line containing
  `local address <host> assigned, listening on <addr>` and continues.
* After 60 s without the address, startup fails with an error
  containing `address <addr> still not assigned after 60s`, and the
  process exits non-zero.

## Consequences

* The fix is not represented in the FSID catalog until the retroactive
  scenario is written, so Rule 15's traceability expectation is not met
  for it in the meantime.
* A node whose address is wrong in its configuration, rather than late,
  now takes 60 s to fail instead of failing at once. The log line names
  the address, so the cause stays visible.
* The case where the address appears while skoed waits cannot be
  reproduced black-box without root changes to the host's interfaces;
  only unit tests cover it.
* The exception is for this fix only. It does not lift Rule 5 for any
  other work.

## Related hypotheses

* That in an LXC container `network-online.target` is reached before the
  container's address is set. This is concluded from the failures above,
  which happened with the unit ordered after that target; it was not
  observed directly.
* That 60 s covers the delay of an LXC container setting its address.
  In T8, systemd's first restart 3 s later succeeded.

## Affected features

* `apps/skoed/internal/bindwait/` — new: the bounded bind retry
* `apps/skoed/internal/cluster/` — Raft listener, plain and mTLS
* `apps/skoed/internal/api/peer_listener.go` — peer API listener
* `apps/skoed/internal/api/server.go` — main API and dual-port HTTPS
  listeners
* `tests/acceptance/startup_unassigned_address_test.go` — new test
* `AGENTS.md` Rule 5 — the rule excepted here
