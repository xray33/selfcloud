package api

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"selfcloud/internal/audit"
	"selfcloud/internal/domain"
	"selfcloud/internal/metrics"
	"selfcloud/internal/middleware"
	"selfcloud/internal/pathutil"
	"selfcloud/internal/repository"
	"selfcloud/internal/storage"
)

type SharesHandler struct {
	transactor repository.Transactor
	shareRepo  repository.ShareRepository
	nodeRepo   repository.NodeRepository
	userRepo   repository.UserRepository
	storage    storage.Storage
	logger     *slog.Logger
}

func NewSharesHandler(transactor repository.Transactor, shareRepo repository.ShareRepository, nodeRepo repository.NodeRepository, userRepo repository.UserRepository, blobStore storage.Storage, logger *slog.Logger) *SharesHandler {
	return &SharesHandler{
		transactor: transactor,
		shareRepo:  shareRepo,
		nodeRepo:   nodeRepo,
		userRepo:   userRepo,
		storage:    blobStore,
		logger:     logger,
	}
}

type CreateShareRequest struct {
	Path      string     `json:"path"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type ShareResponse struct {
	ID        int64      `json:"id"`
	Token     string     `json:"token"`
	Path      string     `json:"path"`
	IsDir     bool       `json:"is_dir"`
	ShareURL  string     `json:"share_url"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// CreateShareHandler creates a new public link share for a file or directory.
func (h *SharesHandler) CreateShareHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	user := middleware.UserFromContext(r.Context())
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req CreateShareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON payload: missing 'path'")
		return
	}

	cleanPath := path.Clean(req.Path)
	var share *domain.Share
	isNew := false

	err := h.transactor.WithTx(r.Context(), func(tx *repository.Repositories) error {
		node, err := tx.Nodes.GetByPath(r.Context(), user.ID, cleanPath)
		if err != nil {
			return err
		}
		if node == nil {
			return errors.New("node not found")
		}

		// Check if already shared
		existing, err := tx.Shares.GetByPath(r.Context(), user.ID, cleanPath)
		if err != nil {
			return err
		}
		if existing != nil {
			share = existing
			return nil
		}

		token := generateShareToken()
		share = &domain.Share{
			Token:     token,
			UserID:    user.ID,
			Path:      cleanPath,
			IsDir:     node.IsDir,
			ExpiresAt: req.ExpiresAt,
			CreatedAt: time.Now(),
		}

		if err := tx.Shares.Create(r.Context(), share); err != nil {
			return fmt.Errorf("failed to create share in tx: %w", err)
		}
		isNew = true
		return nil
	})

	if err != nil {
		if err.Error() == "node not found" {
			writeJSONError(w, http.StatusNotFound, "File or directory not found")
			return
		}
		h.logger.Error("Failed to create share", slog.Any("error", err))
		writeJSONError(w, http.StatusInternalServerError, "Failed to create public share")
		return
	}

	if isNew {
		audit.Record(r.Context(), "api_create_share", cleanPath, fmt.Sprintf("User %q created public share link for %q", user.Username, cleanPath))
		writeJSON(w, http.StatusCreated, map[string]interface{}{
			"share": formatShareResponse(r, share),
		})
	} else {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"share": formatShareResponse(r, share),
		})
	}
}

// ListSharesHandler returns all active shares created by the authenticated user.
func (h *SharesHandler) ListSharesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	user := middleware.UserFromContext(r.Context())
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	shares, err := h.shareRepo.ListByUserID(r.Context(), user.ID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to fetch user shares")
		return
	}

	var res []ShareResponse
	for _, s := range shares {
		res = append(res, formatShareResponse(r, s))
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"shares": res,
	})
}

// RevokeShareHandler deletes/revokes a public share link by token.
func (h *SharesHandler) RevokeShareHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	user := middleware.UserFromContext(r.Context())
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	token := r.URL.Query().Get("token")
	if token == "" {
		var body struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		token = body.Token
	}

	if token == "" {
		writeJSONError(w, http.StatusBadRequest, "Missing 'token' query parameter or body payload")
		return
	}

	if err := h.shareRepo.DeleteByToken(r.Context(), token, user.ID); err != nil {
		writeJSONError(w, http.StatusNotFound, "Share not found or access denied")
		return
	}

	audit.Record(r.Context(), "api_revoke_share", token, fmt.Sprintf("User %q revoked public share %q", user.Username, token))
	writeStatusOK(w)
}

