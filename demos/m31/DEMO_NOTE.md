# M31 — Backup Hardening

## Implemented

### Encrypted Export / Import
- `POST /api/v1/config/export` — accepts optional `{"passphrase":"..."}` body; returns age-encrypted (`application/octet-stream`) when passphrase supplied, plain `tar.gz` otherwise
- `POST /api/v1/config/import` — multipart `archive` + optional `passphrase` field; returns 422 with `"invalid passphrase or corrupted archive"` on wrong passphrase
- Encryption library: `filippo.io/age v1.3.1` (scrypt key derivation, AES-256-GCM)
- Plaintext import still works after the encryption feature ships (backward-compatible)

### Scheduled Auto-Backup
- `PUT /api/v1/settings/backup` — enables scheduled backups with `interval_hours` and `retain_count`; persisted to bbolt `config_backup_settings` bucket via Raft
- `BackupScheduler` goroutine: wakes every `interval_hours`, exports config, deduplicates by SHA-256 hash, prunes oldest entries when `retain_count` is exceeded
- `GET /api/v1/config/backups` — lists stored backup entries (`id`, `created_at`, `size_bytes`)
- `GET /api/v1/config/backups/{id}/download` — streams stored archive (`.tar.gz` or `.age`)
- `POST /api/v1/config/backups/trigger` — forces one backup cycle immediately; returns `{"created":true/false}`

### Deduplication (FS-BackupScheduleSkipsUnchanged)
- On each scheduled interval, the scheduler exports config to an in-memory buffer and computes SHA-256
- Compares against `last_content_hash` stored in bbolt; skips backup if hash matches
- This correctly ignores background Raft log activity (e.g. DoH resolver heartbeats) — only actual config changes trigger a new backup

### Backup Diff
- `POST /api/v1/config/diff` — multipart `archive_a` + `archive_b`; returns JSON diff with `added`, `removed`, `changed` sections covering blocklists, allowlists, local DNS entries, and settings

## Not Implemented / Deferred

- Database-level incremental backups (full snapshots only)
- Remote backup targets (S3, SFTP) — post-M31
- Backup failure webhook event (`backup.failed`) — post-M31
- Backup encryption key rotation after passphrase change
- Restoring a backup without service interruption

## Validation

### Acceptance Tests (Proxmox host, go test direct)
12/12 pass:
- `TestBackupExportEncrypted` ✓
- `TestBackupImportEncryptedSuccess` ✓
- `TestBackupImportEncryptedWrongPassphrase` ✓
- `TestBackupImportPlaintext` ✓
- `TestBackupScheduleEnableAndList` ✓
- `TestBackupScheduleRetainCount` ✓
- `TestBackupDownload` ✓
- `TestBackupScheduleDisable` ✓
- `TestBackupScheduleSkipsUnchanged` ✓ (dedup via SHA-256 content hash)
- `TestBackupDiffDetectsChanges` ✓
- `TestBackupDiffNoChanges` ✓
- `TestBackupDiffSettingsChange` ✓
