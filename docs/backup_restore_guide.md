# Backup & Restore Operational Administration Guide

## 1. Overview

This guide details the operation, verification, and disaster recovery procedures using the automated backup and restore toolchain (`scripts/backup.sh` and `scripts/restore.sh`).

---

## 2. Taking an Online Backup

Run the backup script directly on any cluster node:
```bash
./scripts/backup.sh [config_file]
```

### 2.1 Environment Variable Overrides
The script automatically reads configuration from `config.yaml`, but can be customized with environment variables:
- `BACKUP_DEST_DIR`: Destination directory for generated tarballs (default: `./data/backups`).
- `STORAGE_METADATA_PATH`: Path to source SQLite database.
- `STORAGE_DATA_ROOT`: Path to storage object payload directory.
- `STORAGE_ADMIN_TOKEN`: Admin bearer token for triggering live SQLite vacuum via the management API.

### 2.2 Backup Artifacts
Upon completion, the script produces:
1. `storage-backup-<YYYYMMDD_HHMMSS>.tar.gz`: Complete archive containing `metadata/metadata.db`, `objects/...`, and `manifest.sha256`.
2. `storage-backup-<YYYYMMDD_HHMMSS>.tar.gz.sha256`: Cryptographic SHA-256 signature for transmission validation.

---

## 3. Restoring from a Backup

To restore data onto a target server:

```bash
./scripts/restore.sh <path-to-archive.tar.gz> [--force] [--config <config.yaml>]
```

### 3.1 Safety Guardrails
1. **Safety Overwrite Protection**: The script refuses to overwrite existing non-empty metadata databases or storage directories unless `--force` is explicitly provided.
2. **Signature Validation**: If a `.sha256` file exists alongside the target archive, the script validates the archive's SHA-256 hash before unpacking.
3. **Internal Manifest Verification**: Every extracted physical file is validated against the internal `manifest.sha256` snapshot to guarantee zero bitrot during archive generation or transfer.

---

## 4. Off-Site Backup Replication Example

Sync backup archives to remote archival storage (e.g. NFS, secondary cluster, or tape volume):

```bash
rsync -avz --progress /data/backups/ backup-host:/mnt/cold-storage/s3-backups/
```
