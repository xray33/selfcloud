package webdav

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"selfcloud/internal/domain"
	"selfcloud/internal/middleware"
	"selfcloud/internal/repository"
	"selfcloud/internal/storage"
)

type mockTransactor struct {
	repo *mockNodeRepo
}

func (m *mockTransactor) WithTx(ctx context.Context, fn func(txRepo *repository.Repositories) error) error {
	repos := &repository.Repositories{
		Nodes: m.repo,
	}
	return fn(repos)
}

type mockNodeRepo struct {
	nodes map[string]*domain.Node
}

func newMockNodeRepo() *mockNodeRepo {
	return &mockNodeRepo{nodes: make(map[string]*domain.Node)}
}

func (m *mockNodeRepo) GetByPath(ctx context.Context, userID int64, path string) (*domain.Node, error) {
	return m.nodes[path], nil
}

func (m *mockNodeRepo) ListByParentPath(ctx context.Context, userID int64, parentPath string) ([]*domain.Node, error) {
	var list []*domain.Node
	for _, n := range m.nodes {
		if n.ParentPath == parentPath {
			list = append(list, n)
		}
	}
	return list, nil
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
	delete(m.nodes, path)
	return nil
}

func (m *mockNodeRepo) DeletePrefix(ctx context.Context, userID int64, pathPrefix string) error {
	for k := range m.nodes {
		if k == pathPrefix || len(k) > len(pathPrefix) && k[:len(pathPrefix)] == pathPrefix {
			delete(m.nodes, k)
		}
	}
	return nil
}

func (m *mockNodeRepo) MovePrefix(ctx context.Context, userID int64, oldPrefix, newPrefix string) error {
	return nil
}

func (m *mockNodeRepo) EnsureParentDirectoryExists(ctx context.Context, userID int64, parentPath string) error {
	return nil
}

