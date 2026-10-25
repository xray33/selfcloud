package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"selfcloud/internal/audit"
	"selfcloud/internal/config"
	"selfcloud/internal/domain"
	"selfcloud/internal/middleware"
	"selfcloud/internal/repository"
)

type AuthHandler struct {
	userRepo     repository.UserRepository
	sessionRepo  repository.SessionRepository
	identityRepo repository.IdentityRepository
	cfg          *config.Config
	logger       *slog.Logger
}

func NewAuthHandler(userRepo repository.UserRepository, sessionRepo repository.SessionRepository, logger *slog.Logger) *AuthHandler {
	return &AuthHandler{
		userRepo:    userRepo,
		sessionRepo: sessionRepo,
		logger:      logger,
	}
}

func (h *AuthHandler) WithIdentityRepo(repo repository.IdentityRepository) *AuthHandler {
	h.identityRepo = repo
	return h
}

func (h *AuthHandler) WithConfig(cfg *config.Config) *AuthHandler {
	h.cfg = cfg
	return h
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type UserResponse struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	CreatedAt    time.Time `json:"created_at"`
	GoogleLinked bool      `json:"google_linked"`
	SSOEnabled   bool      `json:"sso_enabled"`
}

type LoginResponse struct {
	User  UserResponse `json:"user"`
	Token string       `json:"token,omitempty"`
}

func (h *AuthHandler) LoginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" || req.Password == "" {
		audit.RecordFailure(r.Context(), "web_login", req.Username, "Login failed: invalid request payload")
		writeJSONError(w, http.StatusBadRequest, "Invalid username or password request")
		return
	}

	user, err := h.userRepo.GetByUsername(r.Context(), req.Username)
	if err != nil || user == nil {
		domain.CheckDummyPassword(req.Password)
		audit.RecordFailure(r.Context(), "web_login", req.Username, fmt.Sprintf("Web login failed for user %q: invalid credentials", req.Username))
		writeJSONError(w, http.StatusUnauthorized, "Invalid username or password")
		return
	}

	if !user.CheckPassword(req.Password) {
		audit.RecordFailure(r.Context(), "web_login", req.Username, fmt.Sprintf("Web login failed for user %q: invalid credentials", req.Username))
		writeJSONError(w, http.StatusUnauthorized, "Invalid username or password")
		return
	}

	token := generateSessionToken()
	session := &domain.Session{
		Token:     token,
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour), // 7 days session
		CreatedAt: time.Now(),
	}

	if err := h.sessionRepo.Create(r.Context(), session); err != nil {
		h.logger.Error("Failed to create session", slog.Any("error", err))
		writeJSONError(w, http.StatusInternalServerError, "Failed to create user session")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     middleware.SessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  session.ExpiresAt,
		HttpOnly: true,
		Secure:   middleware.IsSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})

	audit.SetUsername(r.Context(), user.Username)
	audit.Record(r.Context(), "web_login", user.Username, fmt.Sprintf("User %q logged in via Web UI", user.Username))

	writeJSON(w, http.StatusOK, LoginResponse{
		User:  h.toUserResponse(r.Context(), user),
		Token: token,
	})
}

func (h *AuthHandler) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	user := middleware.UserFromContext(r.Context())
	token := extractCookieOrHeaderToken(r)

	if token != "" {
		if err := h.sessionRepo.DeleteByToken(r.Context(), token); err != nil {
			h.logger.Warn("Failed to delete session token from database during logout", slog.Any("error", err))
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     middleware.SessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   middleware.IsSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})

	username := "-"
	if user != nil {
		username = user.Username
	}
	audit.Record(r.Context(), "web_logout", username, fmt.Sprintf("User %q logged out", username))

	writeStatusOK(w)
}

func (h *AuthHandler) MeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	user := middleware.UserFromContext(r.Context())
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	writeJSON(w, http.StatusOK, map[string]UserResponse{
		"user": h.toUserResponse(r.Context(), user),
	})
}

func (h *AuthHandler) toUserResponse(ctx context.Context, user *domain.User) UserResponse {
	googleLinked := false
	if h.identityRepo != nil {
		if identities, err := h.identityRepo.ListByUserID(ctx, user.ID); err == nil {
			for _, id := range identities {
				if id.Provider == "google" {
					googleLinked = true
					break
				}
			}
		}
	}

	ssoEnabled := h.cfg != nil && h.cfg.SSOGoogleClientID != ""

	return UserResponse{
		ID:           user.ID,
		Username:     user.Username,
		Email:        user.Email,
		CreatedAt:    user.CreatedAt,
		GoogleLinked: googleLinked,
		SSOEnabled:   ssoEnabled,
	}
}

func generateSessionToken() string {
	bytes := make([]byte, 32)
	_, _ = rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func extractCookieOrHeaderToken(r *http.Request) string {
	return middleware.ExtractSessionToken(r)
}
