package api

import (
	"archive/zip"
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"selfcloud/internal/audit"
	"selfcloud/internal/config"
	"selfcloud/internal/domain"
	"selfcloud/internal/metrics"
	"selfcloud/internal/middleware"
	"selfcloud/internal/pathutil"
	"selfcloud/internal/repository"
	"selfcloud/internal/storage"
)

type FilesHandler struct {
	transactor    repository.Transactor
	nodeRepo      repository.NodeRepository
	storage       storage.Storage
	logger        *slog.Logger
	maxUploadSize int64
}

func NewFilesHandler(transactor repository.Transactor, nodeRepo repository.NodeRepository, blobStore storage.Storage, logger *slog.Logger) *FilesHandler {
	return &FilesHandler{
		transactor:    transactor,
		nodeRepo:      nodeRepo,
		storage:       blobStore,
		logger:        logger,
		maxUploadSize: config.DefaultMaxUploadSize,
	}
}

func (h *FilesHandler) WithMaxUploadSize(size int64) *FilesHandler {
	if size > 0 {
		h.maxUploadSize = size
	}
	return h
}

type MkdirRequest struct {
	Path string `json:"path"`
}

type RenameRequest struct {
	OldPath string `json:"old_path"`
	NewPath string `json:"new_path"`
}

type ZipDownloadRequest struct {
	Paths []string `json:"paths"`
}

// GetFilesHandler returns directory contents or a single file metadata.
func (h *FilesHandler) GetFilesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	user := middleware.UserFromContext(r.Context())
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	targetPath := r.URL.Query().Get("path")
	if targetPath == "" {
		targetPath = "/"
	}
	targetPath = path.Clean(targetPath)

	node, err := h.nodeRepo.GetByPath(r.Context(), user.ID, targetPath)
	if err != nil || node == nil {
		audit.RecordFailure(r.Context(), "api_get_files", targetPath, fmt.Sprintf("User %q requested node %q: not found", user.Username, targetPath))
		writeJSONError(w, http.StatusNotFound, "File or directory not found")
		return
	}

	var items []*domain.Node
	if node.IsDir {
		children, err := h.nodeRepo.ListByParentPath(r.Context(), user.ID, node.Path)
		if err != nil {
			h.logger.Error("API failed listing directory items", slog.String("path", targetPath), slog.Any("error", err))
			writeJSONError(w, http.StatusInternalServerError, "Failed to list directory contents")
			return
		}
		filtered := make([]*domain.Node, 0, len(children))
		for _, child := range children {
			if child.Path != "/" && child.Name != "" && child.ID != node.ID {
				filtered = append(filtered, child)
			}
		}
		items = filtered
		audit.Record(r.Context(), "api_list_dir", targetPath, fmt.Sprintf("User %q listed directory %q (%d items)", user.Username, targetPath, len(items)))
	} else {
		audit.Record(r.Context(), "api_get_file_info", targetPath, fmt.Sprintf("User %q inspected file info for %q", user.Username, targetPath))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"node":  node,
		"items": items,
	})
}

func (h *FilesHandler) resolveUploadDestination(ctx context.Context, userID int64, formPath, formDir, filename string) (string, error) {
	if formPath == "" {
		destDir := formDir
		if destDir == "" {
			destDir = "/"
		}
		fileName := path.Base(filename)
		if fileName == "" || fileName == "." || fileName == "/" {
			return "", errors.New("invalid file name")
		}
		destPath := path.Clean(path.Join(destDir, fileName))
		if err := pathutil.ValidatePathLength(destPath); err != nil {
			return "", err
		}
		return destPath, nil
	}

	cleanDest := path.Clean(formPath)
	targetNode, _ := h.nodeRepo.GetByPath(ctx, userID, cleanDest)
	destPath := cleanDest
	if (targetNode != nil && targetNode.IsDir) || strings.HasSuffix(formPath, "/") || cleanDest == "." || cleanDest == "/" {
		destPath = path.Join(cleanDest, filename)
	}
	destPath = path.Clean(destPath)
	if err := pathutil.ValidatePathLength(destPath); err != nil {
		return "", err
	}
	return destPath, nil
}