// PublicShareInfoHandler returns public unauthenticated preview info for a share token.
func (h *SharesHandler) PublicShareInfoHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	token := r.URL.Query().Get("token")
	if token == "" {
		// Extract token from URL path if registered as /s/{token} or /api/v1/public/shares/{token}
		token = path.Base(r.URL.Path)
	}

	share, err := h.shareRepo.GetByToken(r.Context(), token)
	if err != nil || share == nil || share.IsExpired() {
		writeJSONError(w, http.StatusNotFound, "Public share link not found or expired")
		return
	}

	node, err := h.nodeRepo.GetByPath(r.Context(), share.UserID, share.Path)
	if err != nil || node == nil {
		writeJSONError(w, http.StatusNotFound, "Shared file or folder no longer exists")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":        share.Token,
		"name":         path.Base(share.Path),
		"path":         share.Path,
		"is_dir":       share.IsDir,
		"size":         node.Size,
		"mtime":        node.MTime,
		"content_type": node.ContentType,
		"expires_at":   share.ExpiresAt,
	})
}

// PublicShareDownloadHandler allows unauthenticated visitors to download the shared file or directory ZIP.
func (h *SharesHandler) PublicShareDownloadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	token := r.URL.Query().Get("token")
	if token == "" {
		// e.g. /s/{token}/download
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) >= 2 {
			token = parts[len(parts)-2]
		}
	}

	share, err := h.shareRepo.GetByToken(r.Context(), token)
	if err != nil || share == nil || share.IsExpired() {
		writeJSONError(w, http.StatusNotFound, "Public share link not found or expired")
		return
	}

	owner, err := h.userRepo.GetByID(r.Context(), share.UserID)
	if err != nil || owner == nil {
		writeJSONError(w, http.StatusNotFound, "Share owner account no longer exists")
		return
	}

	node, err := h.nodeRepo.GetByPath(r.Context(), share.UserID, share.Path)
	if err != nil || node == nil {
		writeJSONError(w, http.StatusNotFound, "Shared file or folder no longer exists")
		return
	}

	if node.IsDir {
		archiveName := fmt.Sprintf("%s.zip", path.Base(node.Path))
		if node.Path == "/" {
			archiveName = "shared_root.zip"
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", pathutil.FormatContentDisposition("attachment", archiveName))

		zipWriter := zip.NewWriter(w)
		defer zipWriter.Close()

		fh := &FilesHandler{nodeRepo: h.nodeRepo, storage: h.storage, logger: h.logger}
		_ = fh.addFolderToZip(r.Context(), owner, node.Path, zipWriter)
		audit.Record(r.Context(), "public_share_download_zip", share.Path, fmt.Sprintf("Public visitor downloaded ZIP archive for shared folder %q (owner: %s)", share.Path, owner.Username))
		return
	}

	rc, err := h.storage.Read(owner.Username, node.Path)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "Shared file storage read error")
		return
	}
	defer rc.Close()

	w.Header().Set("ETag", fmt.Sprintf("%q", node.ETag))
	if node.ContentType != "" {
		w.Header().Set("Content-Type", node.ContentType)
	}
	w.Header().Set("Content-Disposition", pathutil.FormatContentDisposition("attachment", path.Base(node.Path)))

	metrics.BlobBytesDownloaded.WithLabelValues("share").Add(float64(node.Size))
	audit.Record(r.Context(), "public_share_download", share.Path, fmt.Sprintf("Public visitor downloaded shared file %q (owner: %s)", share.Path, owner.Username))
	http.ServeContent(w, r, path.Base(node.Path), node.MTime, rc)
}

func formatShareResponse(r *http.Request, s *domain.Share) ShareResponse {
	shareURL := fmt.Sprintf("%s/s/%s", resolveBaseURL(r), s.Token)

	return ShareResponse{
		ID:        s.ID,
		Token:     s.Token,
		Path:      s.Path,
		IsDir:     s.IsDir,
		ShareURL:  shareURL,
		ExpiresAt: s.ExpiresAt,
		CreatedAt: s.CreatedAt,
	}
}

func generateShareToken() string {
	bytes := make([]byte, 16)
	_, _ = rand.Read(bytes)
	return fmt.Sprintf("s_%s", hex.EncodeToString(bytes))
}
