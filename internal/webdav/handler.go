package webdav

import (
	"crypto/md5"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
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

// WebDAVHandler handles all WebDAV protocol methods for ownCloud-compatible sync clients.
type WebDAVHandler struct {
	transactor    repository.Transactor
	nodeRepo      repository.NodeRepository
	storage       storage.Storage
	logger        *slog.Logger
	maxUploadSize int64
}

func NewWebDAVHandler(transactor repository.Transactor, nodeRepo repository.NodeRepository, storage storage.Storage, logger *slog.Logger) *WebDAVHandler {
	return &WebDAVHandler{
		transactor:    transactor,
		nodeRepo:      nodeRepo,
		storage:       storage,
		logger:        logger,
		maxUploadSize: config.DefaultMaxUploadSize,
	}
}

func (h *WebDAVHandler) WithMaxUploadSize(size int64) *WebDAVHandler {
	if size > 0 {
		h.maxUploadSize = size
	}
	return h
}

type webdavStatusRecorder struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
}

func (rec *webdavStatusRecorder) WriteHeader(code int) {
	if !rec.wroteHeader {
		rec.statusCode = code
		rec.wroteHeader = true
	}
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *webdavStatusRecorder) Write(b []byte) (int, error) {
	if !rec.wroteHeader {
		rec.WriteHeader(http.StatusOK)
	}
	return rec.ResponseWriter.Write(b)
}

