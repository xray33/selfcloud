package api

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"selfcloud/internal/audit"
	"selfcloud/internal/config"
	"selfcloud/internal/domain"
	"selfcloud/internal/middleware"
	"selfcloud/internal/repository"
)

var ErrAccountLinkConflict = errors.New("account linking conflict: email exists and user is unauthenticated")

type SSOHandler struct {
	cfg          *config.Config
	transactor   repository.Transactor
	userRepo     repository.UserRepository
	sessionRepo  repository.SessionRepository
	nodeRepo     repository.NodeRepository
	identityRepo repository.IdentityRepository
	logger       *slog.Logger
}

func NewSSOHandler(
	cfg *config.Config,
	transactor repository.Transactor,
	userRepo repository.UserRepository,
	sessionRepo repository.SessionRepository,
	nodeRepo repository.NodeRepository,
	identityRepo repository.IdentityRepository,
	logger *slog.Logger,
) *SSOHandler {
	return &SSOHandler{
		cfg:          cfg,
		transactor:   transactor,
		userRepo:     userRepo,
		sessionRepo:  sessionRepo,
		nodeRepo:     nodeRepo,
		identityRepo: identityRepo,
		logger:       logger,
	}
}

type googleUserInfo struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
}

func (h *SSOHandler) getRedirectURI() string {
	return fmt.Sprintf("%s/api/v1/auth/sso/google/callback", strings.TrimRight(h.cfg.SSORedirectBaseURL, "/"))
}

const oauthStateCookieName = "oauth_state"

func (h *SSOHandler) GoogleLoginHandler(w http.ResponseWriter, r *http.Request) {
	if h.cfg.SSOGoogleClientID == "" {
		writeJSONError(w, http.StatusBadRequest, "Google SSO is not configured on this server")
		return
	}

	state := generateOAuthState()
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    state,
		Path:     "/",
		Expires:  time.Now().Add(10 * time.Minute),
		HttpOnly: true,
		Secure:   middleware.IsSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})

	redirectURI := h.getRedirectURI()
	authURL := fmt.Sprintf(
		"https://accounts.google.com/o/oauth2/v2/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=openid%%20email%%20profile&state=%s&prompt=select_account",
		url.QueryEscape(h.cfg.SSOGoogleClientID),
		url.QueryEscape(redirectURI),
		url.QueryEscape(state),
	)

	http.Redirect(w, r, authURL, http.StatusFound)
}

func validateOAuthState(w http.ResponseWriter, r *http.Request) bool {
	cookieState, err := r.Cookie(oauthStateCookieName)
	if err != nil || cookieState.Value == "" {
		audit.RecordFailure(r.Context(), "sso_google_login", "", "SSO failed: missing or expired oauth_state cookie")
		writeJSONError(w, http.StatusBadRequest, "Invalid or expired OAuth state")
		return false
	}

	queryState := r.URL.Query().Get("state")
	if queryState != cookieState.Value {
		audit.RecordFailure(r.Context(), "sso_google_login", "", "SSO failed: state mismatch (possible CSRF)")
		writeJSONError(w, http.StatusBadRequest, "OAuth state mismatch")
		return false
	}

	// Invalidate state cookie
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   middleware.IsSecureRequest(r),
	})
	return true
}

func (h *SSOHandler) GoogleCallbackHandler(w http.ResponseWriter, r *http.Request) {
	if h.cfg.SSOGoogleClientID == "" {
		writeJSONError(w, http.StatusBadRequest, "Google SSO is not configured on this server")
		return
	}

	if !validateOAuthState(w, r) {
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		audit.RecordFailure(r.Context(), "sso_google_login", "", "SSO failed: missing authorization code from Google")
		writeJSONError(w, http.StatusBadRequest, "Missing authorization code")
		return
	}

	userInfo, err := h.exchangeGoogleCode(r.Context(), code, h.getRedirectURI())
	if err != nil {
		h.logger.Error("Failed to exchange Google OAuth code", slog.Any("error", err))
		audit.RecordFailure(r.Context(), "sso_google_login", "", "SSO failed: token exchange with Google failed", slog.Any("error", err))
		writeJSONError(w, http.StatusInternalServerError, "Failed to authenticate with Google")
		return
	}

	if userInfo.Sub == "" || userInfo.Email == "" || !userInfo.EmailVerified {
		audit.RecordFailure(r.Context(), "sso_google_login", "", "SSO failed: unverified or missing email from Google")
		writeJSONError(w, http.StatusBadRequest, "Google account email is not verified or missing")
		return
	}

	var user *domain.User
	sessionToken := generateSessionToken()
	sessionExpiresAt := time.Now().Add(7 * 24 * time.Hour)

	err = h.transactor.WithTx(r.Context(), func(tx *repository.Repositories) error {
		activeUserID := extractActiveUserID(r, tx)
		u, err := resolveOrCreateSSOUser(r.Context(), tx, userInfo, activeUserID)
		if err != nil {
			return err
		}
		user = u

		session := &domain.Session{
			Token:     sessionToken,
			UserID:    user.ID,
			ExpiresAt: sessionExpiresAt,
			CreatedAt: time.Now(),
		}
		if err := tx.Sessions.Create(r.Context(), session); err != nil {
			return fmt.Errorf("failed to create session: %w", err)
		}

		return nil
	})

	if err != nil {
		if errors.Is(err, ErrAccountLinkConflict) {
			audit.RecordFailure(r.Context(), "sso_google_login", userInfo.Email, "SSO failed: account with email exists but user is not authenticated")
			writeJSONError(w, http.StatusConflict, "An account with this email already exists. Please log in with your password first to link Google SSO.")
			return
		}
		h.logger.Error("Google SSO transaction failed", slog.Any("error", err))
		writeJSONError(w, http.StatusInternalServerError, "SSO login processing failed")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     middleware.SessionCookieName,
		Value:    sessionToken,
		Path:     "/",
		Expires:  sessionExpiresAt,
		HttpOnly: true,
		Secure:   middleware.IsSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})

	audit.SetUsername(r.Context(), user.Username)
	audit.Record(r.Context(), "sso_google_login", user.Username, fmt.Sprintf("User %q logged in via Google SSO (%s)", user.Username, userInfo.Email))

	http.Redirect(w, r, "/", http.StatusFound)
}

