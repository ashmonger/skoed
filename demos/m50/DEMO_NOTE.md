# M50 demo note — blocklist content distributed by the leader

Branch `feature/m50-node-local-blocklists` at 7f4150b. Validated by the UoR
on 2026-11-25.

## Environment

Three throwaway Debian 13 containers on Proxmox (pve1), each with its own
LAN address: demo1 192.168.1.179, demo2 192.168.1.192, demo3
192.168.1.155. Raft on `<ip>:7000`, API on 8080, DNS on 53, forwarding to
1.1.1.1. Blocklist: StevenBlack hosts (81,684 domains).

## Implemented scope, as observed

Cluster started on v0.3.2 (the home cluster's version), then upgraded
rolling to the final build, followers first:

| | v0.3.2 | final build |
|---|---|---|
| shadow `config.yaml` | 2.85 MB | about 3 KB |
| `raft-log.bolt` | 4 MB | 1 MB after `skoed raft compact` |
| RSS per node | 140–168 MB | about 54 MB |
| `stevenblack` | 81,684 domains, replicated | 81,684 domains, same `content_sha256`, `current` on every node |

* DNS blocking never stopped on the nodes that were up during the upgrade;
  upgraded followers seeded their copy from the old state without
  downloading.
* The config export holds the URL and none of the domains.
* After 320 writes, every node kept 255 entries before its snapshot.
  `skoed raft compact` refused while the daemon ran
  (`raft-log.bolt is locked: stop the skoed daemon first`), then compacted
  each stopped node (4194304 → 1048576 bytes, `.pre-compact` kept); each
  node rejoined in about 3 s.
* A follower cut from the leader's API with `iptables` kept blocking with
  its old copy and reported `out_of_date`; it was `current` 3 s after the
  rule was removed. The list host saw one download, from the leader.
* Stopping the leader elected a new one in 3 s; blocking continued and the
  new leader downloaded nothing.

## Not implemented / limitations

* **mTLS does not work between real hosts** (pre-existing since M5.3).
  Raft dials without a `ServerName` and node certificates carry only the
  node ID, `localhost`, 127.0.0.1 and ::1, so Raft fails with
  `x509: certificate is valid for 127.0.0.1, ::1, not 192.168.1.192`. A
  join then leaves the cluster without quorum. The M50 peer mTLS listener
  therefore cannot be used across hosts until this is fixed; the acceptance
  tests run on 127.0.0.1 only. Filed in TODO.md; the docs warn about it.
* Downloads from private addresses need `SKOED_ALLOW_PRIVATE_FETCH=1`
  (existing behavior, used in the demo for a test list served on the LAN).
* Known minor follow-ups are listed in TODO.md (M50 follow-ups).
