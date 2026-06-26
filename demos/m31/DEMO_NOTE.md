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

### Proxmox 3-Node Cluster (2026-06-25)

3-node Raft cluster: CT200 (skoed-1 leader), CT201 (skoed-2), CT202 (skoed-3) — Alpine Linux.

**TEST 1 — Encrypted Export:**
- `POST /api/v1/config/export` with passphrase returns age-encrypted body
- Response starts with `age-encryption.org/v1` header ✓

**TEST 2 — Scheduled Backup Config:**
- `PUT /api/v1/settings/backup` enables schedule; response confirms `enabled=true` ✓

**TEST 3 — Trigger + Dedup:**
- First trigger: `{"created":true}` ✓
- Second trigger (no config change): `{"created":false}` — dedup working ✓

**TEST 4 — List Backups:**
- `GET /api/v1/config/backups` returns 1 entry after one trigger ✓

**TEST 5 — Download:**
- `GET /api/v1/config/backups/{id}/download` streams 795 bytes ✓

**TEST 6 — Diff:**
- Export A → add allowlist entry → Export B → `POST /api/v1/config/diff`
- Response: `{"added":{"allowlist":["diff-test.example.com"],...}}`  ✓

### Web UI
Settings page backup section expanded with:
- Optional passphrase field for encrypted export (`.age` archive); empty → plain `.tar.gz`
- Passphrase field for importing encrypted archives
- Scheduled backup toggle, interval (hours), retain count — persisted via `PUT /api/v1/settings/backup`
- Stored backups table: created timestamp, size, per-row download button
- "Trigger now" button with dedup feedback ("Backup created." / "No changes — backup skipped (dedup).")

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
