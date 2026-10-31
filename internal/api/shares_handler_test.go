package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"selfcloud/internal/domain"
	"selfcloud/internal/middleware"
	"selfcloud/internal/repository"
)

type mockTransactor struct {
	repos *repository.Repositories
}

func (m *mockTransactor) WithTx(ctx context.Context, fn func(txRepo *repository.Repositories) error) error {
	return fn(m.repos)
}

type mockNodeRepo struct {
	nodes           map[string]*domain.Node
	deleteErr       error
	deletePrefixErr error
	movePrefixErr   error
}

func (m *mockNodeRepo) GetByPath(ctx context.Context, userID int64, path string) (*domain.Node, error) {
	return m.nodes[path], nil
}
func (m *mockNodeRepo) ListByParentPath(ctx context.Context, userID int64, parentPath string) ([]*domain.Node, error) {
	return nil, nil
}
func (m *mockNodeRepo) Create(ctx context.Context, node *domain.Node) error {
	m.nodes[node.Path] = node
	return nil
}
func (m *mockNodeRepo) Update(ctx context.Context, node *domain.Node) error {
	m.nodes[node.Path] = node
	return nil
}
func (m *mockNodeRepo) Delete(ctx context.Context, userID int64, path string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.nodes, path)
	return nil
}
func (m *mockNodeRepo) DeletePrefix(ctx context.Context, userID int64, pathPrefix string) error {
	if m.deletePrefixErr != nil {
		return m.deletePrefixErr
	}
	delete(m.nodes, pathPrefix)
	return nil
}
func (m *mockNodeRepo) MovePrefix(ctx context.Context, userID int64, oldPrefix, newPrefix string) error {
	if m.movePrefixErr != nil {
		return m.movePrefixErr
	}
	if n, ok := m.nodes[oldPrefix]; ok {
		delete(m.nodes, oldPrefix)
		n.Path = newPrefix
		m.nodes[newPrefix] = n
	}
	return nil
}
func (m *mockNodeRepo) EnsureParentDirectoryExists(ctx context.Context, userID int64, parentPath string) error {
	return nil
}
func (m *mockNodeRepo) TouchAncestors(ctx context.Context, userID int64, paths ...string) error {
	return nil
}
func (m *mockNodeRepo) RecalculateDirectorySizes(ctx context.Context, userID int64) error {
	return nil
}

type mockShareRepo struct {
	shares map[string]*domain.Share
}

func (m *mockShareRepo) Create(ctx context.Context, share *domain.Share) error {
	m.shares[share.Path] = share
	return nil
}
func (m *mockShareRepo) GetByToken(ctx context.Context, token string) (*domain.Share, error) {
	for _, s := range m.shares {
		if s.Token == token {
			return s, nil
		}
	}
	return nil, nil
}
func (m *mockShareRepo) GetByPath(ctx context.Context, userID int64, path string) (*domain.Share, error) {
	return m.shares[path], nil
}
func (m *mockShareRepo) ListByUserID(ctx context.Context, userID int64) ([]*domain.Share, error) {
	var list []*domain.Share
	for _, s := range m.shares {
		if s.UserID == userID {
			list = append(list, s)
		}
	}
	return list, nil
}
func (m *mockShareRepo) DeleteByToken(ctx context.Context, token string, userID int64) error {
	for p, s := range m.shares {
		if s.Token == token && s.UserID == userID {
			delete(m.shares, p)
			return nil
		}
	}
	return fmt.Errorf("not found")
}

func TestCreateShareHandler(t *testing.T) {
	nodeRepo := &mockNodeRepo{nodes: make(map[string]*domain.Node)}
	shareRepo := &mockShareRepo{shares: make(map[string]*domain.Share)}
	repos := &repository.Repositories{
		Nodes:  nodeRepo,
		Shares: shareRepo,
	}
	tx := &mockTransactor{repos: repos}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler := NewSharesHandler(tx, shareRepo, nodeRepo, nil, nil, logger)

	user := &domain.User{ID: 1, Username: "alice"}

	// Seed node
	nodeRepo.nodes["/test.txt"] = &domain.Node{
		ID:        10,
		UserID:    1,
		Path:      "/test.txt",
		IsDir:     false,
		FileID:    "00000001test",
		CreatedAt: time.Now(),
	}

	t.Run("Create new share", func(t *testing.T) {
		reqBody, _ := json.Marshal(CreateShareRequest{Path: "/test.txt"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(reqBody))
		req = req.WithContext(middleware.WithUser(req.Context(), user))
		w := httptest.NewRecorder()

		handler.CreateShareHandler(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("expected status 201 Created, got %d: %s", w.Code, w.Body.String())
		}

		var res struct {
			Share ShareResponse `json:"share"`
		}
		if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if res.Share.Path != "/test.txt" || res.Share.Token == "" {
			t.Fatalf("unexpected share response: %+v", res.Share)
		}
	})

	t.Run("Create share for existing share returns 200 OK with same token", func(t *testing.T) {
		reqBody, _ := json.Marshal(CreateShareRequest{Path: "/test.txt"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(reqBody))
		req = req.WithContext(middleware.WithUser(req.Context(), user))
		w := httptest.NewRecorder()

		handler.CreateShareHandler(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200 OK for existing share, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("Create share for nonexistent node returns 404", func(t *testing.T) {
		reqBody, _ := json.Marshal(CreateShareRequest{Path: "/nonexistent.txt"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/shares", bytes.NewReader(reqBody))
		req = req.WithContext(middleware.WithUser(req.Context(), user))
		w := httptest.NewRecorder()

		handler.CreateShareHandler(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("expected status 404 Not Found, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("Expired share returns 404 in PublicShareInfoHandler", func(t *testing.T) {
		shareRepo.shares["/expired.txt"] = &domain.Share{
			Token:     "expired_token_123",
			UserID:    user.ID,
			Path:      "/expired.txt",
			ExpiresAt: new(time.Now().Add(-1 * time.Hour)),
			CreatedAt: time.Now().Add(-2 * time.Hour),
		}
		nodeRepo.nodes["/expired.txt"] = &domain.Node{
			UserID: user.ID,
			Path:   "/expired.txt",
			Name:   "expired.txt",
		}

		req := httptest.NewRequest(http.MethodGet, "/api/v1/public/shares?token=expired_token_123", nil)
		w := httptest.NewRecorder()

		handler.PublicShareInfoHandler(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for expired share, got %d", w.Code)
		}
	})
}
