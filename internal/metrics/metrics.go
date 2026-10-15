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

var histogramBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0}

type statusCapturingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *statusCapturingResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

type Registry struct {
	TotalRequests      atomic.Uint64
	ActiveUploads      atomic.Int64
	ErrorsCount        atomic.Uint64
	Status2xx          atomic.Uint64
	Status4xx          atomic.Uint64
	Status5xx          atomic.Uint64
	DurationBuckets    [11]atomic.Uint64
	DurationSumSeconds atomic.Uint64 // stored in microsecond units
	DurationCount      atomic.Uint64
	meta               metadata.Store
	engine             storage.Engine
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

		start := time.Now()
		rw := &statusCapturingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, req)

		durationSec := time.Since(start).Seconds()
		r.DurationCount.Add(1)
		r.DurationSumSeconds.Add(uint64(durationSec * 1e6))

		for i, b := range histogramBuckets {
			if durationSec <= b {
				r.DurationBuckets[i].Add(1)
			}
		}

		if rw.statusCode >= 200 && rw.statusCode < 300 {
			r.Status2xx.Add(1)
		} else if rw.statusCode >= 400 && rw.statusCode < 500 {
			r.Status4xx.Add(1)
			r.ErrorsCount.Add(1)
		} else if rw.statusCode >= 500 {
			r.Status5xx.Add(1)
			r.ErrorsCount.Add(1)
		}
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

	fmt.Fprintf(w, "# HELP s3_requests_total S3 HTTP requests broken down by status class\n")
	fmt.Fprintf(w, "# TYPE s3_requests_total counter\n")
	fmt.Fprintf(w, "s3_requests_total{status=\"2xx\"} %d\n", r.Status2xx.Load())
	fmt.Fprintf(w, "s3_requests_total{status=\"4xx\"} %d\n", r.Status4xx.Load())
	fmt.Fprintf(w, "s3_requests_total{status=\"5xx\"} %d\n", r.Status5xx.Load())

	fmt.Fprintf(w, "# HELP s3_errors_total Total number of HTTP client and server errors\n")
	fmt.Fprintf(w, "# TYPE s3_errors_total counter\n")
	fmt.Fprintf(w, "s3_errors_total %d\n", r.ErrorsCount.Load())

	fmt.Fprintf(w, "# HELP s3_request_duration_seconds Request duration histogram\n")
	fmt.Fprintf(w, "# TYPE s3_request_duration_seconds histogram\n")
	for i, b := range histogramBuckets {
		fmt.Fprintf(w, "s3_request_duration_seconds_bucket{le=\"%g\"} %d\n", b, r.DurationBuckets[i].Load())
	}
	fmt.Fprintf(w, "s3_request_duration_seconds_bucket{le=\"+Inf\"} %d\n", r.DurationCount.Load())
	sumSec := float64(r.DurationSumSeconds.Load()) / 1e6
	fmt.Fprintf(w, "s3_request_duration_seconds_sum %g\n", sumSec)
	fmt.Fprintf(w, "s3_request_duration_seconds_count %d\n", r.DurationCount.Load())

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
