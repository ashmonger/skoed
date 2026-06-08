Feature: Config import and export
  As a network administrator
  I want to export the full skoed configuration and import it on another node
  So that I can back up my setup, migrate to new hardware, or seed a fresh node

  What is included in the export:
    - All blocklists (metadata and manually entered domains; URL-sourced domain lists are re-downloaded on import)
    - Global allowlist
    - Local DNS entries
    - Block policy settings (global default and per-blocklist overrides)
    - Upstream resolver settings
    - Trusted subnet settings
    - Log retention settings
    - Admin credentials (hashed)

  What is NOT included in the export:
    - Query log entries
    - Cluster enrollment state (node roles, join tokens)
    - Cached DNS responses

  Non-goals:
    - Partial export (subset of config) is out of scope for M1
    - Scheduled automatic export is out of scope (M4)
    - Export encryption is out of scope for M1

  @fsid:FS-ConfigExport
  Scenario: Admin exports the full configuration
    Given skoed has blocklists, allowlist entries, local DNS entries, and custom settings configured
    When the admin requests a config export
    Then skoed produces a single archive file (tar.gz)
    And the archive contains YAML files representing all included configuration sections
    And the archive is available for download

  @fsid:FS-ConfigImportOnFreshNode
  Scenario: Admin imports a config on a fresh node
    Given a config archive exported from another skoed node
    And a fresh skoed node with no configuration
    When the admin uploads the config archive to the fresh node
    Then the node applies all configuration from the archive atomically
    And the node's blocklists, allowlist, local DNS entries, and settings match the source node
    And URL-sourced blocklists are re-downloaded from their source URLs

  @fsid:FS-ConfigImportAtomic
  Scenario: Import is rolled back if the archive is invalid or incomplete
    Given a corrupt or incomplete config archive
    When the admin uploads the archive
    Then skoed rejects the import
    And the node's existing configuration is unchanged
    And an error message describes the validation failure

  @fsid:FS-ConfigImportOverwritesExisting
  Scenario: Import on a node with existing config replaces it
    Given a skoed node with an existing configuration
    And a valid config archive from another node
    When the admin uploads the archive
    Then the node's configuration is fully replaced by the imported configuration
    And no settings from the previous configuration remain

  @fsid:FS-ConfigExportImportRoundTrip
  Scenario: Exported config imported on another node produces identical DNS behavior
    Given node A has a blocklist blocking "ads.example.com" and a local entry "nas.home" → "192.168.1.50"
    When the admin exports node A's config and imports it on node B
    Then node B blocks queries for "ads.example.com"
    And node B resolves "nas.home" to "192.168.1.50"
