# TS-BackupHardening — Backup Hardening Technical Specification

<!-- x-tsid: TS-BackupHardening -->
<!-- x-fsid-links: [FS-BackupExportEncrypted, FS-BackupImportEncryptedSuccess, FS-BackupImportEncryptedWrongPassphrase, FS-BackupImportPlaintext, FS-BackupScheduleEnableAndList, FS-BackupScheduleRetainCount, FS-BackupDownload, FS-BackupScheduleDisable, FS-BackupScheduleSkipsUnchanged, FS-BackupDiffDetectsChanges, FS-BackupDiffNoChanges, FS-BackupDiffSettingsChange] -->

## Endpoints

| Method | Path | Body | Response |
|--------|------|------|----------|
| GET | /api/v1/config/export | — | 200 application/gzip (plaintext tar.gz) |
| POST | /api/v1/config/export | `{"passphrase":"..."}` optional | 200 application/octet-stream (age-encrypted) or application/gzip (plaintext) |
| POST | /api/v1/config/import | multipart: file=archive, passphrase=passphrase | 200 `{"status":"imported"}` or 422 wrong passphrase |
| PUT | /api/v1/settings/backup | `{"enabled":bool,"interval_hours":int,"retain_count":int}` | 200 settings |
| GET | /api/v1/config/backups | — | 200 `{"backups":[{id,created_at,size_bytes,raft_index,encrypted}]}` |
| GET | /api/v1/config/backups/{id}/download | — | 200 archive bytes |
| POST | /api/v1/config/backups/trigger | — | 200 `{"created":bool}` |
| POST | /api/v1/config/diff | multipart: archive_a, archive_b | 200 `{added:{...},removed:{...},changed:{...}}` |

## Encryption

Age scrypt (`filippo.io/age`) wraps the tar.gz stream.
- Export: `age.NewScryptRecipient(passphrase)` → `age.Encrypt(w, r)` wraps a gzip+tar pipeline.
- Import: peek first 21 bytes for `age-encryption.org/v1` magic string; if detected, decrypt with `age.NewScryptIdentity(passphrase)` before reading the gzip stream.
- Wrong passphrase → HTTP 422 with error `"invalid passphrase or corrupted archive"`.
- Encrypted backup files are named `{id}.age` in `$data_dir/backups/`.

## Scheduled Backup Loop

```
BackupScheduler.Start()
  └─ goroutine: loop()
       ├─ re-read BackupConfig every tick
       ├─ if !enabled → sleep 30s and retry
       └─ sleep interval_hours → runOnce()
            1. read BackupConfig; skip if !enabled
            2. currentIndex = cluster.CommitIndex()
            3. lastIndex = store.BackupLastRaftIndex()
            4. if currentIndex == lastIndex && lastIndex != 0 → return false (dedup)
            5. Export config to $data_dir/backups/{id}.tar.gz
            6. store.UpsertBackupEntry(entry)
            7. store.SetBackupLastRaftIndex(currentIndex)
            8. pruneBackups(retain_count)
            9. return true
```

## Dedup Strategy

`last_backup_raft_index` is a uint64 stored under key `last_raft_index` in the `config_backups` bbolt bucket. Before creating a backup, `cluster.CommitIndex()` is compared to this value. If equal (and non-zero), the backup cycle is skipped without error.

## Diff Algorithm

Both archives are extracted in-memory via `config.Import`. Field-by-field comparison produces structured diff:
- `added.blocklists` — blocklist IDs in B not present in A
- `removed.blocklists` — blocklist IDs in A not present in B
- `added.allowlist` / `removed.allowlist` — domain strings
- `added.local_dns` / `removed.local_dns` — entry IDs
- `changed.settings` — map of setting key → new value for fields that differ (e.g. upstream_resolvers)

## Storage

- bbolt bucket: `config_backups`
- Key `settings` → JSON-serialised `BackupConfig`
- Key `entry:{id}` → JSON-serialised `BackupEntry`
- Key `last_raft_index` → big-endian uint64

## Cluster Replication

`BackupConfig` is persisted locally (node-local bbolt write — NOT Raft-replicated). Each node runs its own scheduler. On a multi-node cluster the leader schedules backups independently. This is intentional: backup files live in `$data_dir/backups/` which is per-node.
