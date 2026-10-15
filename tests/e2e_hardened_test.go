package tests

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/my-s3-bucket/storage-engine/internal/auth"
	"github.com/my-s3-bucket/storage-engine/internal/encryption"
	"github.com/my-s3-bucket/storage-engine/internal/jobs"
	"github.com/my-s3-bucket/storage-engine/internal/metadata"
	"github.com/my-s3-bucket/storage-engine/internal/s3"
	"github.com/my-s3-bucket/storage-engine/internal/storage"
)

func signRequest(req *http.Request, accessKey, secretKey, region, service string, payload []byte) {
	now := time.Now().UTC()
	dateStr := now.Format("20060102")
	dateTimeStr := now.Format("20060102T150405Z")

	payloadHashBytes := sha256.Sum256(payload)
	payloadHash := hex.EncodeToString(payloadHashBytes[:])

	req.Header.Set("Host", req.Host)
	req.Header.Set("X-Amz-Date", dateTimeStr)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	signedHeadersList := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	signedHeaders := strings.Join(signedHeadersList, ";")

	details := &auth.SigV4Details{
		Algorithm:     "AWS4-HMAC-SHA256",
		AccessKeyID:   accessKey,
		DateStr:       dateStr,
		Region:        region,
		Service:       service,
		SignedHeaders: signedHeadersList,
		Timestamp:     now,
	}

	canonicalReq := auth.BuildCanonicalRequest(req, details, payloadHash)
	stringToSign := auth.BuildStringToSign(req, details, canonicalReq)
	signingKey := auth.DeriveSigningKey(secretKey, dateStr, region, service)

	h := hmac.New(sha256.New, signingKey)
	h.Write([]byte(stringToSign))
	signature := hex.EncodeToString(h.Sum(nil))

	authHeader := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s/%s/%s/aws4_request, SignedHeaders=%s, Signature=%s",
		accessKey, dateStr, region, service, signedHeaders, signature)
	req.Header.Set("Authorization", authHeader)
}

