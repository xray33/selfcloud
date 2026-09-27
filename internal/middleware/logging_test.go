package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"selfcloud/internal/audit"
)

func TestLoggingMiddleware(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := LoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := RequestIDFromContext(r.Context())
		if reqID == "" {
			t.Errorf("expected request_id in context")
		}
		intReqID := IntRequestIDFromContext(r.Context())
		if intReqID == "" {
			t.Errorf("expected int_request_id in context")
		}

		audit.Record(r.Context(), "test_action", "/test/target", "User executed test action")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello world"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/test/path", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.195, 198.51.100.1")
	req.Header.Set("X-Request-ID", "custom-req-id-12345")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Header().Get("X-Request-ID") != "custom-req-id-12345" {
		t.Errorf("expected X-Request-ID custom-req-id-12345, got %s", w.Header().Get("X-Request-ID"))
	}
	if w.Header().Get("X-Internal-Request-ID") == "" {
		t.Errorf("expected X-Internal-Request-ID to be set")
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "AUDIT: User executed test action") {
		t.Errorf("expected audit log entry in output: %s", logOutput)
	}
	if !strings.Contains(logOutput, "203.0.113.195") {
		t.Errorf("expected client IP 203.0.113.195 in log output: %s", logOutput)
	}
}

func TestGetClientIP(t *testing.T) {
	t.Run("Extracts first X-Forwarded-For IP", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Forwarded-For", "198.51.100.22, 10.0.0.1")
		if ip := getClientIP(req); ip != "198.51.100.22" {
			t.Errorf("expected 198.51.100.22, got %s", ip)
		}
	})

	t.Run("Extracts X-Real-IP when X-Forwarded-For is missing", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Real-IP", "198.51.100.33")
		if ip := getClientIP(req); ip != "198.51.100.33" {
			t.Errorf("expected 198.51.100.33, got %s", ip)
		}
	})

	t.Run("Ignores headers when proxy is not trusted", func(t *testing.T) {
		ConfigureSecurity(false, "auto")
		defer ConfigureSecurity(true, "auto")

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "192.0.2.1:12345"
		req.Header.Set("X-Forwarded-For", "198.51.100.99")
		req.Header.Set("X-Real-IP", "198.51.100.88")

		if ip := getClientIP(req); ip != "192.0.2.1" {
			t.Errorf("expected RemoteAddr host 192.0.2.1 when proxy untrusted, got %s", ip)
		}
	})
}
