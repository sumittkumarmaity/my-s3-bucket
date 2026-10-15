package metadata

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteStore(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage-meta-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// 1. Bucket Operations
	b := Bucket{
		ID:               "bucket-1",
		Name:             "test-bucket",
		VersioningStatus: "DISABLED",
		CreatedAt:        time.Now().UTC(),
	}
	if err := store.CreateBucket(ctx, b); err != nil {
		t.Fatalf("failed to create bucket: %v", err)
	}

	gotB, err := store.GetBucket(ctx, "test-bucket")
	if err != nil {
		t.Fatalf("failed to get bucket: %v", err)
	}
	if gotB.ID != b.ID {
		t.Fatalf("expected ID %s, got %s", b.ID, gotB.ID)
	}

	// Duplicate bucket error
	if err := store.CreateBucket(ctx, b); err != ErrBucketAlreadyExists {
		t.Fatalf("expected ErrBucketAlreadyExists, got %v", err)
	}

	// 2. Object Operations
	obj1 := Object{
		ID:             "obj-uuid-1",
		BucketID:       b.ID,
		ObjectKey:      "photos/vacation.jpg",
		VersionID:      "null",
		Size:           1024,
		ETag:           "abcdef123456",
		ChecksumCRC32C: "12345678",
		ChecksumSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		ContentType:    "image/jpeg",
		CreatedAt:      time.Now().UTC(),
	}
	if err := store.PutObject(ctx, obj1); err != nil {
		t.Fatalf("failed to put object: %v", err)
	}

	gotObj, err := store.GetObject(ctx, b.ID, "photos/vacation.jpg", "")
	if err != nil {
		t.Fatalf("failed to get object: %v", err)
	}
	if gotObj.Size != 1024 || gotObj.ETag != "abcdef123456" {
		t.Fatalf("mismatched object data: %+v", gotObj)
	}

	// 3. ListObjects
	objs, err := store.ListObjects(ctx, b.ID, "photos/", "", 10)
	if err != nil {
		t.Fatalf("failed to list objects: %v", err)
	}
	if len(objs) != 1 || objs[0].ObjectKey != "photos/vacation.jpg" {
		t.Fatalf("unexpected list results: %+v", objs)
	}

	// 4. User and AccessKey
	u := User{
		ID:        "user-1",
		Username:  "admin",
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateUser(ctx, u); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	ak := AccessKey{
		AccessKeyID:   "AKIA123",
		SecretKeyHash: "hashedsecret",
		UserID:        u.ID,
		Status:        "ACTIVE",
		CreatedAt:     time.Now().UTC(),
	}
	if err := store.CreateAccessKey(ctx, ak); err != nil {
		t.Fatalf("failed to create access key: %v", err)
	}

	gotKey, err := store.GetAccessKey(ctx, "AKIA123")
	if err != nil {
		t.Fatalf("failed to get access key: %v", err)
	}
	if gotKey.SecretKeyHash != "hashedsecret" {
		t.Fatalf("expected hashedsecret, got %s", gotKey.SecretKeyHash)
	}

	// 5. Delete Object
	delObj, err := store.DeleteObject(ctx, b.ID, "photos/vacation.jpg", "")
	if err != nil {
		t.Fatalf("failed to delete object: %v", err)
	}
	if delObj.ID != obj1.ID {
		t.Fatalf("unexpected deleted obj: %+v", delObj)
	}

	// Get after delete should fail
	_, err = store.GetObject(ctx, b.ID, "photos/vacation.jpg", "")
	if err != ErrObjectNotFound {
		t.Fatalf("expected ErrObjectNotFound, got %v", err)
	}

	// Delete bucket should succeed now that it's empty
	if err := store.DeleteBucket(ctx, "test-bucket"); err != nil {
		t.Fatalf("failed to delete bucket: %v", err)
	}

	// 6. Versioning Test
	vBucket := Bucket{
		ID:               "bucket-v",
		Name:             "versioned-bucket",
		VersioningStatus: "ENABLED",
		CreatedAt:        time.Now().UTC(),
	}
	if err := store.CreateBucket(ctx, vBucket); err != nil {
		t.Fatalf("failed to create versioned bucket: %v", err)
	}

	v1 := Object{
		ID:          "v1-id",
		BucketID:    vBucket.ID,
		ObjectKey:   "doc.txt",
		VersionID:   "ver-1",
		Size:        100,
		ETag:        "etag1",
		ContentType: "text/plain",
		CreatedAt:   time.Now().UTC().Add(-10 * time.Minute),
	}
	v2 := Object{
		ID:          "v2-id",
		BucketID:    vBucket.ID,
		ObjectKey:   "doc.txt",
		VersionID:   "ver-2",
		Size:        200,
		ETag:        "etag2",
		ContentType: "text/plain",
		CreatedAt:   time.Now().UTC(),
	}
	if err := store.PutObject(ctx, v1); err != nil {
		t.Fatal(err)
	}
	if err := store.PutObject(ctx, v2); err != nil {
		t.Fatal(err)
	}

	// Latest should be ver-2
	latest, err := store.GetObject(ctx, vBucket.ID, "doc.txt", "")
	if err != nil {
		t.Fatalf("failed to get latest: %v", err)
	}
	if latest.VersionID != "ver-2" {
		t.Fatalf("expected ver-2, got %s", latest.VersionID)
	}

	// Specific version ver-1
	gotV1, err := store.GetObject(ctx, vBucket.ID, "doc.txt", "ver-1")
	if err != nil {
		t.Fatalf("failed to get ver-1: %v", err)
	}
	if gotV1.VersionID != "ver-1" {
		t.Fatalf("expected ver-1, got %s", gotV1.VersionID)
	}

	// Soft delete in versioned bucket creates a delete marker
	delRes, err := store.DeleteObject(ctx, vBucket.ID, "doc.txt", "")
	if err != nil {
		t.Fatalf("failed to soft delete: %v", err)
	}
	if !delRes.IsDeleteMarker {
		t.Fatalf("expected delete marker, got %v", delRes)
	}

	// Latest get should now return ErrObjectNotFound
	_, err = store.GetObject(ctx, vBucket.ID, "doc.txt", "")
	if err != ErrObjectNotFound {
		t.Fatalf("expected ErrObjectNotFound for delete marker, got %v", err)
	}

	// 7. Multipart Test
	mp := MultipartUpload{
		UploadID:    "upload-1",
		BucketID:    vBucket.ID,
		ObjectKey:   "large.zip",
		ContentType: "application/zip",
		CreatedAt:   time.Now().UTC(),
	}
	if err := store.CreateMultipartUpload(ctx, mp); err != nil {
		t.Fatalf("failed to create multipart: %v", err)
	}
	part1 := MultipartPart{
		UploadID:     mp.UploadID,
		PartNumber:   1,
		ETag:         "part1-etag",
		Size:         5000,
		Checksum:     "crc1",
		PhysicalPath: "/tmp/p1",
	}
	if err := store.AddMultipartPart(ctx, part1); err != nil {
		t.Fatalf("failed to add part: %v", err)
	}
	parts, err := store.ListMultipartParts(ctx, mp.UploadID)
	if err != nil || len(parts) != 1 {
		t.Fatalf("failed to list parts: %v, len=%d", err, len(parts))
	}

	// 8. Replication Outbox & Conflicts Test
	job := ReplicationJob{
		ObjectID:  v1.ID,
		BucketID:  vBucket.ID,
		ObjectKey: "doc.txt",
		VersionID: "ver-1",
		Action:    "PUT",
	}
	if err := store.EnqueueReplicationJob(ctx, job); err != nil {
		t.Fatalf("failed to enqueue rep job: %v", err)
	}
	jobs, err := store.FetchPendingReplicationJobs(ctx, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("failed to fetch rep jobs: %v, len=%d", err, len(jobs))
	}
	if err := store.UpdateReplicationJobStatus(ctx, jobs[0].ID, "COMPLETED", ""); err != nil {
		t.Fatalf("failed to update rep job status: %v", err)
	}

	conf := ReplicationConflict{
		BucketName:      "versioned-bucket",
		ObjectKey:       "doc.txt",
		RemoteVersionID: "rem-1",
		LocalVersionID:  "ver-2",
		Resolution:      "LOCAL_KEPT",
	}
	if err := store.RecordReplicationConflict(ctx, conf); err != nil {
		t.Fatalf("failed to record conflict: %v", err)
	}
	confs, err := store.ListReplicationConflicts(ctx)
	if err != nil || len(confs) != 1 {
		t.Fatalf("failed to list conflicts: %v, len=%d", err, len(confs))
	}
}

func TestAccessKeyEnvelopeEncryption(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage-enckey-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Enable encryption
	masterKey := "super-secure-master-key-32-bytes!"
	store.SetEncryptionKey(masterKey)

	// Create user and access key
	user := User{
		ID:        "user-enc-1",
		Username:  "enc-user",
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	rawSecret := "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	ak := AccessKey{
		AccessKeyID:   "AKIAIOSFODNN7EXAMPLE",
		SecretKeyHash: rawSecret,
		UserID:        user.ID,
		Status:        "ACTIVE",
		CreatedAt:     time.Now().UTC(),
	}
	if err := store.CreateAccessKey(ctx, ak); err != nil {
		t.Fatalf("failed to create access key: %v", err)
	}

	// 1. Inspect raw SQLite database value to verify it is encrypted
	var rawStoredSecret string
	err = store.db.QueryRow("SELECT secret_key_hash FROM access_keys WHERE access_key_id = ?", ak.AccessKeyID).Scan(&rawStoredSecret)
	if err != nil {
		t.Fatalf("failed to query raw secret: %v", err)
	}
	if rawStoredSecret == rawSecret {
		t.Fatalf("secret key was stored in plaintext! Expected ciphertext")
	}
	if len(rawStoredSecret) < 10 || rawStoredSecret[:7] != "enc:v1:" {
		t.Fatalf("expected encrypted secret to start with 'enc:v1:', got: %s", rawStoredSecret)
	}

	// 2. Fetch through store API and verify decryption
	fetched, err := store.GetAccessKey(ctx, ak.AccessKeyID)
	if err != nil {
		t.Fatalf("failed to get access key: %v", err)
	}
	if fetched.SecretKeyHash != rawSecret {
		t.Fatalf("expected decrypted secret %s, got %s", rawSecret, fetched.SecretKeyHash)
	}

	// 3. Backward compatibility: insert an unencrypted legacy secret directly
	legacyAKID := "AKIALEGACYUNENCRYPTED"
	legacySecret := "legacySecretPlaintext123456"
	_, err = store.db.Exec("INSERT INTO access_keys (access_key_id, secret_key_hash, user_id, status, created_at) VALUES (?, ?, ?, ?, ?)",
		legacyAKID, legacySecret, user.ID, "ACTIVE", time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("failed to insert legacy key: %v", err)
	}

	fetchedLegacy, err := store.GetAccessKey(ctx, legacyAKID)
	if err != nil {
		t.Fatalf("failed to get legacy key: %v", err)
	}
	if fetchedLegacy.SecretKeyHash != legacySecret {
		t.Fatalf("expected legacy plain secret %s, got %s", legacySecret, fetchedLegacy.SecretKeyHash)
	}
}