func TestHardenedProductionWorkflows(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "s3-hardened-*")
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

	// Enable envelope encryption on SQLite metadata store
	metaMasterSecret := "master-db-secret-32-bytes-long!"
	meta.SetEncryptionKey(metaMasterSecret)

	engine, err := storage.NewLocalDiskEngine(storageDir)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Transparent AES-256-GCM Streaming Encryption
	encProvider, err := encryption.NewAESGCMProvider("master-cluster-aes-encryption-key")
	if err != nil {
		t.Fatal(err)
	}

	jobManager := jobs.NewManager(meta, engine, storageDir, 3600, 86400, encProvider, nil)
	ctx := context.Background()
	jobManager.Start(ctx)
	defer jobManager.Stop()

	// 2. Strict SigV4 Authentication Setup
	adminKey := "AKIAENTERPRISEADMINKEY"
	adminSecret := "top-secret-signing-key-enterprise-production"

	s3Handler := s3.NewS3Handler(meta, engine, encProvider, 5*1024*1024*1024, nil)
	authMiddleware := auth.NewAuthMiddleware(meta, true, adminKey, adminSecret)

	s3Mux := http.NewServeMux()
	s3Mux.Handle("/", auth.CORSMiddleware(authMiddleware.Authenticate(s3Handler)))
	s3Server := httptest.NewServer(s3Mux)
	defer s3Server.Close()

	client := s3Server.Client()

	// Helper to send signed request
	doSigned := func(method, rawURL string, body []byte) *http.Response {
		req, err := http.NewRequest(method, rawURL, bytes.NewReader(body))
		if err != nil {
			t.Fatalf("failed to create req: %v", err)
		}
		req.Host = strings.TrimPrefix(s3Server.URL, "http://")
		signRequest(req, adminKey, adminSecret, "us-east-1", "s3", body)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		return resp
	}

	// Step 1: Create Bucket
	bURL := fmt.Sprintf("%s/secure-vault", s3Server.URL)
	resp := doSigned(http.MethodPut, bURL, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CreateBucket failed with status %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Step 2: Put Encrypted Object
	secretDoc := []byte("CONFIDENTIAL ENTERPRISE PAYLOAD DATA")
	objURL := fmt.Sprintf("%s/secure-vault/document.pdf", s3Server.URL)
	resp = doSigned(http.MethodPut, objURL, secretDoc)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PutObject failed with status %d", resp.StatusCode)
	}
	etag := resp.Header.Get("ETag")
	_ = resp.Body.Close()

	if etag == "" {
		t.Fatalf("missing ETag on PutObject")
	}

	// Step 3: Verify Physical Storage Encryption at Rest
	b, err := meta.GetBucket(ctx, "secure-vault")
	if err != nil {
		t.Fatal(err)
	}
	objMeta, err := meta.GetObject(ctx, b.ID, "document.pdf", "null")
	if err != nil {
		t.Fatal(err)
	}
	rawDiskBytes, err := os.ReadFile(engine.PhysicalPath(objMeta.ID))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(rawDiskBytes, secretDoc) {
		t.Fatalf("SECURITY VIOLATION: Plaintext data was found on disk! Object is not encrypted at rest.")
	}

	// Step 4: Download and Decrypt Object
	resp = doSigned(http.MethodGet, objURL, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GetObject failed with status %d", resp.StatusCode)
	}
	downloaded, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("failed to read object body: %v", err)
	}
	if !bytes.Equal(downloaded, secretDoc) {
		t.Fatalf("decrypted object mismatch! expected %s, got %s", string(secretDoc), string(downloaded))
	}

	// Step 5: Multipart Upload with SSE-S3 Transparent Encryption
	initURL := fmt.Sprintf("%s/secure-vault/bigdata.archive?uploads", s3Server.URL)
	resp = doSigned(http.MethodPost, initURL, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("InitiateMultipart failed with status %d", resp.StatusCode)
	}
	var initRes s3.InitiateMultipartUploadResult
	_ = xml.NewDecoder(resp.Body).Decode(&initRes)
	_ = resp.Body.Close()
	uploadID := initRes.UploadId
	if uploadID == "" {
		t.Fatalf("empty upload ID returned")
	}

	// Upload 2 parts
	p1Data := bytes.Repeat([]byte("CHUNK-ONE-CONTENT-"), 100)
	p2Data := bytes.Repeat([]byte("CHUNK-TWO-CONTENT-"), 100)
	fullMultipartPlain := append(p1Data, p2Data...)

	p1URL := fmt.Sprintf("%s/secure-vault/bigdata.archive?uploadId=%s&partNumber=1", s3Server.URL, uploadID)
	resp = doSigned(http.MethodPut, p1URL, p1Data)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("UploadPart 1 failed: %d", resp.StatusCode)
	}
	p1ETag := resp.Header.Get("ETag")
	_ = resp.Body.Close()

	p2URL := fmt.Sprintf("%s/secure-vault/bigdata.archive?uploadId=%s&partNumber=2", s3Server.URL, uploadID)
	resp = doSigned(http.MethodPut, p2URL, p2Data)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("UploadPart 2 failed: %d", resp.StatusCode)
	}
	p2ETag := resp.Header.Get("ETag")
	_ = resp.Body.Close()

	// Complete Multipart
	compReq := s3.CompleteMultipartUploadRequest{
		Parts: []s3.PartInput{
			{PartNumber: 1, ETag: p1ETag},
			{PartNumber: 2, ETag: p2ETag},
		},
	}
	compXML, _ := xml.Marshal(compReq)
	compURL := fmt.Sprintf("%s/secure-vault/bigdata.archive?uploadId=%s", s3Server.URL, uploadID)
	resp = doSigned(http.MethodPost, compURL, compXML)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("CompleteMultipart failed with %d: %s", resp.StatusCode, string(body))
	}
	var compRes s3.CompleteMultipartUploadResult
	_ = xml.NewDecoder(resp.Body).Decode(&compRes)
	_ = resp.Body.Close()

	// Verify RFC-compliant ETag ends with "-2"
	if !strings.HasSuffix(strings.Trim(compRes.ETag, "\""), "-2") {
		t.Fatalf("expected RFC-compliant multipart ETag ending in '-2', got: %s", compRes.ETag)
	}

	// GET Assembled Multipart Object and Verify Decryption Integrity
	mpObjURL := fmt.Sprintf("%s/secure-vault/bigdata.archive", s3Server.URL)
	resp = doSigned(http.MethodGet, mpObjURL, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET multipart object failed: %d", resp.StatusCode)
	}
	decryptedMP, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("failed reading multipart decrypted body (decryption failed): %v", err)
	}
	if !bytes.Equal(decryptedMP, fullMultipartPlain) {
		t.Fatalf("multipart payload corrupted! length %d vs expected %d", len(decryptedMP), len(fullMultipartPlain))
	}

	// Step 6: Test Unversioned Overwrite Space Reclamation
	firstData := bytes.Repeat([]byte("FIRST-VERSION"), 500)
	testReclaimURL := fmt.Sprintf("%s/secure-vault/overwrite-me.txt", s3Server.URL)
	resp = doSigned(http.MethodPut, testReclaimURL, firstData)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initial write failed: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	firstObjMeta, err := meta.GetObject(ctx, b.ID, "overwrite-me.txt", "null")
	if err != nil {
		t.Fatal(err)
	}
	firstFilePath := engine.PhysicalPath(firstObjMeta.ID)
	if _, err := os.Stat(firstFilePath); err != nil {
		t.Fatalf("first physical file missing: %v", err)
	}

	// Overwrite with second data
	secondData := bytes.Repeat([]byte("SECOND-VERSION-OVERWRITTEN"), 600)
	resp = doSigned(http.MethodPut, testReclaimURL, secondData)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("overwrite failed: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Verify old file was reclaimed immediately from disk
	if _, err := os.Stat(firstFilePath); !os.IsNotExist(err) {
		t.Fatalf("old physical file %s was NOT reclaimed on unversioned overwrite!", firstFilePath)
	}

	// Step 7: Test SigV4 Clock Skew Rejection
	skewedReq, _ := http.NewRequest(http.MethodGet, objURL, nil)
	skewedReq.Host = strings.TrimPrefix(s3Server.URL, "http://")
	// Date set 25 minutes ago
	oldTimestamp := time.Now().UTC().Add(-25 * time.Minute)
	skewedReq.Header.Set("X-Amz-Date", oldTimestamp.Format("20060102T150405Z"))
	skewedReq.Header.Set("X-Amz-Content-Sha256", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	skewedReq.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=AKIAENTERPRISEADMINKEY/20261015/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=0000000000000000000000000000000000000000000000000000000000000000")
	resp, _ = client.Do(skewedReq)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for skewed request, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Step 8: Run Full Background Scrubber Integrity Scan
	scanRep, err := jobManager.RunIntegrityScan(ctx)
	if err != nil {
		t.Fatalf("integrity scan failed: %v", err)
	}
	if scanRep.Corrupted > 0 || scanRep.Missing > 0 {
		t.Fatalf("integrity scan found corrupt or missing objects: %+v", scanRep)
	}
	if scanRep.Passed < 2 {
		t.Fatalf("expected at least 2 objects to pass integrity scan, got %d", scanRep.Passed)
	}
}
