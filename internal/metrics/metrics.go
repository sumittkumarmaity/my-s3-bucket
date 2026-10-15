package metrics

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/my-s3-bucket/storage-engine/internal/metadata"
	"github.com/my-s3-bucket/storage-engine/internal/storage"
)

type Registry struct {
	TotalRequests   atomic.Uint64
	ActiveUploads   atomic.Int64
	ErrorsCount     atomic.Uint64
	meta            metadata.Store
	engine          storage.Engine
}

var DefaultRegistry = &Registry{}

func Init(meta metadata.Store, engine storage.Engine) *Registry {
	DefaultRegistry.meta = meta
	DefaultRegistry.engine = engine
	return DefaultRegistry
}

func (r *Registry) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.TotalRequests.Add(1)
		if req.Method == http.MethodPut || req.Method == http.MethodPost {
			r.ActiveUploads.Add(1)
			defer r.ActiveUploads.Add(-1)
		}
		next.ServeHTTP(w, req)
	})
}

func (r *Registry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()

	var totalBuckets int64
	var totalObjects int64
	var diskUsage storage.DiskUsage

	if r.meta != nil {
		totalBuckets, _ = r.meta.CountBuckets(ctx)
		totalObjects, _ = r.meta.CountObjects(ctx)
	}
	if r.engine != nil {
		diskUsage, _ = r.engine.DiskUsage(ctx)
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "# HELP storage_requests_total Total number of HTTP requests\n")
	fmt.Fprintf(w, "# TYPE storage_requests_total counter\n")
	fmt.Fprintf(w, "storage_requests_total %d\n", r.TotalRequests.Load())

	fmt.Fprintf(w, "# HELP storage_active_uploads Currently active uploads\n")
	fmt.Fprintf(w, "# TYPE storage_active_uploads gauge\n")
	fmt.Fprintf(w, "storage_active_uploads %d\n", r.ActiveUploads.Load())

	fmt.Fprintf(w, "# HELP storage_buckets_total Total number of buckets\n")
	fmt.Fprintf(w, "# TYPE storage_buckets_total gauge\n")
	fmt.Fprintf(w, "storage_buckets_total %d\n", totalBuckets)

	fmt.Fprintf(w, "# HELP storage_objects_total Total number of active objects\n")
	fmt.Fprintf(w, "# TYPE storage_objects_total gauge\n")
	fmt.Fprintf(w, "storage_objects_total %d\n", totalObjects)

	fmt.Fprintf(w, "# HELP storage_disk_total_bytes Total storage capacity in bytes\n")
	fmt.Fprintf(w, "# TYPE storage_disk_total_bytes gauge\n")
	fmt.Fprintf(w, "storage_disk_total_bytes %d\n", diskUsage.TotalBytes)

	fmt.Fprintf(w, "# HELP storage_disk_used_bytes Used storage capacity in bytes\n")
	fmt.Fprintf(w, "# TYPE storage_disk_used_bytes gauge\n")
	fmt.Fprintf(w, "storage_disk_used_bytes %d\n", diskUsage.UsedBytes)

	fmt.Fprintf(w, "# HELP storage_disk_free_bytes Free storage capacity in bytes\n")
	fmt.Fprintf(w, "# TYPE storage_disk_free_bytes gauge\n")
	fmt.Fprintf(w, "storage_disk_free_bytes %d\n", diskUsage.FreeBytes)
}
