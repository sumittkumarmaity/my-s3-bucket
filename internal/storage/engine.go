package storage

import (
	"context"
	"errors"
	"io"
)

// ErrInsufficientStorage is returned when a write is rejected due to low disk space.
var ErrInsufficientStorage = errors.New("insufficient storage space remaining")

// DiskUsage represents the current capacity of the physical volume.
type DiskUsage struct {
	TotalBytes int64 `json:"total_bytes"`
	UsedBytes  int64 `json:"used_bytes"`
	FreeBytes  int64 `json:"free_bytes"`
}

// Engine defines operations for interacting directly with physical object data on disk.
type Engine interface {
	// Put writes the data from reader to a physical file mapped to physicalID.
	Put(ctx context.Context, physicalID string, r io.Reader) (bytesWritten int64, err error)

	// Get returns a reader for the physical file. The caller must close it.
	Get(ctx context.Context, physicalID string) (io.ReadCloser, error)

	// GetRange returns a reader for a specific byte range [offset, offset+length).
	GetRange(ctx context.Context, physicalID string, offset, length int64) (io.ReadCloser, error)

	// Delete removes the physical file.
	Delete(ctx context.Context, physicalID string) error

	// DiskUsage returns the volume statistics.
	DiskUsage(ctx context.Context) (DiskUsage, error)

	// CheckCapacity checks whether there is sufficient storage capacity for requiredBytes.
	CheckCapacity(ctx context.Context, requiredBytes int64) error

	// Multipart operations
	PutPart(ctx context.Context, uploadID string, partNumber int, r io.Reader) (bytesWritten int64, physicalPath string, err error)
	AssembleParts(ctx context.Context, physicalID string, partPaths []string) (totalBytes int64, err error)
	DeleteMultipartUpload(ctx context.Context, uploadID string) error

	// PhysicalPath returns the absolute path on disk for a physical ID
	PhysicalPath(physicalID string) string
}
