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
	"selfcloud/internal/pathutil"
	"selfcloud/internal/repository"
	"selfcloud/internal/storage"
)

type mockTransactor struct {
	repo repository.NodeRepository
}

func (m *mockTransactor) WithTx(ctx context.Context, fn func(txRepo *repository.Repositories) error) error {
	repos := &repository.Repositories{
		Nodes: m.repo,
	}
	return fn(repos)
}

type errorNodeRepo struct {
	*mockNodeRepo
	createErr       error
	updateErr       error
	deleteErr       error
	deletePrefixErr error
	movePrefixErr   error
}

func (e *errorNodeRepo) Create(ctx context.Context, node *domain.Node) error {
	if e.createErr != nil {
		return e.createErr
	}
	return e.mockNodeRepo.Create(ctx, node)
}

func (e *errorNodeRepo) Update(ctx context.Context, node *domain.Node) error {
	if e.updateErr != nil {
		return e.updateErr
	}
	return e.mockNodeRepo.Update(ctx, node)
}

func (e *errorNodeRepo) Delete(ctx context.Context, userID int64, path string) error {
	if e.deleteErr != nil {
		return e.deleteErr
	}
	return e.mockNodeRepo.Delete(ctx, userID, path)
}

func (e *errorNodeRepo) DeletePrefix(ctx context.Context, userID int64, pathPrefix string) error {
	if e.deletePrefixErr != nil {
		return e.deletePrefixErr
	}
	return e.mockNodeRepo.DeletePrefix(ctx, userID, pathPrefix)
}

func (e *errorNodeRepo) MovePrefix(ctx context.Context, userID int64, oldPrefix, newPrefix string) error {
	if e.movePrefixErr != nil {
		return e.movePrefixErr
	}
	return e.mockNodeRepo.MovePrefix(ctx, userID, oldPrefix, newPrefix)
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
	for k, v := range m.nodes {
		if k == oldPrefix || (len(k) > len(oldPrefix) && k[:len(oldPrefix)] == oldPrefix) {
			newPath := newPrefix + k[len(oldPrefix):]
			delete(m.nodes, k)
			v.Path = newPath
			v.Name = filepath.Base(newPath)
			m.nodes[newPath] = v
		}
	}
	return nil
}

func (m *mockNodeRepo) EnsureParentDirectoryExists(ctx context.Context, userID int64, parentPath string) error {
	return nil
}

