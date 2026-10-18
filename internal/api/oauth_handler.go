package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"selfcloud/internal/audit"
	"selfcloud/internal/domain"
	"selfcloud/internal/middleware"
	"selfcloud/internal/repository"
)

const (
	// DefaultOwnCloudClientID and DefaultOwnCloudClientSecret match ownCloud Desktop Client v7+ defaults.
	DefaultOwnCloudClientID     = "xdXOt13JKxym1B1QcEncf2XDkLAexMBFwiT9j6EfhhHFJhs2KM9jbjTmf8JBXE69"
	DefaultOwnCloudClientSecret = "UBntmLjC2yYCeHwsyj73Uwo9TAaecAetRwMw0xYcvNL9yRdLSUi0hUAHfvCHFeFh"

	DefaultAccessTokenTTL  = 24 * time.Hour
	DefaultRefreshTokenTTL = 30 * 24 * time.Hour
	DefaultAuthCodeTTL     = 5 * time.Minute
)

type OAuthHandler struct {
	oauthRepo   repository.OAuthRepository
	userRepo    repository.UserRepository
	sessionRepo repository.SessionRepository
	logger      *slog.Logger
}

func NewOAuthHandler(
	oauthRepo repository.OAuthRepository,
	userRepo repository.UserRepository,
	sessionRepo repository.SessionRepository,
	logger *slog.Logger,
) *OAuthHandler {
	return &OAuthHandler{
		oauthRepo:   oauthRepo,
		userRepo:    userRepo,
		sessionRepo: sessionRepo,
		logger:      logger,
	}
}

// OpenIDConfigurationHandler serves /.well-known/openid-configuration discovery document.
func (h *OAuthHandler) OpenIDConfigurationHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
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

	baseURL := fmt.Sprintf("%s://%s", scheme, host)

	doc := map[string]any{
		"issuer":                                baseURL,
		"authorization_endpoint":                fmt.Sprintf("%s/index.php/apps/oauth2/authorize", baseURL),
		"token_endpoint":                        fmt.Sprintf("%s/index.php/apps/oauth2/api/v1/token", baseURL),
		"userinfo_endpoint":                     fmt.Sprintf("%s/ocs/v2.php/cloud/user", baseURL),
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256", "plain"},
		"prompt_values_supported":               []string{"select_account", "consent"},
		"scopes_supported":                      []string{"openid", "offline_access", "email", "profile"},
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(doc)
}

// AuthorizeHandler handles GET and POST requests for OAuth2 Authorization Code flow.
func (h *OAuthHandler) AuthorizeHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleAuthorizeGet(w, r)
	case http.MethodPost:
		h.handleAuthorizePost(w, r)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func (h *OAuthHandler) handleAuthorizeGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	responseType := q.Get("response_type")
	codeChallenge := q.Get("code_challenge")
	codeChallengeMethod := q.Get("code_challenge_method")
	state := q.Get("state")
	scope := q.Get("scope")
	loginHint := q.Get("login_hint")
	if loginHint == "" {
		loginHint = q.Get("user")
	}

	if !h.validateClient(clientID) {
		http.Error(w, "Invalid client_id", http.StatusBadRequest)
		return
	}

	if responseType != "code" {
		http.Error(w, "Unsupported response_type (must be 'code')", http.StatusBadRequest)
		return
	}

	if !validateRedirectURI(redirectURI) {
		http.Error(w, "Invalid redirect_uri", http.StatusBadRequest)
		return
	}

	// Check if already authenticated via session cookie
	var currentUser *domain.User
	token := middleware.ExtractSessionToken(r)
	if token != "" {
		sess, err := h.sessionRepo.GetByToken(r.Context(), token)
		if err == nil && sess != nil && !sess.IsExpired() {
			user, err := h.userRepo.GetByID(r.Context(), sess.UserID)
			if err == nil && user != nil {
				currentUser = user
			}
		}
	}

	h.renderAuthorizePage(w, r, currentUser, clientID, redirectURI, state, scope, codeChallenge, codeChallengeMethod, loginHint, "")
}

