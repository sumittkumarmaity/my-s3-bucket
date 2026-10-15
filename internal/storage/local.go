package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/google/uuid"
)

const numStripedLocks = 1024

type LocalDiskEngine struct {
	root                string
	objectsDir          string
	multipartDir        string
	stripedLocks        [numStripedLocks]sync.Mutex
	minFreeSpaceBytes   int64
	minFreeSpacePercent float64
}

func NewLocalDiskEngine(root string) (*LocalDiskEngine, error) {
	objectsDir := filepath.Join(root, "objects")
	multipartDir := filepath.Join(root, "multipart")

	if err := os.MkdirAll(objectsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create objects dir: %w", err)
	}
	if err := os.MkdirAll(multipartDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create multipart dir: %w", err)
	}

	return &LocalDiskEngine{
		root:         root,
		objectsDir:   objectsDir,
		multipartDir: multipartDir,
	}, nil
}

func (e *LocalDiskEngine) SetAdmissionThresholds(minBytes int64, minPercent float64) {
	e.minFreeSpaceBytes = minBytes
	e.minFreeSpacePercent = minPercent
}

func (e *LocalDiskEngine) CheckCapacity(ctx context.Context, requiredBytes int64) error {
	if e.minFreeSpaceBytes <= 0 && e.minFreeSpacePercent <= 0 && requiredBytes <= 0 {
		return nil
	}
	usage, err := e.DiskUsage(ctx)
	if err != nil {
		return nil
	}
	if usage.TotalBytes <= 0 {
		return nil
	}
	remainingFree := usage.FreeBytes - requiredBytes
	if remainingFree < 0 {
		return ErrInsufficientStorage
	}
	if e.minFreeSpaceBytes > 0 && remainingFree < e.minFreeSpaceBytes {
		return ErrInsufficientStorage
	}
	if e.minFreeSpacePercent > 0 {
		freePercent := (float64(remainingFree) / float64(usage.TotalBytes)) * 100.0
		if freePercent < e.minFreeSpacePercent {
			return ErrInsufficientStorage
		}
	}
	return nil
}

func (e *LocalDiskEngine) checkWriteAdmission(ctx context.Context) error {
	return e.CheckCapacity(ctx, 0)
}

func fnv32a(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

func (e *LocalDiskEngine) getLock(key string) *sync.Mutex {
	idx := fnv32a(key) % numStripedLocks
	return &e.stripedLocks[idx]
}

// PhysicalPath computes the 2-level hashed directory path for an object.
// E.g. objects/00/a1/00a1...data
func (e *LocalDiskEngine) PhysicalPath(physicalID string) string {
	cleanID := physicalID
	for len(cleanID) < 4 {
		cleanID += "0"
	}
	p1 := cleanID[0:2]
	p2 := cleanID[2:4]
	return filepath.Join(e.objectsDir, p1, p2, physicalID+".data")
}

func (e *LocalDiskEngine) Put(ctx context.Context, physicalID string, r io.Reader) (int64, error) {
	if err := e.checkWriteAdmission(ctx); err != nil {
		return 0, err
	}
	mu := e.getLock(physicalID)
	mu.Lock()
	defer mu.Unlock()

	targetPath := e.PhysicalPath(physicalID)
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, fmt.Errorf("failed to create directory: %w", err)
	}

	// Write to temporary file first for atomic commit
	tmpPath := targetPath + ".tmp." + uuid.NewString()
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return 0, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath) // cleaned up if rename doesn't happen
	}()

	written, err := io.Copy(tmpFile, r)
	if err != nil {
		return 0, fmt.Errorf("failed to write object data: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		return 0, fmt.Errorf("failed to sync object data: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return 0, fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, targetPath); err != nil {
		return 0, fmt.Errorf("failed to commit object file: %w", err)
	}

	return written, nil
}

func (e *LocalDiskEngine) Get(ctx context.Context, physicalID string) (io.ReadCloser, error) {
	targetPath := e.PhysicalPath(physicalID)
	file, err := os.Open(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	return file, nil
}

type rangeReadCloser struct {
	io.Reader
	closer io.Closer
}

func (r *rangeReadCloser) Close() error {
	return r.closer.Close()
}

func (e *LocalDiskEngine) GetRange(ctx context.Context, physicalID string, offset, length int64) (io.ReadCloser, error) {
	targetPath := e.PhysicalPath(physicalID)
	file, err := os.Open(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}

	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("failed to seek: %w", err)
	}

	limited := io.LimitReader(file, length)
	return &rangeReadCloser{Reader: limited, closer: file}, nil
}

