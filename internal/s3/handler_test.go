package s3

import (
	"bytes"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/my-s3-bucket/storage-engine/internal/auth"
	"github.com/my-s3-bucket/storage-engine/internal/metadata"
	"github.com/my-s3-bucket/storage-engine/internal/storage"
)

func setupTestS3(t *testing.T) (http.Handler, func()) {
	tmpDir, err := os.MkdirTemp("", "s3-test-*")
	if err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(tmpDir, "meta.db")
	meta, err := metadata.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	eng, err := storage.NewLocalDiskEngine(filepath.Join(tmpDir, "storage"))
	if err != nil {
		t.Fatal(err)
	}

	handler := NewS3Handler(meta, eng, nil, 0, nil)
	authMw := auth.NewAuthMiddleware(meta, false, "admin", "adminsecret")
	h := authMw.Authenticate(handler)

	cleanup := func() {
		_ = meta.Close()
		_ = os.RemoveAll(tmpDir)
	}

	return h, cleanup
}

func TestS3BucketAndObjectLifecycle(t *testing.T) {
	handler, cleanup := setupTestS3(t)
	defer cleanup()

	// 1. Create Bucket
	req := httptest.NewRequest(http.MethodPut, "/test-bucket", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// 2. List Buckets
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var lb ListAllMyBucketsResult
	if err := xml.Unmarshal(w.Body.Bytes(), &lb); err != nil {
		t.Fatalf("failed to parse list buckets xml: %v", err)
	}
	if len(lb.Buckets) != 1 || lb.Buckets[0].Name != "test-bucket" {
		t.Fatalf("unexpected buckets: %+v", lb.Buckets)
	}

	// 3. Put Object
	bodyContent := "Hello S3 Engine World!"
	req = httptest.NewRequest(http.MethodPut, "/test-bucket/hello.txt", bytes.NewBufferString(bodyContent))
	req.Header.Set("Content-Type", "text/plain")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag in response")
	}

	// 4. Get Object
	req = httptest.NewRequest(http.MethodGet, "/test-bucket/hello.txt", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != bodyContent {
		t.Fatalf("expected %s, got %s", bodyContent, w.Body.String())
	}

	// 5. Head Object
	req = httptest.NewRequest(http.MethodHead, "/test-bucket/hello.txt", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("ETag") != etag {
		t.Fatalf("expected etag %s, got %s", etag, w.Header().Get("ETag"))
	}

	// 6. Range Request
	req = httptest.NewRequest(http.MethodGet, "/test-bucket/hello.txt", nil)
	req.Header.Set("Range", "bytes=0-4") // "Hello"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusPartialContent {
		t.Fatalf("expected 206, got %d", w.Code)
	}
	if w.Body.String() != "Hello" {
		t.Fatalf("expected 'Hello', got '%s'", w.Body.String())
	}

	// 7. List Objects V2
	req = httptest.NewRequest(http.MethodGet, "/test-bucket?list-type=2", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var lo ListBucketResult
	if err := xml.Unmarshal(w.Body.Bytes(), &lo); err != nil {
		t.Fatalf("failed to unmarshal list objects: %v", err)
	}
	if len(lo.Contents) != 1 || lo.Contents[0].Key != "hello.txt" {
		t.Fatalf("unexpected objects: %+v", lo.Contents)
	}

	// 8. Delete Object
	req = httptest.NewRequest(http.MethodDelete, "/test-bucket/hello.txt", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}

	// 9. Get after delete -> 404 NoSuchKey
	req = httptest.NewRequest(http.MethodGet, "/test-bucket/hello.txt", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}

	// 10. Delete Bucket
	req = httptest.NewRequest(http.MethodDelete, "/test-bucket", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
}

func TestS3MultipartUpload(t *testing.T) {
	handler, cleanup := setupTestS3(t)
	defer cleanup()

	// Create bucket
	req := httptest.NewRequest(http.MethodPut, "/mp-bucket", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// 1. Initiate Multipart
	req = httptest.NewRequest(http.MethodPost, "/mp-bucket/bigfile.bin?uploads", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("failed to initiate multipart: %d", w.Code)
	}

	var initRes InitiateMultipartUploadResult
	if err := xml.Unmarshal(w.Body.Bytes(), &initRes); err != nil {
		t.Fatal(err)
	}
	uploadID := initRes.UploadId
	if uploadID == "" {
		t.Fatal("empty upload ID")
	}

	// 2. Upload Part 1
	p1Data := bytes.Repeat([]byte("A"), 1024)
	req = httptest.NewRequest(http.MethodPut, "/mp-bucket/bigfile.bin?uploadId="+uploadID+"&partNumber=1", bytes.NewReader(p1Data))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("failed to upload part 1: %d", w.Code)
	}
	p1ETag := w.Header().Get("ETag")

	// 3. Upload Part 2
	p2Data := bytes.Repeat([]byte("B"), 2048)
	req = httptest.NewRequest(http.MethodPut, "/mp-bucket/bigfile.bin?uploadId="+uploadID+"&partNumber=2", bytes.NewReader(p2Data))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("failed to upload part 2: %d", w.Code)
	}
	p2ETag := w.Header().Get("ETag")

	// 4. List Parts
	req = httptest.NewRequest(http.MethodGet, "/mp-bucket/bigfile.bin?uploadId="+uploadID, nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("failed to list parts: %d", w.Code)
	}
	var lp ListPartsResult
	_ = xml.Unmarshal(w.Body.Bytes(), &lp)
	if len(lp.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(lp.Parts))
	}

	// 5. Complete Multipart
	compReq := CompleteMultipartUploadRequest{
		Parts: []PartInput{
			{PartNumber: 1, ETag: p1ETag},
			{PartNumber: 2, ETag: p2ETag},
		},
	}
	compXML, _ := xml.Marshal(compReq)
	req = httptest.NewRequest(http.MethodPost, "/mp-bucket/bigfile.bin?uploadId="+uploadID, bytes.NewReader(compXML))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("failed to complete multipart: %d", w.Code)
	}

	// 6. Verify Full Object
	req = httptest.NewRequest(http.MethodGet, "/mp-bucket/bigfile.bin", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("failed to get assembled object: %d", w.Code)
	}
	if w.Body.Len() != len(p1Data)+len(p2Data) {
		t.Fatalf("expected length %d, got %d", len(p1Data)+len(p2Data), w.Body.Len())
	}

	// 7. Abort Multipart with Path Traversal Attack -> Must be rejected with 404 NoSuchUpload
	abortTraversalReq := httptest.NewRequest(http.MethodDelete, "/mp-bucket/bigfile.bin?uploadId=..", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, abortTraversalReq)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for traversal uploadId, got %d", w.Code)
	}

	// Verify object still exists and was not deleted by traversal
	verifyReq := httptest.NewRequest(http.MethodGet, "/mp-bucket/bigfile.bin", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, verifyReq)
	if w.Code != http.StatusOK {
		t.Fatalf("object was deleted by traversal attack! status: %d", w.Code)
	}
}

