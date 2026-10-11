package indexer_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"selfcloud/internal/db"
	"selfcloud/internal/domain"
	"selfcloud/internal/indexer"
	"selfcloud/internal/repository"
)

func TestIndexerReindexUser(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		dsn = "selfcloud:selfcloud_pass@tcp(127.0.0.1:3306)/selfcloud?parseTime=true"
	}

	sqlDB, err := db.Connect("mysql", dsn)
	if err != nil {
		t.Skipf("Skipping indexer integration test: MariaDB connection failed: %v", err)
		return
	}
	defer sqlDB.Close()

	if err := db.Migrate(sqlDB); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	ctx := context.Background()
	userRepo := repository.NewUserRepository(sqlDB)
	nodeRepo := repository.NewNodeRepository(sqlDB)

	testUsername := "indexer_test_user"
	_ = userRepo.Delete(ctx, testUsername)

	hash, _ := domain.HashPassword("secret")
	testUser := &domain.User{
		Username:     testUsername,
		PasswordHash: hash,
		Email:        "indexer@example.com",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := userRepo.Create(ctx, testUser); err != nil {
		t.Fatalf("Create user failed: %v", err)
	}
	defer func() {
		_ = userRepo.Delete(ctx, testUsername)
	}()

	// Create temp storage directory
	tempDir, err := os.MkdirTemp("", "selfcloud_idx_test_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	userStorageDir := filepath.Join(tempDir, testUsername)
	if err := os.MkdirAll(filepath.Join(userStorageDir, "folder1"), 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	testFilePath := filepath.Join(userStorageDir, "folder1", "file.txt")
	if err := os.WriteFile(testFilePath, []byte("Hello Indexer!"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	idx := indexer.New(nodeRepo, tempDir, logger, false)

	// 1. ReindexUser creates missing folder1 and file.txt in DB
	stats, err := idx.ReindexUser(ctx, testUser, true)
	if err != nil {
		t.Fatalf("ReindexUser failed: %v", err)
	}
	if stats.Created < 2 {
		t.Errorf("expected at least 2 nodes created, got %d", stats.Created)
	}

	// Verify nodes exist in DB
	folderNode, err := nodeRepo.GetByPath(ctx, testUser.ID, "/folder1")
	if err != nil || folderNode == nil {
		t.Fatalf("expected /folder1 in DB, got %v (err: %v)", folderNode, err)
	}
	fileNode, err := nodeRepo.GetByPath(ctx, testUser.ID, "/folder1/file.txt")
	if err != nil || fileNode == nil {
		t.Fatalf("expected /folder1/file.txt in DB, got %v (err: %v)", fileNode, err)
	}

	// 2. Remove file from disk and run reindexer with removeOrphans=true
	_ = os.Remove(testFilePath)
	stats2, err := idx.ReindexUser(ctx, testUser, true)
	if err != nil {
		t.Fatalf("ReindexUser with orphan cleanup failed: %v", err)
	}
	if stats2.Deleted < 1 {
		t.Errorf("expected at least 1 orphan deleted, got %d", stats2.Deleted)
	}

	// Verify orphan is gone from DB
	orphanNode, _ := nodeRepo.GetByPath(ctx, testUser.ID, "/folder1/file.txt")
	if orphanNode != nil {
		t.Errorf("expected orphan node to be deleted from DB, found: %v", orphanNode)
	}
}

func TestIndexerNonExistentUserDir(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "selfcloud_idx_skip_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	idx := indexer.New(nil, tempDir, logger, true)

	stats, err := idx.ReindexUser(context.Background(), &domain.User{Username: "does_not_exist"}, false)
	if err != nil {
		t.Fatalf("expected nil error for missing directory, got %v", err)
	}
	if stats.Created != 0 {
		t.Errorf("expected 0 created for missing directory, got %d", stats.Created)
	}
}
