# Incident Response Playbook & Emergency Runbooks

## 1. Incident Severity Definitions

- **SEV-1 (Critical Outage)**: S3 service completely inaccessible or widespread data corruption detected.
- **SEV-2 (Degraded Cluster)**: Node failure in multi-node cluster, or disk space in warning state ($< 15\%$).
- **SEV-3 (Minor)**: Isolated replication lag or non-critical background job errors.

---

## 2. Emergency Scenarios & Remediation Protocols

### 2.1 Scenario 1: Disk Space Exhaustion (HTTP 507 InsufficientStorage)

**Symptom**:
Clients receive `HTTP 507 InsufficientStorage` on PUT object and upload part requests.

**Diagnosis**:
```bash
# 1. Check filesystem capacity
df -h /data/storage

# 2. Check cluster metrics
curl -s http://127.0.0.1:9001/metrics | grep storage_disk_
```

**Remediation**:
1. Trigger immediate emergency Garbage Collection to sweep orphaned parts and unreferenced files:
   ```bash
   curl -X POST http://127.0.0.1:9001/api/v1/jobs/gc \
     -H "X-Admin-Token: $ADMIN_TOKEN"
   ```
2. Identify stale multipart directories older than 24h:
   ```bash
   find /data/storage/multipart/ -mindepth 1 -maxdepth 1 -mtime +1
   ```
3. Expand underlying volume (LVM / cloud disk resize):
   ```bash
   lvextend -r -l +100%FREE /dev/mapper/storage-data
   ```

---

### 2.2 Scenario 2: Data Corruption Detected by Scrubber

**Symptom**:
Periodic integrity scrubber reports `corrupted > 0` or client receives decryption failure.

**Diagnosis**:
```bash
# Check scrubber job status
curl -s http://127.0.0.1:9001/api/v1/jobs/status \
  -H "X-Admin-Token: $ADMIN_TOKEN"
```

**Remediation**:
1. Check journal logs for hardware I/O bitrot:
   ```bash
   dmesg -T | grep -E "I/O error|EXT4-fs error|XFS"
   ```
2. Trigger selective re-sync from healthy replica peer node:
   ```bash
   # Re-enqueue replication job from remote peer for the affected object key
   curl -X POST http://remote-node:9001/api/v1/jobs/resync \
     -H "X-Admin-Token: $REMOTE_TOKEN" \
     -d '{"bucket":"target-bucket","key":"corrupted-file.bin"}'
   ```
3. Re-run integrity check to verify 100% pass rate.

---

### 2.3 Scenario 3: Database Lock Contention / Busy Timeout

**Symptom**:
Logs indicate `database is locked (5)` or `busy_timeout` expired.

**Diagnosis**:
SQLite WAL checkpoint contention or long-running transaction.

**Remediation**:
1. Check WAL file size:
   ```bash
   ls -lh /data/metadata/metadata.db*
   ```
2. Force an immediate non-blocking WAL checkpoint:
   ```bash
   sqlite3 /data/metadata/metadata.db "PRAGMA wal_checkpoint(TRUNCATE);"
   ```
3. Restart daemon if an unhandled transaction hung.