func (m *mockNodeRepo) TouchAncestors(ctx context.Context, userID int64, paths ...string) error {
	for _, p := range paths {
		for _, a := range pathutil.Ancestors(p) {
			if node, ok := m.nodes[a]; ok && node.IsDir {
				node.ETag = fmt.Sprintf("touched_%d", time.Now().UnixNano())
				node.MTime = time.Now()
			}
		}
	}
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

	t.Run("MKCOL creates directory successfully", func(t *testing.T) {
		req := withUserCtx(httptest.NewRequest("MKCOL", "/remote.php/webdav/testdir", nil))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusCreated {
			t.Fatalf("expected status 201 Created, got %d: %s", rec.Code, rec.Body.String())
		}
		if repo.nodes["/testdir"] == nil || !repo.nodes["/testdir"].IsDir {
			t.Fatalf("expected /testdir to exist as directory in repo")
		}
	})

	t.Run("MOVE file to new destination succeeds", func(t *testing.T) {
		req := withUserCtx(httptest.NewRequest("MOVE", "/remote.php/webdav/hello.txt", nil))
		req.Header.Set("Destination", "http://localhost:8080/remote.php/webdav/hello_moved.txt")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusCreated {
			t.Fatalf("expected status 201 Created, got %d", rec.Code)
		}
		if repo.nodes["/hello_moved.txt"] == nil {
			t.Fatalf("expected /hello_moved.txt to exist in repo")
		}
	})

	t.Run("COPY directory to new destination succeeds", func(t *testing.T) {
		// Setup source dir and file in repo and storage
		_ = st.Mkdir(user.Username, "/srcdir")
		_, _ = st.Write(user.Username, "/srcdir/file.txt", bytes.NewReader([]byte("dir file")))
		repo.nodes["/srcdir"] = &domain.Node{
			UserID: 1, Path: "/srcdir", Name: "srcdir", IsDir: true,
		}
		repo.nodes["/srcdir/file.txt"] = &domain.Node{
			UserID: 1, Path: "/srcdir/file.txt", ParentPath: "/srcdir", Name: "file.txt", IsDir: false,
		}

		req := withUserCtx(httptest.NewRequest("COPY", "/remote.php/webdav/srcdir", nil))
		req.Header.Set("Destination", "http://localhost:8080/remote.php/webdav/destdir")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusCreated {
			t.Fatalf("expected status 201 Created, got %d", rec.Code)
		}
		if repo.nodes["/destdir"] == nil || !repo.nodes["/destdir"].IsDir {
			t.Fatalf("expected /destdir directory to exist in repo")
		}
		if repo.nodes["/destdir/file.txt"] == nil {
			t.Fatalf("expected /destdir/file.txt to exist in repo")
		}
	})

	t.Run("DELETE file succeeds and updates ancestors", func(t *testing.T) {
		// Setup file
		_, _ = st.Write(user.Username, "/file_to_del.txt", bytes.NewReader([]byte("delete me")))
		repo.nodes["/file_to_del.txt"] = &domain.Node{
			UserID: 1, Path: "/file_to_del.txt", Name: "file_to_del.txt", IsDir: false,
		}

		req := withUserCtx(httptest.NewRequest("DELETE", "/remote.php/webdav/file_to_del.txt", nil))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected status 204 No Content, got %d", rec.Code)
		}
		if repo.nodes["/file_to_del.txt"] != nil {
			t.Fatalf("expected /file_to_del.txt to be removed from repo")
		}
	})

	t.Run("DELETE directory succeeds and updates ancestors", func(t *testing.T) {
		_ = st.Mkdir(user.Username, "/dir_to_del")
		repo.nodes["/dir_to_del"] = &domain.Node{
			UserID: 1, Path: "/dir_to_del", Name: "dir_to_del", IsDir: true,
		}

		req := withUserCtx(httptest.NewRequest("DELETE", "/remote.php/webdav/dir_to_del", nil))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected status 204 No Content, got %d", rec.Code)
		}
		if repo.nodes["/dir_to_del"] != nil {
			t.Fatalf("expected /dir_to_del to be removed from repo")
		}
	})

	t.Run("WebDAV operation DB transaction errors return 500", func(t *testing.T) {
		errRepo := &errorNodeRepo{
			mockNodeRepo: repo,
		}
		errTx := &mockTransactor{repo: errRepo}
		errHandler := NewWebDAVHandler(errTx, errRepo, st, logger)

		// 1. PUT create error
		errRepo.createErr = fmt.Errorf("db create error")
		req := withUserCtx(httptest.NewRequest(http.MethodPut, "/remote.php/webdav/create_err.txt", bytes.NewReader([]byte("data"))))
		rec := httptest.NewRecorder()
		errHandler.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 for PUT create error, got %d", rec.Code)
		}
		errRepo.createErr = nil

		// 2. PUT update error
		repo.nodes["/update_err.txt"] = &domain.Node{UserID: 1, Path: "/update_err.txt", Name: "update_err.txt"}
		errRepo.updateErr = fmt.Errorf("db update error")
		req = withUserCtx(httptest.NewRequest(http.MethodPut, "/remote.php/webdav/update_err.txt", bytes.NewReader([]byte("data"))))
		rec = httptest.NewRecorder()
		errHandler.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 for PUT update error, got %d", rec.Code)
		}
		errRepo.updateErr = nil

		// 3. DELETE dir error
		repo.nodes["/deldir_err"] = &domain.Node{UserID: 1, Path: "/deldir_err", Name: "deldir_err", IsDir: true}
		errRepo.deletePrefixErr = fmt.Errorf("db delete dir error")
		req = withUserCtx(httptest.NewRequest("DELETE", "/remote.php/webdav/deldir_err", nil))
		rec = httptest.NewRecorder()
		errHandler.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 for DELETE dir error, got %d", rec.Code)
		}
		errRepo.deletePrefixErr = nil

		// 4. DELETE file error
		repo.nodes["/delfile_err.txt"] = &domain.Node{UserID: 1, Path: "/delfile_err.txt", Name: "delfile_err.txt", IsDir: false}
		errRepo.deleteErr = fmt.Errorf("db delete file error")
		req = withUserCtx(httptest.NewRequest("DELETE", "/remote.php/webdav/delfile_err.txt", nil))
		rec = httptest.NewRecorder()
		errHandler.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 for DELETE file error, got %d", rec.Code)
		}
		errRepo.deleteErr = nil

		// 5. MOVE error
		repo.nodes["/move_err.txt"] = &domain.Node{UserID: 1, Path: "/move_err.txt", Name: "move_err.txt"}
		_, _ = st.Write(user.Username, "/move_err.txt", bytes.NewReader([]byte("data")))
		errRepo.movePrefixErr = fmt.Errorf("db move error")
		req = withUserCtx(httptest.NewRequest("MOVE", "/remote.php/webdav/move_err.txt", nil))
		req.Header.Set("Destination", "http://localhost:8080/remote.php/webdav/move_err_dest.txt")
		rec = httptest.NewRecorder()
		errHandler.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 for MOVE error, got %d", rec.Code)
		}
		errRepo.movePrefixErr = nil

		// 6. COPY create error
		errRepo.createErr = fmt.Errorf("db copy create error")
		req = withUserCtx(httptest.NewRequest("COPY", "/remote.php/webdav/hello_moved.txt", nil))
		req.Header.Set("Destination", "http://localhost:8080/remote.php/webdav/copy_err_dest.txt")
		rec = httptest.NewRecorder()
		errHandler.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 for COPY create error, got %d", rec.Code)
		}
		errRepo.createErr = nil

		// 7. COPY update error (with overwrite)
		repo.nodes["/copy_dest_existing.txt"] = &domain.Node{UserID: 1, Path: "/copy_dest_existing.txt", Name: "copy_dest_existing.txt"}
		_, _ = st.Write(user.Username, "/copy_dest_existing.txt", bytes.NewReader([]byte("old")))
		errRepo.updateErr = fmt.Errorf("db copy update error")
		req = withUserCtx(httptest.NewRequest("COPY", "/remote.php/webdav/hello_moved.txt", nil))
		req.Header.Set("Destination", "http://localhost:8080/remote.php/webdav/copy_dest_existing.txt")
		req.Header.Set("Overwrite", "T")
		rec = httptest.NewRecorder()
		errHandler.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 for COPY update error, got %d", rec.Code)
		}
		errRepo.updateErr = nil
	})
}
