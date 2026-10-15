package health

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/my-s3-bucket/storage-engine/internal/metadata"
	"github.com/my-s3-bucket/storage-engine/internal/storage"
)

type StatusResponse struct {
	Status    string            `json:"status"` // "UP" or "DOWN"
	Uptime    string            `json:"uptime"`
	Timestamp time.Time         `json:"timestamp"`
	Database  string            `json:"database"` // "HEALTHY" or error
	Disk      storage.DiskUsage `json:"disk"`
}

type HealthChecker struct {
	meta      metadata.Store
	engine    storage.Engine
	startTime time.Time
	tempDir   string
}

func NewHealthChecker(meta metadata.Store, engine storage.Engine, tempDir string) *HealthChecker {
	return &HealthChecker{
		meta:      meta,
		engine:    engine,
		startTime: time.Now().UTC(),
		tempDir:   tempDir,
	}
}

func (h *HealthChecker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	resp := StatusResponse{
		Status:    "UP",
		Uptime:    time.Since(h.startTime).Truncate(time.Second).String(),
		Timestamp: time.Now().UTC(),
	}

	// 1. Check SQLite
	if err := h.meta.Ping(ctx); err != nil {
		resp.Status = "DOWN"
		resp.Database = "UNHEALTHY: " + err.Error()
	} else {
		resp.Database = "HEALTHY"
	}

	// 2. Check Disk Usage
	usage, err := h.engine.DiskUsage(ctx)
	if err != nil {
		resp.Status = "DOWN"
	}
	resp.Disk = usage

	// 3. Test Disk Writable
	testFile := filepath.Join(h.tempDir, ".health_check")
	if err := os.WriteFile(testFile, []byte("ok"), 0644); err != nil {
		resp.Status = "DOWN"
	} else {
		_ = os.Remove(testFile)
	}

	w.Header().Set("Content-Type", "application/json")
	if resp.Status == "DOWN" {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	_ = json.NewEncoder(w).Encode(resp)
}