func TestWebDAVHandlerGetAndRange(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "webdav_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	st, err := storage.NewLocalStorage(tempDir)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}

	repo := newMockNodeRepo()
	tx := &mockTransactor{repo: repo}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	handler := NewWebDAVHandler(tx, repo, st, logger)

	user := &domain.User{ID: 1, Username: "testuser"}
	fileContent := []byte("Hello, WebDAV streaming world!")
	relPath := "/hello.txt"

	// Setup file in storage and repository
	_, err = st.Write(user.Username, relPath, bytes.NewReader(fileContent))
	if err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	modTime := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	etag := "testetag123"
	node := &domain.Node{
		ID:          1,
		UserID:      1,
		Path:        relPath,
		ParentPath:  "/",
		Name:        filepath.Base(relPath),
		IsDir:       false,
		Size:        int64(len(fileContent)),
		ETag:        etag,
		FileID:      "00000001oc12345",
		MTime:       modTime,
		ContentType: "text/plain",
	}
	_ = repo.Create(context.Background(), node)

	withUserCtx := func(r *http.Request) *http.Request {
		ctx := context.WithValue(r.Context(), middleware.UserObjKey, user)
		return r.WithContext(ctx)
	}

	t.Run("Full GET file download", func(t *testing.T) {
		req := withUserCtx(httptest.NewRequest(http.MethodGet, "/remote.php/webdav/hello.txt", nil))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rec.Code)
		}
		if rec.Body.String() != string(fileContent) {
			t.Errorf("expected body %q, got %q", string(fileContent), rec.Body.String())
		}
		if rec.Header().Get("Accept-Ranges") != "bytes" {
			t.Errorf("expected Accept-Ranges: bytes, got %q", rec.Header().Get("Accept-Ranges"))
		}
		if rec.Header().Get("ETag") != fmt.Sprintf(`"%s"`, etag) {
			t.Errorf("expected ETag %q, got %q", fmt.Sprintf(`"%s"`, etag), rec.Header().Get("ETag"))
		}
	})

	t.Run("HEAD request", func(t *testing.T) {
		req := withUserCtx(httptest.NewRequest(http.MethodHead, "/remote.php/webdav/hello.txt", nil))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("expected empty body for HEAD, got %d bytes", rec.Body.Len())
		}
		if rec.Header().Get("Content-Length") != fmt.Sprintf("%d", len(fileContent)) {
			t.Errorf("expected Content-Length %d, got %s", len(fileContent), rec.Header().Get("Content-Length"))
		}
	})

	t.Run("HTTP Range Request (Partial Content)", func(t *testing.T) {
		req := withUserCtx(httptest.NewRequest(http.MethodGet, "/remote.php/webdav/hello.txt", nil))
		req.Header.Set("Range", "bytes=0-4") // "Hello"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusPartialContent {
			t.Errorf("expected status 206 Partial Content, got %d", rec.Code)
		}
		if rec.Body.String() != "Hello" {
			t.Errorf("expected range body 'Hello', got %q", rec.Body.String())
		}
		if rec.Header().Get("Content-Range") != fmt.Sprintf("bytes 0-4/%d", len(fileContent)) {
			t.Errorf("expected Content-Range 'bytes 0-4/%d', got %q", len(fileContent), rec.Header().Get("Content-Range"))
		}
	})

	t.Run("If-None-Match ETag Caching (304 Not Modified)", func(t *testing.T) {
		req := withUserCtx(httptest.NewRequest(http.MethodGet, "/remote.php/webdav/hello.txt", nil))
		req.Header.Set("If-None-Match", fmt.Sprintf(`"%s"`, etag))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotModified {
			t.Errorf("expected status 304 Not Modified, got %d", rec.Code)
		}
	})

	t.Run("PUT rejects payload exceeding MaxUploadSize with 413", func(t *testing.T) {
		limitedHandler := NewWebDAVHandler(tx, repo, st, logger).WithMaxUploadSize(50) // 50 bytes limit
		req := withUserCtx(httptest.NewRequest(http.MethodPut, "/remote.php/webdav/large.txt", bytes.NewReader(make([]byte, 200))))
		rec := httptest.NewRecorder()
		limitedHandler.ServeHTTP(rec, req)

		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("expected status 413 Request Entity Too Large, got %d", rec.Code)
		}
	})

	t.Run("COPY file to new destination returns 201", func(t *testing.T) {
		req := withUserCtx(httptest.NewRequest("COPY", "/remote.php/webdav/hello.txt", nil))
		req.Header.Set("Destination", "http://localhost:8080/remote.php/webdav/hello_copy.txt")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusCreated {
			t.Fatalf("expected status 201 Created, got %d", rec.Code)
		}
		if !st.Exists(user.Username, "/hello_copy.txt") {
			t.Fatalf("expected /hello_copy.txt to exist in storage")
		}
		if repo.nodes["/hello_copy.txt"] == nil {
			t.Fatalf("expected /hello_copy.txt to exist in repo")
		}
	})

	t.Run("COPY file to existing destination with Overwrite F returns 412", func(t *testing.T) {
		req := withUserCtx(httptest.NewRequest("COPY", "/remote.php/webdav/hello.txt", nil))
		req.Header.Set("Destination", "http://localhost:8080/remote.php/webdav/hello_copy.txt")
		req.Header.Set("Overwrite", "F")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusPreconditionFailed {
			t.Fatalf("expected status 412 Precondition Failed, got %d", rec.Code)
		}
	})

	t.Run("COPY file to existing destination with Overwrite T returns 204", func(t *testing.T) {
		req := withUserCtx(httptest.NewRequest("COPY", "/remote.php/webdav/hello.txt", nil))
		req.Header.Set("Destination", "http://localhost:8080/remote.php/webdav/hello_copy.txt")
		req.Header.Set("Overwrite", "T")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected status 204 No Content, got %d", rec.Code)
		}
	})

	t.Run("COPY non-existent source returns 404", func(t *testing.T) {
		req := withUserCtx(httptest.NewRequest("COPY", "/remote.php/webdav/missing.txt", nil))
		req.Header.Set("Destination", "http://localhost:8080/remote.php/webdav/missing_copy.txt")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected status 404 Not Found, got %d", rec.Code)
		}
	})

	t.Run("PUT file with dots and ellipses in name succeeds", func(t *testing.T) {
		dotPath := "/remote.php/webdav/books/sample...title..djvu"
		req := withUserCtx(httptest.NewRequest(http.MethodPut, dotPath, bytes.NewReader([]byte("book content"))))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusCreated && rec.Code != http.StatusNoContent {
			t.Fatalf("expected status 201 or 204, got %d: %s", rec.Code, rec.Body.String())
		}
		if !st.Exists(user.Username, "/books/sample...title..djvu") {
			t.Fatalf("expected file to exist in storage")
		}
	})

	t.Run("PROPFIND returns RFC-compliant getlastmodified in GMT", func(t *testing.T) {
		req := withUserCtx(httptest.NewRequest("PROPFIND", "/remote.php/webdav/hello.txt", nil))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusMultiStatus {
			t.Fatalf("expected status 207 Multi-Status, got %d", rec.Code)
		}

		body := rec.Body.String()
		if !bytes.Contains([]byte(body), []byte("GMT</d:getlastmodified>")) {
			t.Fatalf("expected d:getlastmodified to end with GMT according to HTTP-date specification, got: %s", body)
		}
	})
}