func (h *OAuthHandler) handleAuthorizePost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	clientID := r.FormValue("client_id")
	redirectURI := r.FormValue("redirect_uri")
	codeChallenge := r.FormValue("code_challenge")
	codeChallengeMethod := r.FormValue("code_challenge_method")
	state := r.FormValue("state")
	scope := r.FormValue("scope")
	action := r.FormValue("action")

	if !h.validateClient(clientID) || !validateRedirectURI(redirectURI) {
		http.Error(w, "Invalid client or redirect URI", http.StatusBadRequest)
		return
	}

	if action == "cancel" {
		parsedRedirect, err := url.Parse(redirectURI)
		if err == nil {
			rq := parsedRedirect.Query()
			rq.Set("error", "access_denied")
			if state != "" {
				rq.Set("state", state)
			}
			parsedRedirect.RawQuery = rq.Encode()
			http.Redirect(w, r, parsedRedirect.String(), http.StatusFound)
			return
		}
		http.Error(w, "Access Denied", http.StatusForbidden)
		return
	}

	var user *domain.User

	// Check if user is already logged in
	token := middleware.ExtractSessionToken(r)
	if token != "" {
		sess, err := h.sessionRepo.GetByToken(r.Context(), token)
		if err == nil && sess != nil && !sess.IsExpired() {
			u, err := h.userRepo.GetByID(r.Context(), sess.UserID)
			if err == nil && u != nil {
				user = u
			}
		}
	}

	// If not logged in, authenticate via submitted form credentials
	if user == nil {
		username := strings.TrimSpace(r.FormValue("username"))
		password := r.FormValue("password")

		if username == "" || password == "" {
			h.renderAuthorizePage(w, r, nil, clientID, redirectURI, state, scope, codeChallenge, codeChallengeMethod, username, "Username and password are required")
			return
		}

		u, err := h.userRepo.GetByUsername(r.Context(), username)
		if err != nil || u == nil || !u.CheckPassword(password) {
			if u == nil {
				domain.CheckDummyPassword(password)
			}
			audit.RecordFailure(r.Context(), "oauth_login_failed", username, "Invalid credentials during OAuth authorization")
			h.renderAuthorizePage(w, r, nil, clientID, redirectURI, state, scope, codeChallenge, codeChallengeMethod, username, "Invalid username or password")
			return
		}
		user = u

		// Issue a web session cookie so the user stays authenticated in browser
		sessionToken := generateSecureToken(32)
		sessionExpiry := time.Now().Add(7 * 24 * time.Hour)
		_ = h.sessionRepo.Create(r.Context(), &domain.Session{
			Token:     sessionToken,
			UserID:    user.ID,
			ExpiresAt: sessionExpiry,
			CreatedAt: time.Now(),
		})

		http.SetCookie(w, &http.Cookie{
			Name:     middleware.SessionCookieName,
			Value:    sessionToken,
			Path:     "/",
			Expires:  sessionExpiry,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   middleware.IsSecureRequest(r),
		})
	}

	// Generate one-time authorization code
	code := generateSecureToken(32)
	authCode := &domain.OAuthAuthCode{
		Code:                code,
		ClientID:            clientID,
		UserID:              user.ID,
		RedirectURI:         redirectURI,
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: codeChallengeMethod,
		Scope:               scope,
		ExpiresAt:           time.Now().Add(DefaultAuthCodeTTL),
		CreatedAt:           time.Now(),
	}

	if err := h.oauthRepo.CreateAuthCode(r.Context(), authCode); err != nil {
		h.logger.Error("failed to create oauth auth code", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	audit.Record(r.Context(), "oauth_authorize", user.Username, fmt.Sprintf("Authorized client %s", clientID))

	parsedRedirect, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "Failed to parse redirect URI", http.StatusInternalServerError)
		return
	}

	rq := parsedRedirect.Query()
	rq.Set("code", code)
	if state != "" {
		rq.Set("state", state)
	}
	parsedRedirect.RawQuery = rq.Encode()

	http.Redirect(w, r, parsedRedirect.String(), http.StatusFound)
}

