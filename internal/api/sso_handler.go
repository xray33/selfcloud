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

func (h *SSOHandler) getRedirectURI(r *http.Request) string {
	return fmt.Sprintf("%s/api/v1/auth/sso/google/callback", strings.TrimRight(h.cfg.SSORedirectBaseURL, "/"))
}

func (h *SSOHandler) GoogleLoginHandler(w http.ResponseWriter, r *http.Request) {
	if h.cfg.SSOGoogleClientID == "" {
		writeJSONError(w, http.StatusBadRequest, "Google SSO is not configured on this server")
		return
	}

	state := generateOAuthState()
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/",
		Expires:  time.Now().Add(10 * time.Minute),
		HttpOnly: true,
		Secure:   middleware.IsSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})

	redirectURI := h.getRedirectURI(r)
	authURL := fmt.Sprintf(
		"https://accounts.google.com/o/oauth2/v2/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=openid%%20email%%20profile&state=%s&prompt=select_account",
		url.QueryEscape(h.cfg.SSOGoogleClientID),
		url.QueryEscape(redirectURI),
		url.QueryEscape(state),
	)

	http.Redirect(w, r, authURL, http.StatusFound)
}

func (h *SSOHandler) GoogleCallbackHandler(w http.ResponseWriter, r *http.Request) {
	if h.cfg.SSOGoogleClientID == "" {
		writeJSONError(w, http.StatusBadRequest, "Google SSO is not configured on this server")
		return
	}

	cookieState, err := r.Cookie("oauth_state")
	if err != nil || cookieState.Value == "" {
		audit.RecordFailure(r.Context(), "sso_google_login", "", "SSO failed: missing or expired oauth_state cookie")
		writeJSONError(w, http.StatusBadRequest, "Invalid or expired OAuth state")
		return
	}

	queryState := r.URL.Query().Get("state")
	if queryState != cookieState.Value {
		audit.RecordFailure(r.Context(), "sso_google_login", "", "SSO failed: state mismatch (possible CSRF)")
		writeJSONError(w, http.StatusBadRequest, "OAuth state mismatch")
		return
	}

	// Invalidate state cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   middleware.IsSecureRequest(r),
	})

	code := r.URL.Query().Get("code")
	if code == "" {
		audit.RecordFailure(r.Context(), "sso_google_login", "", "SSO failed: missing authorization code from Google")
		writeJSONError(w, http.StatusBadRequest, "Missing authorization code")
		return
	}

	userInfo, err := h.exchangeGoogleCode(r.Context(), code, h.getRedirectURI(r))
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
		// 1. Check if identity exists by (provider="google", sub=userInfo.Sub)
		identity, err := tx.Identities.GetByProviderUID(r.Context(), "google", userInfo.Sub)
		if err != nil {
			return fmt.Errorf("failed to lookup identity: %w", err)
		}

		if identity != nil {
			user, err = tx.Users.GetByID(r.Context(), identity.UserID)
			if err != nil || user == nil {
				return fmt.Errorf("user account no longer exists")
			}
		} else {
			// 2. Lookup existing user by email
			existingUser, _ := tx.Users.GetByEmail(r.Context(), userInfo.Email)
			if existingUser != nil {
				// To prevent account takeover, an existing account can only be linked to a new SSO identity
				// if the request originates from an active authenticated session belonging to that user.
				var activeUserID int64 = -1
				if sessToken := middleware.ExtractSessionToken(r); sessToken != "" {
					if sess, err := tx.Sessions.GetByToken(r.Context(), sessToken); err == nil && sess != nil && !sess.IsExpired() {
						activeUserID = sess.UserID
					}
				}
				if activeUserID != existingUser.ID {
					return ErrAccountLinkConflict
				}
				user = existingUser
			} else {
				// Auto-provision user account with a unique non-colliding username
				baseUsername := sanitizeUsername(userInfo.Email)
				username := baseUsername
				counter := 1
				for {
					u, err := tx.Users.GetByUsername(r.Context(), username)
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
					Email:        userInfo.Email,
					CreatedAt:    time.Now(),
					UpdatedAt:    time.Now(),
				}
				if err := tx.Users.Create(r.Context(), newUser); err != nil {
					return fmt.Errorf("failed to create user: %w", err)
				}
				user = newUser

				// Seed root directory node for new SSO user
				fileID := fmt.Sprintf("%08doc_%s", user.ID, generateOAuthState()[:12])
				etag := fmt.Sprintf("%x", md5.Sum([]byte("/"+fileID)))
				if err := tx.Nodes.Create(r.Context(), &domain.Node{
					UserID:      user.ID,
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
					return fmt.Errorf("failed to seed root node: %w", err)
				}
			}

			// Bind identity to user
			newIdentity := &domain.UserIdentity{
				UserID:         user.ID,
				Provider:       "google",
				ProviderUserID: userInfo.Sub,
				Email:          userInfo.Email,
				CreatedAt:      time.Now(),
			}
			if err := tx.Identities.Create(r.Context(), newIdentity); err != nil {
				return fmt.Errorf("failed to create identity: %w", err)
			}
		}

		// 3. Issue session token
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
		return nil, fmt.Errorf("invalid token response")
	}

	userReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v3/userinfo", nil)
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
		return nil, fmt.Errorf("invalid userinfo response")
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
