package api

import (
	"encoding/json"
	"net/http"
)

// BuildInfo holds version metadata injected at compile time.
type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
}

// SystemHandler serves system information endpoints.
type SystemHandler struct {
	buildInfo BuildInfo
}

// NewSystemHandler creates a SystemHandler with the given build metadata.
func NewSystemHandler(version, commit, buildDate string) *SystemHandler {
	return &SystemHandler{
		buildInfo: BuildInfo{
			Version:   version,
			Commit:    commit,
			BuildDate: buildDate,
		},
	}
}

// SystemInfoHandler returns build version information.
func (h *SystemHandler) SystemInfoHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"service": h.buildInfo,
	})
}