// TokenHandler handles POST requests for token exchange (authorization_code and refresh_token).
func (h *OAuthHandler) TokenHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		h.writeOAuthError(w, "invalid_request", "Failed to parse form data", http.StatusBadRequest)
		return
	}

	grantType := r.FormValue("grant_type")
	clientID, clientSecret := h.extractClientCredentials(r)

	if !h.validateClientSecret(clientID, clientSecret) {
		h.writeOAuthError(w, "invalid_client", "Client authentication failed", http.StatusUnauthorized)
		return
	}

	switch grantType {
	case "authorization_code":
		h.handleAuthorizationCodeGrant(w, r, clientID)
	case "refresh_token":
		h.handleRefreshTokenGrant(w, r, clientID)
	default:
		h.writeOAuthError(w, "unsupported_grant_type", "Supported grant types: authorization_code, refresh_token", http.StatusBadRequest)
	}
}

func (h *OAuthHandler) handleAuthorizationCodeGrant(w http.ResponseWriter, r *http.Request, clientID string) {
	code := r.FormValue("code")
	redirectURI := r.FormValue("redirect_uri")
	codeVerifier := r.FormValue("code_verifier")

	if code == "" || redirectURI == "" {
		h.writeOAuthError(w, "invalid_request", "Missing code or redirect_uri", http.StatusBadRequest)
		return
	}

	authCode, err := h.oauthRepo.GetAuthCode(r.Context(), code)
	if err != nil || authCode == nil {
		h.writeOAuthError(w, "invalid_grant", "Invalid or expired authorization code", http.StatusBadRequest)
		return
	}

	// Always delete auth code on attempt to prevent replay
	_ = h.oauthRepo.DeleteAuthCode(r.Context(), code)

	if authCode.IsExpired() {
		h.writeOAuthError(w, "invalid_grant", "Authorization code has expired", http.StatusBadRequest)
		return
	}

	if authCode.ClientID != clientID {
		h.writeOAuthError(w, "invalid_grant", "Client mismatch", http.StatusBadRequest)
		return
	}

	if authCode.RedirectURI != redirectURI {
		h.writeOAuthError(w, "invalid_grant", "Redirect URI mismatch", http.StatusBadRequest)
		return
	}

	// Validate PKCE code challenge if present
	if authCode.CodeChallenge != "" {
		if codeVerifier == "" {
			h.writeOAuthError(w, "invalid_request", "code_verifier is required", http.StatusBadRequest)
			return
		}

		if !verifyPKCE(codeVerifier, authCode.CodeChallenge, authCode.CodeChallengeMethod) {
			h.writeOAuthError(w, "invalid_grant", "PKCE verification failed", http.StatusBadRequest)
			return
		}
	}

	h.issueTokens(w, r, authCode.UserID, clientID, authCode.Scope)
}

func (h *OAuthHandler) handleRefreshTokenGrant(w http.ResponseWriter, r *http.Request, clientID string) {
	refreshToken := r.FormValue("refresh_token")
	if refreshToken == "" {
		h.writeOAuthError(w, "invalid_request", "Missing refresh_token", http.StatusBadRequest)
		return
	}

	tok, err := h.oauthRepo.GetTokenByRefreshToken(r.Context(), refreshToken)
	if err != nil || tok == nil {
		h.writeOAuthError(w, "invalid_grant", "Invalid refresh token", http.StatusBadRequest)
		return
	}

	if tok.IsRefreshTokenExpired() {
		_ = h.oauthRepo.DeleteTokenByAccessToken(r.Context(), tok.AccessToken)
		_ = h.sessionRepo.DeleteByToken(r.Context(), tok.AccessToken)
		h.writeOAuthError(w, "invalid_grant", "Refresh token has expired", http.StatusBadRequest)
		return
	}

	if tok.ClientID != clientID {
		h.writeOAuthError(w, "invalid_grant", "Client mismatch", http.StatusBadRequest)
		return
	}

	// Revoke old tokens before issuing new pair
	_ = h.oauthRepo.DeleteTokenByAccessToken(r.Context(), tok.AccessToken)
	_ = h.sessionRepo.DeleteByToken(r.Context(), tok.AccessToken)

	h.issueTokens(w, r, tok.UserID, clientID, "openid offline_access email profile")
}

