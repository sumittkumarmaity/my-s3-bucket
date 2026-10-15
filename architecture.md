# Architecture Document: Storage Engine

## 1. Architectural Risks
* **SQLite Concurrency**: SQLite handles concurrent reads well (with WAL mode), but writes are serialized. For a high-throughput storage engine, metadata updates might bottleneck on SQLite writes.
* **Filesystem Constraints**: Storing millions of objects in a single directory can cause performance issues (inode exhaustion or slow directory listing). We need a hashed directory structure (e.g., `objects/00/a1/00a1...`).
* **Memory Management**: Streaming large files requires careful buffer management to avoid GC pressure and OOM errors, especially with hundreds of concurrent connections.
* **Crash Recovery**: Interruptions during multipart upload completion or multipart cleanup could lead to orphaned files or inconsistent metadata.
* **Replication Conflicts**: Asynchronous replication without distributed consensus (like Raft) can lead to split-brain scenarios if nodes are partitioned and receive conflicting updates.

## 2. Missing Requirements
* **Pagination (Marker/ContinuationToken)**: S3's `ListObjectsV2` needs robust pagination. SQLite offset-based pagination can be slow. Keyset pagination is required.
* **CORS (Cross-Origin Resource Sharing)**: Web clients directly uploading to the API (e.g., via presigned URLs) will require CORS configuration support.
* **Presigned URLs**: Often required by applications for temporary access to objects without embedding AWS credentials.
* **Garbage Collection Strategy**: When are physical files deleted? Synchronously on `DELETE`, or asynchronously via a GC worker? (Asynchronous is safer for crash recovery).

## 3. Internal Interfaces
```go
package storage

type StorageEngine interface {
    Put(ctx context.Context, id string, r io.Reader) (bytesWritten int64, err error)
    Get(ctx context.Context, id string) (io.ReadCloser, error)
    Delete(ctx context.Context, id string) error
    DiskUsage(ctx context.Context) (DiskUsage, error)
}

package metadata

type MetadataStore interface {
    CreateBucket(ctx context.Context, bucket Bucket) error
    GetBucket(ctx context.Context, name string) (Bucket, error)
    PutObject(ctx context.Context, obj Object) error
    GetObject(ctx context.Context, bucketID, key string) (Object, error)
    DeleteObject(ctx context.Context, bucketID, key string) error
    ListObjects(ctx context.Context, bucketID string, prefix string, marker string, limit int) ([]Object, error)
    // Transactional boundaries for multipart and replication
}
```

## 4. Metadata Schema
```sql
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL
);

CREATE TABLE access_keys (
    access_key_id TEXT PRIMARY KEY,
    secret_key_hash TEXT NOT NULL,
    user_id TEXT REFERENCES users(id),
    status TEXT NOT NULL -- ACTIVE, REVOKED
);

CREATE TABLE buckets (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    versioning_status TEXT DEFAULT 'DISABLED'
);

CREATE TABLE objects (
    id TEXT PRIMARY KEY,
    bucket_id TEXT REFERENCES buckets(id),
    object_key TEXT NOT NULL,
    version_id TEXT,
    size INTEGER NOT NULL,
    etag TEXT NOT NULL,
    checksum_crc32c TEXT,
    content_type TEXT,
    is_delete_marker BOOLEAN DEFAULT FALSE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(bucket_id, object_key, version_id)
);

CREATE TABLE multipart_uploads (
    upload_id TEXT PRIMARY KEY,
    bucket_id TEXT REFERENCES buckets(id),
    object_key TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE multipart_parts (
    upload_id TEXT REFERENCES multipart_uploads(upload_id),
    part_number INTEGER NOT NULL,
    etag TEXT NOT NULL,
    size INTEGER NOT NULL,
    physical_path TEXT NOT NULL,
    PRIMARY KEY(upload_id, part_number)
);
```

## 5. Filesystem Layout
Using a 2-level hashed directory structure based on the physical Object ID (UUID) to prevent directory size limits.
```text
/var/lib/storage-engine/
├── objects/
│   ├── 00/
│   │   ├── 00/
│   │   │   └── 00003a2b-1234-4321-abcd-1234567890ab.data
│   │   └── 01/
│   └── ff/
├── multipart/
│   └── <upload_id>/
│       ├── part_1.data
│       └── part_2.data
├── metadata/
│   └── storage.db
└── journal/
```

## 6. S3 API Compatibility Scope
- **Auth**: AWS Signature V4.
- **Buckets**: `CreateBucket`, `DeleteBucket`, `HeadBucket`, `ListBuckets`.
- **Objects**: `PutObject`, `GetObject`, `DeleteObject`, `HeadObject`, `ListObjectsV2`.
- **Multipart**: `CreateMultipartUpload`, `UploadPart`, `CompleteMultipartUpload`, `AbortMultipartUpload`, `ListParts`.
- **Headers**: `Content-Type`, `Content-Length`, `ETag`, `x-amz-meta-*`, `Range`.

## 7. Replication Model
- **Topology**: Hub-and-spoke or Peer-to-Peer asynchronous replication.
- **Queueing**: Outbox pattern in SQLite. Operations insert replication events transactionally alongside metadata updates.
- **Workers**: Background workers poll the outbox, push data to remote nodes using an internal authenticated RPC/HTTP API, verify remote checksums, and mark events as replicated.
- **Conflict Resolution**: Last-Write-Wins based on metadata update timestamp, with a conflict log for administrative review.

## 8. Security Model
- **Authentication**: SigV4 for API clients, internal token/SigV4 for Node-to-Node replication.
- **At Rest**: Transparent optional AES-256-GCM encryption for object data before writing to disk.
- **In Transit**: Enforced TLS.
- **Credentials**: Passwords and Secret Keys are hashed (e.g., Argon2 or bcrypt) in the DB.

## 9. Implementation Roadmap
- **Milestone 1**: Core Go Project Setup, SQLite initialization, S3 HTTP router, basic Auth middleware, Put/Get/Delete object logic.
- **Milestone 2**: Streaming implementations, large file handling (memory bound I/O), Multipart upload API.
- **Milestone 3**: SigV4 authentication engine, IAM policies (basic), Object locking/versioning schema support.
- **Milestone 4**: CLI (`storagectl`), background jobs (GC, Integrity), systemd deployment scripts.
- **Milestone 5**: Node registration API, SQLite outbox for replication, Replication Worker, Conflict logs.
- **Milestone 6**: React SPA Web UI connecting to the Admin API.
