package replication

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/my-s3-bucket/storage-engine/internal/metadata"
	"github.com/my-s3-bucket/storage-engine/internal/storage"
)

func TestReplicationSyncAndConflict(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "rep-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "meta.db")
	meta, err := metadata.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Close()

	eng, err := storage.NewLocalDiskEngine(filepath.Join(tmpDir, "storage"))
	if err != nil {
		t.Fatal(err)
	}

	authToken := "test-secret-token"
	handler := NewSyncHandler(meta, eng, authToken, nil)

	// 1. Initial replication sync (PUT)
	payload := []byte("Remote data payload")
	now := time.Now().UTC()
	req := httptest.NewRequest(http.MethodPost, "/internal/replication/sync", bytes.NewReader(payload))
	req.Header.Set("X-Replication-Token", authToken)
	req.Header.Set("X-Replication-Bucket", "rep-bucket")
	req.Header.Set("X-Replication-Key", "file.txt")
	req.Header.Set("X-Replication-Version", "v1")
	req.Header.Set("X-Replication-Action", "PUT")
	req.Header.Set("X-Replication-Timestamp", now.Format(time.RFC3339Nano))
	req.Header.Set("X-Replication-Size", "19")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify object exists
	b, err := meta.GetBucket(req.Context(), "rep-bucket")
	if err != nil {
		t.Fatal(err)
	}
	obj, err := meta.GetObject(req.Context(), b.ID, "file.txt", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if obj.Size != 19 {
		t.Fatalf("expected size 19, got %d", obj.Size)
	}

	// 2. Replication with OLD timestamp (Conflict where local is kept)
	oldTime := now.Add(-10 * time.Minute)
	oldPayload := []byte("Old remote data")
	reqOld := httptest.NewRequest(http.MethodPost, "/internal/replication/sync", bytes.NewReader(oldPayload))
	reqOld.Header.Set("X-Replication-Token", authToken)
	reqOld.Header.Set("X-Replication-Bucket", "rep-bucket")
	reqOld.Header.Set("X-Replication-Key", "file.txt")
	reqOld.Header.Set("X-Replication-Version", "v1")
	reqOld.Header.Set("X-Replication-Action", "PUT")
	reqOld.Header.Set("X-Replication-Timestamp", oldTime.Format(time.RFC3339Nano))

	wOld := httptest.NewRecorder()
	handler.ServeHTTP(wOld, reqOld)
	if wOld.Code != http.StatusOK {
		t.Fatalf("expected 200 for conflict handled, got %d", wOld.Code)
	}

	// Verify conflict was recorded
	conflicts, err := meta.ListReplicationConflicts(req.Context())
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].Resolution != "LOCAL_KEPT" {
		t.Fatalf("expected LOCAL_KEPT, got %s", conflicts[0].Resolution)
	}
}

func TestMultiPeerReplicationTracking(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "rep-multi-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "meta.db")
	meta, err := metadata.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Close()

	ctx := context.Background()

	// Enqueue a replication job
	job := metadata.ReplicationJob{
		ObjectID:  "obj-123",
		BucketID:  "b-123",
		ObjectKey: "test.dat",
		VersionID: "null",
		Action:    "PUT",
	}
	if err := meta.EnqueueReplicationJob(ctx, job); err != nil {
		t.Fatalf("failed to enqueue: %v", err)
	}

	jobs, err := meta.FetchPendingReplicationJobs(ctx, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	jobID := jobs[0].ID

	// Peer 1 fails
	if err := meta.UpdatePeerReplicationStatus(ctx, jobID, "peer-1", "FAILED", "connection refused"); err != nil {
		t.Fatalf("failed to update peer 1 status: %v", err)
	}

	// Peer 2 succeeds
	if err := meta.UpdatePeerReplicationStatus(ctx, jobID, "peer-2", "COMPLETED", ""); err != nil {
		t.Fatalf("failed to update peer 2 status: %v", err)
	}

	// Verify statuses
	p1Status, p1Retries, err := meta.GetPeerReplicationStatus(ctx, jobID, "peer-1")
	if err != nil || p1Status != "FAILED" || p1Retries != 1 {
		t.Fatalf("expected peer 1 FAILED with 1 retry, got %s, %d", p1Status, p1Retries)
	}

	p2Status, _, err := meta.GetPeerReplicationStatus(ctx, jobID, "peer-2")
	if err != nil || p2Status != "COMPLETED" {
		t.Fatalf("expected peer 2 COMPLETED, got %s", p2Status)
	}

	// Retry Peer 1 and succeed
	if err := meta.UpdatePeerReplicationStatus(ctx, jobID, "peer-1", "COMPLETED", ""); err != nil {
		t.Fatalf("failed to update peer 1 to completed: %v", err)
	}

	p1StatusAfter, _, err := meta.GetPeerReplicationStatus(ctx, jobID, "peer-1")
	if err != nil || p1StatusAfter != "COMPLETED" {
		t.Fatalf("expected peer 1 COMPLETED, got %s", p1StatusAfter)
	}
}

