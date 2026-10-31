package storage

import (
	"bytes"
	"io"
	"os"
	"testing"
)

func TestLocalStorageOperations(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "selfcloud_storage_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	s, err := NewLocalStorage(tempDir)
	if err != nil {
		t.Fatalf("failed to init local storage: %v", err)
	}

	username := "testuser"
	relPath := "/documents/notes.txt"
	content := []byte("Hello, WebDAV World!")

	// 1. Write file
	n, err := s.Write(username, relPath, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("storage Write failed: %v", err)
	}
	if n != int64(len(content)) {
		t.Errorf("expected %d bytes written, got %d", len(content), n)
	}

	// 2. Exists
	if !s.Exists(username, relPath) {
		t.Errorf("expected file %s to exist", relPath)
	}

	// 3. Read and Seek file
	rc, err := s.Read(username, relPath)
	if err != nil {
		t.Fatalf("storage Read failed: %v", err)
	}
	readBuf, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read content failed: %v", err)
	}
	if !bytes.Equal(readBuf, content) {
		t.Errorf("expected content %s, got %s", content, readBuf)
	}

	// 3b. Test Seek functionality
	offset, err := rc.Seek(7, io.SeekStart)
	if err != nil || offset != 7 {
		t.Fatalf("Seek failed: offset=%d, err=%v", offset, err)
	}
	seekBuf := make([]byte, 6)
	_, err = io.ReadFull(rc, seekBuf)
	if err != nil || string(seekBuf) != "WebDAV" {
		t.Fatalf("Seek read failed: got %q, err=%v", string(seekBuf), err)
	}
	rc.Close()

	// 4. Move file
	newRelPath := "/documents/notes_renamed.txt"
	if err := s.Move(username, relPath, newRelPath); err != nil {
		t.Fatalf("storage Move failed: %v", err)
	}
	if s.Exists(username, relPath) {
		t.Errorf("old path %s should not exist after move", relPath)
	}
	if !s.Exists(username, newRelPath) {
		t.Errorf("new path %s should exist after move", newRelPath)
	}

	// 5. Delete file
	if err := s.Delete(username, newRelPath); err != nil {
		t.Fatalf("storage Delete failed: %v", err)
	}
	if s.Exists(username, newRelPath) {
		t.Errorf("deleted path %s should not exist", newRelPath)
	}
}

func TestLocalStorageDirectoryTraversal(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "selfcloud_storage_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	s, err := NewLocalStorage(tempDir)
	if err != nil {
		t.Fatalf("failed to init local storage: %v", err)
	}

	username := "testuser"
	badPaths := []string{
		"../../etc/passwd",
		"/../../etc/passwd",
		"documents/../../../passwd",
		"/documents/../secret",
		"documents/..",
		"..",
		"/..",
		"documents/.. /passwd",
		"documents/\\..\\passwd",
	}

	for _, badPath := range badPaths {
		_, err := s.Write(username, badPath, bytes.NewReader([]byte("hack")))
		if err == nil {
			t.Errorf("expected error for directory traversal path %s, but got none", badPath)
		}
	}

	badUsers := []string{
		"../testuser",
		"user/sub",
		"user\\sub",
		"",
	}
	for _, badUser := range badUsers {
		_, err := s.Write(badUser, "/test.txt", bytes.NewReader([]byte("hack")))
		if err == nil {
			t.Errorf("expected error for invalid username %q, but got none", badUser)
		}
	}
}

func TestLocalStorageValidFilesWithDots(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "selfcloud_storage_dots_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	s, err := NewLocalStorage(tempDir)
	if err != nil {
		t.Fatalf("failed to init local storage: %v", err)
	}

	username := "testuser"
	validPaths := []string{
		"/books/series/item_0001_intro...draft.fb2",
		"/books/series/report_edition...final.fb2",
		"/books/stories/collection_01/story_item - ...excerpt.fb2",
		"/books/stories/collection_01/story_item - ...prologue.fb2",
		"/books/reference/network_guide..djvu",
		"/archive...tar.gz",
		"/file..name.txt",
		"/..hidden_double_dot.txt",
		"/folder/.../deep.txt",
	}

	for _, p := range validPaths {
		content := []byte("valid content for " + p)
		n, writeErr := s.Write(username, p, bytes.NewReader(content))
		if writeErr != nil {
			t.Errorf("expected path %q with dots to be allowed, but got error: %v", p, writeErr)
			continue
		}
		if n != int64(len(content)) {
			t.Errorf("expected %d bytes written for %q, got %d", len(content), p, n)
		}

		if !s.Exists(username, p) {
			t.Errorf("expected path %q to exist after write", p)
		}

		rc, readErr := s.Read(username, p)
		if readErr != nil {
			t.Errorf("failed to read %q: %v", p, readErr)
			continue
		}
		readData, _ := io.ReadAll(rc)
		rc.Close()
		if !bytes.Equal(readData, content) {
			t.Errorf("read content mismatch for %q", p)
		}
	}
}

func TestLocalStorageSymlinkTraversal(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "selfcloud_storage_symlink_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	s, err := NewLocalStorage(tempDir)
	if err != nil {
		t.Fatalf("failed to init local storage: %v", err)
	}

	username := "alice"
	// Write a normal file
	_, err = s.Write(username, "/normal.txt", bytes.NewReader([]byte("normal content")))
	if err != nil {
		t.Fatalf("failed to write normal file: %v", err)
	}

	// Create an external secret file outside tempDir
	outsideDir, err := os.MkdirTemp("", "selfcloud_outside_*")
	if err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}
	defer os.RemoveAll(outsideDir)
	secretFile := outsideDir + "/secret.txt"
	_ = os.WriteFile(secretFile, []byte("super secret"), 0600)

	// Create a symlink inside alice's storage pointing to outsideDir
	userDir := tempDir + "/alice"
	symlinkPath := userDir + "/escaped_link"
	if err := os.Symlink(outsideDir, symlinkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	// Attempting to read via symlink should fail
	_, err = s.Read(username, "/escaped_link/secret.txt")
	if err == nil {
		t.Fatalf("expected symlink escape read to fail, but got nil")
	}

	// Attempting to write via symlink should fail
	_, err = s.Write(username, "/escaped_link/hack.txt", bytes.NewReader([]byte("pwned")))
	if err == nil {
		t.Fatalf("expected symlink escape write to fail, but got nil")
	}
}

func TestLocalStorageConcurrentWrites(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "selfcloud_storage_concurrent_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	s, err := NewLocalStorage(tempDir)
	if err != nil {
		t.Fatalf("failed to init local storage: %v", err)
	}

	username := "concurrentuser"
	relPath := "/data/parallel.txt"

	done := make(chan error, 10)
	for i := range 10 {
		go func(idx int) {
			content := bytes.Repeat([]byte{byte('A' + idx)}, 4096)
			_, writeErr := s.Write(username, relPath, bytes.NewReader(content))
			done <- writeErr
		}(i)
	}

	for i := range 10 {
		if err := <-done; err != nil {
			t.Errorf("concurrent write %d failed: %v", i, err)
		}
	}

	// Verify file exists and is readable
	rc, err := s.Read(username, relPath)
	if err != nil {
		t.Fatalf("failed to read file after concurrent writes: %v", err)
	}
	buf, _ := io.ReadAll(rc)
	rc.Close()

	if len(buf) != 4096 {
		t.Errorf("expected file size 4096, got %d", len(buf))
	}
}
