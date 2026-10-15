package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsPrometheusExport(t *testing.T) {
	reg := &Registry{}

	handler := reg.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	// Execute normal request
	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)

	// Execute failing request
	req2 := httptest.NewRequest(http.MethodGet, "/fail", nil)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)

	// Check metrics output
	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsW := httptest.NewRecorder()
	reg.ServeHTTP(metricsW, metricsReq)

	body := metricsW.Body.String()

	if !strings.Contains(body, "storage_requests_total 2") {
		t.Errorf("expected storage_requests_total 2, body: %s", body)
	}
	if !strings.Contains(body, "s3_requests_total{status=\"2xx\"} 1") {
		t.Errorf("expected 2xx count 1, body: %s", body)
	}
	if !strings.Contains(body, "s3_requests_total{status=\"5xx\"} 1") {
		t.Errorf("expected 5xx count 1, body: %s", body)
	}
	if !strings.Contains(body, "s3_request_duration_seconds_bucket") {
		t.Errorf("missing duration histogram buckets, body: %s", body)
	}
}
