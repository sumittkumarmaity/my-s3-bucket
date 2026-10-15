package jobs

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/my-s3-bucket/storage-engine/internal/encryption"
	"github.com/my-s3-bucket/storage-engine/internal/integrity"
	"github.com/my-s3-bucket/storage-engine/internal/metadata"
	"github.com/my-s3-bucket/storage-engine/internal/storage"
)

type GCReport struct {
	StartTime          time.Time `json:"start_time"`
	Duration           string    `json:"duration"`
	OrphanPartsCleaned int       `json:"orphan_parts_cleaned"`
	OrphanFilesCleaned int       `json:"orphan_files_cleaned"`
	BytesFreed         int64     `json:"bytes_freed"`
	Errors             []string  `json:"errors"`
}

type Manager struct {
	meta             metadata.Store
	engine           storage.Engine
	storageRoot      string
	scanner          *integrity.Scanner
	logger           *slog.Logger
	gcInterval       time.Duration
	scanInterval     time.Duration
	lastGCReport     *GCReport
	lastScanReport   *integrity.ScanReport
	mu               sync.RWMutex
	cancelFunc       context.CancelFunc
	wg               sync.WaitGroup
}

func NewManager(meta metadata.Store, engine storage.Engine, storageRoot string, gcIntervalSec, scanIntervalSec int, enc encryption.Provider, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	if enc == nil {
		enc = &encryption.NoOpProvider{}
	}
	if gcIntervalSec <= 0 {
		gcIntervalSec = 3600
	}
	if scanIntervalSec <= 0 {
		scanIntervalSec = 86400
	}

	return &Manager{
		meta:         meta,
		engine:       engine,
		storageRoot:  storageRoot,
		scanner:      integrity.NewScanner(meta, engine, enc, logger),
		logger:       logger,
		gcInterval:   time.Duration(gcIntervalSec) * time.Second,
		scanInterval: time.Duration(scanIntervalSec) * time.Second,
	}
}

func (m *Manager) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelFunc = cancel

	m.wg.Add(2)
	go m.runGCLoop(ctx)
	go m.runScanLoop(ctx)
	m.logger.Info("Background job manager started", "gc_interval", m.gcInterval, "scan_interval", m.scanInterval)
}

func (m *Manager) Stop() {
	if m.cancelFunc != nil {
		m.cancelFunc()
	}
	m.wg.Wait()
	m.logger.Info("Background job manager stopped")
}

func (m *Manager) runGCLoop(ctx context.Context) {
	defer m.wg.Done()
	ticker := time.NewTicker(m.gcInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = m.RunGC(ctx)
		}
	}
}

func (m *Manager) runScanLoop(ctx context.Context) {
	defer m.wg.Done()
	ticker := time.NewTicker(m.scanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = m.RunIntegrityScan(ctx)
		}
	}
}

// RunGC executes garbage collection of orphaned files and temporary multipart data.
func (m *Manager) RunGC(ctx context.Context) (*GCReport, error) {
	m.logger.Info("Starting garbage collection cycle...")
	start := time.Now().UTC()

	report := &GCReport{
		StartTime: start,
		Errors:    []string{},
	}

	// 1. Clean up multipart uploads older than 24 hours
	multipartDir := filepath.Join(m.storageRoot, "multipart")
	if entries, err := os.ReadDir(multipartDir); err == nil {
		cutoff := time.Now().Add(-24 * time.Hour)
		for _, e := range entries {
			if e.IsDir() {
				uploadPath := filepath.Join(multipartDir, e.Name())
				if fi, err := os.Stat(uploadPath); err == nil && fi.ModTime().Before(cutoff) {
					// Check if still active in DB
					if _, err := m.meta.GetMultipartUpload(ctx, e.Name()); errorsIsNotFound(err) {
						_ = os.RemoveAll(uploadPath)
						report.OrphanPartsCleaned++
						m.logger.Info("Cleaned up orphaned multipart upload folder", "upload_id", e.Name())
					}
				}
			}
		}
	}

	// 2. Scan physical object files, clean orphaned temporary files (.tmp), and clean unreferenced .data files
	objectsDir := filepath.Join(m.storageRoot, "objects")
	cutoffTmp := time.Now().Add(-1 * time.Hour)
	cutoffData := time.Now().Add(-10 * time.Minute)

	activeObjects, err := m.meta.GetAllObjects(ctx)
	activeIDs := make(map[string]struct{})
	hasActiveSet := (err == nil)
	if hasActiveSet {
		for _, o := range activeObjects {
			if !o.IsDeleteMarker {
				activeIDs[o.ID] = struct{}{}
			}
		}
	}

	_ = filepath.WalkDir(objectsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}

		name := d.Name()
		// Clean stale .tmp files
		if strings.Contains(name, ".tmp.") {
			if fi, err := d.Info(); err == nil && fi.ModTime().Before(cutoffTmp) {
				report.BytesFreed += fi.Size()
				_ = os.Remove(path)
				report.OrphanFilesCleaned++
			}
			return nil
		}

		// Clean unreferenced .data files
		if strings.HasSuffix(name, ".data") && hasActiveSet {
			objectID := strings.TrimSuffix(name, ".data")
			if _, active := activeIDs[objectID]; !active {
				if fi, err := d.Info(); err == nil && fi.ModTime().Before(cutoffData) {
					report.BytesFreed += fi.Size()
					_ = os.Remove(path)
					report.OrphanFilesCleaned++
					m.logger.Info("Cleaned unreferenced orphaned object file", "path", path, "object_id", objectID)
				}
			}
		}

		return nil
	})

	report.Duration = time.Since(start).String()
	m.logger.Info("Garbage collection cycle completed",
		"orphan_parts", report.OrphanPartsCleaned,
		"orphan_files", report.OrphanFilesCleaned,
		"bytes_freed", report.BytesFreed,
		"duration", report.Duration,
	)

	m.mu.Lock()
	m.lastGCReport = report
	m.mu.Unlock()

	return report, nil
}

// RunIntegrityScan executes an immediate scan of all stored objects.
func (m *Manager) RunIntegrityScan(ctx context.Context) (*integrity.ScanReport, error) {
	rep, err := m.scanner.RunScan(ctx)
	if err == nil {
		m.mu.Lock()
		m.lastScanReport = rep
		m.mu.Unlock()
	}
	return rep, err
}

func (m *Manager) LastGCReport() *GCReport {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastGCReport
}

func (m *Manager) LastScanReport() *integrity.ScanReport {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastScanReport
}

func errorsIsNotFound(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "not found") || err == metadata.ErrUploadNotFound)
}
