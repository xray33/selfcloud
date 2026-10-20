package pathutil

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mime"
	"path"
	"strings"
)

// FormatContentDisposition safely constructs a Content-Disposition header with proper parameter escaping.
func FormatContentDisposition(dispositionType, filename string) string {
	cleanFilename := strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, filename)
	if cleanFilename == "" {
		cleanFilename = "download"
	}
	formatted := mime.FormatMediaType(dispositionType, map[string]string{
		"filename": cleanFilename,
	})
	if formatted != "" {
		return formatted
	}
	return fmt.Sprintf(`%s; filename="%s"`, dispositionType, cleanFilename)
}

// MaxPathLength is the maximum allowed path length supported by the database schema.
const MaxPathLength = 700

// ValidatePathLength checks if the path exceeds the maximum allowed length (700 characters).
func ValidatePathLength(p string) error {
	if len(p) > MaxPathLength {
		return fmt.Errorf("path length %d exceeds maximum limit of %d characters", len(p), MaxPathLength)
	}
	return nil
}

// SplitPath returns (parentPath, name) for a given absolute path.
func SplitPath(p string) (parentPath, name string) {
	p = path.Clean(p)
	if p == "/" || p == "." || p == "" {
		return "", ""
	}
	dir, file := path.Split(p)
	dir = path.Clean(dir)
	if dir == "." {
		dir = "/"
	}
	return dir, file
}

// NormalizePath cleans and normalizes a WebDAV path with a leading slash and no trailing slash.
func NormalizePath(p string) string {
	if p == "" {
		return ""
	}
	p = path.Clean("/" + strings.TrimPrefix(p, "/"))
	if p == "/" {
		return "/"
	}
	return strings.TrimSuffix(p, "/")
}

// FormatBytes formats a byte count into a human-readable string.
func FormatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n := n / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// GenerateFileID creates a unique, non-colliding oc:fileid for a user.
func GenerateFileID(userID int64) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%08doc_%s", userID, hex.EncodeToString(b))
}

// Ancestors returns all ancestor directory paths for a given path up to and including root "/".
// For example, for "/Temp/macroses.txt", it returns []string{"/Temp", "/"}.
// For "/file.txt", it returns []string{"/"}.
// For "/Temp" or "/", it returns []string{"/"}.
func Ancestors(p string) []string {
	p = NormalizePath(p)
	if p == "" || p == "/" {
		return []string{"/"}
	}

	var ancestors []string
	curr := p
	for {
		parent, _ := SplitPath(curr)
		if parent == "" {
			break
		}
		ancestors = append(ancestors, parent)
		if parent == "/" {
			break
		}
		curr = parent
	}
	if len(ancestors) == 0 {
		return []string{"/"}
	}
	return ancestors
}

