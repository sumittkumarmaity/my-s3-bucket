package metadata

import (
	"context"
	"errors"
	"time"
)

var (
	ErrBucketNotFound      = errors.New("bucket not found")
	ErrBucketAlreadyExists  = errors.New("bucket already exists")
	ErrBucketNotEmpty       = errors.New("bucket not empty")
	ErrObjectNotFound      = errors.New("object not found")
	ErrUserNotFound        = errors.New("user not found")
	ErrUserAlreadyExists   = errors.New("user already exists")
	ErrAccessKeyNotFound   = errors.New("access key not found")
	ErrUploadNotFound      = errors.New("multipart upload not found")
	ErrObjectLocked        = errors.New("object is retention locked")
)

type Bucket struct {
	ID                     string    `json:"id"`
	Name                   string    `json:"name"`
	OwnerID                string    `json:"owner_id"`
	CreatedAt              time.Time `json:"created_at"`
	VersioningStatus       string    `json:"versioning_status"` // DISABLED, ENABLED, SUSPENDED
	RetentionMode          string    `json:"retention_mode"`    // GOVERNANCE, COMPLIANCE, ""
	RetentionPeriodSeconds int64     `json:"retention_period_seconds"`
}

type Object struct {
	ID             string     `json:"id"`
	BucketID       string     `json:"bucket_id"`
	ObjectKey      string     `json:"object_key"`
	VersionID      string     `json:"version_id"`
	Size           int64      `json:"size"`
	ETag           string     `json:"etag"`
	ChecksumCRC32C string     `json:"checksum_crc32c"`
	ChecksumSHA256 string     `json:"checksum_sha256"`
	ContentType    string     `json:"content_type"`
	IsDeleteMarker bool       `json:"is_delete_marker"`
	RetentionUntil *time.Time `json:"retention_until,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

type MultipartUpload struct {
	UploadID    string    `json:"upload_id"`
	BucketID    string    `json:"bucket_id"`
	ObjectKey   string    `json:"object_key"`
	ContentType string    `json:"content_type"`
	CreatedAt   time.Time `json:"created_at"`
}

type MultipartPart struct {
	UploadID     string    `json:"upload_id"`
	PartNumber   int       `json:"part_number"`
	ETag         string    `json:"etag"`
	Size         int64     `json:"size"`
	Checksum     string    `json:"checksum"`
	PhysicalPath string    `json:"physical_path"`
	CreatedAt    time.Time `json:"created_at"`
}

type User struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
}

type AccessKey struct {
	AccessKeyID   string    `json:"access_key_id"`
	SecretKeyHash string    `json:"-"` // never expose raw or hash in JSON
	UserID        string    `json:"user_id"`
	Status        string    `json:"status"` // ACTIVE, REVOKED
	CreatedAt     time.Time `json:"created_at"`
}

type StorageNode struct {
	ID            string    `json:"id"`
	Address       string    `json:"address"`
	AuthToken     string    `json:"auth_token"`
	Status        string    `json:"status"` // ONLINE, OFFLINE
	LastHeartbeat time.Time `json:"last_heartbeat"`
	CreatedAt     time.Time `json:"created_at"`
}

type ReplicationJob struct {
	ID         int64     `json:"id"`
	ObjectID   string    `json:"object_id"`
	BucketID   string    `json:"bucket_id"`
	ObjectKey  string    `json:"object_key"`
	VersionID  string    `json:"version_id"`
	Action     string    `json:"action"` // PUT, DELETE
	Status     string    `json:"status"` // PENDING, PROCESSING, COMPLETED, FAILED
	RetryCount int       `json:"retry_count"`
	LastError  string    `json:"last_error"`
	CreatedAt  time.Time `json:"created_at"`
}

type ReplicationConflict struct {
	ID              int64     `json:"id"`
	BucketName      string    `json:"bucket_name"`
	ObjectKey       string    `json:"object_key"`
	RemoteVersionID string    `json:"remote_version_id"`
	LocalVersionID  string    `json:"local_version_id"`
	Resolution      string    `json:"resolution"` // LOCAL_KEPT, REMOTE_APPLIED
	DetectedAt      time.Time `json:"detected_at"`
}

type AuditLog struct {
	ID        int64     `json:"id"`
	Action    string    `json:"action"`
	Resource  string    `json:"resource"`
	UserID    string    `json:"user_id"`
	IPAddress string    `json:"ip_address"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}

