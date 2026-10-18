package ocs_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"selfcloud/internal/domain"
	"selfcloud/internal/middleware"
	"selfcloud/internal/ocs"
)

func TestStatusHandler(t *testing.T) {
	handler := ocs.NewOCSHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/status.php", nil)

	handler.StatusHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	var status map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}

	if status["installed"] != true {
		t.Errorf("expected installed: true, got %v", status["installed"])
	}
	if status["version"] != "10.11.0.3" {
		t.Errorf("expected version 10.11.0.3, got %v", status["version"])
	}
}

func TestCapabilitiesHandler(t *testing.T) {
	handler := ocs.NewOCSHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/ocs/v1.php/cloud/capabilities", nil)

	handler.CapabilitiesHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}

	ocsMap, ok := body["ocs"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'ocs' key in response")
	}

	meta := ocsMap["meta"].(map[string]any)
	if meta["status"] != "ok" || meta["statuscode"] != float64(100) {
		t.Errorf("unexpected meta: %v", meta)
	}

	data := ocsMap["data"].(map[string]any)
	caps := data["capabilities"].(map[string]any)
	spaces, ok := caps["spaces"].(map[string]any)
	if !ok || spaces["enabled"] != true {
		t.Errorf("expected spaces capability enabled, got %v", caps["spaces"])
	}
}

func TestUserHandler(t *testing.T) {
	handler := ocs.NewOCSHandler()

	// 1. Without authenticated user in context (guest)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/ocs/v1.php/cloud/user", nil)
	handler.UserHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	var guestResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &guestResp); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
	guestData := guestResp["ocs"].(map[string]any)["data"].(map[string]any)
	if guestData["id"] != "guest" {
		t.Errorf("expected id 'guest', got %v", guestData["id"])
	}

	// 2. With authenticated user in context
	user := &domain.User{Username: "bob", Email: "bob@example.com"}
	ctx := middleware.WithUser(context.Background(), user)
	reqWithUser := httptest.NewRequest("GET", "/ocs/v1.php/cloud/user", nil).WithContext(ctx)
	recWithUser := httptest.NewRecorder()

	handler.UserHandler(recWithUser, reqWithUser)

	if recWithUser.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", recWithUser.Code)
	}

	var userResp map[string]any
	if err := json.Unmarshal(recWithUser.Body.Bytes(), &userResp); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
	userData := userResp["ocs"].(map[string]any)["data"].(map[string]any)
	if userData["id"] != "bob" {
		t.Errorf("expected id 'bob', got %v", userData["id"])
	}
	if userData["email"] != "bob@example.com" {
		t.Errorf("expected email 'bob@example.com', got %v", userData["email"])
	}
}
