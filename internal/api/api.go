package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/my-s3-bucket/storage-engine/internal/jobs"
	"github.com/my-s3-bucket/storage-engine/internal/metadata"
	"github.com/my-s3-bucket/storage-engine/internal/metrics"
	"github.com/my-s3-bucket/storage-engine/internal/storage"
)

type Server struct {
	meta        metadata.Store
	engine      storage.Engine
	jobManager  *jobs.Manager
	startTime   time.Time
	mux         *http.ServeMux
	adminSecret string
}

func NewServer(meta metadata.Store, engine storage.Engine, jobManager *jobs.Manager, adminSecret string) *Server {
	s := &Server{
		meta:        meta,
		engine:      engine,
		jobManager:  jobManager,
		startTime:   time.Now().UTC(),
		mux:         http.NewServeMux(),
		adminSecret: adminSecret,
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.corsMiddleware(s.authMiddleware(s.mux))
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/v1/") {
			if s.adminSecret != "" {
				token := r.Header.Get("X-Admin-Token")
				if token == "" {
					authHeader := r.Header.Get("Authorization")
					if strings.HasPrefix(authHeader, "Bearer ") {
						token = strings.TrimPrefix(authHeader, "Bearer ")
					} else if u, p, ok := r.BasicAuth(); ok {
						if p != "" {
							token = p
						} else {
							token = u
						}
					}
				}

				if subtle.ConstantTimeCompare([]byte(token), []byte(s.adminSecret)) != 1 {
					writeError(w, http.StatusUnauthorized, "Unauthorized: invalid or missing admin token")
					return
				}
			}
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Admin-Token")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/v1/status", s.handleStatus)
	s.mux.HandleFunc("GET /api/v1/metrics", s.handleMetrics)

	// Buckets
	s.mux.HandleFunc("GET /api/v1/buckets", s.handleListBuckets)
	s.mux.HandleFunc("POST /api/v1/buckets", s.handleCreateBucket)
	s.mux.HandleFunc("DELETE /api/v1/buckets/{name}", s.handleDeleteBucket)
	s.mux.HandleFunc("GET /api/v1/buckets/{name}/objects", s.handleListBucketObjects)

	// Users & Keys
	s.mux.HandleFunc("GET /api/v1/users", s.handleListUsers)
	s.mux.HandleFunc("POST /api/v1/users", s.handleCreateUser)
	s.mux.HandleFunc("GET /api/v1/access-keys", s.handleListAccessKeys)
	s.mux.HandleFunc("POST /api/v1/access-keys", s.handleCreateAccessKey)
	s.mux.HandleFunc("DELETE /api/v1/access-keys/{id}", s.handleDeleteAccessKey)

	// Distributed Cluster Nodes
	s.mux.HandleFunc("GET /api/v1/nodes", s.handleListNodes)
	s.mux.HandleFunc("POST /api/v1/nodes", s.handleRegisterNode)
	s.mux.HandleFunc("DELETE /api/v1/nodes/{id}", s.handleDeleteNode)

	// Replication Conflicts
	s.mux.HandleFunc("GET /api/v1/replication/conflicts", s.handleListConflicts)

	// Background Jobs
	s.mux.HandleFunc("POST /api/v1/jobs/gc", s.handleTriggerGC)
	s.mux.HandleFunc("POST /api/v1/jobs/integrity", s.handleTriggerIntegrityScan)
	s.mux.HandleFunc("GET /api/v1/jobs/status", s.handleJobStatus)

	// Audit Logs
	s.mux.HandleFunc("GET /api/v1/audit-logs", s.handleListAuditLogs)

	// System Backup
	s.mux.HandleFunc("POST /api/v1/system/backup", s.handleBackup)
}