func (h *FilesHandler) compensateFailedUpload(username, destPath string, isNew bool, err error) {
	if isNew {
		if delErr := h.storage.Delete(username, destPath); delErr != nil {
			h.logger.Error("Failed to clean up storage after DB failure during upload", slog.String("path", destPath), slog.Any("error", delErr))
		}
	}
	h.logger.Error("API upload node persistence failed", slog.Any("error", err))
}

func parseUploadMTime(ocMTime string) time.Time {
	if ocMTime != "" {
		if sec, err := strconv.ParseInt(ocMTime, 10, 64); err == nil {
			return time.Unix(sec, 0)
		}
	}
	return time.Now()
}

func saveUploadedNode(
	ctx context.Context,
	tx *repository.Repositories,
	userID int64,
	destPath, contentType, etag string,
	size int64,
	mtime time.Time,
) (*domain.Node, bool, error) {
	parentPath, name := pathutil.SplitPath(destPath)
	if err := tx.Nodes.EnsureParentDirectoryExists(ctx, userID, parentPath); err != nil {
		return nil, false, fmt.Errorf("failed to ensure parent directory exists: %w", err)
	}

	existingNode, err := tx.Nodes.GetByPath(ctx, userID, destPath)
	if err != nil {
		return nil, false, err
	}

	var node *domain.Node
	isNew := false

	if existingNode == nil {
		node = &domain.Node{
			UserID:      userID,
			Path:        destPath,
			ParentPath:  parentPath,
			Name:        name,
			IsDir:       false,
			Size:        size,
			ETag:        etag,
			FileID:      pathutil.GenerateFileID(userID),
			MTime:       mtime,
			ContentType: contentType,
		}
		if err := tx.Nodes.Create(ctx, node); err != nil {
			return nil, false, err
		}
		isNew = true
	} else {
		existingNode.Size = size
		existingNode.ETag = etag
		existingNode.MTime = mtime
		existingNode.ContentType = contentType
		if err := tx.Nodes.Update(ctx, existingNode); err != nil {
			return nil, false, err
		}
		node = existingNode
	}

	if err := tx.Nodes.TouchAncestors(ctx, userID, destPath); err != nil {
		return nil, false, err
	}
	return node, isNew, nil
}

// UploadHandler handles file uploads from the Web UI.
func (h *FilesHandler) UploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	user := middleware.UserFromContext(r.Context())
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	maxSize := h.maxUploadSize
	if maxSize <= 0 {
		maxSize = 10 << 30
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSize)

	// In memory parser max memory 32MB
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "Upload payload exceeds maximum allowed size or is invalid")
		return
	}

	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Missing 'file' form data")
		return
	}
	defer file.Close()

	destPath, err := h.resolveUploadDestination(r.Context(), user.ID, r.FormValue("path"), r.FormValue("dir"), fileHeader.Filename)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	size, err := h.storage.Write(user.Username, destPath, file)
	if err != nil {
		h.logger.Error("API upload storage write failed", slog.String("path", destPath), slog.Any("error", err))
		writeJSONError(w, http.StatusInternalServerError, "Failed to save file on storage")
		return
	}

	mtime := parseUploadMTime(r.FormValue("mtime"))
	etag := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("%s-%d-%d", destPath, size, mtime.UnixNano()))))
	contentType := fileHeader.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	var node *domain.Node
	var isNew bool

	err = h.transactor.WithTx(r.Context(), func(tx *repository.Repositories) error {
		n, created, err := saveUploadedNode(r.Context(), tx, user.ID, destPath, contentType, etag, size, mtime)
		if err != nil {
			return err
		}
		node = n
		isNew = created
		return nil
	})

	if err != nil {
		h.compensateFailedUpload(user.Username, destPath, isNew, err)
		writeJSONError(w, http.StatusInternalServerError, "Failed to save node metadata")
		return
	}

	metrics.BlobBytesUploaded.WithLabelValues("api").Add(float64(size))

	if isNew {
		audit.Record(r.Context(), "api_file_create", destPath, fmt.Sprintf("User %q uploaded new file %q (%s)", user.Username, destPath, pathutil.FormatBytes(size)))
	} else {
		audit.Record(r.Context(), "api_file_update", destPath, fmt.Sprintf("User %q updated file %q (%s)", user.Username, destPath, pathutil.FormatBytes(size)))
	}

	writeJSON(w, http.StatusOK, map[string]any{"node": node})
}

