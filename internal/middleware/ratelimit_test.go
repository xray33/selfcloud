package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIPRateLimiter(t *testing.T) {
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	t.Run("allows requests within limit and rejects excess", func(t *testing.T) {
		limiter := NewIPRateLimiter(3, 1*time.Second)
		defer limiter.Close()

		handler := limiter.LimitMiddleware(dummyHandler)

		for i := 1; i <= 3; i++ {
			req := httptest.NewRequest(http.MethodPost, "/login", nil)
			req.RemoteAddr = "192.0.2.1:1234"
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("request %d: expected 200, got %d", i, rec.Code)
			}
		}

		// 4th request from same IP should be blocked with 429
		req := httptest.NewRequest(http.MethodPost, "/login", nil)
		req.RemoteAddr = "192.0.2.1:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("expected 429 Too Many Requests, got %d", rec.Code)
		}
		if rec.Header().Get("Retry-After") == "" {
			t.Errorf("expected Retry-After header to be set")
		}

		// Request from a different IP should be allowed
		diffReq := httptest.NewRequest(http.MethodPost, "/login", nil)
		diffReq.RemoteAddr = "192.0.2.2:1234"
		diffRec := httptest.NewRecorder()
		handler.ServeHTTP(diffRec, diffReq)

		if diffRec.Code != http.StatusOK {
			t.Fatalf("expected 200 for different IP, got %d", diffRec.Code)
		}
	})

	t.Run("resets after window expires", func(t *testing.T) {
		limiter := NewIPRateLimiter(1, 50*time.Millisecond)
		defer limiter.Close()

		handler := limiter.LimitMiddleware(dummyHandler)

		req := httptest.NewRequest(http.MethodPost, "/login", nil)
		req.RemoteAddr = "192.0.2.5:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		time.Sleep(70 * time.Millisecond)

		rec2 := httptest.NewRecorder()
		handler.ServeHTTP(rec2, req)

		if rec2.Code != http.StatusOK {
			t.Fatalf("expected 200 after window reset, got %d", rec2.Code)
		}
	})

	t.Run("limit <= 0 disables rate limiting", func(t *testing.T) {
		limiter := NewIPRateLimiter(0, 1*time.Minute)
		defer limiter.Close()

		handler := limiter.LimitMiddleware(dummyHandler)

		for i := 0; i < 10; i++ {
			req := httptest.NewRequest(http.MethodPost, "/login", nil)
			req.RemoteAddr = "192.0.2.10:1234"
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200 when disabled, got %d", rec.Code)
			}
		}
	})
}
