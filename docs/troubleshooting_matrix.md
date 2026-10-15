# Operational Troubleshooting & Diagnostics Matrix

## 1. Quick Diagnostic Decision Matrix

| Symptom / Error Message | Potential Root Cause | Verification & Diagnostic Command | Immediate Resolution Action |
|---|---|---|---|
| **`403 Forbidden` (`RequestTimeTooSkewed`)** | Client system clock drifted $> 15\text{ minutes}$ from storage server. | Check server time: `timedatectl`<br/>Compare with client `Date` header. | Configure NTP/Chrony on client and server to synchronize clocks with upstream stratum-1 servers. |
| **`403 Forbidden` (`SignatureDoesNotMatch`)** | Secret key mismatch, invalid canonical string, or altered payload hash. | Check logs for calculated vs received signature.<br/>Inspect `Authorization` header. | Verify credentials with `storagectl keys list`. Ensure client URL encodes characters identically. |
| **`400 Bad Request` (`XAmzContentSHA256Mismatch`)** | In-flight network corruption or incorrect client hash computation. | Calculate body hash: `shasum -a 256 <file>`. Compare with `X-Amz-Content-Sha256`. | Ensure client calculates hash on raw body or specify `UNSIGNED-PAYLOAD`. |
| **`507 Insufficient Storage`** | Node free disk space is below `min_free_space_bytes` or `min_free_space_percent`. | Run `df -h /data/storage`.<br/>Query `curl localhost:9001/metrics \| grep storage_disk_free`. | Trigger emergency GC (`POST /api/v1/jobs/gc`), remove old backups, or expand underlying LVM volume. |
| **`400 Bad Request` (`InvalidPartOrder`)** | Multipart completion XML has part numbers out of ascending sequence. | Inspect client XML payload `<CompleteMultipartUpload>`. | Ensure client SDK orders parts array ascending by `PartNumber` prior to calling completion API. |
| **`400 Bad Request` (`EntityTooSmall`)** | Non-final multipart chunk is $< 5\text{ MB}$. | Check part sizes via `GET /bucket/key?uploadId=...`. | Adjust client multipart chunk threshold to $\ge 5\text{ MB}$ (5,242,880 bytes). |
| **`404 Not Found` (`NoSuchUpload`)** | Upload ID does not exist, was aborted, or path traversal attempted. | Verify upload ID format (must be valid UUID). | Verify uploadId with `GET /bucket/key?uploadId=...`. Ensure client is not reusing aborted upload IDs. |
| **`500 Internal Error` (`cipher: message authentication failed`)** | AES ciphertext on disk corrupted or tampered with. | Run integrity scanner:<br/>`POST /api/v1/jobs/integrity`. | Restore affected object from healthy cluster replica peer or recent snapshot backup. |
| **`database is locked (5)`** | Long-running transaction or stale WAL lock file. | Inspect `/data/metadata/metadata.db*` file locks (`fuser` / `lsof`). | Force WAL checkpoint: `sqlite3 metadata.db "PRAGMA wal_checkpoint(TRUNCATE);"`. |

---

## 2. On-Call Emergency CLI Toolkit

```bash
# 1. Health Probe
curl -i http://localhost:9001/health

# 2. Cluster Status & Capacity Summary
curl -s http://localhost:9001/api/v1/status | jq .

# 3. Check Replication Conflicts
curl -s http://localhost:9001/api/v1/replication/conflicts -H "X-Admin-Token: $ADMIN_TOKEN" | jq .

# 4. Trigger Immediate Object Scrubber Scan
curl -X POST http://localhost:9001/api/v1/jobs/integrity -H "X-Admin-Token: $ADMIN_TOKEN"

# 5. Check Live Prometheus Metrics
curl -s http://localhost:9001/metrics | grep -E "s3_errors|storage_disk_free"
```
