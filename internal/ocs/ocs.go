package ocs

import (
	"encoding/json"
	"net/http"

	"selfcloud/internal/config"
	"selfcloud/internal/middleware"
)

type OCSHandler struct {
	cfg *config.Config
}

func NewOCSHandler(cfgs ...*config.Config) *OCSHandler {
	h := &OCSHandler{}
	if len(cfgs) > 0 {
		h.cfg = cfgs[0]
	}
	return h
}

func (h *OCSHandler) WithConfig(cfg *config.Config) *OCSHandler {
	h.cfg = cfg
	return h
}

func (h *OCSHandler) StatusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	status := map[string]any{
		"installed":      true,
		"maintenance":    false,
		"needsDbUpgrade": false,
		"version":        "10.11.0.3",
		"versionstring":  "10.11.0",
		"edition":        "Community",
		"productname":    "ownCloud",
	}
	_ = json.NewEncoder(w).Encode(status)
}

func (h *OCSHandler) CapabilitiesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	oauthEnabled := true
	spacesEnabled := false
	if h.cfg != nil {
		oauthEnabled = h.cfg.IsOAuthEnabled()
		spacesEnabled = h.cfg.EnableSpaces
	}

	caps := map[string]any{
		"core": map[string]any{
			"pollinterval": 60,
			"webdav-root":  "remote.php/webdav",
		},
		"files": map[string]any{
			"bigfilechunking": false,
			"undelete":        false,
			"versioning":      false,
		},
		"oauth2": map[string]any{
			"enabled": oauthEnabled,
		},
	}

	if spacesEnabled {
		caps["spaces"] = map[string]any{
			"enabled":                      true,
			"has_multiple_personal_spaces": false,
			"version":                      "1.0.0",
		}
	}

	resp := map[string]any{
		"ocs": map[string]any{
			"meta": map[string]any{
				"status":     "ok",
				"statuscode": 100,
				"message":    "OK",
			},
			"data": map[string]any{
				"version": map[string]any{
					"major":   10,
					"minor":   11,
					"micro":   0,
					"string":  "10.11.0",
					"edition": "Community",
				},
				"capabilities": caps,
			},
		},
	}

	_ = json.NewEncoder(w).Encode(resp)
}

func (h *OCSHandler) UserHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	user := middleware.UserFromContext(r.Context())

	username := "guest"
	email := ""
	if user != nil {
		username = user.Username
		email = user.Email
	}

	resp := map[string]any{
		"ocs": map[string]any{
			"meta": map[string]any{
				"status":     "ok",
				"statuscode": 100,
				"message":    "OK",
			},
			"data": map[string]any{
				"id":           username,
				"display-name": username,
				"email":        email,
				"quota": map[string]any{
					"free":     107374182400, // 100 GB dummy
					"used":     0,
					"total":    107374182400,
					"relative": 0,
					"quota":    107374182400,
				},
			},
		},
	}

	_ = json.NewEncoder(w).Encode(resp)
}
