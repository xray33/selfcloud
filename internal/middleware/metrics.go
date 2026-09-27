package middleware

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"selfcloud/internal/metrics"
)

type statusTracker interface {
	StatusCode() int
}

func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		tracker, ok := w.(statusTracker)
		if !ok {
			rw := &responseWriterWrapper{ResponseWriter: w, statusCode: http.StatusOK}
			w = rw
			tracker = rw
		}

		next.ServeHTTP(w, r)

		duration := time.Since(start).Seconds()
		statusStr := strconv.Itoa(tracker.StatusCode())

		// r.Pattern in Go 1.22+ ServeMux includes the registered method prefix,
		// e.g. "GET /status.php". Strip the method so the label is just the path.
		metricPath := r.Pattern
		if metricPath == "" {
			metricPath = "other"
		} else if i := strings.IndexByte(metricPath, ' '); i >= 0 {
			metricPath = metricPath[i+1:]
		}

		metrics.HTTPRequestsTotal.WithLabelValues(r.Method, metricPath, statusStr).Inc()
		metrics.HTTPRequestDuration.WithLabelValues(r.Method, metricPath).Observe(duration)
	})
}