// MkdirHandler creates a new directory.
func (h *FilesHandler) MkdirHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	user := middleware.UserFromContext(r.Context())
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req MkdirRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON payload: missing 'path'")
		return
	}
	dirPath := path.Clean(req.Path)
	if err := pathutil.ValidatePathLength(dirPath); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	parentPath, name := pathutil.SplitPath(dirPath)
	fileID := pathutil.GenerateFileID(user.ID)
	etag := fmt.Sprintf("%x", md5.Sum([]byte(dirPath+fileID)))
	node := &domain.Node{
		UserID:      user.ID,
		Path:        dirPath,
		ParentPath:  parentPath,
		Name:        name,
		IsDir:       true,
		Size:        0,
		ETag:        etag,
		FileID:      fileID,
		MTime:       time.Now(),
		ContentType: "httpd/unix-directory",
	}

	if err := h.persistDirNode(r.Context(), user, node); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			writeJSONError(w, http.StatusBadRequest, "Directory or file already exists")
			return
		}
		h.logger.Error("API mkdir create node error", slog.Any("error", err))
		writeJSONError(w, http.StatusInternalServerError, "Failed to save directory metadata")
		return
	}

	if err := h.storage.Mkdir(user.Username, dirPath); err != nil {
		h.logger.Error("API mkdir storage error", slog.String("path", dirPath), slog.Any("error", err))
		_ = h.nodeRepo.Delete(r.Context(), user.ID, dirPath)
		writeJSONError(w, http.StatusInternalServerError, "Failed to create directory on storage")
		return
	}

	audit.Record(r.Context(), "api_mkdir", dirPath, fmt.Sprintf("User %q created directory %q", user.Username, dirPath))
	writeJSON(w, http.StatusCreated, map[string]any{"node": node})
}

func (h *FilesHandler) persistDirNode(ctx context.Context, user *domain.User, node *domain.Node) error {
	return h.transactor.WithTx(ctx, func(tx *repository.Repositories) error {
		existing, err := tx.Nodes.GetByPath(ctx, user.ID, node.Path)
		if err != nil {
			return err
		}
		if existing != nil {
			return domain.ErrAlreadyExists
		}

		if err := tx.Nodes.EnsureParentDirectoryExists(ctx, user.ID, node.ParentPath); err != nil {
			return fmt.Errorf("failed to ensure parent directory exists: %w", err)
		}
		if err := tx.Nodes.Create(ctx, node); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "duplicate") || strings.Contains(err.Error(), "1062") {
				return domain.ErrAlreadyExists
			}
			return err
		}
		return tx.Nodes.TouchAncestors(ctx, user.ID, node.Path)
	})
}