func (rec *webdavStatusRecorder) Flush() {
	if flusher, ok := rec.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (h *WebDAVHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromContext(r.Context())
	if user == nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="selfcloud"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	basePath, relPath := extractPaths(r.URL.Path, user.Username)

	// WebDAV Headers required by ownCloud clients
	w.Header().Set("DAV", "1, 2")
	w.Header().Set("MS-Author-Via", "DAV")
	w.Header().Set("Allow", "OPTIONS, GET, HEAD, POST, PUT, DELETE, PROPFIND, PROPPATCH, MKCOL, COPY, MOVE")

	rec := &webdavStatusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
	defer func() {
		status := "success"
		if rec.statusCode >= 400 {
			status = "failure"
		}
		metrics.WebdavOpsTotal.WithLabelValues(r.Method, status).Inc()
	}()

	switch r.Method {
	case "OPTIONS":
		h.handleOptions(rec, r)
	case "PROPFIND":
		h.handlePropfind(rec, r, user, basePath, relPath)
	case "GET", "HEAD":
		h.handleGet(rec, r, user, relPath)
	case "PUT":
		h.handlePut(rec, r, user, relPath)
	case "DELETE":
		h.handleDelete(rec, r, user, relPath)
	case "MKCOL":
		h.handleMkcol(rec, r, user, relPath)
	case "MOVE":
		h.handleMove(rec, r, user, basePath, relPath)
	case "COPY":
		h.handleCopy(rec, r, user, basePath, relPath)
	case "PROPPATCH":
		h.handleProppatch(rec, r, user, basePath, relPath)
	default:
		http.Error(rec, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func (h *WebDAVHandler) handleOptions(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (h *WebDAVHandler) handlePropfind(w http.ResponseWriter, r *http.Request, user *domain.User, basePath, relPath string) {
	depth := r.Header.Get("Depth")
	if depth == "" {
		depth = "1"
	}

	node, err := h.nodeRepo.GetByPath(r.Context(), user.ID, relPath)
	if err != nil || node == nil {
		audit.RecordFailure(r.Context(), "propfind", relPath,
			fmt.Sprintf("User %q tried to propfind %q: not found", user.Username, relPath),
		)
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	var nodes []*domain.Node
	nodes = append(nodes, node)

	if node.IsDir && depth != "0" {
		children, err := h.nodeRepo.ListByParentPath(r.Context(), user.ID, node.Path)
		if err == nil {
			for _, child := range children {
				if child.Path != "/" && child.Name != "" {
					nodes = append(nodes, child)
				}
			}
		}
	}

	xmlData, err := FormatMultiStatus(basePath, nodes)
	if err != nil {
		h.logger.Error("PROPFIND failed to format multistatus XML", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	_, _ = w.Write(xmlData)

	if node.IsDir {
		audit.Record(r.Context(), "dir_propfind", relPath,
			fmt.Sprintf("User %q listed properties for directory %q (depth %s, %d items returned)", user.Username, relPath, depth, len(nodes)),
			slog.Int("item_count", len(nodes)),
			slog.String("depth", depth),
		)
	} else {
		audit.Record(r.Context(), "file_propfind", relPath,
			fmt.Sprintf("User %q fetched properties for file %q", user.Username, relPath),
		)
	}
}

func (h *WebDAVHandler) handleGet(w http.ResponseWriter, r *http.Request, user *domain.User, relPath string) {
	node, err := h.nodeRepo.GetByPath(r.Context(), user.ID, relPath)
	if err != nil || node == nil {
		audit.RecordFailure(r.Context(), "file_download", relPath,
			fmt.Sprintf("User %q tried to download %q: not found", user.Username, relPath),
		)
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	if node.IsDir {
		http.Error(w, "Method Not Allowed on directory", http.StatusMethodNotAllowed)
		return
	}

	rc, err := h.storage.Read(user.Username, relPath)
	if err != nil {
		h.logger.Error("GET failed to read storage blob", slog.String("path", relPath), slog.Any("error", err))
		audit.RecordFailure(r.Context(), "file_download", relPath,
			fmt.Sprintf("User %q failed to download %q: storage read error", user.Username, relPath),
			slog.Any("error", err),
		)
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	defer rc.Close()

	w.Header().Set("ETag", fmt.Sprintf(`"%s"`, node.ETag))
	w.Header().Set("OC-ETag", fmt.Sprintf(`"%s"`, node.ETag))
	w.Header().Set("OC-FileId", node.FileID)
	if node.ContentType != "" {
		w.Header().Set("Content-Type", node.ContentType)
	}

	if r.Method == "HEAD" {
		audit.Record(r.Context(), "file_head", relPath,
			fmt.Sprintf("User %q checked metadata of %q (%s, %s)", user.Username, relPath, node.ContentType, pathutil.FormatBytes(node.Size)),
			slog.Int64("size_bytes", node.Size),
		)
	} else {
		audit.Record(r.Context(), "file_download", relPath,
			fmt.Sprintf("User %q downloaded %q (%s, %s)", user.Username, relPath, node.ContentType, pathutil.FormatBytes(node.Size)),
			slog.Int64("size_bytes", node.Size),
			slog.String("content_type", node.ContentType),
		)
	}

	metrics.BlobBytesDownloaded.WithLabelValues("webdav").Add(float64(node.Size))
	http.ServeContent(w, r, path.Base(node.Path), node.MTime, rc)
}

func (h *WebDAVHandler) handlePut(w http.ResponseWriter, r *http.Request, user *domain.User, relPath string) {
	// Parse optional ownCloud client timestamp header
	mtime := time.Now()
	if ocMTime := r.Header.Get("X-OC-MTime"); ocMTime != "" {
		if sec, err := strconv.ParseInt(ocMTime, 10, 64); err == nil {
			mtime = time.Unix(sec, 0)
		}
	}

	if err := pathutil.ValidatePathLength(relPath); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	maxSize := h.maxUploadSize
	if maxSize <= 0 {
		maxSize = 10 << 30
	}
	limitedBody := http.MaxBytesReader(w, r.Body, maxSize)

	size, err := h.storage.Write(user.Username, relPath, limitedBody)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			audit.RecordFailure(r.Context(), "file_upload", relPath,
				fmt.Sprintf("User %q upload failed: payload exceeds maximum allowed size of %s", user.Username, pathutil.FormatBytes(maxSize)),
			)
			http.Error(w, "Request Entity Too Large", http.StatusRequestEntityTooLarge)
			return
		}
		h.logger.Error("PUT storage write failed", slog.String("path", relPath), slog.Any("error", err))
		audit.RecordFailure(r.Context(), "file_upload", relPath,
			fmt.Sprintf("User %q failed to upload %q: storage write error", user.Username, relPath),
			slog.Any("error", err),
		)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	etag := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("%s-%d-%d", relPath, size, mtime.UnixNano()))))
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	parentPath, name := pathutil.SplitPath(relPath)
	isNew := false

	err = h.transactor.WithTx(r.Context(), func(tx *repository.Repositories) error {
		if err := tx.Nodes.EnsureParentDirectoryExists(r.Context(), user.ID, parentPath); err != nil {
			return fmt.Errorf("failed to ensure parent directory exists: %w", err)
		}

		existingNode, err := tx.Nodes.GetByPath(r.Context(), user.ID, relPath)
		if err != nil {
			return err
		}

		if existingNode == nil {
			isNew = true
			fileID := pathutil.GenerateFileID(user.ID)
			node := &domain.Node{
				UserID:      user.ID,
				Path:        relPath,
				ParentPath:  parentPath,
				Name:        name,
				IsDir:       false,
				Size:        size,
				ETag:        etag,
				FileID:      fileID,
				MTime:       mtime,
				ContentType: contentType,
			}
			return tx.Nodes.Create(r.Context(), node)
		}

		existingNode.Size = size
		existingNode.ETag = etag
		existingNode.MTime = mtime
		existingNode.ContentType = contentType
		return tx.Nodes.Update(r.Context(), existingNode)
	})

	if err != nil {
		if isNew {
			if delErr := h.storage.Delete(user.Username, relPath); delErr != nil {
				h.logger.Error("Failed to clean up storage after DB failure during PUT", slog.String("path", relPath), slog.Any("error", delErr))
			}
		}
		h.logger.Error("PUT database transaction failed", slog.Any("error", err))
		audit.RecordFailure(r.Context(), "file_upload", relPath,
			fmt.Sprintf("User %q failed to save metadata for %q: database error", user.Username, relPath),
			slog.Any("error", err),
		)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	metrics.BlobBytesUploaded.WithLabelValues("webdav").Add(float64(size))

	if isNew {
		audit.Record(r.Context(), "file_create", relPath,
			fmt.Sprintf("User %q uploaded new file %q (%s, %s)", user.Username, relPath, contentType, pathutil.FormatBytes(size)),
			slog.Int64("size_bytes", size),
			slog.String("content_type", contentType),
			slog.String("etag", etag),
		)
	} else {
		audit.Record(r.Context(), "file_update", relPath,
			fmt.Sprintf("User %q updated existing file %q (%s, %s)", user.Username, relPath, contentType, pathutil.FormatBytes(size)),
			slog.Int64("size_bytes", size),
			slog.String("content_type", contentType),
			slog.String("etag", etag),
		)
	}

	w.Header().Set("ETag", fmt.Sprintf(`"%s"`, etag))
	w.Header().Set("OC-ETag", fmt.Sprintf(`"%s"`, etag))
	if isNew {
		w.WriteHeader(http.StatusCreated)
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *WebDAVHandler) handleDelete(w http.ResponseWriter, r *http.Request, user *domain.User, relPath string) {
	node, err := h.nodeRepo.GetByPath(r.Context(), user.ID, relPath)
	if err != nil || node == nil {
		audit.RecordFailure(r.Context(), "node_delete", relPath,
			fmt.Sprintf("User %q tried to delete %q: not found", user.Username, relPath),
		)
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	if node.IsDir {
		// Count children before deletion for the audit log.
		children, _ := h.nodeRepo.ListByParentPath(r.Context(), user.ID, relPath)
		childCount := len(children)

		if err := h.transactor.WithTx(r.Context(), func(tx *repository.Repositories) error {
			return tx.Nodes.DeletePrefix(r.Context(), user.ID, relPath)
		}); err != nil {
			h.logger.Error("DELETE dir database transaction failed", slog.String("path", relPath), slog.Any("error", err))
			audit.RecordFailure(r.Context(), "dir_delete", relPath,
				fmt.Sprintf("User %q failed to delete directory metadata for %q: database error", user.Username, relPath),
				slog.Any("error", err),
			)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		_ = h.storage.DeleteAll(user.Username, relPath)

		audit.Record(r.Context(), "dir_delete", relPath,
			fmt.Sprintf("User %q deleted directory %q and %d contained items", user.Username, relPath, childCount),
			slog.Int("child_count", childCount),
		)
	} else {
		if err := h.transactor.WithTx(r.Context(), func(tx *repository.Repositories) error {
			return tx.Nodes.Delete(r.Context(), user.ID, relPath)
		}); err != nil {
			h.logger.Error("DELETE file database transaction failed", slog.String("path", relPath), slog.Any("error", err))
			audit.RecordFailure(r.Context(), "file_delete", relPath,
				fmt.Sprintf("User %q failed to delete file metadata for %q: database error", user.Username, relPath),
				slog.Any("error", err),
			)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		_ = h.storage.Delete(user.Username, relPath)

		audit.Record(r.Context(), "file_delete", relPath,
			fmt.Sprintf("User %q deleted file %q (%s)", user.Username, relPath, pathutil.FormatBytes(node.Size)),
			slog.Int64("size_bytes", node.Size),
		)
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *WebDAVHandler) handleMkcol(w http.ResponseWriter, r *http.Request, user *domain.User, relPath string) {
	if err := pathutil.ValidatePathLength(relPath); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	parentPath, name := pathutil.SplitPath(relPath)
	fileID := pathutil.GenerateFileID(user.ID)
	etag := fmt.Sprintf("%x", md5.Sum([]byte(relPath+fileID)))

	node := &domain.Node{
		UserID:      user.ID,
		Path:        relPath,
		ParentPath:  parentPath,
		Name:        name,
		IsDir:       true,
		Size:        0,
		ETag:        etag,
		FileID:      fileID,
		MTime:       time.Now(),
		ContentType: "httpd/unix-directory",
	}

	err := h.transactor.WithTx(r.Context(), func(tx *repository.Repositories) error {
		existingNode, err := tx.Nodes.GetByPath(r.Context(), user.ID, relPath)
		if err != nil {
			return err
		}
		if existingNode != nil {
			return domain.ErrAlreadyExists
		}

		if err := tx.Nodes.EnsureParentDirectoryExists(r.Context(), user.ID, parentPath); err != nil {
			return fmt.Errorf("failed to ensure parent directory exists: %w", err)
		}
		if err := tx.Nodes.Create(r.Context(), node); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "duplicate") || strings.Contains(err.Error(), "1062") {
				return domain.ErrAlreadyExists
			}
			return err
		}
		return nil
	})

	if err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			audit.RecordFailure(r.Context(), "dir_create", relPath,
				fmt.Sprintf("User %q tried to create directory %q: already exists", user.Username, relPath),
			)
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		h.logger.Error("MKCOL failed to create node metadata in tx", slog.Any("error", err))
		audit.RecordFailure(r.Context(), "dir_create", relPath,
			fmt.Sprintf("User %q failed to create directory metadata for %q: database error", user.Username, relPath),
			slog.Any("error", err),
		)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if err := h.storage.Mkdir(user.Username, relPath); err != nil {
		h.logger.Error("MKCOL failed storage mkdir", slog.String("path", relPath), slog.Any("error", err))
		_ = h.nodeRepo.Delete(r.Context(), user.ID, relPath)
		audit.RecordFailure(r.Context(), "dir_create", relPath,
			fmt.Sprintf("User %q failed to create directory %q: storage error", user.Username, relPath),
			slog.Any("error", err),
		)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	audit.Record(r.Context(), "dir_create", relPath,
		fmt.Sprintf("User %q created directory %q", user.Username, relPath),
	)
	w.WriteHeader(http.StatusCreated)
}

func (h *WebDAVHandler) handleMove(w http.ResponseWriter, r *http.Request, user *domain.User, basePath, relPath string) {
	destHeader := r.Header.Get("Destination")
	if destHeader == "" {
		http.Error(w, "Bad Request: missing Destination header", http.StatusBadRequest)
		return
	}

	destURL, err := url.Parse(destHeader)
	if err != nil {
		http.Error(w, "Bad Request: invalid Destination header", http.StatusBadRequest)
		return
	}

	_, destRelPath := extractPaths(destURL.Path, user.Username)

	if err := pathutil.ValidatePathLength(destRelPath); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.storage.Move(user.Username, relPath, destRelPath); err != nil {
		h.logger.Error("MOVE storage rename failed", slog.String("old", relPath), slog.String("new", destRelPath), slog.Any("error", err))
		audit.RecordFailure(r.Context(), "file_move", relPath,
			fmt.Sprintf("User %q failed to move %q to %q: storage error", user.Username, relPath, destRelPath),
			slog.String("destination", destRelPath),
			slog.Any("error", err),
		)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if err := h.transactor.WithTx(r.Context(), func(tx *repository.Repositories) error {
		return tx.Nodes.MovePrefix(r.Context(), user.ID, relPath, destRelPath)
	}); err != nil {
		if revertErr := h.storage.Move(user.Username, destRelPath, relPath); revertErr != nil {
			h.logger.Error("CRITICAL: compensating storage move failed, filesystem and database are out of sync",
				slog.String("user", user.Username),
				slog.String("attempted_revert_from", destRelPath),
				slog.String("attempted_revert_to", relPath),
				slog.Any("revert_error", revertErr),
			)
		}
		h.logger.Error("MOVE node metadata rename failed", slog.Any("error", err))
		audit.RecordFailure(r.Context(), "file_move", relPath,
			fmt.Sprintf("User %q failed to update metadata while moving %q to %q: database error", user.Username, relPath, destRelPath),
			slog.String("destination", destRelPath),
			slog.Any("error", err),
		)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	audit.Record(r.Context(), "file_move", relPath,
		fmt.Sprintf("User %q moved %q to %q", user.Username, relPath, destRelPath),
		slog.String("destination", destRelPath),
	)
	w.WriteHeader(http.StatusCreated)
}

func (h *WebDAVHandler) handleCopy(w http.ResponseWriter, r *http.Request, user *domain.User, basePath, relPath string) {
	destHeader := r.Header.Get("Destination")
	if destHeader == "" {
		http.Error(w, "Bad Request: missing Destination header", http.StatusBadRequest)
		return
	}

	destURL, err := url.Parse(destHeader)
	if err != nil {
		http.Error(w, "Bad Request: invalid Destination header", http.StatusBadRequest)
		return
	}

	_, destRelPath := extractPaths(destURL.Path, user.Username)

	if err := pathutil.ValidatePathLength(destRelPath); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	sourceNode, err := h.nodeRepo.GetByPath(r.Context(), user.ID, relPath)
	if err != nil || sourceNode == nil {
		audit.RecordFailure(r.Context(), "file_copy", relPath,
			fmt.Sprintf("User %q failed to copy %q to %q: source not found", user.Username, relPath, destRelPath),
			slog.String("destination", destRelPath),
		)
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	overwriteHeader := r.Header.Get("Overwrite")
	if overwriteHeader == "" {
		overwriteHeader = "T" // RFC 4918 default
	}

	existingDest, _ := h.nodeRepo.GetByPath(r.Context(), user.ID, destRelPath)
	if existingDest != nil && strings.EqualFold(overwriteHeader, "F") {
		audit.RecordFailure(r.Context(), "file_copy", relPath,
			fmt.Sprintf("User %q copy of %q to %q rejected: destination exists and Overwrite=F", user.Username, relPath, destRelPath),
			slog.String("destination", destRelPath),
		)
		http.Error(w, "Precondition Failed", http.StatusPreconditionFailed)
		return
	}

	if sourceNode.IsDir {
		h.handleCopyDir(w, r, user, sourceNode, relPath, destRelPath, existingDest != nil)
		return
	}

	rc, err := h.storage.Read(user.Username, relPath)
	if err != nil {
		audit.RecordFailure(r.Context(), "file_copy", relPath,
			fmt.Sprintf("User %q failed to copy %q to %q: source not found in storage", user.Username, relPath, destRelPath),
			slog.String("destination", destRelPath),
		)
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	defer rc.Close()

	size, err := h.storage.Write(user.Username, destRelPath, rc)
	if err != nil {
		audit.RecordFailure(r.Context(), "file_copy", relPath,
			fmt.Sprintf("User %q failed to copy %q to %q: storage write error", user.Username, relPath, destRelPath),
			slog.String("destination", destRelPath),
			slog.Any("error", err),
		)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	parentPath, name := pathutil.SplitPath(destRelPath)
	fileID := pathutil.GenerateFileID(user.ID)
	etag := fmt.Sprintf("%x", md5.Sum([]byte(destRelPath+fileID)))

	node := &domain.Node{
		UserID:      user.ID,
		Path:        destRelPath,
		ParentPath:  parentPath,
		Name:        name,
		IsDir:       false,
		Size:        size,
		ETag:        etag,
		FileID:      fileID,
		MTime:       time.Now(),
		ContentType: sourceNode.ContentType,
	}

	err = h.transactor.WithTx(r.Context(), func(tx *repository.Repositories) error {
		if err := tx.Nodes.EnsureParentDirectoryExists(r.Context(), user.ID, parentPath); err != nil {
			return fmt.Errorf("failed to ensure parent directory exists: %w", err)
		}
		if existingDest != nil {
			node.ID = existingDest.ID
			return tx.Nodes.Update(r.Context(), node)
		}
		return tx.Nodes.Create(r.Context(), node)
	})
	if err != nil {
		if delErr := h.storage.Delete(user.Username, destRelPath); delErr != nil {
			h.logger.Error("Failed to clean up storage after DB failure during COPY", slog.String("path", destRelPath), slog.Any("error", delErr))
		}
		h.logger.Error("COPY database transaction failed", slog.String("destination", destRelPath), slog.Any("error", err))
		audit.RecordFailure(r.Context(), "file_copy", relPath,
			fmt.Sprintf("User %q failed to save copied file metadata for %q: database error", user.Username, destRelPath),
			slog.Any("error", err),
		)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	audit.Record(r.Context(), "file_copy", relPath,
		fmt.Sprintf("User %q copied %q to %q (%s)", user.Username, relPath, destRelPath, pathutil.FormatBytes(size)),
		slog.String("destination", destRelPath),
		slog.Int64("size_bytes", size),
	)

	if existingDest != nil {
		w.WriteHeader(http.StatusNoContent)
	} else {
		w.WriteHeader(http.StatusCreated)
	}
}

func (h *WebDAVHandler) handleCopyDir(w http.ResponseWriter, r *http.Request, user *domain.User, sourceNode *domain.Node, relPath, destRelPath string, isOverwrite bool) {
	depth := r.Header.Get("Depth")
	if depth == "" {
		depth = "infinity"
	}

	if isOverwrite {
		_ = h.storage.DeleteAll(user.Username, destRelPath)
	}

	if err := h.storage.Mkdir(user.Username, destRelPath); err != nil {
		h.logger.Error("COPY failed storage mkdir for dest", slog.String("dest", destRelPath), slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	parentPath, name := pathutil.SplitPath(destRelPath)
	fileID := pathutil.GenerateFileID(user.ID)
	etag := fmt.Sprintf("%x", md5.Sum([]byte(destRelPath+fileID)))

	destDirNode := &domain.Node{
		UserID:      user.ID,
		Path:        destRelPath,
		ParentPath:  parentPath,
		Name:        name,
		IsDir:       true,
		Size:        0,
		ETag:        etag,
		FileID:      fileID,
		MTime:       time.Now(),
		ContentType: "httpd/unix-directory",
	}

	var nodesToCreate []*domain.Node
	nodesToCreate = append(nodesToCreate, destDirNode)

	// Perform all filesystem I/O outside of the database transaction
	if depth != "0" {
		visited := make(map[string]bool)
		var copyDescendants func(currSrc, currDest string, d int) error
		copyDescendants = func(currSrc, currDest string, d int) error {
			if d > 64 {
				return fmt.Errorf("copy directory recursion depth exceeded")
			}
			if visited[currSrc] {
				return nil
			}
			visited[currSrc] = true

			children, err := h.nodeRepo.ListByParentPath(r.Context(), user.ID, currSrc)
			if err != nil {
				return err
			}
			for _, child := range children {
				childDest := path.Join(currDest, child.Name)
				childParent, childName := pathutil.SplitPath(childDest)
				childFileID := pathutil.GenerateFileID(user.ID)
				childETag := fmt.Sprintf("%x", md5.Sum([]byte(childDest+childFileID)))

				if child.IsDir {
					if err := h.storage.Mkdir(user.Username, childDest); err != nil {
						return err
					}
					cNode := &domain.Node{
						UserID:      user.ID,
						Path:        childDest,
						ParentPath:  childParent,
						Name:        childName,
						IsDir:       true,
						Size:        0,
						ETag:        childETag,
						FileID:      childFileID,
						MTime:       time.Now(),
						ContentType: "httpd/unix-directory",
					}
					nodesToCreate = append(nodesToCreate, cNode)
					if err := copyDescendants(child.Path, childDest, d+1); err != nil {
						return err
					}
				} else {
					rc, err := h.storage.Read(user.Username, child.Path)
					if err != nil {
						return err
					}
					cSize, err := h.storage.Write(user.Username, childDest, rc)
					_ = rc.Close()
					if err != nil {
						return err
					}
					cNode := &domain.Node{
						UserID:      user.ID,
						Path:        childDest,
						ParentPath:  childParent,
						Name:        childName,
						IsDir:       false,
						Size:        cSize,
						ETag:        childETag,
						FileID:      childFileID,
						MTime:       time.Now(),
						ContentType: child.ContentType,
					}
					nodesToCreate = append(nodesToCreate, cNode)
				}
			}
			return nil
		}

		if err := copyDescendants(relPath, destRelPath, 1); err != nil {
			_ = h.storage.DeleteAll(user.Username, destRelPath)
			h.logger.Error("COPY directory storage failed", slog.String("dest", destRelPath), slog.Any("error", err))
			audit.RecordFailure(r.Context(), "dir_copy", relPath,
				fmt.Sprintf("User %q failed to copy directory storage from %q to %q: %v", user.Username, relPath, destRelPath, err),
			)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}

	// Fast atomic DB metadata persistence
	err := h.transactor.WithTx(r.Context(), func(tx *repository.Repositories) error {
		if isOverwrite {
			_ = tx.Nodes.DeletePrefix(r.Context(), user.ID, destRelPath)
		}
		if err := tx.Nodes.EnsureParentDirectoryExists(r.Context(), user.ID, parentPath); err != nil {
			return fmt.Errorf("failed to ensure parent directory exists: %w", err)
		}
		for _, n := range nodesToCreate {
			if err := tx.Nodes.Create(r.Context(), n); err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		_ = h.storage.DeleteAll(user.Username, destRelPath)
		h.logger.Error("COPY directory DB transaction failed", slog.String("dest", destRelPath), slog.Any("error", err))
		audit.RecordFailure(r.Context(), "dir_copy", relPath,
			fmt.Sprintf("User %q failed to copy directory metadata for %q to %q: %v", user.Username, relPath, destRelPath, err),
		)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	audit.Record(r.Context(), "dir_copy", relPath,
		fmt.Sprintf("User %q copied directory %q to %q (depth: %s)", user.Username, relPath, destRelPath, depth),
		slog.String("destination", destRelPath),
	)

	if isOverwrite {
		w.WriteHeader(http.StatusNoContent)
	} else {
		w.WriteHeader(http.StatusCreated)
	}
}

func (h *WebDAVHandler) handleProppatch(w http.ResponseWriter, r *http.Request, user *domain.User, basePath, relPath string) {
	node, err := h.nodeRepo.GetByPath(r.Context(), user.ID, relPath)
	if err != nil || node == nil {
		audit.RecordFailure(r.Context(), "file_proppatch", relPath,
			fmt.Sprintf("User %q tried to patch properties of %q: not found", user.Username, relPath),
		)
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	xmlData, _ := FormatMultiStatus(basePath, []*domain.Node{node})
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	_, _ = w.Write(xmlData)

	audit.Record(r.Context(), "file_proppatch", relPath,
		fmt.Sprintf("User %q patched properties of %q", user.Username, relPath),
	)
}

func extractPaths(rawPath, username string) (basePath, relPath string) {
	// Standard ownCloud client endpoints:
	// /remote.php/webdav/...
	// /remote.php/dav/files/{username}/...
	// /webdav/...
	// /dav/files/{username}/...

	rawPath = path.Clean(rawPath)
	prefixes := []string{
		"/remote.php/dav/files/" + username,
		"/dav/files/" + username,
		"/remote.php/webdav",
		"/webdav",
	}

	for _, p := range prefixes {
		if strings.HasPrefix(rawPath, p) {
			rel := strings.TrimPrefix(rawPath, p)
			if rel == "" {
				rel = "/"
			}
			return p, rel
		}
	}

	return "", rawPath
}
