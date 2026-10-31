package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"selfcloud/internal/domain"
)

type mockSessionRepo struct {
	session *domain.Session
	err     error
}

func (m *mockSessionRepo) Create(ctx context.Context, session *domain.Session) error {
	return nil
}
func (m *mockSessionRepo) GetByToken(ctx context.Context, token string) (*domain.Session, error) {
	return m.session, m.err
}
func (m *mockSessionRepo) DeleteByToken(ctx context.Context, token string) error {
	return nil
}
func (m *mockSessionRepo) DeleteByUserID(ctx context.Context, userID int64) error {
	return nil
}
func (m *mockSessionRepo) DeleteExpired(ctx context.Context) (int64, error) {
	return 0, nil
}

type mockUserRepo struct {
	user *domain.User
	err  error
}

func (m *mockUserRepo) Create(ctx context.Context, user *domain.User) error {
	return nil
}
func (m *mockUserRepo) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	return m.user, m.err
}
func (m *mockUserRepo) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	return m.user, m.err
}
func (m *mockUserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	return m.user, m.err
}
func (m *mockUserRepo) Update(ctx context.Context, user *domain.User) error {
	return nil
}
func (m *mockUserRepo) Delete(ctx context.Context, username string) error {
	return nil
}
func (m *mockUserRepo) ListAll(ctx context.Context) ([]*domain.User, error) {
	return nil, nil
}

func TestRequireSessionAuthMiddleware(t *testing.T) {
	validUser := &domain.User{
		ID:       1,
		Username: "admin",
		Email:    "admin@example.com",
	}

	validSession := &domain.Session{
		UserID:    1,
		Token:     "valid_token",
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}

	expiredSession := &domain.Session{
		UserID:    1,
		Token:     "expired_token",
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	}

	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil {
			t.Fatal("expected user in context")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	t.Run("No token returns 401", func(t *testing.T) {
		sRepo := &mockSessionRepo{}
		uRepo := &mockUserRepo{}
		mw := RequireSessionAuthMiddleware(sRepo, uRepo)(dummyHandler)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/files", nil)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rec.Code)
		}
	})

	t.Run("Expired session returns 401", func(t *testing.T) {
		sRepo := &mockSessionRepo{session: expiredSession}
		uRepo := &mockUserRepo{user: validUser}
		mw := RequireSessionAuthMiddleware(sRepo, uRepo)(dummyHandler)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/files", nil)
		req.Header.Set("Authorization", "Bearer expired_token")
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rec.Code)
		}
	})

	t.Run("Valid Bearer token succeeds", func(t *testing.T) {
		sRepo := &mockSessionRepo{session: validSession}
		uRepo := &mockUserRepo{user: validUser}
		mw := RequireSessionAuthMiddleware(sRepo, uRepo)(dummyHandler)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/files", nil)
		req.Header.Set("Authorization", "Bearer valid_token")
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rec.Code)
		}
		if rec.Body.String() != "ok" {
			t.Errorf("expected body 'ok', got %q", rec.Body.String())
		}
	})

	t.Run("Valid Cookie succeeds", func(t *testing.T) {
		sRepo := &mockSessionRepo{session: validSession}
		uRepo := &mockUserRepo{user: validUser}
		mw := RequireSessionAuthMiddleware(sRepo, uRepo)(dummyHandler)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/files", nil)
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "valid_token"})
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rec.Code)
		}
		if rec.Body.String() != "ok" {
			t.Errorf("expected body 'ok', got %q", rec.Body.String())
		}
	})

	t.Run("CSRF validation on cookie-authenticated mutating requests", func(t *testing.T) {
		sRepo := &mockSessionRepo{session: validSession}
		uRepo := &mockUserRepo{user: validUser}
		mw := RequireSessionAuthMiddleware(sRepo, uRepo)(dummyHandler)

		// 1. Cross-site Origin should be rejected with 403
		req := httptest.NewRequest(http.MethodPost, "http://example.com/api/v1/files/mkdir", nil)
		req.Host = "example.com"
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "valid_token"})
		req.Header.Set("Origin", "http://evil.com")
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("expected 403 Forbidden for cross-origin POST, got %d", rec.Code)
		}

		// 2. Same-origin Origin should succeed with 200
		req = httptest.NewRequest(http.MethodPost, "http://example.com/api/v1/files/mkdir", nil)
		req.Host = "example.com"
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "valid_token"})
		req.Header.Set("Origin", "http://example.com")
		rec = httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK for same-origin POST, got %d", rec.Code)
		}

		// 3. Localhost cross-port Origin (Vite dev server) should succeed
		req = httptest.NewRequest(http.MethodPost, "http://localhost:8080/api/v1/files/mkdir", nil)
		req.Host = "localhost:8080"
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "valid_token"})
		req.Header.Set("Origin", "http://localhost:5173")
		rec = httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK for localhost dev server Origin, got %d", rec.Code)
		}

		// 4. Sec-Fetch-Site: cross-site should be rejected
		req = httptest.NewRequest(http.MethodPost, "http://example.com/api/v1/files/mkdir", nil)
		req.Host = "example.com"
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "valid_token"})
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		rec = httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("expected 403 Forbidden for Sec-Fetch-Site cross-site, got %d", rec.Code)
		}

		// 5. Origin null should be rejected
		req = httptest.NewRequest(http.MethodPost, "http://example.com/api/v1/files/mkdir", nil)
		req.Host = "example.com"
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "valid_token"})
		req.Header.Set("Origin", "null")
		rec = httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("expected 403 Forbidden for Origin null, got %d", rec.Code)
		}

		// 6. Cross-origin Referer should be rejected
		req = httptest.NewRequest(http.MethodPost, "http://example.com/api/v1/files/mkdir", nil)
		req.Host = "example.com"
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "valid_token"})
		req.Header.Set("Referer", "http://evil.com/phishing")
		rec = httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("expected 403 Forbidden for cross-origin Referer, got %d", rec.Code)
		}

		// 7. Same-origin Referer should succeed
		req = httptest.NewRequest(http.MethodPost, "http://example.com/api/v1/files/mkdir", nil)
		req.Host = "example.com"
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "valid_token"})
		req.Header.Set("Referer", "http://example.com/files")
		rec = httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK for same-origin Referer, got %d", rec.Code)
		}

		// 8. Bearer token bypassing CSRF (API client)
		req = httptest.NewRequest(http.MethodPost, "http://example.com/api/v1/files/mkdir", nil)
		req.Host = "example.com"
		req.Header.Set("Authorization", "Bearer valid_token")
		req.Header.Set("Origin", "http://another-client.org")
		rec = httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK for Bearer token request regardless of Origin, got %d", rec.Code)
		}

		// 9. Safe GET with cross-site Origin should succeed
		req = httptest.NewRequest(http.MethodGet, "http://example.com/api/v1/files", nil)
		req.Host = "example.com"
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "valid_token"})
		req.Header.Set("Origin", "http://evil.com")
		rec = httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK for GET request even with cross-site Origin, got %d", rec.Code)
		}
	})
}

