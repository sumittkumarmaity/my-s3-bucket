# Enterprise Disaster Recovery (DR) Runbook & Continuity Plan

## 1. Objectives & Service Level Agreements (SLAs)

| Metric | Target | Description |
|---|---|---|
| **RPO (Recovery Point Objective)** | $< 1 \text{ hour}$ (Snapshot) / $< 5 \text{ seconds}$ (Active Replication) | Maximum acceptable data loss window during total catastrophic facility failure. |
| **RTO (Recovery Time Objective)** | $< 15 \text{ minutes}$ | Maximum allowable duration to restore full S3 read/write service on cold infrastructure. |

---

## 2. Backup Execution Procedures

### 2.1 Online Automated Backup (`scripts/backup.sh`)
Execute automated online snapshot:
```bash
./scripts/backup.sh config.yaml
```

**Actions Performed by Script**:
1. Connects to local Admin API (`POST /api/v1/system/backup`) to trigger an online SQLite `VACUUM INTO` backup (ensuring zero write blocking or reader lock contention).
2. Takes a consistent filesystem snapshot of `/data/storage/objects/`.
3. Calculates SHA-256 integrity checksums for all objects into `manifest.sha256`.
4. Compresses all assets into a timestamped archive: `data/backups/storage-backup-YYYYMMDD_HHMMSS.tar.gz`.
5. Emits an accompanying cryptographic signature: `storage-backup-YYYYMMDD_HHMMSS.tar.gz.sha256`.

### 2.2 Cron Configuration
Add to root crontab (`crontab -e`):
```cron
# Run S3 Engine full snapshot hourly
0 * * * * cd /opt/storage-engine && ./scripts/backup.sh config.yaml >> /var/log/storage-backup.log 2>&1
```

---

## 3. Disaster Recovery Restoration Procedure

### 3.1 Step 1: Prepare Target Host
Install prerequisites on fresh node (RHEL/Ubuntu/Debian):
```bash
# Verify architecture and storage mount point
mkdir -p /data/storage /data/metadata
```

### 3.2 Step 2: Validate Archive Integrity
```bash
shasum -a 256 -c /mnt/backup/storage-backup-20261015_120000.tar.gz.sha256
```

### 3.3 Step 3: Execute Restoration Script (`scripts/restore.sh`)
```bash
./scripts/restore.sh /mnt/backup/storage-backup-20261015_120000.tar.gz --force --config config.yaml
```

### 3.4 Step 4: Start Engine Daemon
```bash
systemctl start storage-server
# Or direct execution:
./storage-server --config config.yaml
```

### 3.5 Step 5: Execute Cryptographic Data Verification
Trigger immediate object scrubber to verify zero post-restore bitrot:
```bash
curl -X POST http://127.0.0.1:9001/api/v1/jobs/integrity \
  -H "X-Admin-Token: $ADMIN_TOKEN"
```
Verify the output confirms `"corrupted": 0` and `"missing": 0`.
