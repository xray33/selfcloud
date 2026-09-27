package metrics

import (
	"testing"
)

func TestMetricsRegistration(t *testing.T) {
	HTTPRequestsTotal.WithLabelValues("GET", "/test", "200").Inc()
	HTTPRequestDuration.WithLabelValues("GET", "/test").Observe(0.012)
	WebdavOpsTotal.WithLabelValues("PROPFIND", "attempt").Inc()
	BlobBytesUploaded.WithLabelValues("webdav").Add(1024)
	BlobBytesDownloaded.WithLabelValues("webdav").Add(2048)
	BlobBytesUploaded.WithLabelValues("api").Add(4096)
	BlobBytesDownloaded.WithLabelValues("api").Add(8192)
	BlobBytesDownloaded.WithLabelValues("share").Add(512)
}
