package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/my-s3-bucket/storage-engine/internal/metadata"
	"github.com/my-s3-bucket/storage-engine/internal/storage"
)

func TestManagementAPI(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "api-test-*")
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

	server := NewServer(meta, eng, nil, "adminsecret")
	handler := server.Handler()

	// 1. Unauthenticated request must return 401 Unauthorized
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", w.Code)
	}

	// 2. Request with invalid token must return 401 Unauthorized
	req = httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("X-Admin-Token", "wrong-token")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong token, got %d", w.Code)
	}

	// 3. Status endpoint with valid X-Admin-Token
	req = httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("X-Admin-Token", "adminsecret")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// 4. Create Bucket with Bearer token
	bBody := []byte(`{"name": "marketing", "versioning": "ENABLED"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/buckets", bytes.NewReader(bBody))
	req.Header.Set("Authorization", "Bearer adminsecret")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// 5. List Buckets with X-Admin-Token
	req = httptest.NewRequest(http.MethodGet, "/api/v1/buckets", nil)
	req.Header.Set("X-Admin-Token", "adminsecret")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var buckets []metadata.Bucket
	_ = json.Unmarshal(w.Body.Bytes(), &buckets)
	if len(buckets) != 1 || buckets[0].Name != "marketing" {
		t.Fatalf("unexpected buckets: %+v", buckets)
	}

	// 6. Create User
	uBody := []byte(`{"username": "developer1"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewReader(uBody))
	req.Header.Set("X-Admin-Token", "adminsecret")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// 7. Create Access Key
	kBody := []byte(`{"username": "developer1"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/access-keys", bytes.NewReader(kBody))
	req.Header.Set("X-Admin-Token", "adminsecret")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var keyResp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &keyResp)
	if keyResp["access_key_id"] == nil || keyResp["secret_key"] == nil {
		t.Fatalf("missing key details: %+v", keyResp)
	}

	// 8. Delete Bucket
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/buckets/marketing", nil)
	req.Header.Set("X-Admin-Token", "adminsecret")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}
