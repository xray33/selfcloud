package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"selfcloud/internal/config"
)

func TestSSOHandler(t *testing.T) {
	cfg := &config.Config{
		SSOGoogleClientID:     "test-client-id",
		SSOGoogleClientSecret: "test-client-secret",
		SSORedirectBaseURL:    "http://localhost:8080",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewSSOHandler(cfg, nil, nil, nil, nil, nil, logger)

	t.Run("GoogleLoginHandler sets cookie and redirects", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/google/login", nil)
		w := httptest.NewRecorder()

		handler.GoogleLoginHandler(w, req)

		if w.Code != http.StatusFound {
			t.Fatalf("expected 302 Found, got %d", w.Code)
		}

		cookies := w.Result().Cookies()
		foundState := false
		for _, c := range cookies {
			if c.Name == "oauth_state" && c.Value != "" {
				foundState = true
				break
			}
		}
		if !foundState {
			t.Fatalf("expected oauth_state cookie to be set")
		}
	})

	t.Run("GoogleCallbackHandler rejects state mismatch", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/google/callback?state=wrong_state&code=test_code", nil)
		req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "correct_state"})
		w := httptest.NewRecorder()

		handler.GoogleCallbackHandler(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request on state mismatch, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("GoogleCallbackHandler rejects missing oauth_state cookie", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/google/callback?state=some_state&code=test_code", nil)
		w := httptest.NewRecorder()

		handler.GoogleCallbackHandler(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request on missing cookie, got %d", w.Code)
		}
	})

	t.Run("GoogleCallbackHandler rejects when SSO not configured", func(t *testing.T) {
		unconfigured := NewSSOHandler(&config.Config{}, nil, nil, nil, nil, nil, logger)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/google/callback", nil)
		w := httptest.NewRecorder()

		unconfigured.GoogleCallbackHandler(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request when not configured, got %d", w.Code)
		}
	})
}

