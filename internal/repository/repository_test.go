package repository_test

import (
	"context"
	"os"
	"testing"
	"time"

	"selfcloud/internal/db"
	"selfcloud/internal/domain"
	"selfcloud/internal/repository"
)

func setupDB(t *testing.T) (*repository.Repositories, func()) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		dsn = "selfcloud:selfcloud_pass@tcp(127.0.0.1:3306)/selfcloud?parseTime=true"
	}

	sqlDB, err := db.Connect("mysql", dsn)
	if err != nil {
		t.Skipf("Skipping repository integration test: MariaDB connection failed: %v", err)
		return nil, func() {}
	}

	if err := db.Migrate(sqlDB); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	repos := repository.NewRepositories(sqlDB)
	cleanup := func() {
		_ = sqlDB.Close()
	}
	return repos, cleanup
}

func TestRepositoryOperations(t *testing.T) {
	repos, cleanup := setupDB(t)
	defer cleanup()

	ctx := context.Background()
	testUsername := "repo_test_user"

	// Cleanup any previous test data
	_ = repos.Users.Delete(ctx, testUsername)

	// 1. User Repository
	user := &domain.User{
		Username:     testUsername,
		PasswordHash: "hashed_secret",
		Email:        "repo_test@example.com",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := repos.Users.Create(ctx, user); err != nil {
		t.Fatalf("Create user failed: %v", err)
	}

	fetchedUser, err := repos.Users.GetByUsername(ctx, testUsername)
	if err != nil || fetchedUser == nil {
		t.Fatalf("GetByUsername failed: %v", err)
	}
	if fetchedUser.Email != user.Email {
		t.Errorf("expected email %s, got %s", user.Email, fetchedUser.Email)
	}

	fetchedUser.Email = "updated_email@example.com"
	if err := repos.Users.Update(ctx, fetchedUser); err != nil {
		t.Fatalf("Update user failed: %v", err)
	}

	// 2. Node Repository & MovePrefix
	nodeRoot := &domain.Node{
		UserID:      fetchedUser.ID,
		Path:        "/",
		ParentPath:  "",
		Name:        "",
		IsDir:       true,
		Size:        0,
		ETag:        "root_etag",
		FileID:      "test_root",
		MTime:       time.Now(),
		ContentType: "httpd/unix-directory",
	}
	if err := repos.Nodes.Create(ctx, nodeRoot); err != nil {
		t.Fatalf("Create root node failed: %v", err)
	}

	dirA := &domain.Node{
		UserID:      fetchedUser.ID,
		Path:        "/folderA",
		ParentPath:  "/",
		Name:        "folderA",
		IsDir:       true,
		Size:        0,
		ETag:        "folderA_etag",
		FileID:      "folderA_id",
		MTime:       time.Now(),
		ContentType: "httpd/unix-directory",
	}
	if err := repos.Nodes.Create(ctx, dirA); err != nil {
		t.Fatalf("Create /folderA failed: %v", err)
	}

	fileA := &domain.Node{
		UserID:      fetchedUser.ID,
		Path:        "/folderA/child.txt",
		ParentPath:  "/folderA",
		Name:        "child.txt",
		IsDir:       false,
		Size:        42,
		ETag:        "file_etag",
		FileID:      "file_id",
		MTime:       time.Now(),
		ContentType: "text/plain",
	}
	if err := repos.Nodes.Create(ctx, fileA); err != nil {
		t.Fatalf("Create child.txt failed: %v", err)
	}

	// Test MovePrefix: move /folderA to /folderB
	if err := repos.Nodes.MovePrefix(ctx, fetchedUser.ID, "/folderA", "/folderB"); err != nil {
		t.Fatalf("MovePrefix failed: %v", err)
	}

	// Verify old path gone, new path present
	oldDir, _ := repos.Nodes.GetByPath(ctx, fetchedUser.ID, "/folderA")
	if oldDir != nil {
		t.Errorf("expected /folderA to be moved, still found")
	}

	newDir, err := repos.Nodes.GetByPath(ctx, fetchedUser.ID, "/folderB")
	if err != nil || newDir == nil {
		t.Fatalf("expected /folderB after MovePrefix: %v", err)
	}

	newFile, err := repos.Nodes.GetByPath(ctx, fetchedUser.ID, "/folderB/child.txt")
	if err != nil || newFile == nil {
		t.Fatalf("expected /folderB/child.txt after MovePrefix: %v", err)
	}
	if newFile.ParentPath != "/folderB" {
		t.Errorf("expected parent_path /folderB, got %s", newFile.ParentPath)
	}
	if newFile.Name != "child.txt" {
		t.Errorf("expected name child.txt, got %s", newFile.Name)
	}

	// 3. Share Repository with ExpiresAt
	expiresAt := time.Now().Add(24 * time.Hour)
	share := &domain.Share{
		Token:     "share_token_repo_test",
		UserID:    fetchedUser.ID,
		Path:      "/folderB/child.txt",
		IsDir:     false,
		ExpiresAt: &expiresAt,
		CreatedAt: time.Now(),
	}
	if err := repos.Shares.Create(ctx, share); err != nil {
		t.Fatalf("Create share failed: %v", err)
	}

	fetchedShare, err := repos.Shares.GetByToken(ctx, "share_token_repo_test")
	if err != nil || fetchedShare == nil {
		t.Fatalf("GetByToken failed: %v", err)
	}
	if fetchedShare.ExpiresAt == nil {
		t.Errorf("expected ExpiresAt to be set, got nil")
	} else if fetchedShare.IsExpired() {
		t.Errorf("share should not be expired yet")
	}

	// 4. Clean up user cascades
	if err := repos.Users.Delete(ctx, testUsername); err != nil {
		t.Fatalf("Delete user failed: %v", err)
	}

	// Ensure cascade deleted nodes and shares
	postDeleteNode, _ := repos.Nodes.GetByPath(ctx, fetchedUser.ID, "/folderB")
	if postDeleteNode != nil {
		t.Errorf("expected node to be deleted by cascade, found %v", postDeleteNode)
	}
}
