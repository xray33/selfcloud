package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Storage interface {
	Write(username, relPath string, r io.Reader) (int64, error)
	Read(username, relPath string) (io.ReadSeekCloser, error)
	Delete(username, relPath string) error
	DeleteAll(username, relPath string) error
	Mkdir(username, relPath string) error
	Move(username, oldRelPath, newRelPath string) error
	Exists(username, relPath string) bool
}

type localStorage struct {
	baseDir string
}

func NewLocalStorage(baseDir string) (Storage, error) {
	abs, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("invalid storage base dir: %w", err)
	}
	if err := os.MkdirAll(abs, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base storage directory: %w", err)
	}
	return &localStorage{baseDir: abs}, nil
}

func (s *localStorage) resolvePath(username, relPath string) (string, error) {
	if username == "" || strings.Contains(username, "..") || strings.ContainsAny(username, `/\`) {
		return "", fmt.Errorf("invalid username: %s", username)
	}

	for _, part := range strings.FieldsFunc(relPath, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if part == ".." || strings.TrimSpace(part) == ".." {
			return "", fmt.Errorf("directory traversal attempt detected: %s", relPath)
		}
	}

	cleanRel := filepath.Clean(relPath)
	cleanRel = strings.TrimPrefix(cleanRel, "/")
	cleanRel = strings.TrimPrefix(cleanRel, `\`)

	userDir := filepath.Clean(filepath.Join(s.baseDir, username))
	fullPath := filepath.Clean(filepath.Join(userDir, cleanRel))

	userDirSep := userDir + string(filepath.Separator)
	if fullPath != userDir && !strings.HasPrefix(fullPath, userDirSep) {
		return "", fmt.Errorf("directory traversal attempt detected: %s", relPath)
	}

	if err := checkSymlinkEscape(userDir, fullPath); err != nil {
		return "", err
	}

	return fullPath, nil
}

func checkSymlinkEscape(userDir, fullPath string) error {
	realUserDir, err := filepath.EvalSymlinks(userDir)
	if err != nil {
		if os.IsNotExist(err) {
			realBaseDir, bErr := filepath.EvalSymlinks(filepath.Dir(userDir))
			if bErr == nil {
				realUserDir = filepath.Join(realBaseDir, filepath.Base(userDir))
			} else {
				realUserDir = userDir
			}
		} else {
			return fmt.Errorf("failed to evaluate user directory symlinks: %w", err)
		}
	}
	realUserDirSep := realUserDir + string(filepath.Separator)

	curr := fullPath
	for {
		_, err := os.Lstat(curr)
		if err == nil {
			realCurr, evalErr := filepath.EvalSymlinks(curr)
			if evalErr != nil {
				return fmt.Errorf("symlink resolution failed: %w", evalErr)
			}
			if realCurr != realUserDir && !strings.HasPrefix(realCurr, realUserDirSep) {
				return fmt.Errorf("symlink traversal attempt detected: target resolves outside user storage root")
			}
			break
		}
		parent := filepath.Dir(curr)
		if parent == curr || len(parent) < len(userDir) {
			break
		}
		curr = parent
	}
	return nil
}

func (s *localStorage) Write(username, relPath string, r io.Reader) (int64, error) {
	fullPath, err := s.resolvePath(username, relPath)
	if err != nil {
		return 0, err
	}

	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, fmt.Errorf("failed to create directory structure: %w", err)
	}

	baseName := filepath.Base(fullPath)
	out, err := os.CreateTemp(dir, "."+baseName+"-*.tmp")
	if err != nil {
		return 0, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpFile := out.Name()

	n, err := io.Copy(out, r)
	closeErr := out.Close()
	if err != nil {
		_ = os.Remove(tmpFile)
		return 0, fmt.Errorf("failed to write file content: %w", err)
	}
	if closeErr != nil {
		_ = os.Remove(tmpFile)
		return 0, fmt.Errorf("failed to flush file content: %w", closeErr)
	}

	if err := os.Rename(tmpFile, fullPath); err != nil {
		_ = os.Remove(tmpFile)
		return 0, fmt.Errorf("failed to finalize file: %w", err)
	}

	return n, nil
}

func (s *localStorage) Read(username, relPath string) (io.ReadSeekCloser, error) {
	fullPath, err := s.resolvePath(username, relPath)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	return f, nil
}

func (s *localStorage) Delete(username, relPath string) error {
	fullPath, err := s.resolvePath(username, relPath)
	if err != nil {
		return err
	}
	return os.Remove(fullPath)
}

func (s *localStorage) DeleteAll(username, relPath string) error {
	fullPath, err := s.resolvePath(username, relPath)
	if err != nil {
		return err
	}
	return os.RemoveAll(fullPath)
}

func (s *localStorage) Mkdir(username, relPath string) error {
	fullPath, err := s.resolvePath(username, relPath)
	if err != nil {
		return err
	}
	return os.MkdirAll(fullPath, 0755)
}

func (s *localStorage) Move(username, oldRelPath, newRelPath string) error {
	oldPath, err := s.resolvePath(username, oldRelPath)
	if err != nil {
		return err
	}
	newPath, err := s.resolvePath(username, newRelPath)
	if err != nil {
		return err
	}

	dir := filepath.Dir(newPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.Rename(oldPath, newPath)
}

func (s *localStorage) Exists(username, relPath string) bool {
	fullPath, err := s.resolvePath(username, relPath)
	if err != nil {
		return false
	}
	_, err = os.Stat(fullPath)
	return err == nil
}