func TestS3Versioning(t *testing.T) {
	handler, cleanup := setupTestS3(t)
	defer cleanup()

	// 1. Create Bucket
	req := httptest.NewRequest(http.MethodPut, "/ver-bucket", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// 2. Enable Versioning
	cfg := VersioningConfiguration{Status: "Enabled"}
	cfgXML, _ := xml.Marshal(cfg)
	req = httptest.NewRequest(http.MethodPut, "/ver-bucket?versioning", bytes.NewReader(cfgXML))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("failed to enable versioning: %d", w.Code)
	}

	// 3. Put Version 1
	req = httptest.NewRequest(http.MethodPut, "/ver-bucket/notes.txt", bytes.NewBufferString("Version 1 content"))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	v1ID := w.Header().Get("x-amz-version-id")
	if v1ID == "" || v1ID == "null" {
		t.Fatalf("invalid v1ID: %s", v1ID)
	}

	// 4. Put Version 2
	req = httptest.NewRequest(http.MethodPut, "/ver-bucket/notes.txt", bytes.NewBufferString("Version 2 content"))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	v2ID := w.Header().Get("x-amz-version-id")
	if v2ID == "" || v2ID == v1ID {
		t.Fatalf("invalid v2ID: %s", v2ID)
	}

	// 5. Get Latest -> Version 2
	req = httptest.NewRequest(http.MethodGet, "/ver-bucket/notes.txt", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Body.String() != "Version 2 content" {
		t.Fatalf("expected Version 2, got %s", w.Body.String())
	}

	// 6. Get Specific Version 1
	req = httptest.NewRequest(http.MethodGet, "/ver-bucket/notes.txt?versionId="+v1ID, nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Body.String() != "Version 1 content" {
		t.Fatalf("expected Version 1, got %s", w.Body.String())
	}

	// 7. Delete Object (creates delete marker)
	req = httptest.NewRequest(http.MethodDelete, "/ver-bucket/notes.txt", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Header().Get("x-amz-delete-marker") != "true" {
		t.Fatal("expected x-amz-delete-marker true")
	}

	// 8. Get Latest should now be 404
	req = httptest.NewRequest(http.MethodGet, "/ver-bucket/notes.txt", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete marker, got %d", w.Code)
	}

	// 9. Specific Version 1 still accessible!
	req = httptest.NewRequest(http.MethodGet, "/ver-bucket/notes.txt?versionId="+v1ID, nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "Version 1 content" {
		t.Fatalf("version 1 should still be accessible")
	}
}

func TestS3MultiTenantAuthorization(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "s3-tenant-*")
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

	handler := NewS3Handler(meta, eng, nil, 0, nil)

	pUserA := &auth.Principal{UserID: "user-a", IsAdmin: false}
	pUserB := &auth.Principal{UserID: "user-b", IsAdmin: false}
	pAdmin := &auth.Principal{UserID: "admin", IsAdmin: true}

	// 1. Unauthenticated request without principal -> 403 AccessDenied
	req := httptest.NewRequest(http.MethodPut, "/tenant-bucket", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 AccessDenied for anonymous request, got %d", w.Code)
	}

	// 2. User A creates bucket
	req = httptest.NewRequest(http.MethodPut, "/tenant-bucket", nil)
	req = req.WithContext(auth.ContextWithPrincipal(req.Context(), pUserA))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for user A create bucket, got %d: %s", w.Code, w.Body.String())
	}

	// 3. User A puts object
	req = httptest.NewRequest(http.MethodPut, "/tenant-bucket/data.txt", bytes.NewBufferString("tenant-data"))
	req = req.WithContext(auth.ContextWithPrincipal(req.Context(), pUserA))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for user A put object, got %d: %s", w.Code, w.Body.String())
	}

	// 4. User B tries to get object from User A's bucket -> 403
	req = httptest.NewRequest(http.MethodGet, "/tenant-bucket/data.txt", nil)
	req = req.WithContext(auth.ContextWithPrincipal(req.Context(), pUserB))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for user B accessing user A's bucket, got %d", w.Code)
	}

	// 5. User B tries to put object into User A's bucket -> 403
	req = httptest.NewRequest(http.MethodPut, "/tenant-bucket/hack.txt", bytes.NewBufferString("malicious"))
	req = req.WithContext(auth.ContextWithPrincipal(req.Context(), pUserB))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for user B putting to user A's bucket, got %d", w.Code)
	}

	// 6. User B tries to list objects in User A's bucket -> 403
	req = httptest.NewRequest(http.MethodGet, "/tenant-bucket", nil)
	req = req.WithContext(auth.ContextWithPrincipal(req.Context(), pUserB))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for user B listing user A's bucket, got %d", w.Code)
	}

	// 7. User B tries to delete User A's bucket -> 403
	req = httptest.NewRequest(http.MethodDelete, "/tenant-bucket", nil)
	req = req.WithContext(auth.ContextWithPrincipal(req.Context(), pUserB))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for user B deleting user A's bucket, got %d", w.Code)
	}

	// 8. User B lists buckets -> should NOT see user A's bucket
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(auth.ContextWithPrincipal(req.Context(), pUserB))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for user B list buckets, got %d", w.Code)
	}
	var lbB ListAllMyBucketsResult
	_ = xml.Unmarshal(w.Body.Bytes(), &lbB)
	if len(lbB.Buckets) != 0 {
		t.Fatalf("expected 0 buckets for user B, got %d", len(lbB.Buckets))
	}

	// 9. Admin accesses User A's bucket -> 200 OK
	req = httptest.NewRequest(http.MethodGet, "/tenant-bucket/data.txt", nil)
	req = req.WithContext(auth.ContextWithPrincipal(req.Context(), pAdmin))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for admin accessing user A's bucket, got %d", w.Code)
	}
	if w.Body.String() != "tenant-data" {
		t.Fatalf("expected 'tenant-data', got %s", w.Body.String())
	}

	// 10. Invalid bucket names rejected with 400
	invalidNames := []string{"AB", "sh", "Invalid_Under_Score", "-startdash", "enddash-", "double..dot"}
	for _, badName := range invalidNames {
		req = httptest.NewRequest(http.MethodPut, "/"+badName, nil)
		req = req.WithContext(auth.ContextWithPrincipal(req.Context(), pUserA))
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 InvalidBucketName for %q, got %d: %s", badName, w.Code, w.Body.String())
		}
	}
}

func TestS3InsufficientStorageAdmission(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "s3-507-*")
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

	handler := NewS3Handler(meta, eng, nil, 0, nil)
	pAdmin := &auth.Principal{UserID: "admin", IsAdmin: true}

	// Create bucket
	req := httptest.NewRequest(http.MethodPut, "/quota-bucket", nil)
	req = req.WithContext(auth.ContextWithPrincipal(req.Context(), pAdmin))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Trigger low space condition
	eng.SetAdmissionThresholds(100*1024*1024*1024*1024*1024, 0)

	// PutObject must be rejected with HTTP 507
	req = httptest.NewRequest(http.MethodPut, "/quota-bucket/huge.bin", bytes.NewBufferString("huge data"))
	req = req.WithContext(auth.ContextWithPrincipal(req.Context(), pAdmin))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusInsufficientStorage {
		t.Fatalf("expected 507 InsufficientStorage, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "InsufficientStorage") {
		t.Fatalf("expected InsufficientStorage XML error code, got %s", w.Body.String())
	}
}
