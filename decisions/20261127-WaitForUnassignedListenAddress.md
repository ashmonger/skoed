# Wait for a listen address that is not assigned yet at start

## Context

During the 5-node performance run on Proxmox LXC containers, on
2026-11-26 and 2026-11-27, skoed failed to start three times after a
container (re)boot (`demos/perf-5node-lxc/REPORT.md`, T8 and finding 6).
systemd started the daemon before the container's IP was set, although
the unit has `After=network-online.target` and
`Wants=network-online.target`. The Raft listener failed, the daemon
exited, and systemd restarted it 3 s later:

```
start raft: raft transport: listen tcp 10.10.10.21:7000: bind: cannot assign requested address
```

With cluster mTLS enabled, the same failure is reported by the TLS
listener:

```
tls listen 10.10.10.14:7000: bind: cannot assign requested address
```

No functional specification covers what skoed does when a configured
local address is missing at start.

## Problem

Rule 5 makes a functional specification, and its UoR validation, a
precondition for tests and code. The defect is reproduced, its cause is
known, and the expected behavior was stated by the UoR, so a
specification step would not settle anything that is in question.

## Options considered

1. **Retry the bind in skoed for a bounded time.** On
   `EADDRNOTAVAIL`, retry every 500 ms for up to 60 s, log once that the
   node waits for the address, and fail startup after 60 s. Works
   whatever the init system or container runtime.
2. **systemd unit changes only.** Does not work: in an LXC container,
   `network-online.target` is reached before the container's address is
   set, so the unit already has the ordering this option would add.
3. **Leave it to systemd restarts.** Works, but logs a failed start on
   every boot and delays the node by the restart interval.

## Decision

Option 1, approved directly by the UoR on 2026-11-27.

An acceptance test carries the behavior contract in place of a
functional specification:
`tests/acceptance/startup_unassigned_address_test.go`
(`TestStartupWaitsForUnassignedAddress`). It is written before the fix
and was seen to fail against the current code. It has no FSID, so it
does not appear in the traceability matrix.

The contract:

* On `EADDRNOTAVAIL`, skoed retries every 500 ms for up to 60 s and logs
  one line containing
  `waiting for local address <host> to be assigned (listener <addr>)`.
* When the address appears, it logs
  `local address <host> assigned, listening on <addr>` and continues.
* After 60 s without the address, startup fails with an error
  containing `address <addr> still not assigned after 60s`, and the
  process exits non-zero.

## Consequences

* The fix is not represented in the FSID catalog, so Rule 15's
  traceability expectation is not met for it.
* A node whose address is wrong in its configuration, rather than late,
  now takes 60 s to fail instead of failing at once. The log line names
  the address, so the cause stays visible.
* The case where the address appears while skoed waits cannot be
  reproduced black-box without root changes to the host's interfaces;
  only unit tests cover it.
* The exception is for this fix only. It does not lift Rule 5 for any
  other work.

## Related hypotheses

* That 60 s covers the delay of an LXC container setting its address.
  On the Proxmox run, systemd's first restart 3 s later succeeded.

## Affected features

* `apps/skoed/internal/cluster/` — Raft and mTLS listener startup
* `tests/acceptance/startup_unassigned_address_test.go` — new test
* `AGENTS.md` Rule 5 — the rule excepted here
