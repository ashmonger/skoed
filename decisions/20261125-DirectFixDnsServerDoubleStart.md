# Direct fix for the DNS server started twice at boot

## Context

During the M50 acceptance runs on 2026-11-25, nodes occasionally exited at
start with:

```
start dns server: listen tcp 0.0.0.0:<port>: bind: address already in use
```

It also happened on master before M50 (tests failing after the 2-minute
readiness wait, e.g. TestConfigImportPreservesCredentials). The harness
gives every port from one counter per test process, and `ss` showed no
other socket on the port after the exit, so the process held it itself.
The chain, in `apps/skoed/cmd/skoed/main.go`:

1. `api.NewApp(..., rebuildDNS)` registers `rebuildDNS`, which runs on
   every replicated change.
2. If a change is applied while `dnsServer` is still nil, `rebuildDNS`
   builds a DNS server and calls `Start()`, which binds the DNS port.
3. Further down, `main` assigns a new, unstarted server to `dnsServer`.
4. `main` then calls `dnsServer.Start()` on that second server, which
   fails against the first one's listener, and `log.Fatalf` exits.

A node joining a cluster receives the Raft log during start, so it hits
this most often; the same can happen in production. `dnsServer` is also
read and written from two goroutines without a lock.

## Problem

Rule 5 requires a validated functional specification before code. The
defect is understood, small, outside every existing specification, and
it makes a joining node crash at start.

## Options considered

1. Write a functional spec first and fix it in its own milestone.
2. Fix it directly in M50, as was done for the v0.4.3 validation defects
   (`decisions/20260917-DirectFixForValidationDefects.md`).

## Decision

Option 2. `rebuildDNS` must not start a DNS server before `main` has
started the first one: `main` creates and starts the server, then
publishes it under a lock; `rebuildDNS` only reconfigures or restarts a
server that `main` started, and does nothing before that. A retroactive
functional scenario ("a node joining a cluster starts its DNS listener
once") is owed, recorded in TODO.md.

## Consequences

* The startup flake disappears from the acceptance suite; the harness
  retry added in M50 stays as a safety net and reports the cause.
* No behavior change after start.

## Related hypotheses

None.

## Affected features

* M1 DNS engine startup, M2 node enrollment (joining nodes).
