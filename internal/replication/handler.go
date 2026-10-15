package replication

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/my-s3-bucket/storage-engine/internal/metadata"
	"github.com/my-s3-bucket/storage-engine/internal/storage"
)

type SyncHandler struct {
	meta      metadata.Store
	engine    storage.Engine
	authToken string
	logger    *slog.Logger
}

func NewSyncHandler(meta metadata.Store, engine storage.Engine, authToken string, logger *slog.Logger) *SyncHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &SyncHandler{
		meta:      meta,
		engine:    engine,
		authToken: authToken,
		logger:    logger,
	}
}

// ServeHTTP handles /internal/replication/sync requests
func (h *SyncHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("X-Replication-Token")
	if token != h.authToken {
		http.Error(w, "Unauthorized replication token", http.StatusUnauthorized)
		return
	}

	bucketName := r.Header.Get("X-Replication-Bucket")
	objectKey := r.Header.Get("X-Replication-Key")
	versionID := r.Header.Get("X-Replication-Version")
	action := r.Header.Get("X-Replication-Action")
	tsStr := r.Header.Get("X-Replication-Timestamp")
	remoteTS, _ := time.Parse(time.RFC3339Nano, tsStr)

	ctx := r.Context()

	// Ensure bucket exists or create it
	b, err := h.meta.GetBucket(ctx, bucketName)
	if err != nil {
		if errors.Is(err, metadata.ErrBucketNotFound) {
			b = metadata.Bucket{
				ID:               uuid.NewString(),
				Name:             bucketName,
				VersioningStatus: "DISABLED",
				CreatedAt:        time.Now().UTC(),
			}
			_ = h.meta.CreateBucket(ctx, b)
		} else {
			http.Error(w, "Failed to get/create bucket: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if action == "DELETE" {
		_, _ = h.meta.DeleteObject(ctx, b.ID, objectKey, versionID)
		w.WriteHeader(http.StatusOK)
		return
	}

	if action == "PUT" {
		// Conflict check: Check if local object exists
		localObj, err := h.meta.GetObject(ctx, b.ID, objectKey, versionID)
		if err == nil {
			// Object already exists: check Last-Write-Wins
			if localObj.CreatedAt.After(remoteTS) {
				// Local is newer: reject remote update, record conflict
				h.logger.Warn("Replication conflict detected: local version is newer, keeping local",
					"bucket", bucketName, "key", objectKey, "local_ts", localObj.CreatedAt, "remote_ts", remoteTS)

				_ = h.meta.RecordReplicationConflict(ctx, metadata.ReplicationConflict{
					BucketName:      bucketName,
					ObjectKey:       objectKey,
					RemoteVersionID: versionID,
					LocalVersionID:  localObj.VersionID,
					Resolution:      "LOCAL_KEPT",
					DetectedAt:      time.Now().UTC(),
				})

				// Return 200 so remote doesn't endlessly retry
				w.WriteHeader(http.StatusOK)
				return
			} else {
				// Remote is newer: record conflict that remote was applied
				_ = h.meta.RecordReplicationConflict(ctx, metadata.ReplicationConflict{
					BucketName:      bucketName,
					ObjectKey:       objectKey,
					RemoteVersionID: versionID,
					LocalVersionID:  localObj.VersionID,
					Resolution:      "REMOTE_APPLIED",
					DetectedAt:      time.Now().UTC(),
				})
			}
		}

		// Write object data to disk
		objectID := uuid.NewString()
		sizeStr := r.Header.Get("X-Replication-Size")
		size, _ := strconv.ParseInt(sizeStr, 10, 64)
		etag := r.Header.Get("X-Replication-ETag")
		crc32c := r.Header.Get("X-Replication-CRC32C")
		contentType := r.Header.Get("X-Replication-ContentType")
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		_, err = h.engine.Put(ctx, objectID, r.Body)
		if err != nil {
			http.Error(w, "Failed to write data: "+err.Error(), http.StatusInternalServerError)
			return
		}

		newObj := metadata.Object{
			ID:             objectID,
			BucketID:       b.ID,
			ObjectKey:      objectKey,
			VersionID:      versionID,
			Size:           size,
			ETag:           etag,
			ChecksumCRC32C: crc32c,
			ContentType:    contentType,
			CreatedAt:      remoteTS,
		}

		if err := h.meta.PutObject(ctx, newObj); err != nil {
			_ = h.engine.Delete(ctx, objectID)
			http.Error(w, "Failed to commit metadata: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		return
	}

	http.Error(w, "Unknown replication action", http.StatusBadRequest)
}

type NodeRegisterRequest struct {
	ID        string `json:"id"`
	Address   string `json:"address"`
	AuthToken string `json:"auth_token"`
}

func HandleNodeRegister(meta metadata.Store, authToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Replication-Token")
		if token != authToken {
			http.Error(w, "Unauthorized replication token", http.StatusUnauthorized)
			return
		}

		var req NodeRegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid body: "+err.Error(), http.StatusBadRequest)
			return
		}

		node := metadata.StorageNode{
			ID:            req.ID,
			Address:       req.Address,
			AuthToken:     req.AuthToken,
			Status:        "ONLINE",
			LastHeartbeat: time.Now().UTC(),
			CreatedAt:     time.Now().UTC(),
		}

		if err := meta.RegisterNode(r.Context(), node); err != nil {
			http.Error(w, "Failed to register node: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "registered"})
	}
}
