Feature: Backup Hardening
  As an operator running skoed on a multi-node cluster,
  I want to encrypt backups, schedule automatic exports, and diff two archives,
  so that my configuration data is protected at rest and restorable with confidence.

  # Non-goals:
  # - Database-level incremental backups (full snapshots only)
  # - Backup monitoring / alerting via external channels (use M22 webhooks)
  # - Restoring a backup without service interruption
  # - Remote backup targets (S3/SFTP) — deferred post-M31

  Background:
    Given a running skoed node with admin credentials

  # ─── Encrypted export ────────────────────────────────────────────────────────

  @fsid:FS-BackupExportEncrypted
  Scenario: Export produces an encrypted archive when a passphrase is supplied
    When the admin POSTs to /api/v1/config/export with body {"passphrase":"s3cr3t"}
    Then the response status is 200
    And the response Content-Type is application/octet-stream
    And the response body begins with the age encryption header "age-encryption.org/v1"

  @fsid:FS-BackupImportEncryptedSuccess
  Scenario: Importing an encrypted backup with the correct passphrase succeeds
    Given the admin has exported an encrypted backup with passphrase "s3cr3t"
    When the admin POSTs the encrypted archive to /api/v1/config/import with passphrase "s3cr3t"
    Then the response status is 200
    And the cluster configuration matches the exported snapshot

  @fsid:FS-BackupImportEncryptedWrongPassphrase
  Scenario: Importing an encrypted backup with the wrong passphrase returns 422
    Given the admin has exported an encrypted backup with passphrase "s3cr3t"
    When the admin POSTs the encrypted archive to /api/v1/config/import with passphrase "wrong"
    Then the response status is 422
    And the error body contains "invalid passphrase or corrupted archive"

  @fsid:FS-BackupImportPlaintext
  Scenario: Importing an unencrypted archive still works after the encryption feature ships
    Given the admin has exported a plaintext backup (no passphrase)
    When the admin POSTs the plaintext archive to /api/v1/config/import with no passphrase
    Then the response status is 200

  # ─── Scheduled auto-backup ───────────────────────────────────────────────────

  @fsid:FS-BackupScheduleEnableAndList
  Scenario: Enabling scheduled backups produces an entry in the backup list
    Given the admin enables scheduled backups with interval_hours=1 and retain_count=3
    And one hour elapses
    When the admin GETs /api/v1/config/backups
    Then the response contains at least one backup entry with fields id, created_at, and size_bytes

  @fsid:FS-BackupScheduleRetainCount
  Scenario: Stored backups are pruned to retain_count when the limit is exceeded
    Given the admin enables scheduled backups with interval_hours=1 and retain_count=3
    And 4 scheduled backup cycles have elapsed
    When the admin GETs /api/v1/config/backups
    Then the response contains exactly 3 backup entries

  @fsid:FS-BackupDownload
  Scenario: A stored backup can be downloaded by its ID
    Given at least one backup exists in /api/v1/config/backups
    When the admin GETs /api/v1/config/backups/{id}/download
    Then the response status is 200
    And the response body is a valid tar.gz or age-encrypted archive

  @fsid:FS-BackupScheduleDisable
  Scenario: Disabling scheduled backups stops new backups from being created
    Given scheduled backups are enabled
    When the admin disables scheduled backups via PUT /api/v1/settings/backup
    And one backup interval elapses
    Then no new backup entries appear in /api/v1/config/backups

  @fsid:FS-BackupScheduleSkipsUnchanged
  Scenario: No new backup is created when configuration has not changed since the last backup
    Given scheduled backups are enabled with interval_hours=1
    And a backup was created at the last interval with no configuration changes since
    When the next backup interval fires
    Then the backup list count does not increase
    And the existing backup entry is unchanged

  # ─── Backup diff ─────────────────────────────────────────────────────────────

  @fsid:FS-BackupDiffDetectsChanges
  Scenario: Diffing two archives returns a structured list of changes
    Given backup A with one blocklist "list-a" and backup B with blocklists "list-a" and "list-b"
    When the admin POSTs both archives to /api/v1/config/diff
    Then the response status is 200
    And the diff shows "list-b" as added under blocklists
    And no entries appear under removed or changed

  @fsid:FS-BackupDiffNoChanges
  Scenario: Diffing two identical archives returns empty diff sections
    Given two exports of the same configuration state
    When the admin POSTs both archives to /api/v1/config/diff
    Then the response status is 200
    And all diff sections (added, removed, changed) are empty arrays

  @fsid:FS-BackupDiffSettingsChange
  Scenario: A changed setting appears in the diff under the changed section
    Given backup A with upstream DNS "1.1.1.1" and backup B with upstream DNS "8.8.8.8"
    When the admin POSTs both archives to /api/v1/config/diff
    Then the response contains a changed entry for the upstream resolver setting
