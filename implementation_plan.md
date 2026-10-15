# Storage Engine Implementation Plan

This document outlines the detailed step-by-step implementation plan for the self-managed S3-compatible storage engine, aligned with the architectural vision.

---

## Pre-requisites & Setup ✅
- [x] **Initialize Go Module**: Set up `go.mod` (`github.com/my-s3-bucket/storage-engine`).
- [x] **Dependencies**: Added robust dependencies:
  - `modernc.org/sqlite` for CGO-free high-performance SQLite.
  - Standard `net/http` with Go 1.22+ pattern routing.
  - Configuration parsing using `gopkg.in/yaml.v3`.
  - Structured logging with `log/slog`.
  - `github.com/spf13/cobra` for the CLI.
  - `github.com/google/uuid` for IDs.
- [x] **Repository Structure**: Full tree implemented across `cmd/`, `internal/`, `pkg/`, `deploy/`, `web/`, and `tests/`.

---

## Milestone 1: Single-Node Core (The S3 Foundation) ✅
**Goal**: Basic functionality for bucket and object operations.
- [x] **Configuration & Logging**: Initialized `slog`, YAML configuration parsing (`internal/config/config.go`).
- [x] **Metadata DB Layer (SQLite)**:
  - Migrations for `users`, `access_keys`, `buckets`, `objects`, `multipart_uploads`, `multipart_parts`, `storage_nodes`, `replication_outbox`, `replication_conflicts`, `audit_logs` (`internal/metadata/sqlite.go`).
  - Implemented repository pattern in `internal/metadata/store.go` and `internal/metadata/sqlite.go`.
- [x] **Filesystem Storage Engine**:
  - Built `LocalDiskEngine` adhering to `storage.Engine` interface (`internal/storage/local.go`).
  - Hashed 2-level directory translation: `objects/aa/bb/aabb...data`.
- [x] **S3 HTTP Router**:
  - Mapped routes: `GET /`, `PUT /{bucket}`, `GET /{bucket}`, `HEAD /{bucket}`, `DELETE /{bucket}`, `PUT /{bucket}/{key}`, `GET /{bucket}/{key}`, `HEAD /{bucket}/{key}`, `DELETE /{bucket}/{key}`.
  - S3 XML error responses: `<Error><Code>NoSuchKey</Code>...` (`internal/s3/xml.go`).
- [x] **Basic Bucket/Object Operations**:
  - Bucket creation & deletion.
  - Object `PUT`, `GET`, `HEAD`, `DELETE`.
  - ListObjectsV2 (`GET /{bucket}?list-type=2`) with keyset pagination, prefix, and delimiter support.
- [x] **Testing**: Comprehensive unit and integration test suite (`internal/metadata/sqlite_test.go`, `internal/storage/local_test.go`, `internal/s3/handler_test.go`).

---

## Milestone 2: Production Data Handling ✅
**Goal**: Make the system reliable for real-world file sizes and concurrent workloads.
- [x] **Streaming Uploads/Downloads**:
  - Replaced in-memory buffers with streaming `io.Copy` between HTTP request body and physical disk with temp-file atomic rename.
  - Range request streaming (`Accept-Ranges: bytes`, `Range: bytes=start-end`) for partial content download.
- [x] **Checksums & Data Integrity**:
  - Castagnoli `crc32c`, `sha256`, and `md5` computed simultaneously via streaming `integrity.HashingReader` (`internal/integrity/checksum.go`).
  - Checksums stored in metadata and validated on responses (`x-amz-checksum-crc32c`, `ETag`).
- [x] **Multipart Upload Support**:
  - DB Schema: `multipart_uploads` and `multipart_parts`.
  - APIs: `CreateMultipartUpload` (`POST /{bucket}/{key}?uploads`), `UploadPart` (`PUT ?uploadId=...&partNumber=...`), `ListParts` (`GET ?uploadId=...`).
  - Completion: `CompleteMultipartUpload` (`POST ?uploadId=...`) merges physical parts into final object.
  - Cleanup: `AbortMultipartUpload` (`DELETE ?uploadId=...`) cleans up chunks on disk and metadata.