func (s *Server) audit(r *http.Request, action, resource, status string) {
	user := "admin"
	if u, _, ok := r.BasicAuth(); ok && u != "" {
		user = u
	}
	ip := r.RemoteAddr
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		ip = strings.Split(fwd, ",")[0]
	}
	_ = s.meta.RecordAuditLog(r.Context(), metadata.AuditLog{
		Action:    action,
		Resource:  resource,
		UserID:    user,
		IPAddress: ip,
		Status:    status,
		Timestamp: time.Now().UTC(),
	})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	disk, err := s.engine.DiskUsage(ctx)
	if err != nil {
		disk = storage.DiskUsage{}
	}

	totalBuckets, _ := s.meta.CountBuckets(ctx)
	totalObjects, _ := s.meta.CountObjects(ctx)

	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "HEALTHY",
		"version":        "1.0.0",
		"uptime":         time.Since(s.startTime).Truncate(time.Second).String(),
		"disk":           disk,
		"total_buckets":  totalBuckets,
		"total_objects":  totalObjects,
		"active_uploads": metrics.DefaultRegistry.ActiveUploads.Load(),
		"total_requests": metrics.DefaultRegistry.TotalRequests.Load(),
	})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	disk, _ := s.engine.DiskUsage(ctx)
	buckets, _ := s.meta.CountBuckets(ctx)
	objects, _ := s.meta.CountObjects(ctx)

	writeJSON(w, http.StatusOK, map[string]any{
		"buckets_count":  buckets,
		"objects_count":  objects,
		"disk_total":     disk.TotalBytes,
		"disk_used":      disk.UsedBytes,
		"disk_free":      disk.FreeBytes,
		"requests_total": metrics.DefaultRegistry.TotalRequests.Load(),
	})
}

// -------------------------------------------------------------
// Buckets
// -------------------------------------------------------------

func (s *Server) handleListBuckets(w http.ResponseWriter, r *http.Request) {
	buckets, err := s.meta.ListBuckets(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, buckets)
}

type CreateBucketRequest struct {
	Name       string `json:"name"`
	Versioning string `json:"versioning"` // ENABLED or DISABLED
}