// DeleteHandler removes a file or directory.
func (h *FilesHandler) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	user := middleware.UserFromContext(r.Context())
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	targetPath := r.URL.Query().Get("path")
	if targetPath == "" {
		var body struct {
			Path string `json:"path"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		targetPath = body.Path
	}
	targetPath = path.Clean(targetPath)

	if targetPath == "/" || targetPath == "." {
		writeJSONError(w, http.StatusBadRequest, "Cannot delete root directory")
		return
	}

	node, err := h.nodeRepo.GetByPath(r.Context(), user.ID, targetPath)
	if err != nil || node == nil {
		writeJSONError(w, http.StatusNotFound, "File or directory not found")
		return
	}

	if err := h.deleteNodeMetadata(r.Context(), user.ID, targetPath, node.IsDir); err != nil {
		h.logger.Error("API delete DB transaction failed", slog.String("path", targetPath), slog.Bool("is_dir", node.IsDir), slog.Any("error", err))
		writeJSONError(w, http.StatusInternalServerError, "Failed to delete metadata")
		return
	}

	if node.IsDir {
		_ = h.storage.DeleteAll(user.Username, targetPath)
		audit.Record(r.Context(), "api_delete_dir", targetPath, fmt.Sprintf("User %q deleted directory %q", user.Username, targetPath))
	} else {
		_ = h.storage.Delete(user.Username, targetPath)
		audit.Record(r.Context(), "api_delete_file", targetPath, fmt.Sprintf("User %q deleted file %q", user.Username, targetPath))
	}

	writeStatusOK(w)
}

func (h *FilesHandler) deleteNodeMetadata(ctx context.Context, userID int64, targetPath string, isDir bool) error {
	return h.transactor.WithTx(ctx, func(tx *repository.Repositories) error {
		if isDir {
			if err := tx.Nodes.DeletePrefix(ctx, userID, targetPath); err != nil {
				return err
			}
		} else {
			if err := tx.Nodes.Delete(ctx, userID, targetPath); err != nil {
				return err
			}
		}
		return tx.Nodes.TouchAncestors(ctx, userID, targetPath)
	})
}

// RenameHandler renames or moves a node.
func (h *FilesHandler) RenameHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	user := middleware.UserFromContext(r.Context())
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req RenameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OldPath == "" || req.NewPath == "" {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON payload: missing 'old_path' or 'new_path'")
		return
	}

	oldPath := path.Clean(req.OldPath)
	newPath := path.Clean(req.NewPath)
	if err := pathutil.ValidatePathLength(newPath); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.storage.Move(user.Username, oldPath, newPath); err != nil {
		h.logger.Error("API rename storage move error", slog.String("old", oldPath), slog.String("new", newPath), slog.Any("error", err))
		writeJSONError(w, http.StatusInternalServerError, "Failed to move file in storage")
		return
	}

	if err := h.transactor.WithTx(r.Context(), func(tx *repository.Repositories) error {
		if err := tx.Nodes.MovePrefix(r.Context(), user.ID, oldPath, newPath); err != nil {
			return err
		}
		return tx.Nodes.TouchAncestors(r.Context(), user.ID, oldPath, newPath)
	}); err != nil {
		if revertErr := h.storage.Move(user.Username, newPath, oldPath); revertErr != nil {
			h.logger.Error("CRITICAL: compensating storage move failed, filesystem and database are out of sync",
				slog.String("user", user.Username),
				slog.String("attempted_revert_from", newPath),
				slog.String("attempted_revert_to", oldPath),
				slog.Any("revert_error", revertErr),
			)
		}
		h.logger.Error("API rename node move error", slog.Any("error", err))
		writeJSONError(w, http.StatusInternalServerError, "Failed to update node metadata")
		return
	}

	audit.Record(r.Context(), "api_rename", oldPath, fmt.Sprintf("User %q renamed %q to %q", user.Username, oldPath, newPath))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "old_path": oldPath, "new_path": newPath})
}

// DownloadHandler streams a single file download.
func (h *FilesHandler) DownloadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	user := middleware.UserFromContext(r.Context())
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	filePath := path.Clean(r.URL.Query().Get("path"))
	node, err := h.nodeRepo.GetByPath(r.Context(), user.ID, filePath)
	if err != nil || node == nil || node.IsDir {
		writeJSONError(w, http.StatusNotFound, "File not found")
		return
	}

	rc, err := h.storage.Read(user.Username, filePath)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "File storage read error")
		return
	}
	defer rc.Close()

	w.Header().Set("ETag", fmt.Sprintf("%q", node.ETag))
	if node.ContentType != "" {
		w.Header().Set("Content-Type", node.ContentType)
	}
	w.Header().Set("Content-Disposition", pathutil.FormatContentDisposition("attachment", path.Base(node.Path)))

	metrics.BlobBytesDownloaded.WithLabelValues("api").Add(float64(node.Size))
	audit.Record(r.Context(), "api_download_file", filePath, fmt.Sprintf("User %q downloaded file %q (%s)", user.Username, filePath, pathutil.FormatBytes(node.Size)))
	http.ServeContent(w, r, path.Base(node.Path), node.MTime, rc)
}

// DownloadZipHandler streams a dynamic ZIP archive for a directory or multiple selected paths on the fly.
func (h *FilesHandler) DownloadZipHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	user := middleware.UserFromContext(r.Context())
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	targetPaths := extractZipTargetPaths(r)
	if len(targetPaths) == 0 {
		writeJSONError(w, http.StatusBadRequest, "No paths specified for ZIP download")
		return
	}

	archiveName := resolveArchiveName(targetPaths)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", pathutil.FormatContentDisposition("attachment", archiveName))

	zipWriter := zip.NewWriter(w)
	defer zipWriter.Close()

	h.streamZipNodes(r.Context(), user, targetPaths, zipWriter)
	audit.Record(r.Context(), "api_download_zip", targetPaths[0], fmt.Sprintf("User %q downloaded ZIP archive for %v", user.Username, targetPaths))
}

func extractZipTargetPaths(r *http.Request) []string {
	var targetPaths []string
	if reqPath := r.URL.Query().Get("path"); reqPath != "" {
		targetPaths = append(targetPaths, path.Clean(reqPath))
	} else if r.Method == http.MethodPost {
		var req ZipDownloadRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		for _, p := range req.Paths {
			targetPaths = append(targetPaths, path.Clean(p))
		}
	}
	return targetPaths
}

func resolveArchiveName(targetPaths []string) string {
	if len(targetPaths) == 1 && targetPaths[0] != "/" {
		return fmt.Sprintf("%s.zip", path.Base(targetPaths[0]))
	}
	return "download.zip"
}

func (h *FilesHandler) streamZipNodes(ctx context.Context, user *domain.User, targetPaths []string, zw *zip.Writer) {
	for _, targetPath := range targetPaths {
		if ctx.Err() != nil {
			break
		}
		node, err := h.nodeRepo.GetByPath(ctx, user.ID, targetPath)
		if err != nil || node == nil {
			continue
		}
		if node.IsDir {
			if err := h.addFolderToZip(ctx, user, node.Path, zw); err != nil {
				h.logger.WarnContext(ctx, "ZIP directory streaming interrupted", slog.String("path", node.Path), slog.Any("error", err))
				break
			}
		} else {
			if err := h.addFileToZip(ctx, user.Username, node, node.Name, zw); err != nil {
				h.logger.WarnContext(ctx, "ZIP file streaming interrupted", slog.String("path", node.Path), slog.Any("error", err))
				break
			}
		}
	}
}

const maxZipRecursionDepth = 64

type zipDirContext struct {
	ctx     context.Context
	user    *domain.User
	zw      *zip.Writer
	h       *FilesHandler
	visited map[string]bool
}

func (z *zipDirContext) addRecursive(currentPath, zipPrefix string, depth int) error {
	if depth > maxZipRecursionDepth {
		return errors.New("maximum zip directory depth exceeded")
	}
	if err := z.ctx.Err(); err != nil {
		return err
	}
	if z.visited[currentPath] {
		return nil
	}
	z.visited[currentPath] = true

	children, err := z.h.nodeRepo.ListByParentPath(z.ctx, z.user.ID, currentPath)
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := z.ctx.Err(); err != nil {
			return err
		}
		if child.Path == currentPath || child.Path == "/" || child.Name == "" || z.visited[child.Path] {
			continue
		}

		zipEntryPath := path.Join(zipPrefix, child.Name)
		if err := z.addZipChild(child, zipEntryPath, depth); err != nil {
			return err
		}
	}
	return nil
}

func (z *zipDirContext) addZipChild(child *domain.Node, zipEntryPath string, depth int) error {
	if child.IsDir {
		if _, err := z.zw.Create(zipEntryPath + "/"); err != nil {
			return err
		}
		return z.addRecursive(child.Path, zipEntryPath, depth+1)
	}
	return z.h.addFileToZip(z.ctx, z.user.Username, child, zipEntryPath, z.zw)
}

func (h *FilesHandler) addFolderToZip(ctx context.Context, user *domain.User, folderPath string, zw *zip.Writer) error {
	zCtx := &zipDirContext{
		ctx:     ctx,
		user:    user,
		zw:      zw,
		h:       h,
		visited: make(map[string]bool),
	}

	if folderPath == "/" {
		return zCtx.addRecursive("/", "", 0)
	}
	baseDir := path.Base(folderPath)
	if _, err := zw.Create(baseDir + "/"); err != nil {
		return err
	}
	return zCtx.addRecursive(folderPath, baseDir, 0)
}

func (h *FilesHandler) addFileToZip(ctx context.Context, username string, node *domain.Node, zipPath string, zw *zip.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rc, err := h.storage.Read(username, node.Path)
	if err != nil {
		return err
	}
	defer rc.Close()

	header := &zip.FileHeader{
		Name:     zipPath,
		Method:   zip.Deflate,
		Modified: node.MTime,
	}
	writer, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}

	_, err = io.Copy(writer, rc)
	return err
}