- [x] **Concurrency**:
  - Goroutine-safe SQLite connection pool with WAL mode (`PRAGMA journal_mode=WAL`) and `busy_timeout`.
  - Fine-grained per-object locking (`LocalDiskEngine.locks`) preventing concurrent overwrite corruption.

---

## Milestone 3: Security & Advanced Features ✅
**Goal**: AWS Signature V4, access controls, and object retention.
- [x] **Authentication (SigV4)**:
  - Header and query parameter parser (`AWS4-HMAC-SHA256`, presigned URLs).
  - Canonical request reconstructor, string-to-sign builder, and HMAC signing key derivation (`internal/auth/sigv4.go`).
  - Authentication middleware (`internal/auth/middleware.go`) with CORS support.
- [x] **Authorization / IAM**:
  - User and access key management in SQLite.
  - Status check (active vs revoked access keys).
- [x] **Versioning & Object Locking**:
  - Bucket versioning (`ENABLED`, `SUSPENDED`, `DISABLED`).
  - Version-aware queries, delete markers for soft deletes, permanent version deletes when `versionId` specified (`internal/metadata/sqlite.go`).
  - Object retention locking checks (`x-amz-object-lock-retain-until-date`).
- [x] **Server-Side Encryption**:
  - `encryption.Provider` interface (`internal/encryption/provider.go`).
  - Transparent AES-256-GCM chunked streaming encryption provider (`AESGCMProvider`).

---

## Milestone 4: Operations & System Management ✅
**Goal**: Ready the system for deployment, background maintenance, and observability.
- [x] **CLI (`storagectl`)**:
  - Built with `spf13/cobra` (`cmd/storagectl/main.go`).
  - Commands: `status`, `bucket list/create/delete`, `user list/create`, `access-key list/create/delete`, `gc run`, `integrity scan`, `replication nodes/conflicts`.
- [x] **Background Jobs Engine**:
  - Background job manager with scheduled & on-demand execution (`internal/jobs/manager.go`).
  - GC worker cleaning orphaned multipart chunk folders and stale temporary files.
  - Integrity Scanner (`internal/integrity/scanner.go`) validating disk objects against CRC32C metadata.
- [x] **Metrics & Health**:
  - Prometheus metrics exporter at `/metrics` (`internal/metrics/metrics.go`).
  - Health check endpoint at `/health` (`internal/health/health.go`) verifying DB connectivity and disk writable status.
- [x] **Packaging**:
  - Automated installer: `deploy/install.sh`.
  - Systemd unit template: `deploy/systemd/storage-server.service`.
  - Production config example: `deploy/config.example.yaml`.

---

## Milestone 5: Distributed Capabilities ✅
**Goal**: Server-to-server asynchronous replication.
- [x] **Node Registration**:
  - Schema for `storage_nodes`.
  - API endpoints `/internal/nodes/register` and node management.
- [x] **Replication Outbox Pattern**:
  - `replication_outbox` table in SQLite.
  - S3 operations transactionally insert replication events on commit.
- [x] **Replication Worker**:
  - Background polling worker (`internal/replication/worker.go`).
  - Streams object data to peer nodes via `/internal/replication/sync`.
  - Retries on failure with exponential backoff.
- [x] **Conflict Resolution**:
  - Last-Write-Wins (LWW) timestamp evaluation with audit logging in `replication_conflicts` (`internal/replication/handler.go`).

---

## Milestone 6: Management UI ✅
**Goal**: A polished operational dashboard.
- [x] **Management API**:
  - Dedicated REST JSON endpoints under `/api/v1/` (`internal/api/api.go`).
- [x] **React/TypeScript SPA**:
  - Modern dark-mode glassmorphic dashboard in `web/` using React, TypeScript, and Lucide icons.
  - Views: Overview & Storage Gauge, Bucket & Object Explorer, IAM & Access Keys, Cluster Replication & Conflict Logs, Maintenance & Job Runners.
  - Production bundle compiled to `web/dist` and served directly from the Go binary using `embed.FS` (`web/embed.go`).

---

## End-to-End Verification
- **Test Suite**: 100% PASS across all packages (`go test -v ./...` and `tests/e2e_test.go`).
- **Binary Binaries**: Both `storage-server` and `storagectl` build and run cleanly.
