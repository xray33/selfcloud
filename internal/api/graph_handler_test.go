package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"selfcloud/internal/api"
	"selfcloud/internal/domain"
	"selfcloud/internal/middleware"
)

type mockNodeRepoForGraph struct{}

func (m *mockNodeRepoForGraph) GetByPath(ctx context.Context, userID int64, path string) (*domain.Node, error) {
	return &domain.Node{
		ID:     1,
		UserID: userID,
		Path:   "",
		Size:   12345,
		ETag:   "test-etag",
	}, nil
}
func (m *mockNodeRepoForGraph) ListByParentPath(ctx context.Context, userID int64, parentPath string) ([]*domain.Node, error) {
	return nil, nil
}
func (m *mockNodeRepoForGraph) Create(ctx context.Context, node *domain.Node) error { return nil }
func (m *mockNodeRepoForGraph) Update(ctx context.Context, node *domain.Node) error { return nil }
func (m *mockNodeRepoForGraph) Delete(ctx context.Context, userID int64, path string) error {
	return nil
}
func (m *mockNodeRepoForGraph) DeletePrefix(ctx context.Context, userID int64, pathPrefix string) error {
	return nil
}
func (m *mockNodeRepoForGraph) MovePrefix(ctx context.Context, userID int64, oldPrefix, newPrefix string) error {
	return nil
}
func (m *mockNodeRepoForGraph) EnsureParentDirectoryExists(ctx context.Context, userID int64, parentPath string) error {
	return nil
}
func (m *mockNodeRepoForGraph) TouchAncestors(ctx context.Context, userID int64, paths ...string) error {
	return nil
}

func TestMeDrivesHandler(t *testing.T) {
	handler := api.NewGraphHandler(nil, &mockNodeRepoForGraph{})

	t.Run("Unauthorized request returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/graph/v1.0/me/drives", nil)
		rec := httptest.NewRecorder()
		handler.MeDrivesHandler(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized, got %d", rec.Code)
		}
	})

	t.Run("Authorized request returns personal space with webDavUrl", func(t *testing.T) {
		user := &domain.User{ID: 42, Username: "alice"}
		ctx := middleware.WithUser(context.Background(), user)
		req := httptest.NewRequest(http.MethodGet, "/graph/v1.0/me/drives", nil).WithContext(ctx)
		req.Host = "cloud.example.com"
		rec := httptest.NewRecorder()

		handler.MeDrivesHandler(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp struct {
			Value []struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				DriveType string `json:"driveType"`
				Root      struct {
					ID        string `json:"id"`
					WebDavURL string `json:"webDavUrl"`
					WebURL    string `json:"webUrl"`
					ETag      string `json:"eTag"`
				} `json:"root"`
				Quota struct {
					Total int64 `json:"total"`
					Used  int64 `json:"used"`
				} `json:"quota"`
			} `json:"value"`
		}

		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse JSON response: %v", err)
		}

		if len(resp.Value) != 1 {
			t.Fatalf("expected 1 drive, got %d", len(resp.Value))
		}

		drive := resp.Value[0]
		if drive.DriveType != "personal" {
			t.Errorf("expected driveType 'personal', got %s", drive.DriveType)
		}
		expectedDavURL := "http://cloud.example.com/remote.php/webdav"
		if drive.Root.WebDavURL != expectedDavURL {
			t.Errorf("expected root webDavUrl %q, got %q", expectedDavURL, drive.Root.WebDavURL)
		}
		if drive.Quota.Used != 12345 {
			t.Errorf("expected used quota 12345, got %d", drive.Quota.Used)
		}
	})
}
