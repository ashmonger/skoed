Feature: Config Shadow YAML
  As an administrator running skoed in a container snapshotted by Proxmox Backup Server
  I want the YAML representation of the cluster config to always be current on disk
  So that a filesystem-level backup captures the latest state without needing to call any API.

  Background:
    Given a running skoed node with its data directory at /var/lib/skoed
    And the cluster has at least one committed config change

  @fsid:FS-ConfigShadowYamlPresentOnDisk
  Scenario: The shadow YAML file is always present
    When the administrator inspects /var/lib/skoed/config.yaml
    Then the file exists
    And the file is a valid M1-format YAML config
    And the file contains every blocklist, allowlist entry, local DNS entry, and setting currently committed in bbolt

  @fsid:FS-ConfigShadowYamlUpdatesAfterWrite
  Scenario: The shadow YAML reflects a new write within the debounce window
    When the administrator commits a blocklist addition via any node
    Then within 5 seconds /var/lib/skoed/config.yaml on the receiving node contains the new blocklist
    And within 5 seconds /var/lib/skoed/config.yaml on every other cluster node also contains the new blocklist

  @fsid:FS-ConfigShadowYamlAtomicWrite
  Scenario: The shadow YAML is never partially written
    When the YAML write is interrupted mid-flush by a process kill
    Then on next startup /var/lib/skoed/config.yaml is either the previous fully-valid version or the new fully-valid version
    And it is never a truncated or partially-written file

  @fsid:FS-ConfigShadowYamlIgnoredOnRead
  Scenario: Editing the YAML on disk does not change running cluster state
    Given the node is running with config version N
    When the administrator edits /var/lib/skoed/config.yaml to add a new blocklist
    And the node continues running without restart
    Then the running filter engine does not gain the new blocklist
    And within 5 seconds the next Raft-committed change overwrites the manual edit

  @fsid:FS-ConfigShadowYamlExcludesNodeLocal
  Scenario: The shadow YAML excludes node-local and operational state
    When the administrator inspects /var/lib/skoed/config.yaml
    Then it does NOT contain Raft membership (cluster/members)
    And it does NOT contain join tokens (cluster/tokens)
    And it does NOT contain query log aggregates (stats/*)
    And it does NOT contain node-local settings (node.id, raft_address, api_address)

  @fsid:FS-ConfigShadowYamlRebuiltOnBoot
  Scenario: The shadow YAML is rebuilt from bbolt on every startup
    Given the node has crashed leaving a stale config.yaml on disk
    And bbolt holds a newer committed state
    When the node restarts
    Then within 2 seconds /var/lib/skoed/config.yaml matches bbolt's current state

  @fsid:FS-ConfigShadowYamlRoundTrips
  Scenario: A PBS-restored YAML round-trips through M1→M2 migration
    Given /var/lib/skoed/config.yaml was restored from a Proxmox Backup Server snapshot
    And no cluster.bbolt exists on the restored node
    When the node starts for the first time after restore
    Then the YAML is imported into bbolt via the normal M1→M2 migration
    And the node bootstraps as a single-node Raft cluster with the restored config
    And the imported state matches what was in the YAML

  Non-goals:
    - Honouring manual edits to config.yaml on a running node (this would re-introduce two writers).
    - Sub-second freshness of the shadow YAML (1–5 second debounce is acceptable for backup purposes).
    - Writing per-node state into the shadow YAML (would break PBS-restore-to-different-host).
