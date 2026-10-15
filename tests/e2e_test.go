package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/my-s3-bucket/storage-engine/internal/api"
	"github.com/my-s3-bucket/storage-engine/internal/auth"
	"github.com/my-s3-bucket/storage-engine/internal/encryption"
	"github.com/my-s3-bucket/storage-engine/internal/health"
	"github.com/my-s3-bucket/storage-engine/internal/jobs"
	"github.com/my-s3-bucket/storage-engine/internal/metadata"
	"github.com/my-s3-bucket/storage-engine/internal/metrics"
	"github.com/my-s3-bucket/storage-engine/internal/s3"
	"github.com/my-s3-bucket/storage-engine/internal/storage"
)

func TestEndToEndStorageEngine(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "s3-e2e-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "meta.db")
	storageDir := filepath.Join(tmpDir, "storage")

	meta, err := metadata.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Close()

	engine, err := storage.NewLocalDiskEngine(storageDir)
	if err != nil {
		t.Fatal(err)
	}

	encProvider := &encryption.NoOpProvider{}

	jobManager := jobs.NewManager(meta, engine, storageDir, 3600, 86400, nil)
	ctx := context.Background()
	jobManager.Start(ctx)
	defer jobManager.Stop()

	// 1. S3 Server Setup
	s3Handler := s3.NewS3Handler(meta, engine, encProvider, 5*1024*1024*1024, nil)
	authMiddleware := auth.NewAuthMiddleware(meta, false, "admin", "adminsecret")
	s3Mux := http.NewServeMux()
	s3Mux.Handle("/", auth.CORSMiddleware(authMiddleware.Authenticate(s3Handler)))
	s3Server := httptest.NewServer(s3Mux)
	defer s3Server.Close()

	// 2. Admin Server Setup
	adminAPI := api.NewServer(meta, engine, jobManager, "adminsecret")
	healthChecker := health.NewHealthChecker(meta, engine, storageDir)
	metricsReg := metrics.Init(meta, engine)
	adminMux := http.NewServeMux()
	adminMux.Handle("/health", healthChecker)
	adminMux.Handle("/metrics", metricsReg)
	adminMux.Handle("/api/v1/", adminAPI.Handler())
	adminServer := httptest.NewServer(adminMux)
	defer adminServer.Close()

	client := s3Server.Client()

	// Step 1: Health check
	resp, err := client.Get(adminServer.URL + "/health")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("health check failed: %v, status: %d", err, resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Step 2: Create Bucket via S3 API
	req, _ := http.NewRequest(http.MethodPut, s3Server.URL+"/company-docs", nil)
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("create bucket failed: %v, status: %d", err, resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Step 3: Put Object via S3 API
	objData := []byte("Antigravity Cloud Storage Engine 2026 - Top Secret Document")
	req, _ = http.NewRequest(http.MethodPut, s3Server.URL+"/company-docs/spec.txt", bytes.NewReader(objData))
	req.Header.Set("Content-Type", "text/plain")
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("put object failed: %v, status: %d", err, resp.StatusCode)
	}
	etag := resp.Header.Get("ETag")
	_ = resp.Body.Close()

	// Step 4: Get Object and verify content
	req, _ = http.NewRequest(http.MethodGet, s3Server.URL+"/company-docs/spec.txt", nil)
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("get object failed: %v, status: %d", err, resp.StatusCode)
	}
	readData, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !bytes.Equal(readData, objData) {
		t.Fatalf("expected %s, got %s", string(objData), string(readData))
	}

	// Step 5: Multipart upload flow (3 parts)
	req, _ = http.NewRequest(http.MethodPost, s3Server.URL+"/company-docs/archive.tar.gz?uploads", nil)
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("initiate multipart failed: %v", err)
	}
	var initRes s3.InitiateMultipartUploadResult
	bodyBytes, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	_ = xml.Unmarshal(bodyBytes, &initRes)
	uploadID := initRes.UploadId

	var partEtags []string
	var totalMultipartBytes []byte
	for i := 1; i <= 3; i++ {
		chunk := bytes.Repeat([]byte{byte('A' + i)}, 5000)
		totalMultipartBytes = append(totalMultipartBytes, chunk...)

		partURL := fmt.Sprintf("%s/company-docs/archive.tar.gz?uploadId=%s&partNumber=%d", s3Server.URL, uploadID, i)
		pReq, _ := http.NewRequest(http.MethodPut, partURL, bytes.NewReader(chunk))
		pResp, pErr := client.Do(pReq)
		if pErr != nil || pResp.StatusCode != http.StatusOK {
			t.Fatalf("upload part %d failed: %v", i, pErr)
		}
		partEtags = append(partEtags, pResp.Header.Get("ETag"))
		_ = pResp.Body.Close()
	}

	// Complete multipart
	compReq := s3.CompleteMultipartUploadRequest{
		Parts: []s3.PartInput{
			{PartNumber: 1, ETag: partEtags[0]},
			{PartNumber: 2, ETag: partEtags[1]},
			{PartNumber: 3, ETag: partEtags[2]},
		},
	}
	compXML, _ := xml.Marshal(compReq)
	req, _ = http.NewRequest(http.MethodPost, fmt.Sprintf("%s/company-docs/archive.tar.gz?uploadId=%s", s3Server.URL, uploadID), bytes.NewReader(compXML))
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("complete multipart failed: %v", err)
	}
	_ = resp.Body.Close()

	// Get assembled multipart object
	req, _ = http.NewRequest(http.MethodGet, s3Server.URL+"/company-docs/archive.tar.gz", nil)
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("get multipart object failed: %v", err)
	}
	assData, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !bytes.Equal(assData, totalMultipartBytes) {
		t.Fatalf("assembled multipart data length mismatch: expected %d, got %d", len(totalMultipartBytes), len(assData))
	}

	// Step 6: Verify Admin API endpoints
	adminReq, _ := http.NewRequest(http.MethodGet, adminServer.URL+"/api/v1/status", nil)
	adminReq.Header.Set("X-Admin-Token", "adminsecret")
	resp, err = client.Do(adminReq)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("admin status failed: %v", err)
	}
	var statusMap map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&statusMap)
	_ = resp.Body.Close()
	if statusMap["status"] != "HEALTHY" {
		t.Fatalf("expected HEALTHY status: %+v", statusMap)
	}

	// Step 7: Trigger GC & Integrity Scan
	gcReq, _ := http.NewRequest(http.MethodPost, adminServer.URL+"/api/v1/jobs/gc", nil)
	gcReq.Header.Set("X-Admin-Token", "adminsecret")
	resp, err = client.Do(gcReq)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("admin trigger GC failed: %v", err)
	}
	_ = resp.Body.Close()

	scanReq, _ := http.NewRequest(http.MethodPost, adminServer.URL+"/api/v1/jobs/integrity", nil)
	scanReq.Header.Set("X-Admin-Token", "adminsecret")
	resp, err = client.Do(scanReq)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("admin trigger integrity scan failed: %v", err)
	}
	var scanRep map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&scanRep)
	_ = resp.Body.Close()
	if scanRep["passed"].(float64) < 2 {
		t.Fatalf("expected at least 2 objects passed: %+v", scanRep)
	}

	// Step 8: Prometheus metrics check
	resp, err = client.Get(adminServer.URL + "/metrics")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics check failed: %v", err)
	}
	metricText, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !bytes.Contains(metricText, []byte("storage_objects_total")) {
		t.Fatalf("metrics missing storage_objects_total: %s", string(metricText))
	}

	_ = etag
}
