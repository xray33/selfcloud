package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests processed by status code and method.",
		},
		[]string{"method", "path", "status"},
	)

	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Histogram of response latency for HTTP requests.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	WebdavOpsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "webdav_operations_total",
			Help: "Total number of WebDAV protocol operations by operation type and status.",
		},
		[]string{"operation", "status"},
	)

	BlobBytesUploaded = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "blob_bytes_uploaded_total",
			Help: "Total number of bytes uploaded to storage by protocol.",
		},
		[]string{"protocol"},
	)

	BlobBytesDownloaded = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "blob_bytes_downloaded_total",
			Help: "Total number of bytes downloaded from storage by protocol.",
		},
		[]string{"protocol"},
	)
)
