# M50 — blocklist content distribution: acceptance tests

Branch: `feature/m50-node-local-blocklists`. Status: specs validated,
acceptance tests written and merged, implementation not started.

## What M50 changes

On 2026-11-25 the 2-node home cluster (skoed 0.3.2) was found in an OOM
loop. Its 6 URL blocklists hold 510,980 domains, and every refresh wrote
them into one Raft entry, so `raft-log.bolt` reached 1.8 GB. The decision
(`decisions/20261125-LeaderDistributedBlocklistContent.md`):

* the leader downloads URL lists into `<data_dir>/blocklists/<id>.list`;
* Raft carries only the list definition and a version (hash, count,
  refresh status), through a new `blocklist.content_set` entry;
* followers copy the file from the leader through
  `GET /api/v1/cluster/_internal/blocklists/{id}/content`;
* with mTLS enabled, all peer HTTP goes through a peer listener secured by
  the cluster CA (`encrypted-cluster-mesh.md`, "Internal API");
* `TrailingLogs` drops to 256, and `skoed raft compact` shrinks a log file
  left oversized by earlier versions.

Specs: `specs/functional/blocklist-content-distribution.feature`,
`specs/technical/blocklist-content-distribution.md`,
`specs/technical/encrypted-cluster-mesh.md`.

## The tests

| File | Covers |
|------|--------|
| `tests/acceptance/blocklist_content_distribution_test.go` | the 13 `FS-BlocklistContent*` scenarios |
| `tests/acceptance/raft_log_bounds_test.go` | `FS-RaftLogStaysBounded`, `FS-RaftLogCompactOffline`, `FS-RaftLogCompactRefusedWhileRunning` |
| `tests/acceptance/encrypted_mesh_test.go` | `FS-MtlsInternalApiHTTPS` (previously claimed as "implicitly" covered, which it was not) |
| `tests/acceptance/m50_harness_test.go` | shared helpers |

How they observe the behavior without hooks in the binary:

* **Who downloaded.** A local list server counts requests per node from the
  User-Agent `skoed/<version> (node <node_id>)`, which the implementation
  must send.
* **A follower cut off from the leader.** The follower runs as uid 65534
  and an `iptables` owner-match rule rejects its connections to the
  leader's API port, while Raft keeps working. This needs root and
  `NET_ADMIN`; without them the test is skipped and says so.
* **Upgrade.** `run-in-docker.sh` builds v0.4.4 (`SKOED_PREVIOUS_REF`) as
  `SKOED_PREVIOUS_BINARY`; the test starts a cluster with it, swaps every
  node to the binary under test with the list URL failing, and checks that
  filtering continues with no download.
* **Peer mTLS.** The harness reserves RaftPort+1 for each node
  (`ClusterNode.PeerAPIPort`), the default peer listener address.

## Running them

```
./tests/acceptance/run-in-docker.sh -run '^(TestBlocklistContent|TestRaftLog|TestMtlsInternalApiHTTPS)'
```

Not on a developer laptop: run on a test host or container where root and
`iptables` are available.

Against the current binary (2026-11-25, Debian 13 CT on Proxmox, 2 cores):
`TestBlocklistContentConfigReplicates` and
`TestBlocklistContentInlineStillReplicates` pass (behavior the design
keeps); the other 15 fail because the feature does not exist yet.

## Open items

* UoR validation of the tests, then implementation (TODO.md, M50 section).
* Test follow-ups: `iptables -w`; re-check blocking after the legacy
  domains are cleared in the upgrade test.
* Found on the way, outside M50: the audit middleware truncates every write
  body to 8 KB; `tools/traceability/traceability_check.sh` does not
  recognize Go tests.
