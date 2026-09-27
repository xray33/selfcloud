package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"selfcloud/internal/domain"
	"selfcloud/internal/middleware"
)

type mockUserRepo struct {
	users map[string]*domain.User
}

func (m *mockUserRepo) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	if u, ok := m.users[username]; ok {
		return u, nil
	}
	return nil, nil
}

func (m *mockUserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	for _, u := range m.users {
		if u.Email == email {
			return u, nil
		}
	}
	return nil, nil
}

func (m *mockUserRepo) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	for _, u := range m.users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, nil
}

func (m *mockUserRepo) Create(ctx context.Context, user *domain.User) error {
	m.users[user.Username] = user
	return nil
}

func (m *mockUserRepo) Update(ctx context.Context, user *domain.User) error {
	m.users[user.Username] = user
	return nil
}

func (m *mockUserRepo) ListAll(ctx context.Context) ([]*domain.User, error) {
	var list []*domain.User
	for _, u := range m.users {
		list = append(list, u)
	}
	return list, nil
}

func (m *mockUserRepo) Delete(ctx context.Context, username string) error {
	delete(m.users, username)
	return nil
}

type mockSessionRepo struct {
	sessions map[string]*domain.Session
}

func (m *mockSessionRepo) Create(ctx context.Context, session *domain.Session) error {
	m.sessions[session.Token] = session
	return nil
}

func (m *mockSessionRepo) GetByToken(ctx context.Context, token string) (*domain.Session, error) {
	if s, ok := m.sessions[token]; ok {
		return s, nil
	}
	return nil, errors.New("not found")
}

func (m *mockSessionRepo) DeleteByToken(ctx context.Context, token string) error {
	delete(m.sessions, token)
	return nil
}

func (m *mockSessionRepo) DeleteByUserID(ctx context.Context, userID int64) error {
	for t, s := range m.sessions {
		if s.UserID == userID {
			delete(m.sessions, t)
		}
	}
	return nil
}

func (m *mockSessionRepo) DeleteExpired(ctx context.Context) (int64, error) {
	var count int64
	for t, s := range m.sessions {
		if s.IsExpired() {
			delete(m.sessions, t)
			count++
		}
	}
	return count, nil
}

func TestAuthHandler(t *testing.T) {
	passHash, _ := domain.HashPassword("secretpass")
	testUser := &domain.User{
		ID:           1,
		Username:     "alice",
		PasswordHash: passHash,
		Email:        "alice@example.com",
		CreatedAt:    time.Now(),
	}

	userRepo := &mockUserRepo{users: map[string]*domain.User{"alice": testUser}}
	sessionRepo := &mockSessionRepo{sessions: make(map[string]*domain.Session)}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewAuthHandler(userRepo, sessionRepo, logger)

	t.Run("LoginHandler with valid credentials", func(t *testing.T) {
		body, _ := json.Marshal(LoginRequest{Username: "alice", Password: "secretpass"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
		w := httptest.NewRecorder()

		handler.LoginHandler(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}

		var res LoginResponse
		if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if res.User.Username != "alice" {
			t.Errorf("expected username alice, got %s", res.User.Username)
		}
		if res.Token == "" {
			t.Errorf("expected non-empty session token")
		}

		cookies := w.Result().Cookies()
		foundSession := false
		for _, c := range cookies {
			if c.Name == middleware.SessionCookieName && c.Value == res.Token {
				foundSession = true
				if !c.HttpOnly {
					t.Errorf("expected HttpOnly cookie")
				}
				break
			}
		}
		if !foundSession {
			t.Errorf("expected session cookie to be set")
		}
	})

	t.Run("LoginHandler with unknown user executes dummy check and returns 401", func(t *testing.T) {
		body, _ := json.Marshal(LoginRequest{Username: "nonexistent", Password: "wrongpassword"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
		w := httptest.NewRecorder()

		handler.LoginHandler(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("LoginHandler with wrong password returns 401", func(t *testing.T) {
		body, _ := json.Marshal(LoginRequest{Username: "alice", Password: "wrongpassword"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
		w := httptest.NewRecorder()

		handler.LoginHandler(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("LoginHandler rejects non-POST methods", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/login", nil)
		w := httptest.NewRecorder()

		handler.LoginHandler(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405 Method Not Allowed, got %d", w.Code)
		}
	})

	t.Run("LogoutHandler removes session and clears cookie", func(t *testing.T) {
		sess := &domain.Session{Token: "test_token", UserID: 1, ExpiresAt: time.Now().Add(time.Hour)}
		_ = sessionRepo.Create(context.Background(), sess)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "test_token"})
		w := httptest.NewRecorder()

		handler.LogoutHandler(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", w.Code)
		}

		if _, err := sessionRepo.GetByToken(context.Background(), "test_token"); err == nil {
			t.Errorf("expected session to be deleted from repo")
		}

		cookies := w.Result().Cookies()
		foundCleared := false
		for _, c := range cookies {
			if c.Name == middleware.SessionCookieName && c.MaxAge == -1 {
				foundCleared = true
				break
			}
		}
		if !foundCleared {
			t.Errorf("expected session cookie to be cleared with MaxAge=-1")
		}
	})

	t.Run("MeHandler returns authenticated user info", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
		req = req.WithContext(middleware.WithUser(req.Context(), testUser))
		w := httptest.NewRecorder()

		handler.MeHandler(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", w.Code)
		}

		var body map[string]UserResponse
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if body["user"].Username != "alice" {
			t.Errorf("expected username alice, got %s", body["user"].Username)
		}
	})
}
