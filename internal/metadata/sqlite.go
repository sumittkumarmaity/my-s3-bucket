package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite db: %w", err)
	}

	// Optimize connection pooling for WAL mode
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(time.Hour)

	store := &SQLiteStore{db: db}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return store, nil
}

func (s *SQLiteStore) migrate() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT UNIQUE NOT NULL,
			created_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS access_keys (
			access_key_id TEXT PRIMARY KEY,
			secret_key_hash TEXT NOT NULL,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			status TEXT NOT NULL,
			created_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS buckets (
			id TEXT PRIMARY KEY,
			name TEXT UNIQUE NOT NULL,
			owner_id TEXT NOT NULL DEFAULT 'admin-user-01',
			versioning_status TEXT NOT NULL DEFAULT 'DISABLED',
			retention_mode TEXT NOT NULL DEFAULT '',
			retention_period_seconds INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS objects (
			id TEXT PRIMARY KEY,
			bucket_id TEXT NOT NULL REFERENCES buckets(id) ON DELETE CASCADE,
			object_key TEXT NOT NULL,
			version_id TEXT NOT NULL,
			size INTEGER NOT NULL,
			etag TEXT NOT NULL,
			checksum_crc32c TEXT NOT NULL,
			checksum_sha256 TEXT NOT NULL,
			content_type TEXT NOT NULL,
			is_delete_marker BOOLEAN NOT NULL DEFAULT 0,
			retention_until DATETIME,
			created_at DATETIME NOT NULL,
			UNIQUE(bucket_id, object_key, version_id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_objects_lookup 
			ON objects(bucket_id, object_key, created_at DESC);`,
		`CREATE TABLE IF NOT EXISTS multipart_uploads (
			upload_id TEXT PRIMARY KEY,
			bucket_id TEXT NOT NULL REFERENCES buckets(id) ON DELETE CASCADE,
			object_key TEXT NOT NULL,
			content_type TEXT NOT NULL,
			created_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS multipart_parts (
			upload_id TEXT NOT NULL REFERENCES multipart_uploads(upload_id) ON DELETE CASCADE,
			part_number INTEGER NOT NULL,
			etag TEXT NOT NULL,
			size INTEGER NOT NULL,
			checksum TEXT NOT NULL,
			physical_path TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			PRIMARY KEY(upload_id, part_number)
		);`,
		`CREATE TABLE IF NOT EXISTS storage_nodes (
			id TEXT PRIMARY KEY,
			address TEXT NOT NULL,
			auth_token TEXT NOT NULL,
			status TEXT NOT NULL,
			last_heartbeat DATETIME NOT NULL,
			created_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS replication_outbox (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			object_id TEXT NOT NULL,
			bucket_id TEXT NOT NULL,
			object_key TEXT NOT NULL,
			version_id TEXT NOT NULL,
			action TEXT NOT NULL,
			status TEXT NOT NULL,
			retry_count INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_outbox_status 
			ON replication_outbox(status, created_at ASC);`,
		`CREATE TABLE IF NOT EXISTS replication_conflicts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			bucket_name TEXT NOT NULL,
			object_key TEXT NOT NULL,
			remote_version_id TEXT NOT NULL,
			local_version_id TEXT NOT NULL,
			resolution TEXT NOT NULL,
			detected_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			action TEXT NOT NULL,
			resource TEXT NOT NULL,
			user_id TEXT NOT NULL,
			ip_address TEXT NOT NULL,
			status TEXT NOT NULL,
			timestamp DATETIME NOT NULL
		);`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("error executing %s: %w", q, err)
		}
	}

	// Safe backward-compatible migration for existing databases
	_, _ = s.db.Exec(`ALTER TABLE buckets ADD COLUMN owner_id TEXT NOT NULL DEFAULT 'admin-user-01';`)
	_, _ = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_buckets_owner ON buckets(owner_id);`)

	return nil
}

// Ping verifies database connectivity.
func (s *SQLiteStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// -------------------------------------------------------------
// Bucket Operations
// -------------------------------------------------------------

func (s *SQLiteStore) CreateBucket(ctx context.Context, bucket Bucket) error {

	if bucket.ID == "" {
		bucket.ID = uuid.NewString()
	}
	if bucket.OwnerID == "" {
		bucket.OwnerID = "admin-user-01"
	}
	if bucket.CreatedAt.IsZero() {
		bucket.CreatedAt = time.Now().UTC()
	}
	if bucket.VersioningStatus == "" {
		bucket.VersioningStatus = "DISABLED"
	}

	query := `INSERT INTO buckets (id, name, owner_id, versioning_status, retention_mode, retention_period_seconds, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, query,
		bucket.ID,
		bucket.Name,
		bucket.OwnerID,
		bucket.VersioningStatus,
		bucket.RetentionMode,
		bucket.RetentionPeriodSeconds,
		bucket.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		if isConstraintViolation(err) {
			return ErrBucketAlreadyExists
		}
		return err
	}
	return nil
}

func (s *SQLiteStore) GetBucket(ctx context.Context, name string) (Bucket, error) {

	query := `SELECT id, name, owner_id, versioning_status, retention_mode, retention_period_seconds, created_at 
		FROM buckets WHERE name = ?`
	row := s.db.QueryRowContext(ctx, query, name)

	var b Bucket
	var createdAtStr string
	err := row.Scan(&b.ID, &b.Name, &b.OwnerID, &b.VersioningStatus, &b.RetentionMode, &b.RetentionPeriodSeconds, &createdAtStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Bucket{}, ErrBucketNotFound
		}
		return Bucket{}, err
	}
	b.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAtStr)
	return b, nil
}

func (s *SQLiteStore) GetBucketByID(ctx context.Context, id string) (Bucket, error) {

	query := `SELECT id, name, owner_id, versioning_status, retention_mode, retention_period_seconds, created_at 
		FROM buckets WHERE id = ?`
	row := s.db.QueryRowContext(ctx, query, id)

	var b Bucket
	var createdAtStr string
	err := row.Scan(&b.ID, &b.Name, &b.OwnerID, &b.VersioningStatus, &b.RetentionMode, &b.RetentionPeriodSeconds, &createdAtStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Bucket{}, ErrBucketNotFound
		}
		return Bucket{}, err
	}
	b.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAtStr)
	return b, nil
}

func (s *SQLiteStore) ListBuckets(ctx context.Context) ([]Bucket, error) {

	query := `SELECT id, name, owner_id, versioning_status, retention_mode, retention_period_seconds, created_at 
		FROM buckets ORDER BY name ASC`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var buckets []Bucket
	for rows.Next() {
		var b Bucket
		var createdAtStr string
		if err := rows.Scan(&b.ID, &b.Name, &b.OwnerID, &b.VersioningStatus, &b.RetentionMode, &b.RetentionPeriodSeconds, &createdAtStr); err != nil {
			return nil, err
		}
		b.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAtStr)
		buckets = append(buckets, b)
	}
	return buckets, nil
}

func (s *SQLiteStore) ListBucketsByOwner(ctx context.Context, ownerID string) ([]Bucket, error) {

	query := `SELECT id, name, owner_id, versioning_status, retention_mode, retention_period_seconds, created_at 
		FROM buckets WHERE owner_id = ? ORDER BY name ASC`
	rows, err := s.db.QueryContext(ctx, query, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var buckets []Bucket
	for rows.Next() {
		var b Bucket
		var createdAtStr string
		if err := rows.Scan(&b.ID, &b.Name, &b.OwnerID, &b.VersioningStatus, &b.RetentionMode, &b.RetentionPeriodSeconds, &createdAtStr); err != nil {
			return nil, err
		}
		b.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAtStr)
		buckets = append(buckets, b)
	}
	return buckets, nil
}

func (s *SQLiteStore) DeleteBucket(ctx context.Context, name string) error {

	b, err := s.GetBucket(ctx, name)
	if err != nil {
		return err
	}

	// Check if bucket has any objects
	var count int
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM objects WHERE bucket_id = ?`, b.ID).Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return ErrBucketNotEmpty
	}

	res, err := s.db.ExecContext(ctx, `DELETE FROM buckets WHERE id = ?`, b.ID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrBucketNotFound
	}
	return nil
}

func (s *SQLiteStore) SetBucketVersioning(ctx context.Context, name, status string) error {

	res, err := s.db.ExecContext(ctx, `UPDATE buckets SET versioning_status = ? WHERE name = ?`, status, name)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrBucketNotFound
	}
	return nil
}

func (s *SQLiteStore) CountBuckets(ctx context.Context) (int64, error) {

	var count int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM buckets`).Scan(&count)
	return count, err
}

// -------------------------------------------------------------
// Object Operations
// -------------------------------------------------------------

func (s *SQLiteStore) PutObject(ctx context.Context, obj Object) error {

	if obj.ID == "" {
		obj.ID = uuid.NewString()
	}
	if obj.CreatedAt.IsZero() {
		obj.CreatedAt = time.Now().UTC()
	}
	if obj.VersionID == "" {
		obj.VersionID = "null"
	}

	var retentionStr *string
	if obj.RetentionUntil != nil {
		formatted := obj.RetentionUntil.Format(time.RFC3339Nano)
		retentionStr = &formatted
	}

	// If version is null (unversioned bucket), replace or upsert
	query := `INSERT INTO objects (
		id, bucket_id, object_key, version_id, size, etag, checksum_crc32c, 
		checksum_sha256, content_type, is_delete_marker, retention_until, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(bucket_id, object_key, version_id) DO UPDATE SET
		id = excluded.id,
		size = excluded.size,
		etag = excluded.etag,
		checksum_crc32c = excluded.checksum_crc32c,
		checksum_sha256 = excluded.checksum_sha256,
		content_type = excluded.content_type,
		is_delete_marker = excluded.is_delete_marker,
		retention_until = excluded.retention_until,
		created_at = excluded.created_at;`

	_, err := s.db.ExecContext(ctx, query,
		obj.ID,
		obj.BucketID,
		obj.ObjectKey,
		obj.VersionID,
		obj.Size,
		obj.ETag,
		obj.ChecksumCRC32C,
		obj.ChecksumSHA256,
		obj.ContentType,
		obj.IsDeleteMarker,
		retentionStr,
		obj.CreatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLiteStore) GetObject(ctx context.Context, bucketID, key string, versionID string) (Object, error) {

	var query string
	var args []any

	if versionID != "" {
		query = `SELECT id, bucket_id, object_key, version_id, size, etag, checksum_crc32c, checksum_sha256,
				content_type, is_delete_marker, retention_until, created_at
			FROM objects 
			WHERE bucket_id = ? AND object_key = ? AND version_id = ?`
		args = []any{bucketID, key, versionID}
	} else {
		// Latest version
		query = `SELECT id, bucket_id, object_key, version_id, size, etag, checksum_crc32c, checksum_sha256,
				content_type, is_delete_marker, retention_until, created_at
			FROM objects 
			WHERE bucket_id = ? AND object_key = ?
			ORDER BY created_at DESC LIMIT 1`
		args = []any{bucketID, key}
	}

	row := s.db.QueryRowContext(ctx, query, args...)

	var obj Object
	var retStr sql.NullString
	var createdStr string
	err := row.Scan(
		&obj.ID,
		&obj.BucketID,
		&obj.ObjectKey,
		&obj.VersionID,
		&obj.Size,
		&obj.ETag,
		&obj.ChecksumCRC32C,
		&obj.ChecksumSHA256,
		&obj.ContentType,
		&obj.IsDeleteMarker,
		&retStr,
		&createdStr,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Object{}, ErrObjectNotFound
		}
		return Object{}, err
	}

	obj.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
	if retStr.Valid {
		t, err := time.Parse(time.RFC3339Nano, retStr.String)
		if err == nil {
			obj.RetentionUntil = &t
		}
	}

	// If latest object is a delete marker and no specific version was requested
	if versionID == "" && obj.IsDeleteMarker {
		return obj, ErrObjectNotFound
	}

	return obj, nil
}

func (s *SQLiteStore) DeleteObject(ctx context.Context, bucketID, key string, versionID string) (Object, error) {

	// 1. Fetch bucket to check versioning status
	var versioningStatus, retentionMode string
	var retentionPeriod int64
	err := s.db.QueryRowContext(ctx,
		`SELECT versioning_status, retention_mode, retention_period_seconds FROM buckets WHERE id = ?`,
		bucketID,
	).Scan(&versioningStatus, &retentionMode, &retentionPeriod)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Object{}, ErrBucketNotFound
		}
		return Object{}, err
	}

	// 2. Fetch the target object
	var target Object
	var retStr sql.NullString
	var createdStr string

	if versionID != "" {
		err = s.db.QueryRowContext(ctx,
			`SELECT id, bucket_id, object_key, version_id, size, etag, checksum_crc32c, checksum_sha256,
				content_type, is_delete_marker, retention_until, created_at
			FROM objects WHERE bucket_id = ? AND object_key = ? AND version_id = ?`,
			bucketID, key, versionID,
		).Scan(
			&target.ID, &target.BucketID, &target.ObjectKey, &target.VersionID,
			&target.Size, &target.ETag, &target.ChecksumCRC32C, &target.ChecksumSHA256,
			&target.ContentType, &target.IsDeleteMarker, &retStr, &createdStr,
		)
	} else {
		err = s.db.QueryRowContext(ctx,
			`SELECT id, bucket_id, object_key, version_id, size, etag, checksum_crc32c, checksum_sha256,
				content_type, is_delete_marker, retention_until, created_at
			FROM objects WHERE bucket_id = ? AND object_key = ?
			ORDER BY created_at DESC LIMIT 1`,
			bucketID, key,
		).Scan(
			&target.ID, &target.BucketID, &target.ObjectKey, &target.VersionID,
			&target.Size, &target.ETag, &target.ChecksumCRC32C, &target.ChecksumSHA256,
			&target.ContentType, &target.IsDeleteMarker, &retStr, &createdStr,
		)
	}

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Object{}, ErrObjectNotFound
		}
		return Object{}, err
	}

	target.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
	if retStr.Valid {
		t, err := time.Parse(time.RFC3339Nano, retStr.String)
		if err == nil {
			target.RetentionUntil = &t
			// Object locking check
			if target.RetentionUntil.After(time.Now().UTC()) {
				return Object{}, ErrObjectLocked
			}
		}
	}

	// If versionID is specified: hard delete this specific version
	if versionID != "" {
		_, err = s.db.ExecContext(ctx, `DELETE FROM objects WHERE id = ?`, target.ID)
		return target, err
	}

	// If versioning is ENABLED: insert a delete marker
	if versioningStatus == "ENABLED" {
		marker := Object{
			ID:             uuid.NewString(),
			BucketID:       bucketID,
			ObjectKey:      key,
			VersionID:      uuid.NewString(),
			Size:           0,
			ETag:           "",
			ChecksumCRC32C: "",
			ChecksumSHA256: "",
			ContentType:    "",
			IsDeleteMarker: true,
			CreatedAt:      time.Now().UTC(),
		}
		query := `INSERT INTO objects (
			id, bucket_id, object_key, version_id, size, etag, checksum_crc32c,
			checksum_sha256, content_type, is_delete_marker, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		_, err = s.db.ExecContext(ctx, query,
			marker.ID, marker.BucketID, marker.ObjectKey, marker.VersionID,
			0, "", "", "", "", 1, marker.CreatedAt.Format(time.RFC3339Nano),
		)
		return marker, err
	}

	// If versioning is DISABLED or SUSPENDED: hard delete all records for this key
	_, err = s.db.ExecContext(ctx, `DELETE FROM objects WHERE bucket_id = ? AND object_key = ?`, bucketID, key)
	return target, err
}

func (s *SQLiteStore) HardDeleteObject(ctx context.Context, objectID string) error {

	_, err := s.db.ExecContext(ctx, `DELETE FROM objects WHERE id = ?`, objectID)
	return err
}

func (s *SQLiteStore) ListObjects(ctx context.Context, bucketID string, prefix string, marker string, limit int) ([]Object, error) {

	if limit <= 0 || limit > 1000 {
		limit = 1000
	}

	// Query latest version per object_key
	query := `WITH RankedObjects AS (
		SELECT id, bucket_id, object_key, version_id, size, etag, checksum_crc32c,
			checksum_sha256, content_type, is_delete_marker, retention_until, created_at,
			ROW_NUMBER() OVER(PARTITION BY object_key ORDER BY created_at DESC) as rn
		FROM objects
		WHERE bucket_id = ?
		  AND object_key LIKE ? || '%'
		  AND object_key > ?
	)
	SELECT id, bucket_id, object_key, version_id, size, etag, checksum_crc32c,
		checksum_sha256, content_type, is_delete_marker, retention_until, created_at
	FROM RankedObjects
	WHERE rn = 1 AND is_delete_marker = 0
	ORDER BY object_key ASC
	LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, bucketID, prefix, marker, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var objects []Object
	for rows.Next() {
		var obj Object
		var retStr sql.NullString
		var createdStr string

		err := rows.Scan(
			&obj.ID, &obj.BucketID, &obj.ObjectKey, &obj.VersionID,
			&obj.Size, &obj.ETag, &obj.ChecksumCRC32C, &obj.ChecksumSHA256,
			&obj.ContentType, &obj.IsDeleteMarker, &retStr, &createdStr,
		)
		if err != nil {
			return nil, err
		}
		obj.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
		if retStr.Valid {
			t, err := time.Parse(time.RFC3339Nano, retStr.String)
			if err == nil {
				obj.RetentionUntil = &t
			}
		}
		objects = append(objects, obj)
	}

	return objects, nil
}

func (s *SQLiteStore) ListObjectVersions(ctx context.Context, bucketID string, prefix string, keyMarker string, versionIdMarker string, limit int) ([]Object, error) {

	if limit <= 0 || limit > 1000 {
		limit = 1000
	}

	query := `SELECT id, bucket_id, object_key, version_id, size, etag, checksum_crc32c,
			checksum_sha256, content_type, is_delete_marker, retention_until, created_at
		FROM objects
		WHERE bucket_id = ?
		  AND object_key LIKE ? || '%'
		  AND (object_key > ? OR (object_key = ? AND version_id > ?))
		ORDER BY object_key ASC, created_at DESC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, bucketID, prefix, keyMarker, keyMarker, versionIdMarker, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var objects []Object
	for rows.Next() {
		var obj Object
		var retStr sql.NullString
		var createdStr string

		err := rows.Scan(
			&obj.ID, &obj.BucketID, &obj.ObjectKey, &obj.VersionID,
			&obj.Size, &obj.ETag, &obj.ChecksumCRC32C, &obj.ChecksumSHA256,
			&obj.ContentType, &obj.IsDeleteMarker, &retStr, &createdStr,
		)
		if err != nil {
			return nil, err
		}
		obj.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
		if retStr.Valid {
			t, err := time.Parse(time.RFC3339Nano, retStr.String)
			if err == nil {
				obj.RetentionUntil = &t
			}
		}
		objects = append(objects, obj)
	}

	return objects, nil
}

func (s *SQLiteStore) CountObjects(ctx context.Context) (int64, error) {

	var count int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM objects WHERE is_delete_marker = 0`).Scan(&count)
	return count, err
}

func (s *SQLiteStore) GetAllObjects(ctx context.Context) ([]Object, error) {

	query := `SELECT id, bucket_id, object_key, version_id, size, etag, checksum_crc32c,
			checksum_sha256, content_type, is_delete_marker, retention_until, created_at
		FROM objects`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var objects []Object
	for rows.Next() {
		var obj Object
		var retStr sql.NullString
		var createdStr string

		if err := rows.Scan(
			&obj.ID, &obj.BucketID, &obj.ObjectKey, &obj.VersionID,
			&obj.Size, &obj.ETag, &obj.ChecksumCRC32C, &obj.ChecksumSHA256,
			&obj.ContentType, &obj.IsDeleteMarker, &retStr, &createdStr,
		); err != nil {
			return nil, err
		}
		obj.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
		if retStr.Valid {
			t, err := time.Parse(time.RFC3339Nano, retStr.String)
			if err == nil {
				obj.RetentionUntil = &t
			}
		}
		objects = append(objects, obj)
	}

	return objects, nil
}

// -------------------------------------------------------------
// Multipart Upload Operations
// -------------------------------------------------------------

func (s *SQLiteStore) CreateMultipartUpload(ctx context.Context, mp MultipartUpload) error {

	if mp.UploadID == "" {
		mp.UploadID = uuid.NewString()
	}
	if mp.CreatedAt.IsZero() {
		mp.CreatedAt = time.Now().UTC()
	}

	query := `INSERT INTO multipart_uploads (upload_id, bucket_id, object_key, content_type, created_at)
		VALUES (?, ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, query,
		mp.UploadID, mp.BucketID, mp.ObjectKey, mp.ContentType, mp.CreatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLiteStore) GetMultipartUpload(ctx context.Context, uploadID string) (MultipartUpload, error) {

	query := `SELECT upload_id, bucket_id, object_key, content_type, created_at 
		FROM multipart_uploads WHERE upload_id = ?`
	var mp MultipartUpload
	var createdStr string
	err := s.db.QueryRowContext(ctx, query, uploadID).Scan(
		&mp.UploadID, &mp.BucketID, &mp.ObjectKey, &mp.ContentType, &createdStr,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MultipartUpload{}, ErrUploadNotFound
		}
		return MultipartUpload{}, err
	}
	mp.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
	return mp, nil
}

func (s *SQLiteStore) AddMultipartPart(ctx context.Context, part MultipartPart) error {

	if part.CreatedAt.IsZero() {
		part.CreatedAt = time.Now().UTC()
	}

	query := `INSERT INTO multipart_parts (upload_id, part_number, etag, size, checksum, physical_path, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(upload_id, part_number) DO UPDATE SET
			etag = excluded.etag,
			size = excluded.size,
			checksum = excluded.checksum,
			physical_path = excluded.physical_path,
			created_at = excluded.created_at;`

	_, err := s.db.ExecContext(ctx, query,
		part.UploadID, part.PartNumber, part.ETag, part.Size, part.Checksum,
		part.PhysicalPath, part.CreatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLiteStore) ListMultipartParts(ctx context.Context, uploadID string) ([]MultipartPart, error) {

	query := `SELECT upload_id, part_number, etag, size, checksum, physical_path, created_at
		FROM multipart_parts WHERE upload_id = ? ORDER BY part_number ASC`
	rows, err := s.db.QueryContext(ctx, query, uploadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var parts []MultipartPart
	for rows.Next() {
		var p MultipartPart
		var createdStr string
		if err := rows.Scan(&p.UploadID, &p.PartNumber, &p.ETag, &p.Size, &p.Checksum, &p.PhysicalPath, &createdStr); err != nil {
			return nil, err
		}
		p.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
		parts = append(parts, p)
	}
	return parts, nil
}

func (s *SQLiteStore) DeleteMultipartUpload(ctx context.Context, uploadID string) error {

	_, err := s.db.ExecContext(ctx, `DELETE FROM multipart_uploads WHERE upload_id = ?`, uploadID)
	return err
}

// -------------------------------------------------------------
// User & Access Key Operations
// -------------------------------------------------------------

func (s *SQLiteStore) CreateUser(ctx context.Context, user User) error {

	if user.ID == "" {
		user.ID = uuid.NewString()
	}
	if user.CreatedAt.IsZero() {
		user.CreatedAt = time.Now().UTC()
	}

	query := `INSERT INTO users (id, username, created_at) VALUES (?, ?, ?)`
	_, err := s.db.ExecContext(ctx, query, user.ID, user.Username, user.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		if isConstraintViolation(err) {
			return ErrUserAlreadyExists
		}
		return err
	}
	return nil
}

func (s *SQLiteStore) GetUser(ctx context.Context, id string) (User, error) {

	query := `SELECT id, username, created_at FROM users WHERE id = ?`
	var u User
	var createdStr string
	err := s.db.QueryRowContext(ctx, query, id).Scan(&u.ID, &u.Username, &createdStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrUserNotFound
		}
		return User{}, err
	}
	u.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
	return u, nil
}

func (s *SQLiteStore) GetUserByUsername(ctx context.Context, username string) (User, error) {

	query := `SELECT id, username, created_at FROM users WHERE username = ?`
	var u User
	var createdStr string
	err := s.db.QueryRowContext(ctx, query, username).Scan(&u.ID, &u.Username, &createdStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrUserNotFound
		}
		return User{}, err
	}
	u.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
	return u, nil
}

func (s *SQLiteStore) ListUsers(ctx context.Context) ([]User, error) {

	query := `SELECT id, username, created_at FROM users ORDER BY username ASC`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		var createdStr string
		if err := rows.Scan(&u.ID, &u.Username, &createdStr); err != nil {
			return nil, err
		}
		u.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
		users = append(users, u)
	}
	return users, nil
}

func (s *SQLiteStore) CreateAccessKey(ctx context.Context, key AccessKey) error {

	if key.CreatedAt.IsZero() {
		key.CreatedAt = time.Now().UTC()
	}
	if key.Status == "" {
		key.Status = "ACTIVE"
	}

	query := `INSERT INTO access_keys (access_key_id, secret_key_hash, user_id, status, created_at)
		VALUES (?, ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, query,
		key.AccessKeyID, key.SecretKeyHash, key.UserID, key.Status, key.CreatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLiteStore) GetAccessKey(ctx context.Context, accessKeyID string) (AccessKey, error) {

	query := `SELECT access_key_id, secret_key_hash, user_id, status, created_at 
		FROM access_keys WHERE access_key_id = ?`
	var k AccessKey
	var createdStr string
	err := s.db.QueryRowContext(ctx, query, accessKeyID).Scan(
		&k.AccessKeyID, &k.SecretKeyHash, &k.UserID, &k.Status, &createdStr,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AccessKey{}, ErrAccessKeyNotFound
		}
		return AccessKey{}, err
	}
	k.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
	return k, nil
}

func (s *SQLiteStore) ListAccessKeys(ctx context.Context, userID string) ([]AccessKey, error) {

	var query string
	var args []any
	if userID != "" {
		query = `SELECT access_key_id, secret_key_hash, user_id, status, created_at 
			FROM access_keys WHERE user_id = ? ORDER BY created_at DESC`
		args = []any{userID}
	} else {
		query = `SELECT access_key_id, secret_key_hash, user_id, status, created_at 
			FROM access_keys ORDER BY created_at DESC`
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []AccessKey
	for rows.Next() {
		var k AccessKey
		var createdStr string
		if err := rows.Scan(&k.AccessKeyID, &k.SecretKeyHash, &k.UserID, &k.Status, &createdStr); err != nil {
			return nil, err
		}
		k.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
		keys = append(keys, k)
	}
	return keys, nil
}

func (s *SQLiteStore) DeleteAccessKey(ctx context.Context, accessKeyID string) error {

	_, err := s.db.ExecContext(ctx, `DELETE FROM access_keys WHERE access_key_id = ?`, accessKeyID)
	return err
}

// -------------------------------------------------------------
// Storage Nodes
// -------------------------------------------------------------

func (s *SQLiteStore) RegisterNode(ctx context.Context, node StorageNode) error {

	if node.CreatedAt.IsZero() {
		node.CreatedAt = time.Now().UTC()
	}
	if node.LastHeartbeat.IsZero() {
		node.LastHeartbeat = time.Now().UTC()
	}

	query := `INSERT INTO storage_nodes (id, address, auth_token, status, last_heartbeat, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			address = excluded.address,
			auth_token = excluded.auth_token,
			status = excluded.status,
			last_heartbeat = excluded.last_heartbeat;`

	_, err := s.db.ExecContext(ctx, query,
		node.ID, node.Address, node.AuthToken, node.Status,
		node.LastHeartbeat.Format(time.RFC3339Nano), node.CreatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLiteStore) GetNode(ctx context.Context, id string) (StorageNode, error) {

	query := `SELECT id, address, auth_token, status, last_heartbeat, created_at FROM storage_nodes WHERE id = ?`
	var n StorageNode
	var heartStr, createdStr string
	err := s.db.QueryRowContext(ctx, query, id).Scan(&n.ID, &n.Address, &n.AuthToken, &n.Status, &heartStr, &createdStr)
	if err != nil {
		return StorageNode{}, err
	}
	n.LastHeartbeat, _ = time.Parse(time.RFC3339Nano, heartStr)
	n.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
	return n, nil
}

func (s *SQLiteStore) ListNodes(ctx context.Context) ([]StorageNode, error) {

	query := `SELECT id, address, auth_token, status, last_heartbeat, created_at FROM storage_nodes ORDER BY id ASC`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []StorageNode
	for rows.Next() {
		var n StorageNode
		var heartStr, createdStr string
		if err := rows.Scan(&n.ID, &n.Address, &n.AuthToken, &n.Status, &heartStr, &createdStr); err != nil {
			return nil, err
		}
		n.LastHeartbeat, _ = time.Parse(time.RFC3339Nano, heartStr)
		n.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
		nodes = append(nodes, n)
	}
	return nodes, nil
}

func (s *SQLiteStore) UpdateNodeHeartbeat(ctx context.Context, id string) error {

	_, err := s.db.ExecContext(ctx,
		`UPDATE storage_nodes SET last_heartbeat = ?, status = 'ONLINE' WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	return err
}

func (s *SQLiteStore) DeleteNode(ctx context.Context, id string) error {

	_, err := s.db.ExecContext(ctx, `DELETE FROM storage_nodes WHERE id = ?`, id)
	return err
}

// -------------------------------------------------------------
// Replication Outbox & Conflicts
// -------------------------------------------------------------

func (s *SQLiteStore) EnqueueReplicationJob(ctx context.Context, job ReplicationJob) error {

	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	if job.Status == "" {
		job.Status = "PENDING"
	}

	query := `INSERT INTO replication_outbox (object_id, bucket_id, object_key, version_id, action, status, retry_count, last_error, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, query,
		job.ObjectID, job.BucketID, job.ObjectKey, job.VersionID, job.Action,
		job.Status, job.RetryCount, job.LastError, job.CreatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLiteStore) FetchPendingReplicationJobs(ctx context.Context, limit int) ([]ReplicationJob, error) {

	if limit <= 0 {
		limit = 50
	}

	query := `SELECT id, object_id, bucket_id, object_key, version_id, action, status, retry_count, last_error, created_at
		FROM replication_outbox 
		WHERE status IN ('PENDING', 'FAILED') AND retry_count < 5
		ORDER BY created_at ASC LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []ReplicationJob
	for rows.Next() {
		var j ReplicationJob
		var createdStr string
		if err := rows.Scan(&j.ID, &j.ObjectID, &j.BucketID, &j.ObjectKey, &j.VersionID, &j.Action, &j.Status, &j.RetryCount, &j.LastError, &createdStr); err != nil {
			return nil, err
		}
		j.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
		jobs = append(jobs, j)
	}
	return jobs, nil
}

func (s *SQLiteStore) UpdateReplicationJobStatus(ctx context.Context, id int64, status string, errMsg string) error {

	var query string
	if status == "FAILED" {
		query = `UPDATE replication_outbox SET status = ?, last_error = ?, retry_count = retry_count + 1 WHERE id = ?`
		_, err := s.db.ExecContext(ctx, query, status, errMsg, id)
		return err
	} else if status == "COMPLETED" {
		// Can either delete or update status
		query = `UPDATE replication_outbox SET status = 'COMPLETED', last_error = '' WHERE id = ?`
		_, err := s.db.ExecContext(ctx, query, id)
		return err
	} else {
		query = `UPDATE replication_outbox SET status = ? WHERE id = ?`
		_, err := s.db.ExecContext(ctx, query, status, id)
		return err
	}
}

func (s *SQLiteStore) RecordReplicationConflict(ctx context.Context, conflict ReplicationConflict) error {

	if conflict.DetectedAt.IsZero() {
		conflict.DetectedAt = time.Now().UTC()
	}

	query := `INSERT INTO replication_conflicts (bucket_name, object_key, remote_version_id, local_version_id, resolution, detected_at)
		VALUES (?, ?, ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, query,
		conflict.BucketName, conflict.ObjectKey, conflict.RemoteVersionID, conflict.LocalVersionID, conflict.Resolution,
		conflict.DetectedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLiteStore) ListReplicationConflicts(ctx context.Context) ([]ReplicationConflict, error) {

	query := `SELECT id, bucket_name, object_key, remote_version_id, local_version_id, resolution, detected_at 
		FROM replication_conflicts ORDER BY detected_at DESC LIMIT 100`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var conflicts []ReplicationConflict
	for rows.Next() {
		var c ReplicationConflict
		var detectedStr string
		if err := rows.Scan(&c.ID, &c.BucketName, &c.ObjectKey, &c.RemoteVersionID, &c.LocalVersionID, &c.Resolution, &detectedStr); err != nil {
			return nil, err
		}
		c.DetectedAt, _ = time.Parse(time.RFC3339Nano, detectedStr)
		conflicts = append(conflicts, c)
	}
	return conflicts, nil
}

// -------------------------------------------------------------
// Audit Logs
// -------------------------------------------------------------

func (s *SQLiteStore) RecordAuditLog(ctx context.Context, log AuditLog) error {

	if log.Timestamp.IsZero() {
		log.Timestamp = time.Now().UTC()
	}

	query := `INSERT INTO audit_logs (action, resource, user_id, ip_address, status, timestamp)
		VALUES (?, ?, ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, query,
		log.Action, log.Resource, log.UserID, log.IPAddress, log.Status, log.Timestamp.Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLiteStore) ListAuditLogs(ctx context.Context, limit int) ([]AuditLog, error) {

	if limit <= 0 {
		limit = 100
	}
	query := `SELECT id, action, resource, user_id, ip_address, status, timestamp 
		FROM audit_logs ORDER BY timestamp DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []AuditLog
	for rows.Next() {
		var l AuditLog
		var tsStr string
		if err := rows.Scan(&l.ID, &l.Action, &l.Resource, &l.UserID, &l.IPAddress, &l.Status, &tsStr); err != nil {
			return nil, err
		}
		l.Timestamp, _ = time.Parse(time.RFC3339Nano, tsStr)
		logs = append(logs, l)
	}
	return logs, nil
}

func isConstraintViolation(err error) bool {
	if err == nil {
		return false
	}
	str := err.Error()
	return contains(str, "UNIQUE constraint failed") || contains(str, "constraint failed")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && searchSubstr(s, substr))
}

func searchSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