func (e *LocalDiskEngine) Delete(ctx context.Context, physicalID string) error {
	mu := e.getLock(physicalID)
	mu.Lock()
	defer mu.Unlock()

	targetPath := e.PhysicalPath(physicalID)
	err := os.Remove(targetPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (e *LocalDiskEngine) PutPart(ctx context.Context, uploadID string, partNumber int, r io.Reader) (int64, string, error) {
	if err := e.checkWriteAdmission(ctx); err != nil {
		return 0, "", err
	}
	mu := e.getLock(fmt.Sprintf("%s-part-%d", uploadID, partNumber))
	mu.Lock()
	defer mu.Unlock()

	uploadDir, err := e.validateUploadDir(uploadID)
	if err != nil {
		return 0, "", err
	}
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		return 0, "", fmt.Errorf("failed to create upload dir: %w", err)
	}

	partPath := filepath.Join(uploadDir, fmt.Sprintf("part_%d.data", partNumber))
	tmpPath := partPath + ".tmp." + uuid.NewString()

	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return 0, "", err
	}
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}()

	written, err := io.Copy(tmpFile, r)
	if err != nil {
		return 0, "", err
	}
	if err := tmpFile.Sync(); err != nil {
		return 0, "", err
	}
	if err := tmpFile.Close(); err != nil {
		return 0, "", err
	}
	if err := os.Rename(tmpPath, partPath); err != nil {
		return 0, "", err
	}

	return written, partPath, nil
}

func (e *LocalDiskEngine) AssembleParts(ctx context.Context, physicalID string, partPaths []string) (int64, error) {
	if err := e.checkWriteAdmission(ctx); err != nil {
		return 0, err
	}
	mu := e.getLock(physicalID)
	mu.Lock()
	defer mu.Unlock()

	targetPath := e.PhysicalPath(physicalID)
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, err
	}

	tmpPath := targetPath + ".tmp." + uuid.NewString()
	outFile, err := os.Create(tmpPath)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = outFile.Close()
		_ = os.Remove(tmpPath)
	}()

	var totalWritten int64
	for _, partPath := range partPaths {
		partFile, err := os.Open(partPath)
		if err != nil {
			return 0, fmt.Errorf("failed to open part %s: %w", partPath, err)
		}
		written, err := io.Copy(outFile, partFile)
		_ = partFile.Close()
		if err != nil {
			return 0, fmt.Errorf("failed to append part %s: %w", partPath, err)
		}
		totalWritten += written
	}

	if err := outFile.Sync(); err != nil {
		return 0, err
	}
	if err := outFile.Close(); err != nil {
		return 0, err
	}

	if err := os.Rename(tmpPath, targetPath); err != nil {
		return 0, err
	}

	return totalWritten, nil
}

func (e *LocalDiskEngine) validateUploadDir(uploadID string) (string, error) {
	cleanID := filepath.Clean(uploadID)
	if strings.Contains(cleanID, "..") || strings.Contains(cleanID, "/") || strings.Contains(cleanID, "\\") || cleanID == "." {
		return "", errors.New("invalid multipart upload ID: path traversal attempt")
	}
	uploadDir := filepath.Join(e.multipartDir, cleanID)
	rel, err := filepath.Rel(e.multipartDir, uploadDir)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
		return "", errors.New("invalid multipart upload ID: path escapes multipart directory")
	}
	return uploadDir, nil
}

func (e *LocalDiskEngine) DeleteMultipartUpload(ctx context.Context, uploadID string) error {
	uploadDir, err := e.validateUploadDir(uploadID)
	if err != nil {
		return err
	}
	return os.RemoveAll(uploadDir)
}

func (e *LocalDiskEngine) DiskUsage(ctx context.Context) (DiskUsage, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(e.root, &stat); err != nil {
		return DiskUsage{}, err
	}

	blockSize := int64(stat.Bsize)
	totalBytes := int64(stat.Blocks) * blockSize
	freeBytes := int64(stat.Bavail) * blockSize
	usedBytes := totalBytes - freeBytes
	if usedBytes < 0 {
		usedBytes = 0
	}

	return DiskUsage{
		TotalBytes: totalBytes,
		UsedBytes:  usedBytes,
		FreeBytes:  freeBytes,
	}, nil
}
