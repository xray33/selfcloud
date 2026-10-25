package middleware

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"selfcloud/internal/audit"
)

type contextKey string

const (
	RequestIDKey    contextKey = "request_id"
	IntRequestIDKey contextKey = "int_request_id"
	UserKey         contextKey = "user"
)

type responseWriterWrapper struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
}

func (rw *responseWriterWrapper) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriterWrapper) Write(b []byte) (int, error) {
	if rw.statusCode == 0 {
		rw.statusCode = http.StatusOK
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += int64(n)
	return n, err
}

func (rw *responseWriterWrapper) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

func (rw *responseWriterWrapper) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rw *responseWriterWrapper) StatusCode() int {
	if rw.statusCode == 0 {
		return http.StatusOK
	}
	return rw.statusCode
}

func generateRequestID() string {
	var b [8]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func getClientIP(r *http.Request) string {
	return GetClientIP(r)
}

func getTrustedProxyIP(r *http.Request) string {
	if !IsProxyTrusted() {
		return ""
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if clientIP := strings.TrimSpace(parts[0]); clientIP != "" {
			return clientIP
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	return ""
}

// GetClientIP resolves the client IP address, only respecting proxy headers if trusted.
func GetClientIP(r *http.Request) string {
	if ip := getTrustedProxyIP(r); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func RequestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(RequestIDKey).(string); ok {
		return id
	}
	return ""
}

func IntRequestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(IntRequestIDKey).(string); ok {
		return id
	}
	return ""
}

func LoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			reqID := r.Header.Get("X-Request-ID")
			if reqID == "" {
				reqID = generateRequestID()
			}
			w.Header().Set("X-Request-ID", reqID)

			intReqID := generateRequestID()
			w.Header().Set("X-Internal-Request-ID", intReqID)

			ctx := context.WithValue(r.Context(), RequestIDKey, reqID)
			ctx = context.WithValue(ctx, IntRequestIDKey, intReqID)

			// Attach an empty audit event collector to the context.
			// Handlers call audit.Record to annotate it; we emit it below.
			ctx, _ = audit.Attach(ctx)

			r = r.WithContext(ctx)

			wrapper := &responseWriterWrapper{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			next.ServeHTTP(wrapper, r)

			duration := time.Since(start)

			// Username is written into *Event by BasicAuthMiddleware via audit.SetUsername.
			// Reading from the *Event pointer is the only reliable way to get it back here,
			// because auth sets UserKey on a child context that doesn't propagate upward.
			username := "-"
			ev := audit.FromContext(ctx)
			if ev != nil && ev.Username != "" {
				username = ev.Username
			}

			// Audit log first: the meaningful "what happened" entry.
			if ev != nil && ev.Action != "" {
				level := slog.LevelInfo
				if ev.Failed {
					level = slog.LevelWarn
				}

				baseAttrs := []slog.Attr{
					slog.String("request_id", reqID),
					slog.String("int_request_id", intReqID),
					slog.String("username", username),
					slog.String("action", ev.Action),
					slog.String("target", ev.Target),
					slog.Bool("failed", ev.Failed),
				}
				allAttrs := append(baseAttrs, ev.Attrs...) //nolint:gocritic // intentional append to new slice

				logger.LogAttrs(r.Context(), level,
					fmt.Sprintf("AUDIT: %s", ev.Summary),
					allAttrs...,
				)
			}

			// Access log second: the terse traffic entry for dashboards/alerting.
			logger.Info("HTTP request processed",
				slog.String("request_id", reqID),
				slog.String("int_request_id", intReqID),
				slog.String("method", r.Method),
				slog.String("uri", r.RequestURI),
				slog.Int("status", wrapper.statusCode),
				slog.Int64("bytes_written", wrapper.bytesWritten),
				slog.Duration("duration", duration),
				slog.String("remote_addr", getClientIP(r)),
				slog.String("user_agent", r.UserAgent()),
				slog.String("username", username),
			)
		})
	}
}
