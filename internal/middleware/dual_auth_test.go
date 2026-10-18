package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"selfcloud/internal/config"
	"selfcloud/internal/domain"
)

type mockDualAuthSessionRepo struct {
	validToken string
	session    *domain.Session
}

func (m *mockDualAuthSessionRepo) Create(ctx context.Context, session *domain.Session) error {
	return nil
}
func (m *mockDualAuthSessionRepo) GetByToken(ctx context.Context, token string) (*domain.Session, error) {
	if token == m.validToken {
		return m.session, nil
	}
	return nil, nil
}
func (m *mockDualAuthSessionRepo) DeleteByToken(ctx context.Context, token string) error {
	return nil
}
func (m *mockDualAuthSessionRepo) DeleteByUserID(ctx context.Context, userID int64) error {
	return nil
}
func (m *mockDualAuthSessionRepo) DeleteExpired(ctx context.Context) (int64, error) {
	return 0, nil
}

func TestDualAuthMiddleware(t *testing.T) {
	hashedPassword, _ := domain.HashPassword("validpassword")
	user := &domain.User{
		ID:           1,
		Username:     "testuser",
		PasswordHash: hashedPassword,
	}

	userRepo := &mockUserRepo{user: user}
	sessionRepo := &mockDualAuthSessionRepo{
		validToken: "valid_bearer_token",
		session: &domain.Session{
			Token:     "valid_bearer_token",
			UserID:    1,
			ExpiresAt: time.Now().Add(1 * time.Hour),
		},
	}

	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := UserFromContext(r.Context())
		if u == nil {
			http.Error(w, "missing user in context", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok: " + u.Username))
	})

	mw := DualAuthMiddleware(userRepo, sessionRepo, nil)(dummyHandler)

	t.Run("Missing credentials returns 401 with dual WWW-Authenticate", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/remote.php/webdav", nil)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized, got %d", rec.Code)
		}
		authHeader := rec.Header().Get("WWW-Authenticate")
		if !strings.Contains(strings.ToLower(authHeader), "bearer ") {
			t.Errorf("expected WWW-Authenticate to contain 'bearer ', got %q", authHeader)
		}
		if !strings.Contains(strings.ToLower(authHeader), "basic ") {
			t.Errorf("expected WWW-Authenticate to contain 'basic ', got %q", authHeader)
		}
	})

	t.Run("Valid Bearer token succeeds", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/remote.php/webdav", nil)
		req.Header.Set("Authorization", "Bearer valid_bearer_token")
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", rec.Code)
		}
		if rec.Body.String() != "ok: testuser" {
			t.Errorf("expected body 'ok: testuser', got %q", rec.Body.String())
		}
	})

	t.Run("Invalid Bearer token returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/remote.php/webdav", nil)
		req.Header.Set("Authorization", "Bearer bad_token")
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized, got %d", rec.Code)
		}
	})

	t.Run("Valid Basic Auth succeeds", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/remote.php/webdav", nil)
		req.SetBasicAuth("testuser", "validpassword")
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", rec.Code)
		}
		if rec.Body.String() != "ok: testuser" {
			t.Errorf("expected body 'ok: testuser', got %q", rec.Body.String())
		}
	})

	t.Run("Invalid Basic Auth returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/remote.php/webdav", nil)
		req.SetBasicAuth("testuser", "wrongpassword")
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized, got %d", rec.Code)
		}
	})

	t.Run("Basic only configuration rejects Bearer and only advertises Basic", func(t *testing.T) {
		basicCfg := &config.Config{AuthMethods: []string{"basic"}}
		basicMw := DualAuthMiddleware(userRepo, sessionRepo, basicCfg)(dummyHandler)

		// Unauthenticated
		req := httptest.NewRequest(http.MethodGet, "/remote.php/webdav", nil)
		rec := httptest.NewRecorder()
		basicMw.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized, got %d", rec.Code)
		}
		authHeader := rec.Header().Get("WWW-Authenticate")
		if authHeader != `Basic realm="selfcloud"` {
			t.Errorf("expected only Basic realm in WWW-Authenticate, got %q", authHeader)
		}

		// Bearer attempt should be rejected
		bReq := httptest.NewRequest(http.MethodGet, "/remote.php/webdav", nil)
		bReq.Header.Set("Authorization", "Bearer valid_bearer_token")
		bRec := httptest.NewRecorder()
		basicMw.ServeHTTP(bRec, bReq)
		if bRec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized for Bearer when basic-only, got %d", bRec.Code)
		}

		// Basic attempt succeeds
		sReq := httptest.NewRequest(http.MethodGet, "/remote.php/webdav", nil)
		sReq.SetBasicAuth("testuser", "validpassword")
		sRec := httptest.NewRecorder()
		basicMw.ServeHTTP(sRec, sReq)
		if sRec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for valid Basic Auth, got %d", sRec.Code)
		}
	})

	t.Run("OAuth only configuration rejects Basic and only advertises Bearer", func(t *testing.T) {
		oauthCfg := &config.Config{AuthMethods: []string{"oauth"}}
		oauthMw := DualAuthMiddleware(userRepo, sessionRepo, oauthCfg)(dummyHandler)

		// Unauthenticated
		req := httptest.NewRequest(http.MethodGet, "/remote.php/webdav", nil)
		rec := httptest.NewRecorder()
		oauthMw.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized, got %d", rec.Code)
		}
		authHeader := rec.Header().Get("WWW-Authenticate")
		if authHeader != `Bearer realm="selfcloud"` {
			t.Errorf("expected only Bearer realm in WWW-Authenticate, got %q", authHeader)
		}

		// Basic attempt should be rejected
		bReq := httptest.NewRequest(http.MethodGet, "/remote.php/webdav", nil)
		bReq.SetBasicAuth("testuser", "validpassword")
		bRec := httptest.NewRecorder()
		oauthMw.ServeHTTP(bRec, bReq)
		if bRec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized for Basic when oauth-only, got %d", bRec.Code)
		}

		// Bearer attempt succeeds
		sReq := httptest.NewRequest(http.MethodGet, "/remote.php/webdav", nil)
		sReq.Header.Set("Authorization", "Bearer valid_bearer_token")
		sRec := httptest.NewRecorder()
		oauthMw.ServeHTTP(sRec, sReq)
		if sRec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for valid Bearer Auth, got %d", sRec.Code)
		}
	})
}
