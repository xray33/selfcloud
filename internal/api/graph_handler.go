package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"selfcloud/internal/middleware"
	"selfcloud/internal/repository"
)

type GraphHandler struct {
	userRepo repository.UserRepository
	nodeRepo repository.NodeRepository
}

func NewGraphHandler(userRepo repository.UserRepository, nodeRepo repository.NodeRepository) *GraphHandler {
	return &GraphHandler{
		userRepo: userRepo,
		nodeRepo: nodeRepo,
	}
}

// MeDrivesHandler serves Microsoft Graph compatible drive listing for ownCloud Infinite Scale client.
func (h *GraphHandler) MeDrivesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	user := middleware.UserFromContext(r.Context())
	if user == nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="selfcloud"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = "localhost:8080"
	}

	webDavURL := fmt.Sprintf("%s://%s/remote.php/webdav", scheme, host)
	webURL := fmt.Sprintf("%s://%s/", scheme, host)

	var usedBytes int64
	rootETag := `"1"`
	if h.nodeRepo != nil {
		rootNode, err := h.nodeRepo.GetByPath(r.Context(), user.ID, "")
		if err == nil && rootNode != nil {
			if rootNode.ETag != "" {
				rootETag = fmt.Sprintf("%q", rootNode.ETag)
			}
			usedBytes = rootNode.Size
		}
	}

	resp := map[string]any{
		"value": []map[string]any{
			{
				"id":        fmt.Sprintf("personal-%d", user.ID),
				"name":      "Personal",
				"driveType": "personal",
				"root": map[string]any{
					"id":        fmt.Sprintf("root-%d", user.ID),
					"webDavUrl": webDavURL,
					"webUrl":    webURL,
					"eTag":      rootETag,
				},
				"quota": map[string]any{
					"total": 107374182400, // 100 GB default
					"used":  usedBytes,
				},
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