func (s *Server) handleCreateBucket(w http.ResponseWriter, r *http.Request) {
	var req CreateBucketRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "Bucket name is required")
		return
	}

	versioning := "DISABLED"
	if strings.ToUpper(req.Versioning) == "ENABLED" {
		versioning = "ENABLED"
	}

	b := metadata.Bucket{
		ID:               uuid.NewString(),
		Name:             req.Name,
		VersioningStatus: versioning,
		CreatedAt:        time.Now().UTC(),
	}

	if err := s.meta.CreateBucket(r.Context(), b); err != nil {
		if errors.Is(err, metadata.ErrBucketAlreadyExists) {
			writeError(w, http.StatusConflict, "Bucket already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.audit(r, "BUCKET_CREATE", b.Name, "SUCCESS")
	writeJSON(w, http.StatusCreated, b)
}

func (s *Server) handleDeleteBucket(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.meta.DeleteBucket(r.Context(), name); err != nil {
		if errors.Is(err, metadata.ErrBucketNotFound) {
			writeError(w, http.StatusNotFound, "Bucket not found")
			return
		}
		if errors.Is(err, metadata.ErrBucketNotEmpty) {
			writeError(w, http.StatusConflict, "Bucket is not empty")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "BUCKET_DELETE", name, "SUCCESS")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleListBucketObjects(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	b, err := s.meta.GetBucket(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusNotFound, "Bucket not found")
		return
	}

	prefix := r.URL.Query().Get("prefix")
	marker := r.URL.Query().Get("marker")
	objects, err := s.meta.ListObjects(r.Context(), b.ID, prefix, marker, 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, objects)
}

// -------------------------------------------------------------
// Users & Access Keys
// -------------------------------------------------------------

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.meta.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	if body.Username == "" {
		writeError(w, http.StatusBadRequest, "Username is required")
		return
	}

	u := metadata.User{
		ID:        uuid.NewString(),
		Username:  body.Username,
		CreatedAt: time.Now().UTC(),
	}

	if err := s.meta.CreateUser(r.Context(), u); err != nil {
		if errors.Is(err, metadata.ErrUserAlreadyExists) {
			writeError(w, http.StatusConflict, "User already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.audit(r, "USER_CREATE", u.Username, "SUCCESS")
	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) handleListAccessKeys(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	keys, err := s.meta.ListAccessKeys(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

func (s *Server) handleCreateAccessKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		UserID   string `json:"user_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	var user metadata.User
	var err error

	if body.UserID != "" {
		user, err = s.meta.GetUser(r.Context(), body.UserID)
	} else if body.Username != "" {
		user, err = s.meta.GetUserByUsername(r.Context(), body.Username)
	} else {
		writeError(w, http.StatusBadRequest, "Must provide username or user_id")
		return
	}

	if err != nil {
		writeError(w, http.StatusNotFound, "User not found")
		return
	}

	accessKeyID := "AKIA" + strings.ToUpper(randomHex(8))
	rawSecretKey := randomHex(20)

	ak := metadata.AccessKey{
		AccessKeyID:   accessKeyID,
		SecretKeyHash: rawSecretKey, // stored for SigV4 HMAC computation
		UserID:        user.ID,
		Status:        "ACTIVE",
		CreatedAt:     time.Now().UTC(),
	}

	if err := s.meta.CreateAccessKey(r.Context(), ak); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.audit(r, "KEY_CREATE", accessKeyID, "SUCCESS")
	// Return secret key on creation only
	writeJSON(w, http.StatusCreated, map[string]any{
		"access_key_id": accessKeyID,
		"secret_key":    rawSecretKey,
		"user_id":       user.ID,
		"status":        "ACTIVE",
		"created_at":    ak.CreatedAt,
	})
}

func (s *Server) handleDeleteAccessKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.meta.DeleteAccessKey(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "KEY_DELETE", id, "SUCCESS")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// -------------------------------------------------------------
// Cluster Nodes
// -------------------------------------------------------------

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.meta.ListNodes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, nodes)
}

func (s *Server) handleRegisterNode(w http.ResponseWriter, r *http.Request) {
	var node metadata.StorageNode
	if err := json.NewDecoder(r.Body).Decode(&node); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if node.ID == "" || node.Address == "" {
		writeError(w, http.StatusBadRequest, "Node ID and Address are required")
		return
	}
	node.Status = "ONLINE"
	node.LastHeartbeat = time.Now().UTC()
	node.CreatedAt = time.Now().UTC()

	if err := s.meta.RegisterNode(r.Context(), node); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "NODE_REGISTER", node.ID, "SUCCESS")
	writeJSON(w, http.StatusOK, node)
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.meta.DeleteNode(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "NODE_DELETE", id, "SUCCESS")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// -------------------------------------------------------------
// Replication Conflicts & Jobs
// -------------------------------------------------------------

func (s *Server) handleListConflicts(w http.ResponseWriter, r *http.Request) {
	confs, err := s.meta.ListReplicationConflicts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, confs)
}

func (s *Server) handleTriggerGC(w http.ResponseWriter, r *http.Request) {
	if s.jobManager == nil {
		writeError(w, http.StatusServiceUnavailable, "Job manager not initialized")
		return
	}
	rep, err := s.jobManager.RunGC(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "JOB_TRIGGER_GC", "gc", "SUCCESS")
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) handleTriggerIntegrityScan(w http.ResponseWriter, r *http.Request) {
	if s.jobManager == nil {
		writeError(w, http.StatusServiceUnavailable, "Job manager not initialized")
		return
	}
	rep, err := s.jobManager.RunIntegrityScan(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "JOB_TRIGGER_INTEGRITY", "integrity", "SUCCESS")
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) handleJobStatus(w http.ResponseWriter, r *http.Request) {
	if s.jobManager == nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"last_gc":        s.jobManager.LastGCReport(),
		"last_integrity": s.jobManager.LastScanReport(),
	})
}

func (s *Server) handleListAuditLogs(w http.ResponseWriter, r *http.Request) {
	logs, err := s.meta.ListAuditLogs(r.Context(), 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Destination string `json:"destination"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Destination == "" {
		req.Destination = filepath.Join("data", "backups", fmt.Sprintf("metadata-backup-%d.db", time.Now().Unix()))
	}
	cleanDest := filepath.Clean(req.Destination)
	if err := s.meta.Backup(r.Context(), cleanDest); err != nil {
		s.audit(r, "SYSTEM_BACKUP", cleanDest, "FAILED")
		writeError(w, http.StatusInternalServerError, "Backup failed: "+err.Error())
		return
	}
	s.audit(r, "SYSTEM_BACKUP", cleanDest, "SUCCESS")
	writeJSON(w, http.StatusOK, map[string]string{
		"status":      "SUCCESS",
		"destination": cleanDest,
	})
}

func randomHex(bytesLen int) string {
	b := make([]byte, bytesLen)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

