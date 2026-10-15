package integrity

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/my-s3-bucket/storage-engine/internal/metadata"
	"github.com/my-s3-bucket/storage-engine/internal/storage"
)

type ScanReport struct {
	TotalChecked int       `json:"total_checked"`
	Corrupted    int       `json:"corrupted"`
	Missing      int       `json:"missing"`
	Passed       int       `json:"passed"`
	StartTime    time.Time `json:"start_time"`
	Duration     string    `json:"duration"`
	Errors       []string  `json:"errors"`
}

type Scanner struct {
	meta   metadata.Store
	engine storage.Engine
	logger *slog.Logger
}

func NewScanner(meta metadata.Store, engine storage.Engine, logger *slog.Logger) *Scanner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scanner{
		meta:   meta,
		engine: engine,
		logger: logger,
	}
}

// RunScan checks all objects currently registered in metadata against physical files on disk.
func (s *Scanner) RunScan(ctx context.Context) (*ScanReport, error) {
	start := time.Now()
	s.logger.Info("Starting object integrity scan...")

	objects, err := s.meta.GetAllObjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch objects for scan: %w", err)
	}

	report := &ScanReport{
		TotalChecked: len(objects),
		StartTime:    start,
		Errors:       []string{},
	}

	for _, obj := range objects {
		select {
		case <-ctx.Done():
			return report, ctx.Err()
		default:
		}

		if obj.IsDeleteMarker {
			report.Passed++
			continue
		}

		rc, err := s.engine.Get(ctx, obj.ID)
		if err != nil {
			report.Missing++
			msg := fmt.Sprintf("Object %s (key: %s) is missing from disk: %v", obj.ID, obj.ObjectKey, err)
			s.logger.Warn(msg)
			report.Errors = append(report.Errors, msg)
			continue
		}

		crc, _, _, bytesRead, err := ComputeChecksums(rc)
		_ = rc.Close()
		if err != nil {
			report.Corrupted++
			msg := fmt.Sprintf("Error reading object %s (key: %s): %v", obj.ID, obj.ObjectKey, err)
			s.logger.Error(msg)
			report.Errors = append(report.Errors, msg)
			continue
		}

		if bytesRead != obj.Size {
			report.Corrupted++
			msg := fmt.Sprintf("Size mismatch on object %s: DB=%d, Disk=%d", obj.ID, obj.Size, bytesRead)
			s.logger.Error(msg)
			report.Errors = append(report.Errors, msg)
			continue
		}

		if obj.ChecksumCRC32C != "" && crc != obj.ChecksumCRC32C {
			report.Corrupted++
			msg := fmt.Sprintf("CRC32C mismatch on object %s: expected %s, computed %s", obj.ID, obj.ChecksumCRC32C, crc)
			s.logger.Error(msg)
			report.Errors = append(report.Errors, msg)
			continue
		}

		report.Passed++
	}

	report.Duration = time.Since(start).String()
	s.logger.Info("Integrity scan complete",
		"total", report.TotalChecked,
		"passed", report.Passed,
		"missing", report.Missing,
		"corrupted", report.Corrupted,
		"duration", report.Duration,
	)

	return report, nil
}
