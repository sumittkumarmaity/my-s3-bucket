# Migration Guide: Moving to the Self-Hosted S3 Storage Engine

## 1. Overview

This document provides step-by-step instructions for migrating workloads from existing object storage providers (AWS S3, MinIO, or Ceph RadosGW) to this self-hosted S3-compatible storage engine with minimal application downtime.

---

## 2. Using `rclone` for Data Migration

`rclone` is the recommended enterprise tool for live data synchronization.

### 2.1 Configure `~/.config/rclone/rclone.conf`:
```ini
[source_minio]
type = s3
provider = Minio
endpoint = https://minio.internal.corp:9000
access_key_id = OLD_ACCESS_KEY
secret_access_key = OLD_SECRET_KEY

[target_engine]
type = s3
provider = Other
endpoint = http://10.0.10.11:9000
access_key_id = NEW_ACCESS_KEY
secret_access_key = NEW_SECRET_KEY
```

### 2.2 Execute Initial Bulk Synchronization:
```bash
rclone sync source_minio:production-bucket target_engine:production-bucket \
  --transfers 16 \
  --checkers 32 \
  --fast-list \
  --progress
```

### 2.3 Cutover Synchronization (Incremental):
Prior to DNS cutover, execute final incremental sync:
```bash
rclone sync source_minio:production-bucket target_engine:production-bucket \
  --transfers 32 \
  --fast-list
```

---

## 3. Using AWS CLI (`aws s3`)

Configure AWS CLI profile for the target engine:
```bash
aws configure --profile local-s3
# AWS Access Key ID: <Your-Access-Key>
# AWS Secret Access Key: <Your-Secret-Key>
# Default region: us-east-1
```

Sync buckets directly:
```bash
aws s3 sync s3://source-bucket s3://target-bucket \
  --endpoint-url http://10.0.10.11:9000 \
  --profile local-s3
```

---

## 4. Application Configuration Updates

Update your application environment variables or configuration files:
- **S3 Endpoint**: Change to `http://<storage-host>:9000` (or `https://...` if TLS is enabled).
- **S3 Region**: Use `us-east-1` (or your configured cluster region).
- **S3 Path Style**: Set `s3_force_path_style = true` (or `pathStyle = true` in SDKs).
- **Credentials**: Update to new Access Key ID and Secret Access Key.
