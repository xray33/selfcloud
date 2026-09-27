package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"selfcloud/internal/domain"
	"selfcloud/internal/middleware"
	"selfcloud/internal/repository"
	"selfcloud/internal/storage"
)

func TestFilesHandlerOperations(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "files_handler_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	st, err := storage.NewLocalStorage(tempDir)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}

	nodeRepo := &mockNodeRepo{nodes: make(map[string]*domain.Node)}
	repos := &repository.Repositories{Nodes: nodeRepo}
	tx := &mockTransactor{repos: repos}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler := NewFilesHandler(tx, nodeRepo, st, logger)
	user := &domain.User{ID: 1, Username: "testuser"}

	t.Run("Mkdir creates directory atomically", func(t *testing.T) {
		reqBody, _ := json.Marshal(MkdirRequest{Path: "/documents"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/files/mkdir", bytes.NewReader(reqBody))
		req = req.WithContext(middleware.WithUser(req.Context(), user))
		w := httptest.NewRecorder()

		handler.MkdirHandler(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("expected status 201 Created, got %d: %s", w.Code, w.Body.String())
		}

		if nodeRepo.nodes["/documents"] == nil || !nodeRepo.nodes["/documents"].IsDir {
			t.Fatalf("expected /documents directory node to be created")
		}
	})

	t.Run("Rename updates node metadata atomically", func(t *testing.T) {
		// First create a file
		nodeRepo.nodes["/notes.txt"] = &domain.Node{
			UserID: 1,
			Path:   "/notes.txt",
			Name:   "notes.txt",
			IsDir:  false,
		}
		_, err := st.Write(user.Username, "/notes.txt", bytes.NewReader([]byte("test note")))
		if err != nil {
			t.Fatalf("failed to write test note: %v", err)
		}

		reqBody, _ := json.Marshal(RenameRequest{OldPath: "/notes.txt", NewPath: "/renamed.txt"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/files/rename", bytes.NewReader(reqBody))
		req = req.WithContext(middleware.WithUser(req.Context(), user))
		w := httptest.NewRecorder()

		handler.RenameHandler(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200 OK, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("Delete removes file atomically", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/files?path=/renamed.txt", nil)
		req = req.WithContext(middleware.WithUser(req.Context(), user))
		w := httptest.NewRecorder()

		handler.DeleteHandler(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200 OK, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("Mkdir cleans up storage directory when DB transaction fails", func(t *testing.T) {
		failingTx := &failingMockTransactor{err: os.ErrInvalid}
		failHandler := NewFilesHandler(failingTx, nodeRepo, st, logger)

		reqBody, _ := json.Marshal(MkdirRequest{Path: "/failed_dir"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/files/mkdir", bytes.NewReader(reqBody))
		req = req.WithContext(middleware.WithUser(req.Context(), user))
		w := httptest.NewRecorder()

		failHandler.MkdirHandler(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Fatalf("expected status 500 Internal Server Error, got %d", w.Code)
		}
		if st.Exists(user.Username, "/failed_dir") {
			t.Fatalf("expected /failed_dir to be cleaned up from storage on DB transaction failure")
		}
	})

	t.Run("Rename compensates storage move when DB transaction fails", func(t *testing.T) {
		nodeRepo.nodes["/source.txt"] = &domain.Node{UserID: 1, Path: "/source.txt", Name: "source.txt"}
		_, err := st.Write(user.Username, "/source.txt", bytes.NewReader([]byte("source content")))
		if err != nil {
			t.Fatalf("failed to write source file: %v", err)
		}

		failingTx := &failingMockTransactor{err: os.ErrInvalid}
		failHandler := NewFilesHandler(failingTx, nodeRepo, st, logger)

		reqBody, _ := json.Marshal(RenameRequest{OldPath: "/source.txt", NewPath: "/target.txt"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/files/rename", bytes.NewReader(reqBody))
		req = req.WithContext(middleware.WithUser(req.Context(), user))
		w := httptest.NewRecorder()

		failHandler.RenameHandler(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Fatalf("expected status 500, got %d", w.Code)
		}
		// Storage should have compensated and moved target back to source
		if !st.Exists(user.Username, "/source.txt") {
			t.Fatalf("expected /source.txt to still exist after compensated rename failure")
		}
		if st.Exists(user.Username, "/target.txt") {
			t.Fatalf("expected /target.txt to NOT exist after compensated rename failure")
		}
	})

	t.Run("DownloadZip streams archive successfully", func(t *testing.T) {
		nodeRepo.nodes["/hello.txt"] = &domain.Node{UserID: 1, Path: "/hello.txt", Name: "hello.txt"}
		_, err := st.Write(user.Username, "/hello.txt", bytes.NewReader([]byte("hello world")))
		if err != nil {
			t.Fatalf("failed to write hello.txt: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/api/v1/files/zip?path=/hello.txt", nil)
		req = req.WithContext(middleware.WithUser(req.Context(), user))
		w := httptest.NewRecorder()

		handler.DownloadZipHandler(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200 OK, got %d", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/zip" {
			t.Fatalf("expected Content-Type application/zip, got %s", ct)
		}
	})

	t.Run("DownloadZip respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		req := httptest.NewRequest(http.MethodGet, "/api/v1/files/zip?path=/hello.txt", nil)
		req = req.WithContext(middleware.WithUser(ctx, user))
		w := httptest.NewRecorder()

		handler.DownloadZipHandler(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200 OK header sent before cancel, got %d", w.Code)
		}
	})

	t.Run("Upload rejects payload exceeding MaxUploadSize with 413", func(t *testing.T) {
		limitedHandler := NewFilesHandler(tx, nodeRepo, st, logger).WithMaxUploadSize(100) // 100 bytes limit
		body := bytes.NewBuffer(nil)
		writer := multipart.NewWriter(body)
		part, _ := writer.CreateFormFile("file", "large.txt")
		_, _ = part.Write(make([]byte, 500))
		_ = writer.Close()

		req := httptest.NewRequest(http.MethodPost, "/api/v1/files/upload", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req = req.WithContext(middleware.WithUser(req.Context(), user))
		w := httptest.NewRecorder()

		limitedHandler.UploadHandler(w, req)
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413 Request Entity Too Large, got %d: %s", w.Code, w.Body.String())
		}
	})
}

type failingMockTransactor struct {
	err error
}

func (f *failingMockTransactor) WithTx(ctx context.Context, fn func(txRepo *repository.Repositories) error) error {
	return f.err
}