func extractActiveUserID(r *http.Request, tx *repository.Repositories) int64 {
	sessToken := middleware.ExtractSessionToken(r)
	if sessToken == "" {
		return -1
	}
	sess, err := tx.Sessions.GetByToken(r.Context(), sessToken)
	if err == nil && sess != nil && !sess.IsExpired() {
		return sess.UserID
	}
	return -1
}

func resolveOrCreateSSOUser(ctx context.Context, tx *repository.Repositories, info *googleUserInfo, activeUserID int64) (*domain.User, error) {
	identity, err := tx.Identities.GetByProviderUID(ctx, "google", info.Sub)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup identity: %w", err)
	}

	if identity != nil {
		user, err := tx.Users.GetByID(ctx, identity.UserID)
		if err != nil || user == nil {
			return nil, errors.New("user account no longer exists")
		}
		return user, nil
	}

	user, err := linkOrCreateUser(ctx, tx, info, activeUserID)
	if err != nil {
		return nil, err
	}

	// Bind identity to user
	newIdentity := &domain.UserIdentity{
		UserID:         user.ID,
		Provider:       "google",
		ProviderUserID: info.Sub,
		Email:          info.Email,
		CreatedAt:      time.Now(),
	}
	if err := tx.Identities.Create(ctx, newIdentity); err != nil {
		return nil, fmt.Errorf("failed to create identity: %w", err)
	}

	return user, nil
}

func linkOrCreateUser(ctx context.Context, tx *repository.Repositories, info *googleUserInfo, activeUserID int64) (*domain.User, error) {
	existingUser, _ := tx.Users.GetByEmail(ctx, info.Email)
	if existingUser != nil {
		if activeUserID != existingUser.ID {
			return nil, ErrAccountLinkConflict
		}
		return existingUser, nil
	}

	return provisionSSOUser(ctx, tx, info.Email)
}

func provisionSSOUser(ctx context.Context, tx *repository.Repositories, email string) (*domain.User, error) {
	baseUsername := sanitizeUsername(email)
	username := baseUsername
	counter := 1
	for {
		u, err := tx.Users.GetByUsername(ctx, username)
		if err != nil || u == nil {
			break
		}
		username = fmt.Sprintf("%s_%d", baseUsername, counter)
		counter++
	}

	randomPass, _ := domain.HashPassword(generateOAuthState())
	newUser := &domain.User{
		Username:     username,
		PasswordHash: randomPass,
		Email:        email,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := tx.Users.Create(ctx, newUser); err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	// Seed root directory node for new SSO user
	fileID := fmt.Sprintf("%08doc_%s", newUser.ID, generateOAuthState()[:12])
	etag := fmt.Sprintf("%x", md5.Sum([]byte("/"+fileID)))
	if err := tx.Nodes.Create(ctx, &domain.Node{
		UserID:      newUser.ID,
		Path:        "/",
		ParentPath:  "",
		Name:        "",
		IsDir:       true,
		Size:        0,
		ETag:        etag,
		FileID:      fileID,
		MTime:       time.Now(),
		ContentType: "httpd/unix-directory",
	}); err != nil {
		return nil, fmt.Errorf("failed to seed root node: %w", err)
	}

	return newUser, nil
}

func (h *SSOHandler) exchangeGoogleCode(ctx context.Context, code, redirectURI string) (*googleUserInfo, error) {
	data := url.Values{}
	data.Set("code", code)
	data.Set("client_id", h.cfg.SSOGoogleClientID)
	data.Set("client_secret", h.cfg.SSOGoogleClientSecret)
	data.Set("redirect_uri", redirectURI)
	data.Set("grant_type", "authorization_code")

	tokenReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(tokenReq)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token endpoint returned status %d: %s", resp.StatusCode, string(b))
	}

	var tokenRes struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenRes); err != nil || tokenRes.AccessToken == "" {
		return nil, errors.New("invalid token response")
	}

	userReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v3/userinfo", http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create userinfo request: %w", err)
	}
	userReq.Header.Set("Authorization", "Bearer "+tokenRes.AccessToken)

	userResp, err := client.Do(userReq)
	if err != nil {
		return nil, fmt.Errorf("userinfo request failed: %w", err)
	}
	defer userResp.Body.Close()

	var info googleUserInfo
	if err := json.NewDecoder(userResp.Body).Decode(&info); err != nil || info.Sub == "" {
		return nil, errors.New("invalid userinfo response")
	}

	return &info, nil
}

func generateOAuthState() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func sanitizeUsername(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) > 0 && parts[0] != "" {
		clean := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
				return r
			}
			return '_'
		}, parts[0])
		clean = strings.Trim(clean, "._-")
		if clean != "" {
			return clean
		}
	}
	return "user_" + generateOAuthState()[:8]
}