// Store defines the transactional metadata operations needed by the engine.
type Store interface {
	// Buckets
	CreateBucket(ctx context.Context, bucket Bucket) error
	GetBucket(ctx context.Context, name string) (Bucket, error)
	GetBucketByID(ctx context.Context, id string) (Bucket, error)
	ListBuckets(ctx context.Context) ([]Bucket, error)
	ListBucketsByOwner(ctx context.Context, ownerID string) ([]Bucket, error)
	DeleteBucket(ctx context.Context, name string) error
	SetBucketVersioning(ctx context.Context, name, status string) error

	// Objects
	PutObject(ctx context.Context, obj Object) error
	GetObject(ctx context.Context, bucketID, key string, versionID string) (Object, error)
	DeleteObject(ctx context.Context, bucketID, key string, versionID string) (Object, error)
	HardDeleteObject(ctx context.Context, objectID string) error
	ListObjects(ctx context.Context, bucketID string, prefix string, marker string, limit int) ([]Object, error)
	ListObjectVersions(ctx context.Context, bucketID string, prefix string, keyMarker string, versionIdMarker string, limit int) ([]Object, error)
	CountObjects(ctx context.Context) (int64, error)
	CountBuckets(ctx context.Context) (int64, error)
	GetAllObjects(ctx context.Context) ([]Object, error)

	// Multipart Uploads
	CreateMultipartUpload(ctx context.Context, mp MultipartUpload) error
	GetMultipartUpload(ctx context.Context, uploadID string) (MultipartUpload, error)
	AddMultipartPart(ctx context.Context, part MultipartPart) error
	ListMultipartParts(ctx context.Context, uploadID string) ([]MultipartPart, error)
	DeleteMultipartUpload(ctx context.Context, uploadID string) error

	// Users & Keys
	CreateUser(ctx context.Context, user User) error
	GetUser(ctx context.Context, id string) (User, error)
	GetUserByUsername(ctx context.Context, username string) (User, error)
	ListUsers(ctx context.Context) ([]User, error)
	CreateAccessKey(ctx context.Context, key AccessKey) error
	GetAccessKey(ctx context.Context, accessKeyID string) (AccessKey, error)
	ListAccessKeys(ctx context.Context, userID string) ([]AccessKey, error)
	DeleteAccessKey(ctx context.Context, accessKeyID string) error

	// Storage Nodes
	RegisterNode(ctx context.Context, node StorageNode) error
	GetNode(ctx context.Context, id string) (StorageNode, error)
	ListNodes(ctx context.Context) ([]StorageNode, error)
	UpdateNodeHeartbeat(ctx context.Context, id string) error
	DeleteNode(ctx context.Context, id string) error

	// Replication Outbox & Conflicts
	EnqueueReplicationJob(ctx context.Context, job ReplicationJob) error
	FetchPendingReplicationJobs(ctx context.Context, limit int) ([]ReplicationJob, error)
	UpdateReplicationJobStatus(ctx context.Context, id int64, status string, errMsg string) error
	GetPeerReplicationStatus(ctx context.Context, outboxID int64, peerID string) (status string, retryCount int, err error)
	UpdatePeerReplicationStatus(ctx context.Context, outboxID int64, peerID string, status string, errMsg string) error
	RecordReplicationConflict(ctx context.Context, conflict ReplicationConflict) error
	ListReplicationConflicts(ctx context.Context) ([]ReplicationConflict, error)

	// Audit Logs
	RecordAuditLog(ctx context.Context, log AuditLog) error
	ListAuditLogs(ctx context.Context, limit int) ([]AuditLog, error)

	// Health / DB
	Ping(ctx context.Context) error
	Close() error
	Backup(ctx context.Context, destPath string) error
}