func (h *OAuthHandler) issueTokens(w http.ResponseWriter, r *http.Request, userID int64, clientID, scope string) {
	accessToken := generateSecureToken(32)
	refreshToken := generateSecureToken(32)
	accessExpiresAt := time.Now().Add(DefaultAccessTokenTTL)
	refreshExpiresAt := time.Now().Add(DefaultRefreshTokenTTL)

	oauthToken := &domain.OAuthToken{
		ClientID:              clientID,
		UserID:                userID,
		AccessToken:           accessToken,
		RefreshToken:          refreshToken,
		AccessTokenExpiresAt:  accessExpiresAt,
		RefreshTokenExpiresAt: refreshExpiresAt,
		CreatedAt:             time.Now(),
	}

	if err := h.oauthRepo.CreateToken(r.Context(), oauthToken); err != nil {
		h.logger.Error("failed to create oauth token", "error", err)
		h.writeOAuthError(w, "server_error", "Failed to store token", http.StatusInternalServerError)
		return
	}

	// Also insert into sessions table so all standard session-checking middleware accepts it
	_ = h.sessionRepo.Create(r.Context(), &domain.Session{
		Token:     accessToken,
		UserID:    userID,
		ExpiresAt: accessExpiresAt,
		CreatedAt: time.Now(),
	})

	username := ""
	if user, err := h.userRepo.GetByID(r.Context(), userID); err == nil && user != nil {
		username = user.Username
	}

	resp := map[string]any{
		"access_token":  accessToken,
		"token_type":    "Bearer",
		"expires_in":    int(DefaultAccessTokenTTL.Seconds()),
		"refresh_token": refreshToken,
		"scope":         scope,
		"user_id":       username,
	}

	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *OAuthHandler) extractClientCredentials(r *http.Request) (string, string) {
	clientID, clientSecret, ok := r.BasicAuth()
	if ok {
		return clientID, clientSecret
	}
	return r.FormValue("client_id"), r.FormValue("client_secret")
}

func (h *OAuthHandler) validateClient(clientID string) bool {
	if clientID == "" {
		return false
	}
	if clientID == DefaultOwnCloudClientID {
		return true
	}
	return true
}

func (h *OAuthHandler) validateClientSecret(clientID, clientSecret string) bool {
	if clientID == DefaultOwnCloudClientID {
		if clientSecret == "" || clientSecret == DefaultOwnCloudClientSecret {
			return true
		}
	}
	return true
}

func validateRedirectURI(rawURL string) bool {
	if rawURL == "" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	// Native apps per RFC 8252 use loopback with dynamic port
	hostname := u.Hostname()
	if strings.EqualFold(hostname, "localhost") || hostname == "127.0.0.1" || hostname == "::1" {
		return u.Scheme == "http" || u.Scheme == "https"
	}

	ip := net.ParseIP(hostname)
	if ip != nil && ip.IsLoopback() {
		return u.Scheme == "http" || u.Scheme == "https"
	}

	return false
}

func verifyPKCE(verifier, challenge, method string) bool {
	switch strings.ToUpper(method) {
	case "S256", "":
		h := sha256.Sum256([]byte(verifier))
		computed := base64.RawURLEncoding.EncodeToString(h[:])
		return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
	case "PLAIN":
		return subtle.ConstantTimeCompare([]byte(verifier), []byte(challenge)) == 1
	default:
		return false
	}
}

func generateSecureToken(byteLength int) string {
	b := make([]byte, byteLength)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *OAuthHandler) writeOAuthError(w http.ResponseWriter, errCode, errDesc string, status int) {
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             errCode,
		"error_description": errDesc,
	})
}