func TestBasicAuthMiddleware(t *testing.T) {
	passHash, _ := domain.HashPassword("validpassword")
	user := &domain.User{
		ID:           42,
		Username:     "bob",
		PasswordHash: passHash,
	}
	uRepo := &mockUserRepo{user: user}

	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := UserFromContext(r.Context())
		if u == nil {
			t.Errorf("expected user in context")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("basic_ok"))
	})

	mw := BasicAuthMiddleware(uRepo)(dummyHandler)

	t.Run("Missing credentials returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/remote.php/webdav", nil)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
		if rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("expected WWW-Authenticate header")
		}
	})

	t.Run("Unknown user burns dummy bcrypt and returns 401", func(t *testing.T) {
		emptyRepo := &mockUserRepo{user: nil}
		emptyMw := BasicAuthMiddleware(emptyRepo)(dummyHandler)

		req := httptest.NewRequest(http.MethodGet, "/remote.php/webdav", nil)
		req.SetBasicAuth("unknown_user", "somepassword")
		rec := httptest.NewRecorder()
		emptyMw.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("Wrong password returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/remote.php/webdav", nil)
		req.SetBasicAuth("bob", "wrongpass")
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("Valid credentials succeeds and sets context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/remote.php/webdav", nil)
		req.SetBasicAuth("bob", "validpassword")
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if rec.Body.String() != "basic_ok" {
			t.Errorf("expected basic_ok, got %s", rec.Body.String())
		}
	})
}

