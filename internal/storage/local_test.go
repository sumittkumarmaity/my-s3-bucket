package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalDiskEngine(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage-engine-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	engine, err := NewLocalDiskEngine(tmpDir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	ctx := context.Background()
	objID := "00a1b2c3-4d5e-6f70-8192-a3b4c5d6e7f8"
	payload := []byte("Hello, S3 Storage Engine! This is a test file stream.")

	// 1. Put
	written, err := engine.Put(ctx, objID, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("failed to put object: %v", err)
	}
	if written != int64(len(payload)) {
		t.Fatalf("expected %d written, got %d", len(payload), written)
	}

	// Verify hashed path structure: objects/00/a1/00a1...
	expectedSubpath := filepath.Join("objects", "00", "a1", objID+".data")
	expectedFullpath := filepath.Join(tmpDir, expectedSubpath)
	if engine.PhysicalPath(objID) != expectedFullpath {
		t.Fatalf("expected path %s, got %s", expectedFullpath, engine.PhysicalPath(objID))
	}
	if _, err := os.Stat(expectedFullpath); err != nil {
		t.Fatalf("file not found at hashed path: %v", err)
	}

	// 2. Get
	rc, err := engine.Get(ctx, objID)
	if err != nil {
		t.Fatalf("failed to get object: %v", err)
	}
	data, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatalf("failed to read object data: %v", err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("read data does not match payload")
	}

	// 3. GetRange
	rangeRC, err := engine.GetRange(ctx, objID, 7, 10)
	if err != nil {
		t.Fatalf("failed to get range: %v", err)
	}
	rangeData, err := io.ReadAll(rangeRC)
	_ = rangeRC.Close()
	if err != nil {
		t.Fatalf("failed to read range data: %v", err)
	}
	expectedRange := payload[7 : 7+10]
	if !bytes.Equal(rangeData, expectedRange) {
		t.Fatalf("expected range %s, got %s", string(expectedRange), string(rangeData))
	}

	// 4. Multipart Part and Assembly
	uploadID := "upload-test-123"
	p1Data := []byte("Part 1: The quick brown fox ")
	p2Data := []byte("jumps over the lazy dog.")
	_, p1Path, err := engine.PutPart(ctx, uploadID, 1, bytes.NewReader(p1Data))
	if err != nil {
		t.Fatalf("failed to put part 1: %v", err)
	}
	_, p2Path, err := engine.PutPart(ctx, uploadID, 2, bytes.NewReader(p2Data))
	if err != nil {
		t.Fatalf("failed to put part 2: %v", err)
	}

	assembledID := "ff001122-3344-5566-7788-99aabbccddeeff"
	totalAssembled, err := engine.AssembleParts(ctx, assembledID, []string{p1Path, p2Path})
	if err != nil {
		t.Fatalf("failed to assemble parts: %v", err)
	}
	if totalAssembled != int64(len(p1Data)+len(p2Data)) {
		t.Fatalf("mismatched assembled length")
	}

	// Read assembled
	assRC, err := engine.Get(ctx, assembledID)
	if err != nil {
		t.Fatalf("failed to read assembled: %v", err)
	}
	assData, _ := io.ReadAll(assRC)
	_ = assRC.Close()
	expectedCombined := append(p1Data, p2Data...)
	if !bytes.Equal(assData, expectedCombined) {
		t.Fatalf("assembled content mismatch")
	}

	_ = engine.DeleteMultipartUpload(ctx, uploadID)

	// 5. DiskUsage
	usage, err := engine.DiskUsage(ctx)
	if err != nil {
		t.Fatalf("failed to get disk usage: %v", err)
	}
	if usage.TotalBytes <= 0 || usage.FreeBytes <= 0 {
		t.Fatalf("unexpected disk usage: %+v", usage)
	}

	// 6. Delete
	if err := engine.Delete(ctx, objID); err != nil {
		t.Fatalf("failed to delete object: %v", err)
	}
	_, err = engine.Get(ctx, objID)
	if err != os.ErrNotExist {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
}

func TestAdmissionControl(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage-admission-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	engine, err := NewLocalDiskEngine(tmpDir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	ctx := context.Background()

	// 1. With no thresholds configured, writes succeed
	_, err = engine.Put(ctx, "test-obj-1", bytes.NewReader([]byte("test data")))
	if err != nil {
		t.Fatalf("expected write to succeed without thresholds, got: %v", err)
	}

	// 2. Set an impossibly large MinFreeSpaceBytes (e.g. 100 Petabytes) -> must return ErrInsufficientStorage
	engine.SetAdmissionThresholds(100*1024*1024*1024*1024*1024, 0)
	_, err = engine.Put(ctx, "test-obj-2", bytes.NewReader([]byte("test data")))
	if !errors.Is(err, ErrInsufficientStorage) {
		t.Fatalf("expected ErrInsufficientStorage on Put, got: %v", err)
	}

	_, _, err = engine.PutPart(ctx, "upload-x", 1, bytes.NewReader([]byte("part data")))
	if !errors.Is(err, ErrInsufficientStorage) {
		t.Fatalf("expected ErrInsufficientStorage on PutPart, got: %v", err)
	}

	_, err = engine.AssembleParts(ctx, "test-obj-3", []string{"part-1"})
	if !errors.Is(err, ErrInsufficientStorage) {
		t.Fatalf("expected ErrInsufficientStorage on AssembleParts, got: %v", err)
	}

	// 3. Set an impossibly large MinFreeSpacePercent (e.g. 99.99%) -> must return ErrInsufficientStorage
	engine.SetAdmissionThresholds(0, 99.99)
	_, err = engine.Put(ctx, "test-obj-4", bytes.NewReader([]byte("test data")))
	if !errors.Is(err, ErrInsufficientStorage) {
		t.Fatalf("expected ErrInsufficientStorage with 99.99%% free requirement, got: %v", err)
	}

	// 4. Reset thresholds to 1 byte and 0.001% (realistic) -> writes succeed again
	engine.SetAdmissionThresholds(1, 0.001)
	_, err = engine.Put(ctx, "test-obj-5", bytes.NewReader([]byte("test data")))
	if err != nil {
		t.Fatalf("expected write to succeed with small threshold, got: %v", err)
	}
}
