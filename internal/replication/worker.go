package replication

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/my-s3-bucket/storage-engine/internal/metadata"
	"github.com/my-s3-bucket/storage-engine/internal/storage"
)

type Worker struct {
	meta         metadata.Store
	engine       storage.Engine
	nodeID       string
	authToken    string
	pollInterval time.Duration
	client       *http.Client
	logger       *slog.Logger
	cancelFunc   context.CancelFunc
	wg           sync.WaitGroup
}

func NewWorker(meta metadata.Store, engine storage.Engine, nodeID, authToken string, pollIntervalSec int, logger *slog.Logger) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	if pollIntervalSec <= 0 {
		pollIntervalSec = 5
	}
	return &Worker{
		meta:         meta,
		engine:       engine,
		nodeID:       nodeID,
		authToken:    authToken,
		pollInterval: time.Duration(pollIntervalSec) * time.Second,
		client:       &http.Client{Timeout: 30 * time.Second},
		logger:       logger,
	}
}

func (w *Worker) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	w.cancelFunc = cancel

	w.wg.Add(1)
	go w.runLoop(ctx)
	w.logger.Info("Replication worker started", "node_id", w.nodeID, "poll_interval", w.pollInterval)
}

func (w *Worker) Stop() {
	if w.cancelFunc != nil {
		w.cancelFunc()
	}
	w.wg.Wait()
	w.logger.Info("Replication worker stopped")
}

func (w *Worker) runLoop(ctx context.Context) {
	defer w.wg.Done()
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.processPendingJobs(ctx)
		}
	}
}

func (w *Worker) processPendingJobs(ctx context.Context) {
	jobs, err := w.meta.FetchPendingReplicationJobs(ctx, 20)
	if err != nil || len(jobs) == 0 {
		return
	}

	nodes, err := w.meta.ListNodes(ctx)
	if err != nil || len(nodes) == 0 {
		return
	}

	for _, job := range jobs {
		// Filter out self
		for _, peer := range nodes {
			if peer.ID == w.nodeID {
				continue
			}
			err := w.replicateJobToPeer(ctx, job, peer)
			if err != nil {
				w.logger.Warn("Failed to replicate job to peer",
					"job_id", job.ID, "peer", peer.Address, "err", err)
				_ = w.meta.UpdateReplicationJobStatus(ctx, job.ID, "FAILED", err.Error())
			} else {
				w.logger.Info("Successfully replicated job to peer",
					"job_id", job.ID, "peer", peer.Address)
				_ = w.meta.UpdateReplicationJobStatus(ctx, job.ID, "COMPLETED", "")
			}
		}
	}
}

func (w *Worker) replicateJobToPeer(ctx context.Context, job metadata.ReplicationJob, peer metadata.StorageNode) error {
	syncURL := fmt.Sprintf("%s/internal/replication/sync", peer.Address)

	// Fetch bucket to get name
	b, err := w.meta.GetBucketByID(ctx, job.BucketID)
	if err != nil {
		return fmt.Errorf("failed to get bucket: %w", err)
	}

	var body io.Reader
	var req *http.Request

	if job.Action == "PUT" {
		obj, err := w.meta.GetObject(ctx, job.BucketID, job.ObjectKey, job.VersionID)
		if err != nil {
			return fmt.Errorf("failed to fetch object metadata: %w", err)
		}

		rc, err := w.engine.Get(ctx, obj.ID)
		if err != nil {
			return fmt.Errorf("failed to read physical object: %w", err)
		}
		defer rc.Close()
		body = rc

		req, err = http.NewRequestWithContext(ctx, http.MethodPost, syncURL, body)
		if err != nil {
			return err
		}

		req.Header.Set("X-Replication-Token", peer.AuthToken)
		req.Header.Set("X-Replication-Bucket", b.Name)
		req.Header.Set("X-Replication-Key", job.ObjectKey)
		req.Header.Set("X-Replication-Version", job.VersionID)
		req.Header.Set("X-Replication-Action", "PUT")
		req.Header.Set("X-Replication-Timestamp", obj.CreatedAt.Format(time.RFC3339Nano))
		req.Header.Set("X-Replication-ETag", obj.ETag)
		req.Header.Set("X-Replication-CRC32C", obj.ChecksumCRC32C)
		req.Header.Set("X-Replication-Size", fmt.Sprintf("%d", obj.Size))
		req.Header.Set("X-Replication-ContentType", obj.ContentType)
	} else {
		// DELETE action
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, syncURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("X-Replication-Token", peer.AuthToken)
		req.Header.Set("X-Replication-Bucket", b.Name)
		req.Header.Set("X-Replication-Key", job.ObjectKey)
		req.Header.Set("X-Replication-Version", job.VersionID)
		req.Header.Set("X-Replication-Action", "DELETE")
		req.Header.Set("X-Replication-Timestamp", time.Now().UTC().Format(time.RFC3339Nano))
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("peer returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
