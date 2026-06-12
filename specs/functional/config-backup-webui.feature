Feature: Configuration backup and restore via web UI
  As a network administrator
  I want to download and upload the full skoed configuration directly from the web interface
  So that I can back up, migrate, or restore my configuration without SSH or command-line access.

  Non-goals:
    - Scheduled automatic backups are out of scope.
    - Partial (per-section) export is out of scope.
    - Backup encryption at rest is out of scope.
    - Cluster enrollment state (Raft membership, join tokens) is not included in the backup.
    - Query log entries are not included in the backup.
    - Admin credentials are not included in the backup — they are a per-node
      secret. Restoring a backup never changes or locks the current admin login.

  Background:
    Given a running skoed node with blocklists, allowlist entries, local DNS entries, and custom settings configured
    And the administrator is authenticated on the web UI

  @fsid:FS-ConfigBackupWebUiDownload
  Scenario: Administrator downloads the configuration backup from the Settings page
    Given the administrator is on the Settings page
    And a "Configuration backup" section is present on the page
    When the administrator clicks "Download backup"
    Then the browser downloads a file named "skoed-config.tar.gz"
    And the archive contains YAML files representing the full configuration
    And no SSH or command-line access was required

  @fsid:FS-ConfigBackupWebUiImport
  Scenario: Administrator restores a configuration backup via the Settings page
    Given a valid "skoed-config.tar.gz" archive previously exported from a skoed node
    And the administrator is on the Settings page
    When the administrator selects the archive file using the "Restore backup" file picker
    And clicks "Restore"
    Then the web UI shows a confirmation prompt warning that existing configuration will be replaced
    When the administrator confirms the restore
    Then the node applies all configuration from the archive atomically
    And the Settings page reloads showing the restored configuration
    And an error message is shown if the archive is invalid or incomplete, leaving existing config unchanged

  @fsid:FS-ConfigBackupWebUiRoundTrip
  Scenario: Configuration round-trips correctly through the web UI
    Given the administrator has downloaded a backup from node A via the Settings page
    And a fresh node B with no configuration
    When the administrator uploads the backup to node B via the Settings page restore flow
    Then node B's blocklists, allowlist, local DNS entries, and settings match node A's
