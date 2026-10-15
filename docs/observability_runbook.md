# Enterprise Observability, Prometheus Metrics & Alerting Runbook

## 1. Metrics Endpoint Overview

The storage engine exposes real-time Prometheus metrics on `http://<host>:9001/metrics`. Scrapes occur over standard HTTP/HTTPS with standard Prometheus text formatting (`text/plain; version=0.0.4`).

---

## 2. Metrics Reference Dictionary

| Metric Name | Type | Description | Operational Significance |
|---|---|---|---|
| `storage_requests_total` | Counter | Total count of HTTP requests processed | Baseline traffic rate monitoring |
| `s3_requests_total{status="2xx"}` | Counter | Successful S3 operations | SLA calculation |
| `s3_requests_total{status="4xx"}` | Counter | Client errors (403, 404, 400) | Auth failures or missing keys |
| `s3_requests_total{status="5xx"}` | Counter | Server-side errors (500, 507) | Critical operational alerts |
| `s3_errors_total` | Counter | Cumulative 4xx and 5xx error counter | Error rate calculation |
| `s3_request_duration_seconds_bucket` | Histogram | Request latency histogram across 11 buckets | p50, p95, p99 latency evaluation |
| `s3_request_duration_seconds_sum` | Counter | Total time spent in S3 requests | Average latency computation |
| `s3_request_duration_seconds_count` | Counter | Count of duration observations | Latency denominator |
| `storage_active_uploads` | Gauge | Number of upload streams currently active | Concurrency load monitoring |
| `storage_buckets_total` | Gauge | Total registered buckets | Metadata capacity |
| `storage_objects_total` | Gauge | Total active objects in cluster | Inventory tracking |
| `storage_disk_total_bytes` | Gauge | Total capacity of storage filesystem | Volume dimensioning |
| `storage_disk_used_bytes` | Gauge | Bytes currently consumed on disk | Storage consumption rate |
| `storage_disk_free_bytes` | Gauge | Bytes available before disk saturation | Capacity depletion alerting |

---

## 3. Prometheus Alerting Rules (`storage_alerts.yml`)

```yaml
groups:
  - name: storage_engine_alerts
    rules:
      - alert: S3NodeDiskCriticallyLow
        expr: (storage_disk_free_bytes / storage_disk_total_bytes) * 100 < 10
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "Storage node free disk space is below 10%"
          description: "Node {{ $labels.instance }} has less than 10% disk space remaining. Admission control will begin rejecting writes."

      - alert: S3HighServerErrorRate
        expr: rate(s3_requests_total{status="5xx"}[5m]) / rate(storage_requests_total[5m]) > 0.05
        for: 2m
        labels:
          severity: critical
        annotations:
          summary: "S3 5xx server error rate exceeds 5%"
          description: "S3 cluster is reporting elevated server errors on instance {{ $labels.instance }}."

      - alert: S3ElevatedP99Latency
        expr: histogram_quantile(0.99, sum(rate(s3_request_duration_seconds_bucket[5m])) by (le)) > 2.0
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "S3 p99 latency exceeds 2 seconds"
          description: "99th percentile of S3 requests is taking longer than 2s on {{ $labels.instance }}."
```
