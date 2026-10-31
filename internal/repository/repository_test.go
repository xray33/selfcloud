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

	// Test Node Update
	newFile.Size = 999
	newFile.ETag = "updated_etag"
	if err := repos.Nodes.Update(ctx, newFile); err != nil {
		t.Errorf("Update node failed: %v", err)
	}

	// Test Node Delete
	tempNode := &domain.Node{
		UserID:      fetchedUser.ID,
		Path:        "/folderB/temp.txt",
		ParentPath:  "/folderB",
		Name:        "temp.txt",
		IsDir:       false,
		Size:        10,
		ETag:        "temp_etag",
		FileID:      "temp_id",
		MTime:       time.Now(),
		ContentType: "text/plain",
	}
	_ = repos.Nodes.Create(ctx, tempNode)
	if err := repos.Nodes.Delete(ctx, fetchedUser.ID, "/folderB/temp.txt"); err != nil {
		t.Errorf("Delete node failed: %v", err)
	}

	// Test TouchAncestors
	if err := repos.Nodes.TouchAncestors(ctx, fetchedUser.ID); err != nil {
		t.Errorf("TouchAncestors with empty paths failed: %v", err)
	}
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if err := repos.Nodes.TouchAncestors(canceledCtx, fetchedUser.ID, "/folderB/child.txt"); err == nil {
		t.Errorf("expected error with canceled context in TouchAncestors, got nil")
	}
	oldRoot, _ := repos.Nodes.GetByPath(ctx, fetchedUser.ID, "/")
	oldFolderB, _ := repos.Nodes.GetByPath(ctx, fetchedUser.ID, "/folderB")
	if err := repos.Nodes.TouchAncestors(ctx, fetchedUser.ID, "/folderB/child.txt"); err != nil {
		t.Errorf("TouchAncestors failed: %v", err)
	}
	newRoot, _ := repos.Nodes.GetByPath(ctx, fetchedUser.ID, "/")
	newFolderB, _ := repos.Nodes.GetByPath(ctx, fetchedUser.ID, "/folderB")
	if newRoot.ETag == oldRoot.ETag {
		t.Errorf("expected root ETag to change after TouchAncestors, remained %s", newRoot.ETag)
	}
	if newFolderB.ETag == oldFolderB.ETag {
		t.Errorf("expected /folderB ETag to change after TouchAncestors, remained %s", newFolderB.ETag)
	}
	if newFolderB.Size != 999 {
		t.Errorf("expected /folderB Size to be 999 after TouchAncestors, got %d", newFolderB.Size)
	}
	if newRoot.Size < 999 {
		t.Errorf("expected root Size to be at least 999 after TouchAncestors, got %d", newRoot.Size)
	}

	// Test RecalculateDirectorySizes
	canceledCtx2, cancel2 := context.WithCancel(ctx)
	cancel2()
	if err := repos.Nodes.RecalculateDirectorySizes(canceledCtx2, fetchedUser.ID); err == nil {
		t.Errorf("expected error with canceled context in RecalculateDirectorySizes, got nil")
	}

	if err := repos.Nodes.RecalculateDirectorySizes(ctx, fetchedUser.ID); err != nil {
		t.Errorf("RecalculateDirectorySizes failed: %v", err)
	}
	recalcFolderB, _ := repos.Nodes.GetByPath(ctx, fetchedUser.ID, "/folderB")
	if recalcFolderB.Size != 999 {
		t.Errorf("expected /folderB Size to be 999 after RecalculateDirectorySizes, got %d", recalcFolderB.Size)
	}

	// 3. Share Repository with ExpiresAt & ListByUserID
	share := &domain.Share{
		Token:     "share_token_repo_test",
		UserID:    fetchedUser.ID,
		Path:      "/folderB/child.txt",
		IsDir:     false,
		ExpiresAt: new(time.Now().Add(24 * time.Hour)),
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

	shareByPath, err := repos.Shares.GetByPath(ctx, fetchedUser.ID, "/folderB/child.txt")
	if err != nil || shareByPath == nil {
		t.Fatalf("GetByPath share failed: %v", err)
	}

	userShares, err := repos.Shares.ListByUserID(ctx, fetchedUser.ID)
	if err != nil || len(userShares) != 1 {
		t.Fatalf("expected 1 share for user, got %d (err: %v)", len(userShares), err)
	}

	// 4. Session Repository
	session := &domain.Session{
		Token:     "sess_token_repo_test",
		UserID:    fetchedUser.ID,
		ExpiresAt: time.Now().Add(1 * time.Hour),
		CreatedAt: time.Now(),
	}
	if err := repos.Sessions.Create(ctx, session); err != nil {
		t.Fatalf("Create session failed: %v", err)
	}

	fetchedSess, err := repos.Sessions.GetByToken(ctx, "sess_token_repo_test")
	if err != nil || fetchedSess == nil {
		t.Fatalf("GetByToken session failed: %v", err)
	}

	expiredSess := &domain.Session{
		Token:     "sess_token_expired",
		UserID:    fetchedUser.ID,
		ExpiresAt: time.Now().Add(-1 * time.Hour),
		CreatedAt: time.Now().Add(-2 * time.Hour),
	}
	if err := repos.Sessions.Create(ctx, expiredSess); err != nil {
		t.Fatalf("Create expired session failed: %v", err)
	}
	deletedCount, err := repos.Sessions.DeleteExpired(ctx)
	if err != nil || deletedCount == 0 {
		t.Errorf("expected DeleteExpired to delete sessions, got count %d (err: %v)", deletedCount, err)
	}

	if err := repos.Sessions.DeleteByToken(ctx, "sess_token_repo_test"); err != nil {
		t.Errorf("DeleteByToken failed: %v", err)
	}

	// Test DeleteByUserID
	sessForUser := &domain.Session{
		Token:     "sess_token_for_user",
		UserID:    fetchedUser.ID,
		ExpiresAt: time.Now().Add(1 * time.Hour),
		CreatedAt: time.Now(),
	}
	_ = repos.Sessions.Create(ctx, sessForUser)
	if err := repos.Sessions.DeleteByUserID(ctx, fetchedUser.ID); err != nil {
		t.Errorf("DeleteByUserID failed: %v", err)
	}

	// 5. Identity Repository
	identity := &domain.UserIdentity{
		UserID:         fetchedUser.ID,
		Provider:       "google",
		ProviderUserID: "goog_12345",
		Email:          "repo_test@example.com",
		CreatedAt:      time.Now(),
	}
	if err := repos.Identities.Create(ctx, identity); err != nil {
		t.Fatalf("Create identity failed: %v", err)
	}

	fetchedIdent, err := repos.Identities.GetByProviderUID(ctx, "google", "goog_12345")
	if err != nil || fetchedIdent == nil {
		t.Fatalf("GetByProviderUID failed: %v", err)
	}

	idents, err := repos.Identities.ListByUserID(ctx, fetchedUser.ID)
	if err != nil || len(idents) != 1 {
		t.Fatalf("expected 1 identity for user, got %d (err: %v)", len(idents), err)
	}

	// 6. User Repository: GetByEmail, GetByID, ListAll
	userByEmail, err := repos.Users.GetByEmail(ctx, "updated_email@example.com")
	if err != nil || userByEmail == nil {
		t.Errorf("GetByEmail failed: %v", err)
	}
	userByID, err := repos.Users.GetByID(ctx, fetchedUser.ID)
	if err != nil || userByID == nil {
		t.Errorf("GetByID failed: %v", err)
	}
	allUsers, err := repos.Users.ListAll(ctx)
	if err != nil || len(allUsers) == 0 {
		t.Errorf("ListAll failed: %v", err)
	}

	// 7. Delete Share directly
	if err := repos.Shares.DeleteByToken(ctx, "share_token_repo_test", fetchedUser.ID); err != nil {
		t.Errorf("DeleteByToken share failed: %v", err)
	}

	// 8. Clean up user cascades
	if err := repos.Users.Delete(ctx, testUsername); err != nil {
		t.Fatalf("Delete user failed: %v", err)
	}

	// Ensure cascade deleted nodes, sessions, identities
	postDeleteNode, _ := repos.Nodes.GetByPath(ctx, fetchedUser.ID, "/folderB")
	if postDeleteNode != nil {
		t.Errorf("expected node to be deleted by cascade, found %v", postDeleteNode)
	}
	postDeleteIdent, _ := repos.Identities.GetByProviderUID(ctx, "google", "goog_12345")
	if postDeleteIdent != nil {
		t.Errorf("expected identity to be deleted by cascade, found %v", postDeleteIdent)
	}
}