func (h *OAuthHandler) renderAuthorizePage(
	w http.ResponseWriter,
	r *http.Request,
	user *domain.User,
	clientID, redirectURI, state, scope, challenge, method, loginHint, errMsg string,
) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	escapedError := ""
	if errMsg != "" {
		escapedError = fmt.Sprintf(`
			<div class="error-box">
				<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>
				<span>%s</span>
			</div>
		`, html.EscapeString(errMsg))
	}

	bodyContent := ""
	if user != nil {
		initial := "U"
		if len(user.Username) > 0 {
			initial = strings.ToUpper(string([]rune(user.Username)[0]))
		}
		bodyContent = fmt.Sprintf(`
			<p class="app-info">
				Application <span class="app-name">ownCloud Desktop Client</span> is requesting access to your account:
			</p>
			<div class="user-badge-card">
				<div class="user-avatar">%s</div>
				<div class="user-details">
					<div class="user-name">%s</div>
					<div class="user-status">Currently signed in</div>
				</div>
			</div>
			<div class="btn-group">
				<button type="submit" name="action" value="authorize" class="btn-primary">
					<span>Authorize</span>
					<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="5" y1="12" x2="19" y2="12"/><polyline points="12 5 19 12 12 19"/></svg>
				</button>
				<button type="submit" name="action" value="cancel" class="btn-secondary">
					Cancel
				</button>
			</div>
		`, html.EscapeString(initial), html.EscapeString(user.Username))
	} else {
		usernameAutofocus := "autofocus"
		passwordAutofocus := ""
		if loginHint != "" {
			usernameAutofocus = ""
			passwordAutofocus = "autofocus"
		}
		bodyContent = fmt.Sprintf(`
			<p class="app-info">
				Sign in to your SelfCloud account to connect the desktop client:
			</p>
			<div style="margin-bottom: 20px;">
				<label class="form-label">Username</label>
				<div class="input-wrapper">
					<div class="input-icon">
						<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/></svg>
					</div>
					<input type="text" name="username" value="%s" %s required placeholder="e.g. admin" class="form-input" />
				</div>
			</div>
			<div style="margin-bottom: 24px;">
				<label class="form-label">Password</label>
				<div class="input-wrapper">
					<div class="input-icon">
						<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>
					</div>
					<input type="password" name="password" %s required placeholder="••••••••" class="form-input" />
				</div>
			</div>
			<div class="btn-group">
				<button type="submit" name="action" value="authorize" class="btn-primary">
					<span>Sign In & Authorize</span>
					<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="5" y1="12" x2="19" y2="12"/><polyline points="12 5 19 12 12 19"/></svg>
				</button>
				<button type="submit" name="action" value="cancel" class="btn-secondary">
					Cancel
				</button>
			</div>
		`, html.EscapeString(loginHint), usernameAutofocus, passwordAutofocus)
	}

	page := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Authorize ownCloud Client - SelfCloud</title>
	<style>
		*, *::before, *::after { box-sizing: border-box; }
		body {
			font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif;
			background-color: #020617;
			color: #f8fafc;
			display: flex;
			flex-direction: column;
			align-items: center;
			justify-content: center;
			min-height: 100vh;
			margin: 0;
			padding: 16px;
		}
		.card-wrapper {
			width: 100%%;
			max-width: 440px;
		}
		.brand-header {
			text-align: center;
			margin-bottom: 32px;
		}
		.brand-logo-badge {
			display: inline-flex;
			padding: 12px;
			background: rgba(0, 107, 166, 0.2);
			color: #0082c9;
			border-radius: 16px;
			border: 1px solid rgba(0, 130, 201, 0.3);
			margin-bottom: 16px;
			box-shadow: 0 20px 25px -5px rgba(0, 107, 166, 0.1), 0 8px 10px -6px rgba(0, 107, 166, 0.1);
		}
		.brand-title {
			font-size: 24px;
			font-weight: 700;
			color: #ffffff;
			letter-spacing: -0.025em;
			margin: 0;
		}
		.brand-subtitle {
			font-size: 14px;
			color: #94a3b8;
			margin-top: 4px;
		}
		.card {
			background-color: #0f172a;
			border: 1px solid #1e293b;
			border-radius: 24px;
			padding: 32px;
			box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.5);
		}
		.error-box {
			margin-bottom: 24px;
			padding: 14px 16px;
			background: rgba(239, 68, 68, 0.1);
			border: 1px solid rgba(239, 68, 68, 0.2);
			border-radius: 16px;
			display: flex;
			align-items: center;
			gap: 12px;
			color: #f87171;
			font-size: 14px;
		}
		.form-label {
			display: block;
			font-size: 11px;
			font-weight: 600;
			text-transform: uppercase;
			letter-spacing: 0.05em;
			color: #94a3b8;
			margin-bottom: 8px;
		}
		.input-wrapper {
			position: relative;
		}
		.input-icon {
			position: absolute;
			top: 0;
			bottom: 0;
			left: 14px;
			display: flex;
			align-items: center;
			pointer-events: none;
			color: #64748b;
		}
		.form-input {
			width: 100%%;
			padding: 12px 16px 12px 42px;
			background-color: #020617;
			border: 1px solid #1e293b;
			border-radius: 12px;
			font-size: 14px;
			color: #f8fafc;
			outline: none;
			transition: border-color 0.15s, box-shadow 0.15s;
		}
		.form-input::placeholder {
			color: #475569;
		}
		.form-input:focus {
			border-color: #0082c9;
			box-shadow: 0 0 0 1px #0082c9;
		}
		.user-badge-card {
			background: #020617;
			border: 1px solid #1e293b;
			border-radius: 14px;
			padding: 14px 16px;
			display: flex;
			align-items: center;
			gap: 12px;
			margin-bottom: 24px;
		}
		.user-avatar {
			width: 38px;
			height: 38px;
			border-radius: 12px;
			background: rgba(0, 107, 166, 0.25);
			color: #38bdf8;
			display: flex;
			align-items: center;
			justify-content: center;
			font-weight: 600;
			font-size: 15px;
			border: 1px solid rgba(0, 130, 201, 0.3);
		}
		.user-details {
			flex: 1;
		}
		.user-name {
			font-size: 15px;
			font-weight: 600;
			color: #f8fafc;
		}
		.user-status {
			font-size: 12px;
			color: #94a3b8;
			margin-top: 2px;
		}
		.btn-group {
			display: flex;
			gap: 12px;
			margin-top: 8px;
		}
		.btn-primary {
			flex: 1;
			display: flex;
			align-items: center;
			justify-content: center;
			gap: 8px;
			padding: 14px 16px;
			background-color: #006ba6;
			color: #ffffff;
			font-weight: 600;
			font-size: 14px;
			border-radius: 12px;
			border: none;
			cursor: pointer;
			box-shadow: 0 10px 15px -3px rgba(0, 107, 166, 0.25);
			transition: background-color 0.15s, transform 0.1s;
		}
		.btn-primary:hover {
			background-color: #0082c9;
		}
		.btn-secondary {
			padding: 14px 20px;
			background-color: #020617;
			border: 1px solid #1e293b;
			color: #94a3b8;
			font-weight: 500;
			font-size: 14px;
			border-radius: 12px;
			cursor: pointer;
			transition: background-color 0.15s, color 0.15s;
		}
		.btn-secondary:hover {
			background-color: #1e293b;
			color: #f8fafc;
		}
		.app-info {
			font-size: 14px;
			color: #94a3b8;
			line-height: 1.5;
			margin: 0 0 20px 0;
		}
		.app-name {
			color: #f8fafc;
			font-weight: 600;
		}
	</style>
</head>
<body>
	<div class="card-wrapper">
		<div class="brand-header">
			<div class="brand-logo-badge">
				<svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
					<path d="M17.5 19H9a7 7 0 1 1 6.71-9h1.79a4.5 4.5 0 1 1 0 9Z"/>
				</svg>
			</div>
			<h1 class="brand-title">Welcome to SelfCloud</h1>
			<p class="brand-subtitle">selfCloud Authorization</p>
		</div>

		<div class="card">
			%s
			<form method="POST" action="">
				<input type="hidden" name="client_id" value="%s" />
				<input type="hidden" name="redirect_uri" value="%s" />
				<input type="hidden" name="state" value="%s" />
				<input type="hidden" name="scope" value="%s" />
				<input type="hidden" name="code_challenge" value="%s" />
				<input type="hidden" name="code_challenge_method" value="%s" />
				%s
			</form>
		</div>
	</div>
</body>
</html>`, escapedError, html.EscapeString(clientID), html.EscapeString(redirectURI), html.EscapeString(state), html.EscapeString(scope), html.EscapeString(challenge), html.EscapeString(method), bodyContent)

	_, _ = w.Write([]byte(page))
}
