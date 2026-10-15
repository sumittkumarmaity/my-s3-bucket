# S3-Compatible Storage Engine

[![Go Version](https://img.shields.io/badge/go-1.22%2B-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
[![Tests](https://img.shields.io/badge/tests-passing-brightgreen.svg)]()

A lightweight, standalone, high-performance S3-compatible object storage service written in Go. Features a CGO-free SQLite metadata engine, a 2-level hashed filesystem store, AWS Signature V4 authentication, multipart uploads, server-side AES-256-GCM encryption, background garbage collection, and an embedded React administrative console.

---

## Table of Contents

- [Features](#features)
- [Enterprise Documentation & Architecture Guides](#enterprise-documentation--architecture-guides)
- [Architecture & Port Overview](#architecture--port-overview)
- [Prerequisites](#prerequisites)
- [Installation](#installation)
  - [Option A: Build from Source (Local / Development)](#option-a-build-from-source-local--development)
  - [Option B: Automated Production Install (Linux / systemd)](#option-b-automated-production-install-linux--systemd)
  - [Option C: Rebuilding the Web Console (Optional)](#option-c-rebuilding-the-web-console-optional)
- [Configuration & Environment Variables](#configuration--environment-variables)
  - [Configuration Reference (`config.yaml`)](#configuration-reference-configyaml)
  - [Environment Variable Overrides](#environment-variable-overrides)
- [Starting the Application](#starting-the-application)
  - [1. Running Locally (Foreground)](#1-running-locally-foreground)
  - [2. Running as a Service (systemd)](#2-running-as-a-service-systemd)
- [Enterprise Backup & Disaster Recovery](#enterprise-backup--disaster-recovery)
- [Web Management Console](#web-management-console)
- [Command-Line Management (`storagectl`)](#command-line-management-storagectl)
- [Connecting with S3 Clients & SDKs](#connecting-with-s3-clients--sdks)
  - [AWS CLI](#aws-cli)
  - [Python (`boto3`)](#python-boto3)
  - [Node.js (`@aws-sdk/client-s3`)](#nodejs-aws-sdkclient-s3)
  - [cURL](#curl)
- [Observability & Monitoring](#observability--monitoring)
- [Running Tests](#running-tests)
- [Directory Structure](#directory-structure)
- [Troubleshooting](#troubleshooting)
- [License](#license)

---

## Features

- **Full S3 Compatibility**:
  - Bucket operations: Create, Delete, List (`ListObjectsV2`), Versioning (`ENABLED`/`SUSPENDED`).
  - Object operations: `PUT`, `GET`, `HEAD`, `DELETE`, HTTP `Range` requests (byte streaming).
  - Multipart Uploads: Initiate, Upload Part, List Parts, Complete, and Abort with automatic atomic assembly.
- **Authentication & Security**:
  - AWS Signature Version 4 (SigV4) header and query-parameter (Presigned URL) validation.
  - Granular IAM user and access key generation/revocation.
  - Optional transparent server-side AES-256-GCM encryption at rest.
  - Built-in CORS middleware for direct web client uploads.
- **Data Integrity & Resiliency**:
  - Streaming checksum computation (Castagnoli CRC32C, MD5, SHA256).
  - Scheduled bit-rot integrity scanner validating physical files against stored metadata.
  - Two-level hashed directory layout (`objects/aa/bb/...data`) preventing inode limits and directory bottlenecks.
- **Background Maintenance**:
  - Automatic Garbage Collection (GC) to clean up abandoned multipart chunks and orphaned files.
  - Storage admission control enforcing minimum free disk space thresholds.
- **Administration & Monitoring**:
  - Embedded React 19 + Vite Web Console served directly from the server binary.
  - Full-featured CLI utility (`storagectl`) for administration and scripting.
  - Native Prometheus `/metrics` endpoint and `/health` probes.

---

## Enterprise Documentation & Architecture Guides

Comprehensive technical documentation, operational runbooks, security specifications, and architecture manuals are maintained in the [`docs/`](docs/) directory:

| Manual / Runbook | Description |
| :--- | :--- |
| **[System Architecture](docs/architecture.md)** | End-to-end component interactions, concurrency patterns, and design principles |
| **[Threat Model & Security](docs/threat_model.md)** | STRIDE threat matrix, attack surface analysis, and defense-in-depth mitigations |
| **[S3 API Compatibility](docs/s3_api_compatibility.md)** | Supported S3 REST operations, XML error formats, and AWS SDK compatibility |
| **[Storage Engine Layout](docs/storage_layout.md)** | 2-level hashed filesystem layout, inode optimization, and atomic commit semantics |
| **[Database Schema & Migrations](docs/database_schema.md)** | SQLite DDL schemas, indexes, WAL mode, pragmas, and versioning migrations |
| **[Replication Topology](docs/replication_topology.md)** | Multi-peer outbox pattern, state machine, vector clocks, and conflict resolution |
| **[Disaster Recovery Runbook](docs/disaster_recovery_runbook.md)** | Step-by-step catastrophic loss recovery, validation checklists, and RTO/RPO targets |
| **[High Availability Architecture](docs/high_availability.md)** | Active-passive failover clustering, Keepalived/VRRP, and reverse proxy setup |
| **[Security Hardening Guide](docs/security_hardening_guide.md)** | TLS 1.3 configuration, OS-level permissions, firewalls, and credential hygiene |
| **[Capacity Planning Guide](docs/capacity_planning.md)** | IOPS, bandwidth, inode exhaustion modeling, and hardware sizing formulas |
| **[Observability Runbook](docs/observability_runbook.md)** | Prometheus metrics catalog, Grafana dashboard configuration, and alert thresholds |
| **[Incident Response Playbook](docs/incident_response_playbook.md)** | Playbooks for split-brain, corruption, disk exhaustion, and authentication lockouts |
| **[Backup & Restore Guide](docs/backup_restore_guide.md)** | Hot backup procedures (`VACUUM INTO`), physical snapshots, and restoration validation |
| **[Migration Guide](docs/migration_guide.md)** | Zero-downtime migration strategies from AWS S3, MinIO, or Ceph |
| **[Performance Tuning Guide](docs/performance_tuning.md)** | Linux kernel sysctl, filesystem mount flags, Go GC tuning, and SQLite optimizations |
| **[Troubleshooting Matrix](docs/troubleshooting_matrix.md)** | Fast diagnostics for error codes, HTTP statuses, and common operational failure modes |

---

## Architecture & Port Overview

The application listens on two dedicated network interfaces:

```
+---------------------------------------------------------------------------------+
|                                 storage-server                                  |
|                                                                                 |
|  [Port 9000] - S3 API Gateway                [Port 9001] - Management & Web UI  |
|  * S3 XML Protocol                           * Embedded React Dashboard SPA     |
|  * AWS SigV4 Auth                            * REST Admin API (/api/v1/*)       |
|  * Bucket & Object CRUD                      * Prometheus Metrics (/metrics)    |
|  * Multipart Uploads                         * Health Checks (/health)          |
|  * Presigned URLs                            * Node Replication Endpoints       |
+---------------------------------------------------------------------------------+
            |                                                    |
            v                                                    v
+-------------------------------+              +----------------------------------+
|    Local Filesystem Engine    |              |     SQLite Metadata Store        |
|    Hashed Object Storage      |              |     WAL Mode, Connection Pool    |
|    ./data/storage/objects/    |              |     ./data/metadata/storage.db   |
+-------------------------------+              +----------------------------------+
```

| Service | Default Port | Protocol | Description |
| :--- | :--- | :--- | :--- |
| **S3 Storage API** | `9000` | HTTP / XML | S3-compatible object storage operations |
| **Admin Console & REST API** | `9001` | HTTP / JSON / HTML | Web Dashboard, REST management, `/metrics`, `/health` |

---

## Prerequisites

- **Go**: Version `1.22` or newer (Go `1.26+` recommended)
- **Git**: For cloning the repository
- **Node.js & npm** *(Optional)*: Version `18+` (only required if modifying and re-compiling the frontend UI; pre-built production assets are already included)
- **Operating System**: macOS, Linux (`amd64`, `arm64`)

---

## Installation

### Option A: Build from Source (Local / Development)

1. **Clone and enter the project directory**:
   ```bash
   git clone https://github.com/my-s3-bucket/storage-engine.git
   cd storage-engine
   ```

2. **Download Go dependencies**:
   ```bash
   go mod download
   ```

3. **Build the server and CLI binaries**:
   ```bash
   # Build the main storage daemon
   go build -o storage-server ./cmd/storage-server

   # Build the CLI management tool
   go build -o storagectl ./cmd/storagectl
   ```

   This produces two self-contained binaries in your current working directory:
   - `storage-server`: The storage engine and web server daemon.
   - `storagectl`: The command-line management client.

---

### Option B: Automated Production Install (Linux / systemd)

For production deployment on Linux servers, use the automated installer script located at `deploy/install.sh`:

1. **Make the installer executable and run it with `sudo`**:
   ```bash
   chmod +x deploy/install.sh
   sudo ./deploy/install.sh
   ```

2. **What the installer automates**:
   - Creates a dedicated system user and group (`storage:storage`).
   - Creates system directories:
     - Configuration: `/etc/storage-engine/`
     - Storage data: `/var/lib/storage-engine/data/`
     - Metadata database: `/var/lib/storage-engine/metadata/`
   - Compiles and installs `storage-server` and `storagectl` to `/usr/local/bin/`.
   - Copies production configuration template to `/etc/storage-engine/config.yaml`.
   - Installs and enables the `systemd` service (`storage-server.service`).

---

### Option C: Rebuilding the Web Console (Optional)

The React web console is already pre-built and embedded into `web/dist/`. If you modify any frontend code in `web/src/`, recompile the static assets:

```bash
cd web
npm install
npm run build
cd ..

# Rebuild the server binary to embed updated static files
go build -o storage-server ./cmd/storage-server
```

---

## Configuration & Environment Variables

The storage server is configured via a YAML file and supports full 12-factor environment variable overrides. A default development configuration is available in [config.yaml](file:///Users/sumit/CodeRepos/my-s3-bucket/config.yaml), and a production template is provided in [deploy/config.example.yaml](file:///Users/sumit/CodeRepos/my-s3-bucket/deploy/config.example.yaml).

### Configuration Reference (`config.yaml`)

```yaml
server:
  host: "127.0.0.1"           # Bind interface ("0.0.0.0" for external access)
  s3_port: 9000               # S3 API port
  admin_port: 9001            # Web UI, Admin REST API, /metrics, /health
  tls:
    enabled: false            # Set true to enable native TLS/HTTPS on both ports
    cert_file: ""             # Path to TLS certificate PEM (e.g. /etc/ssl/certs/s3.crt)
    key_file: ""              # Path to TLS private key PEM (e.g. /etc/ssl/private/s3.key)

storage:
  root: "./data/storage"      # Root directory for object storage files
  max_object_size: 5368709120 # Maximum object size in bytes (5 GB)
  min_free_space_bytes: 10737418240 # Minimum free space before rejecting writes (10 GiB, 0 = disabled)
  min_free_space_percent: 5.0 # Minimum free disk percentage before rejecting writes (default 5.0%)

metadata:
  driver: "sqlite"            # Metadata store driver
  path: "./data/metadata/storage.db" # SQLite database file path

security:
  auth_enabled: true          # Set false to allow unauthenticated S3 requests
  admin_access_key: "storageadmin"
  admin_secret_key: "storageadminsecretkey123"
  admin_token: "storageadminsecretkey123" # Bearer / X-Admin-Token for Admin API
  # encryption_key: "01234567890123456789012345678901" # Optional 32-byte AES-256 key for encryption-at-rest

replication:
  enabled: false              # Multi-node replication
  node_id: "node-1"
  listen_address: "http://127.0.0.1:9001"
  auth_token: "rep-secret-token"

jobs:
  gc_interval_seconds: 3600             # Garbage collector run interval (seconds)
  integrity_scan_interval_seconds: 86400 # Bit-rot scanner interval (seconds)
```

> [!NOTE]
> On the first startup, if no access keys exist in the database, the server automatically bootstraps an `admin` user using the `admin_access_key` and `admin_secret_key` specified in your configuration file. All secret keys stored in SQLite are transparently envelope-encrypted using AES-256-GCM.

### Environment Variable Overrides

Any setting in `config.yaml` can be overridden at runtime using environment variables:

| Environment Variable | Overrides Config Setting | Example Value |
| :--- | :--- | :--- |
| `STORAGE_SERVER_HOST` | `server.host` | `0.0.0.0` |
| `STORAGE_S3_PORT` | `server.s3_port` | `9000` |
| `STORAGE_ADMIN_PORT` | `server.admin_port` | `9001` |
| `STORAGE_TLS_ENABLED` | `server.tls.enabled` | `true` |
| `STORAGE_TLS_CERT_FILE` | `server.tls.cert_file` | `/etc/ssl/certs/s3.crt` |
| `STORAGE_TLS_KEY_FILE` | `server.tls.key_file` | `/etc/ssl/private/s3.key` |
| `STORAGE_DATA_ROOT` | `storage.root` | `/var/lib/storage-engine/data` |
| `STORAGE_METADATA_PATH` | `metadata.path` | `/var/lib/storage-engine/metadata/storage.db` |
| `STORAGE_ADMIN_ACCESS_KEY` | `security.admin_access_key` | `admin-key-prod` |
| `STORAGE_ADMIN_SECRET_KEY` | `security.admin_secret_key` | `super-secret-key-prod` |
| `STORAGE_ADMIN_TOKEN` | `security.admin_token` | `admin-token-prod` |
| `STORAGE_ENCRYPTION_KEY` | `security.encryption_key` | `32-byte-hex-or-passphrase-here` |
| `STORAGE_REPLICATION_NODE_ID` | `replication.node_id` | `node-dc1-01` |
| `STORAGE_REPLICATION_AUTH_TOKEN` | `replication.auth_token` | `rep-token-prod` |

---

## Starting the Application

### 1. Running Locally (Foreground)

To run the server in the foreground using the default [config.yaml](file:///Users/sumit/CodeRepos/my-s3-bucket/config.yaml):

```bash
./storage-server -config config.yaml
```

**Expected Startup Output:**
```text
2026/10/12 11:15:00 INFO Starting Storage Engine daemon...
2026/10/12 11:15:00 INFO Bootstrapped default admin credentials access_key=storageadmin
2026/10/12 11:15:00 INFO Server-side encryption disabled (plaintext)
2026/10/12 11:15:00 INFO S3 API listening addr=127.0.0.1:9000
2026/10/12 11:15:00 INFO Admin API & Web UI listening addr=127.0.0.1:9001
```

To stop the server, press `Ctrl + C`. The engine will gracefully drain in-flight connections and stop background job workers within 10 seconds.

---

### 2. Running as a Service (systemd)

If installed via `deploy/install.sh` or manual systemd setup:

```bash
# Start the service
sudo systemctl start storage-server

# Enable auto-start on system boot
sudo systemctl enable storage-server

# Check service status
sudo systemctl status storage-server

# View live system logs
sudo journalctl -u storage-server -f
```

---

## Enterprise Backup & Disaster Recovery

Production-grade live hot backups and verified restores are supported out of the box via automated scripts in [`scripts/`](scripts/):

### 1. Creating a Hot Online Backup

The backup script uses SQLite's atomic `VACUUM INTO` mechanism to create a consistent metadata snapshot without stopping writes, followed by storage payload copying and SHA256 checksum manifest generation:

```bash
# Run backup to target directory
./scripts/backup.sh /var/backups/storage-engine
```

**Artifacts Generated**:
- `metadata.db`: Transactionally consistent SQLite database backup.
- `storage_data.tar.gz`: Compressed archive of all physical object payloads.
- `manifest.sha256`: Cryptographic checksums of all backup artifacts for tamper verification.

### 2. Restoring from a Backup

The restore script cryptographically validates SHA256 checksums, enforces safeguards to prevent accidental overwrites, and restores metadata and object files:

```bash
# Verify and restore from backup directory
./scripts/restore.sh /var/backups/storage-engine/backup_20261015_050000 --force
```

For complete recovery workflows, point-in-time scenarios, and disaster playbooks, consult the **[Backup & Restore Guide](docs/backup_restore_guide.md)** and **[Disaster Recovery Runbook](docs/disaster_recovery_runbook.md)**.

---

## Web Management Console

Open your web browser and navigate to:

👉 **[http://localhost:9001](http://localhost:9001)**

### Default Login / Admin Credentials
- **Access Key**: `storageadmin`
- **Secret Key**: `storageadminsecretkey123`
- **Admin Token**: `storageadminsecretkey123`

### Console Capabilities
- **Overview Dashboard**: Live cluster health, disk capacity and utilization, active uploads, total request counters, and uptime.
- **Bucket Manager**: Create and delete buckets, toggle bucket versioning.
- **Object Browser**: Inspect bucket contents, object sizes, ETags, and version IDs.
- **IAM & Access Keys**: Create users, generate S3 access/secret key pairs, and revoke compromised credentials.
- **Maintenance Actions**: Trigger manual Garbage Collection (GC) sweeps or Bit-Rot Integrity Scans.
- **Replication Status**: Inspect cluster node topologies and conflict resolutions.

---

## Command-Line Management (`storagectl`)

The `storagectl` tool provides management capabilities over the Admin REST API.

### Authentication Setup

By default, `storagectl` connects to `http://localhost:9001` with token `storageadminsecretkey123`. You can specify a custom endpoint and token using flags or environment variables:

```bash
# Via flags
./storagectl --endpoint http://localhost:9001 --token storageadminsecretkey123 <command>

# Or export environment variables in your shell
export STORAGE_ADMIN_TOKEN="storageadminsecretkey123"
```

### Common Commands

#### 1. System Status
```bash
./storagectl status
```
*Output:*
```text
HEALTH:         healthy
VERSION:        1.0.0
UPTIME:         2h 15m
BUCKETS:        3
OBJECTS:        142
ACTIVE UPLOADS: 0
REQUESTS:       1250
STORAGE TOTAL:  500.00 GB
STORAGE USED:   12.45 GB
STORAGE FREE:   487.55 GB
```

#### 2. Bucket Management
```bash
# List all buckets
./storagectl bucket list

# Create a standard bucket
./storagectl bucket create my-app-assets

# Create a bucket with versioning enabled
./storagectl bucket create my-backups --versioning

# Delete an empty bucket
./storagectl bucket delete my-app-assets
```

#### 3. User & Access Key Management
```bash
# List existing IAM users
./storagectl user list

# Create a new IAM user
./storagectl user create backend-service

# Generate an S3 access key pair for a user
./storagectl access-key create backend-service
# Save the printed AccessKeyID and SecretKey!

# List access keys
./storagectl access-key list

# Revoke an access key
./storagectl access-key delete <ACCESS_KEY_ID>
```

#### 4. Background Job Management
```bash
# Trigger an immediate Garbage Collection run
./storagectl gc run

# Trigger a bit-rot data integrity scan
./storagectl integrity scan
```

#### 5. Cluster Replication
```bash
# List connected replication nodes
./storagectl replication nodes

# List replication conflict events
./storagectl replication conflicts
```

---

## Connecting with S3 Clients & SDKs

The storage engine is compatible with any standard AWS S3 client SDK or tool.

### AWS CLI

Configure credentials in `~/.aws/credentials` or pass environment variables:

```bash
export AWS_ACCESS_KEY_ID="storageadmin"
export AWS_SECRET_ACCESS_KEY="storageadminsecretkey123"
export AWS_DEFAULT_REGION="us-east-1"
```

Use `--endpoint-url` pointing to port `9000`:

```bash
# Create a bucket
aws --endpoint-url=http://localhost:9000 s3 mb s3://my-test-bucket

# Upload a file
aws --endpoint-url=http://localhost:9000 s3 cp ./sample.txt s3://my-test-bucket/sample.txt

# List bucket contents
aws --endpoint-url=http://localhost:9000 s3 ls s3://my-test-bucket/

# Download a file
aws --endpoint-url=http://localhost:9000 s3 cp s3://my-test-bucket/sample.txt ./downloaded.txt

# Delete an object
aws --endpoint-url=http://localhost:9000 s3 rm s3://my-test-bucket/sample.txt
```

---

### Python (`boto3`)

```python
import boto3

# Initialize S3 client pointing to the local storage engine
s3 = boto3.client(
    's3',
    endpoint_url='http://localhost:9000',
    aws_access_key_id='storageadmin',
    aws_secret_access_key='storageadminsecretkey123',
    region_name='us-east-1'
)

# 1. Create a bucket
s3.create_bucket(Bucket='data-pipeline')

# 2. Upload an object
s3.put_object(
    Bucket='data-pipeline',
    Key='records/dataset.csv',
    Body=b'id,name,value\n1,alpha,100\n2,beta,200\n',
    ContentType='text/csv'
)

# 3. Retrieve the object
response = s3.get_object(Bucket='data-pipeline', Key='records/dataset.csv')
content = response['Body'].read().decode('utf-8')
print("Retrieved data:\n", content)
```

---

### Node.js (`@aws-sdk/client-s3`)

```typescript
import { S3Client, PutObjectCommand, GetObjectCommand } from "@aws-sdk/client-s3";

const s3 = new S3Client({
  endpoint: "http://localhost:9000",
  region: "us-east-1",
  credentials: {
    accessKeyId: "storageadmin",
    secretAccessKey: "storageadminsecretkey123"
  },
  forcePathStyle: true // Mandatory for non-AWS custom domains
});

// Upload an object
await s3.send(new PutObjectCommand({
  Bucket: "data-pipeline",
  Key: "hello.txt",
  Body: "Hello from Node.js!"
}));
```

---

### cURL

```bash
# Check service health
curl http://localhost:9000/health
# Response: {"status":"healthy","database":"connected","disk":"writable"}

# Query admin API status
curl -H "X-Admin-Token: storageadminsecretkey123" \
     http://localhost:9001/api/v1/status

# List buckets via Admin REST API
curl -H "X-Admin-Token: storageadminsecretkey123" \
     http://localhost:9001/api/v1/buckets
```

---

## Observability & Monitoring

### Health Checks
- **S3 API Port**: `http://localhost:9000/health`
- **Admin API Port**: `http://localhost:9001/health`
- Returns HTTP `200 OK` when both SQLite metadata and local storage disk are accessible and writable.

### Prometheus Metrics
Exposed at `http://localhost:9001/metrics`. Standard exported metrics include:
- `storage_requests_total{method, path, status}`: Total HTTP request counters.
- `storage_request_duration_seconds{method, path}`: Request latency histogram.
- `storage_disk_total_bytes`: Total storage filesystem capacity.
- `storage_disk_used_bytes`: Used disk space in bytes.
- `storage_disk_free_bytes`: Free disk space available.
- `storage_buckets_total`: Current total bucket count.
- `storage_objects_total`: Current total object count.
- `storage_active_multipart_uploads`: Active in-progress multipart uploads.

---

## Running Tests

Run the test suite across all packages:

```bash
# Run all unit tests
go test ./...

# Run all tests with verbose output
go test -v ./...

# Run end-to-end integration tests
go test -v ./tests/...
```

---

## Directory Structure

```text
.
├── cmd/
│   ├── storage-server/       # Storage Engine daemon main entrypoint
│   └── storagectl/           # CLI management client entrypoint
├── docs/                     # 16 Enterprise operational & architecture manuals
├── deploy/
│   ├── config.example.yaml   # Production configuration template
│   ├── install.sh            # Automated Linux system installer script
│   └── systemd/              # systemd service unit template
├── internal/
│   ├── api/                  # Admin REST API & endpoints (/api/v1/*)
│   ├── auth/                 # AWS SigV4 authentication & CORS middleware
│   ├── config/               # Configuration file parser
│   ├── encryption/           # AES-256-GCM server-side encryption provider
│   ├── health/               # Database and disk health probes
│   ├── integrity/            # Checksum (CRC32C, MD5) and bit-rot scanner
│   ├── jobs/                 # Background job scheduler (GC, integrity)
│   ├── metadata/             # SQLite metadata repository & schema migrations
│   ├── metrics/              # Prometheus exporter & metrics middleware
│   ├── replication/          # Multi-node outbox replication & conflict handler
│   ├── s3/                   # S3 XML API handlers, router, and error responses
│   └── storage/              # 2-level hashed filesystem object engine
├── scripts/                  # Hot backup and verified disaster restore scripts
├── tests/                    # End-to-end integration tests
├── web/                      # React 19 + TypeScript admin console (embedded)
├── config.yaml               # Local development configuration
├── go.mod                    # Go module definition
└── go.sum                    # Dependency checksums
```

---

## Troubleshooting

### 1. Port 9000 or 9001 Already in Use
If another service (such as MinIO, Portainer, or another web service) is using port 9000 or 9001, update the ports in `config.yaml`:
```yaml
server:
  s3_port: 9100
  admin_port: 9101
```

### 2. "SignatureDoesNotMatch" on AWS CLI or SDK
- Ensure your client's access key and secret key match the credentials in `config.yaml` (`storageadmin` / `storageadminsecretkey123`).
- Ensure client time is synchronized with the server (SigV4 enforces timestamp validity).
- For SDKs like AWS SDK v3, make sure `forcePathStyle: true` is configured.

### 3. Permission Denied on Storage Directory
Ensure the user running `storage-server` has read/write permissions to the `storage.root` and `metadata.path` directories specified in `config.yaml`:
```bash
chmod -R u+rwX ./data
```

### 4. Admin API Returns 401 Unauthorized
Include the administrative token via header:
```bash
curl -H "X-Admin-Token: storageadminsecretkey123" http://localhost:9001/api/v1/status
```
Or with Bearer authentication:
```bash
curl -H "Authorization: Bearer storageadminsecretkey123" http://localhost:9001/api/v1/status
```

---

## License

This project is licensed under the MIT License.
