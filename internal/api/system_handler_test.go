package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSystemInfoHandler(t *testing.T) {
	handler := NewSystemHandler("1.2.3", "abcdef", "2026-09-06T12:00:00Z")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/info", nil)
	rr := httptest.NewRecorder()

	handler.SystemInfoHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	contentType := rr.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}

	var resp struct {
		Service BuildInfo `json:"service"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Service.Version != "1.2.3" {
		t.Errorf("expected version 1.2.3, got %s", resp.Service.Version)
	}
	if resp.Service.Commit != "abcdef" {
		t.Errorf("expected commit abcdef, got %s", resp.Service.Commit)
	}
	if resp.Service.BuildDate != "2026-09-06T12:00:00Z" {
		t.Errorf("expected build date 2026-09-06T12:00:00Z, got %s", resp.Service.BuildDate)
	}
}
