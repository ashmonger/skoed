# Blocklist content distributed by the leader, and a bounded Raft log

## Context

skoed replaces AdGuard Home and Pi-hole with one configuration shared by
every node. Neither of those replicates anything: each instance holds the
list URLs and downloads the lists itself. skoed replicates the configuration
through Raft, and today that configuration includes the downloaded domains
of every URL blocklist (`config.Blocklist.Domains`).

On 2026-11-25 the 2-node home cluster (skoed 0.3.2, CTs 666 and 667, 512 MB
each) was found in an OOM loop: skoed1 had 84 OOM kills in 24 hours, skoed2
1415 restarts in total. Measured on skoed2:

* 7 blocklists, 6 of them URL lists, hold 510,980 domains; `config.yaml`
  is 16 MB, of which 16,000,759 bytes are the `filtering` section.
* `raft-log.bolt` is 1.8 GB (696 MB on disk after ZFS compression); the
  newest snapshot is from 2026-10-20 at index 8769.
* After raising the containers to 2 GB, the process held 1.1–1.9 GB of
  anonymous memory and stayed up.

The chain:

1. Every refresh, by the leader-only scheduler (`internal/refresh`) or by
   `POST /api/v1/blocklists/{id}/refresh`, writes the full domain list into
   one `blocklist.upsert` Raft entry, often several MB.
2. hashicorp/raft keeps `TrailingLogs` entries (default 10240) after each
   snapshot. The cluster has committed about 8.8k entries in total, so its
   log has never been truncated.
3. A follower catching up decodes these entries in batches, rebuilds the
   filter and rewrites `config.yaml` for each, with several copies of the
   domains in memory at once.
4. bbolt files never shrink, so the log file keeps its peak size.

## Problem

Replicating downloaded content makes memory, disk and Raft traffic grow with
list size and refresh count, not with the configuration. It also ties a
follower's ability to filter to replaying multi-MB log entries.

## Options considered

1. **Each node downloads the URL itself** and caches it under `data_dir`;
   Raft carries only the blocklist definition.
2. Each node downloads, with a fallback that copies the cached list from a
   peer over the cluster API.
3. The leader downloads and followers copy from it over the cluster API.
4. Keep replicating the content and only lower `TrailingLogs`.

## Decision

Option 3, chosen by the UoR on 2026-11-25 (after first choosing option 1
the same day). The leader keeps downloading, as the refresh scheduler does
today, but stores the content in a cache under `data_dir` and replicates
only its version (hash, domain count, refresh status). Followers that see a
new version copy the content from the leader over the authenticated cluster
API, check it against the replicated hash, and keep their previous copy
until they succeed. When the leader node goes down, the Raft election picks
a new leader, which takes over the downloads; a failed download on a live
leader does not change the leader.

Option 4 is applied in addition: `TrailingLogs` is set to 256 so the log is
truncated after each snapshot whatever the entries contain. An offline
`skoed raft compact` command shrinks log files left oversized by earlier
versions. On upgrade, each node seeds its cache from the domains still
present in the old state before they are dropped.

## Consequences

* Each list is downloaded once per refresh, by the leader only;
  FS-AutoRefreshLeaderOnly and FS-AutoRefreshUpdatesAllNodes still hold.
* A new authenticated endpoint serves cached list content to followers,
  and followers depend on reaching the leader to get a new version.
* A follower reports when its copy is older than the replicated version.
* Config exports no longer contain the domains of URL lists, so backups
  shrink and an import triggers a download on the leader.

## Related hypotheses

H4 (hashicorp/raft + bbolt suit a 1–10 MB state) holds only if the state
excludes downloaded content; with it, the state reached 35 MB and the log
1.8 GB.

## Affected features

* M5.4 Automated Blocklist Refresh (`automated-blocklist-refresh.feature`)
* M2 Cluster Config Sync (`cluster-config-sync.feature`)
* Config import/export and backups
* M50 Hardening & Technical Debt (this work)