func TestIsSecureRequest(t *testing.T) {
	defer ConfigureSecurity(true, "auto") // restore default

	t.Run("auto with TLS returns true", func(t *testing.T) {
		ConfigureSecurity(true, "auto")
		req := httptest.NewRequest(http.MethodGet, "https://example.com/test", nil)
		if !IsSecureRequest(req) {
			t.Errorf("expected secure for TLS request")
		}
	})

	t.Run("auto with trusted proxy and X-Forwarded-Proto https returns true", func(t *testing.T) {
		ConfigureSecurity(true, "auto")
		req := httptest.NewRequest(http.MethodGet, "http://example.com/test", nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		if !IsSecureRequest(req) {
			t.Errorf("expected secure when proxy is trusted and header is https")
		}
	})

	t.Run("auto with untrusted proxy and X-Forwarded-Proto https returns false", func(t *testing.T) {
		ConfigureSecurity(false, "auto")
		req := httptest.NewRequest(http.MethodGet, "http://example.com/test", nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		if IsSecureRequest(req) {
			t.Errorf("expected insecure when proxy is not trusted despite X-Forwarded-Proto header")
		}
	})

	t.Run("explicit cookieSecure true returns true even over plain HTTP", func(t *testing.T) {
		ConfigureSecurity(false, "true")
		req := httptest.NewRequest(http.MethodGet, "http://example.com/test", nil)
		if !IsSecureRequest(req) {
			t.Errorf("expected secure when cookieSecure is forced true")
		}
	})

	t.Run("explicit cookieSecure false returns false even over TLS", func(t *testing.T) {
		ConfigureSecurity(true, "false")
		req := httptest.NewRequest(http.MethodGet, "https://example.com/test", nil)
		if IsSecureRequest(req) {
			t.Errorf("expected insecure when cookieSecure is forced false")
		}
	})
}

func TestBasicAuthMiddleware_RateLimiting(t *testing.T) {
	hash, err := domain.HashPassword("secret123")
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}

	testUser := &domain.User{
		ID:           1,
		Username:     "alice",
		PasswordHash: hash,
	}

	userRepo := &mockUserRepo{user: testUser}

	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := UserFromContext(r.Context())
		if u == nil {
			t.Errorf("expected user in context")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("authenticated"))
	})

	t.Run("successful auth allows access", func(t *testing.T) {
		auth := BasicAuthMiddleware(userRepo)(dummyHandler)

		req := httptest.NewRequest(http.MethodGet, "/remote.php/webdav", nil)
		req.SetBasicAuth("alice", "secret123")
		w := httptest.NewRecorder()

		auth.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", w.Code)
		}
	})

	t.Run("missing auth returns 401 with WWW-Authenticate", func(t *testing.T) {
		auth := BasicAuthMiddleware(userRepo)(dummyHandler)

		req := httptest.NewRequest(http.MethodGet, "/remote.php/webdav", nil)
		w := httptest.NewRecorder()

		auth.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized, got %d", w.Code)
		}
		if w.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("expected WWW-Authenticate header")
		}
	})

	t.Run("invalid credentials with rate limiter triggers 429 after threshold", func(t *testing.T) {
		limiter := NewIPRateLimiter(2, 1*time.Minute)
		defer limiter.Close()

		auth := BasicAuthMiddleware(userRepo, limiter)(dummyHandler)

		// 1st failed attempt
		req1 := httptest.NewRequest(http.MethodGet, "/remote.php/webdav", nil)
		req1.RemoteAddr = "192.0.2.55:1234"
		req1.SetBasicAuth("alice", "wrong-password")
		w1 := httptest.NewRecorder()
		auth.ServeHTTP(w1, req1)
		if w1.Code != http.StatusUnauthorized {
			t.Fatalf("attempt 1: expected 401, got %d", w1.Code)
		}

		// 2nd failed attempt (reaches limit)
		req2 := httptest.NewRequest(http.MethodGet, "/remote.php/webdav", nil)
		req2.RemoteAddr = "192.0.2.55:1234"
		req2.SetBasicAuth("alice", "wrong-password-2")
		w2 := httptest.NewRecorder()
		auth.ServeHTTP(w2, req2)
		if w2.Code != http.StatusUnauthorized {
			t.Fatalf("attempt 2: expected 401, got %d", w2.Code)
		}

		// 3rd attempt: should be blocked by rate limiter with 429
		req3 := httptest.NewRequest(http.MethodGet, "/remote.php/webdav", nil)
		req3.RemoteAddr = "192.0.2.55:1234"
		req3.SetBasicAuth("alice", "wrong-password-3")
		w3 := httptest.NewRecorder()
		auth.ServeHTTP(w3, req3)
		if w3.Code != http.StatusTooManyRequests {
			t.Fatalf("attempt 3: expected 429 Too Many Requests, got %d", w3.Code)
		}
		if w3.Header().Get("Retry-After") == "" {
			t.Errorf("expected Retry-After header on 429")
		}

		// Different IP should still be allowed to authenticate
		reqOther := httptest.NewRequest(http.MethodGet, "/remote.php/webdav", nil)
		reqOther.RemoteAddr = "192.0.2.66:1234"
		reqOther.SetBasicAuth("alice", "secret123")
		wOther := httptest.NewRecorder()
		auth.ServeHTTP(wOther, reqOther)
		if wOther.Code != http.StatusOK {
			t.Fatalf("expected 200 for other IP, got %d", wOther.Code)
		}
	})

	t.Run("valid credentials do not trigger rate limiting across multiple requests", func(t *testing.T) {
		limiter := NewIPRateLimiter(2, 1*time.Minute)
		defer limiter.Close()

		auth := BasicAuthMiddleware(userRepo, limiter)(dummyHandler)

		for i := range 5 {
			req := httptest.NewRequest(http.MethodGet, "/remote.php/webdav", nil)
			req.RemoteAddr = "192.0.2.77:1234"
			req.SetBasicAuth("alice", "secret123")
			w := httptest.NewRecorder()
			auth.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("request %d: expected 200 OK for valid sync traffic, got %d", i, w.Code)
			}
		}
	})
}
