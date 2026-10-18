package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"selfcloud/internal/api"
	"selfcloud/internal/domain"
)

type mockOAuthRepo struct {
	codes  map[string]*domain.OAuthAuthCode
	tokens map[string]*domain.OAuthToken
}

func newMockOAuthRepo() *mockOAuthRepo {
	return &mockOAuthRepo{
		codes:  make(map[string]*domain.OAuthAuthCode),
		tokens: make(map[string]*domain.OAuthToken),
	}
}

func (m *mockOAuthRepo) CreateAuthCode(ctx context.Context, code *domain.OAuthAuthCode) error {
	m.codes[code.Code] = code
	return nil
}

func (m *mockOAuthRepo) GetAuthCode(ctx context.Context, code string) (*domain.OAuthAuthCode, error) {
	return m.codes[code], nil
}

func (m *mockOAuthRepo) DeleteAuthCode(ctx context.Context, code string) error {
	delete(m.codes, code)
	return nil
}

func (m *mockOAuthRepo) CreateToken(ctx context.Context, token *domain.OAuthToken) error {
	m.tokens[token.AccessToken] = token
	return nil
}

func (m *mockOAuthRepo) GetTokenByAccessToken(ctx context.Context, accessToken string) (*domain.OAuthToken, error) {
	return m.tokens[accessToken], nil
}

func (m *mockOAuthRepo) GetTokenByRefreshToken(ctx context.Context, refreshToken string) (*domain.OAuthToken, error) {
	for _, t := range m.tokens {
		if t.RefreshToken == refreshToken {
			return t, nil
		}
	}
	return nil, nil
}

func (m *mockOAuthRepo) DeleteTokenByAccessToken(ctx context.Context, accessToken string) error {
	delete(m.tokens, accessToken)
	return nil
}

func (m *mockOAuthRepo) DeleteTokensByUserID(ctx context.Context, userID int64) error {
	for k, t := range m.tokens {
		if t.UserID == userID {
			delete(m.tokens, k)
		}
	}
	return nil
}

type mockUserRepoForOAuth struct {
	user *domain.User
}

func (m *mockUserRepoForOAuth) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	if m.user != nil && m.user.Username == username {
		return m.user, nil
	}
	return nil, nil
}
func (m *mockUserRepoForOAuth) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	return nil, nil
}
func (m *mockUserRepoForOAuth) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	if m.user != nil && m.user.ID == id {
		return m.user, nil
	}
	return nil, nil
}
func (m *mockUserRepoForOAuth) Create(ctx context.Context, user *domain.User) error { return nil }
func (m *mockUserRepoForOAuth) Update(ctx context.Context, user *domain.User) error { return nil }
func (m *mockUserRepoForOAuth) ListAll(ctx context.Context) ([]*domain.User, error)  { return nil, nil }
func (m *mockUserRepoForOAuth) Delete(ctx context.Context, username string) error    { return nil }

type mockSessionRepoForOAuth struct {
	sessions map[string]*domain.Session
}

func newMockSessionRepoForOAuth() *mockSessionRepoForOAuth {
	return &mockSessionRepoForOAuth{sessions: make(map[string]*domain.Session)}
}

func (m *mockSessionRepoForOAuth) Create(ctx context.Context, session *domain.Session) error {
	m.sessions[session.Token] = session
	return nil
}
func (m *mockSessionRepoForOAuth) GetByToken(ctx context.Context, token string) (*domain.Session, error) {
	return m.sessions[token], nil
}
func (m *mockSessionRepoForOAuth) DeleteByToken(ctx context.Context, token string) error {
	delete(m.sessions, token)
	return nil
}
func (m *mockSessionRepoForOAuth) DeleteByUserID(ctx context.Context, userID int64) error {
	return nil
}
func (m *mockSessionRepoForOAuth) DeleteExpired(ctx context.Context) (int64, error) {
	return 0, nil
}

func TestOpenIDConfigurationHandler(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	userRepo := &mockUserRepoForOAuth{}
	sessionRepo := newMockSessionRepoForOAuth()
	handler := api.NewOAuthHandler(oauthRepo, userRepo, sessionRepo, nil)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/openid-configuration", nil)
	req.Host = "cloud.example.com"
	rec := httptest.NewRecorder()

	handler.OpenIDConfigurationHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if doc["issuer"] != "http://cloud.example.com" {
		t.Errorf("unexpected issuer: %v", doc["issuer"])
	}
	if doc["authorization_endpoint"] != "http://cloud.example.com/index.php/apps/oauth2/authorize" {
		t.Errorf("unexpected authorization_endpoint: %v", doc["authorization_endpoint"])
	}
	if doc["token_endpoint"] != "http://cloud.example.com/index.php/apps/oauth2/api/v1/token" {
		t.Errorf("unexpected token_endpoint: %v", doc["token_endpoint"])
	}
}

func TestOAuth2FullFlowPKCE(t *testing.T) {
	passwordHash, _ := domain.HashPassword("supersecret")
	user := &domain.User{
		ID:           10,
		Username:     "alice",
		PasswordHash: passwordHash,
	}

	oauthRepo := newMockOAuthRepo()
	userRepo := &mockUserRepoForOAuth{user: user}
	sessionRepo := newMockSessionRepoForOAuth()
	handler := api.NewOAuthHandler(oauthRepo, userRepo, sessionRepo, nil)

	// PKCE parameters
	codeVerifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	h := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(h[:])
	redirectURI := "http://127.0.0.1:54321"
	clientID := api.DefaultOwnCloudClientID

	// 1. GET /authorize page renders correctly
	authURL := "/api/v1/auth/oauth2/authorize?response_type=code&client_id=" + clientID +
		"&redirect_uri=" + url.QueryEscape(redirectURI) +
		"&code_challenge=" + codeChallenge +
		"&code_challenge_method=S256&state=xyz123"
	getReq := httptest.NewRequest(http.MethodGet, authURL, nil)
	getRec := httptest.NewRecorder()
	handler.AuthorizeHandler(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on GET /authorize, got %d", getRec.Code)
	}
	if !strings.Contains(getRec.Body.String(), "selfCloud Authorization") {
		t.Errorf("expected HTML page to contain authorization title")
	}

	// 2. POST /authorize with login credentials
	form := url.Values{
		"action":                {"authorize"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"state":                 {"xyz123"},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
		"username":              {"alice"},
		"password":              {"supersecret"},
	}
	postReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth2/authorize", strings.NewReader(form.Encode()))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postRec := httptest.NewRecorder()
	handler.AuthorizeHandler(postRec, postReq)

	if postRec.Code != http.StatusFound {
		t.Fatalf("expected 302 Found redirect, got %d", postRec.Code)
	}

	location := postRec.Header().Get("Location")
	parsedLoc, err := url.Parse(location)
	if err != nil {
		t.Fatalf("invalid redirect location: %v", err)
	}

	authCode := parsedLoc.Query().Get("code")
	if authCode == "" {
		t.Fatalf("expected authorization code in redirect query: %s", location)
	}
	if parsedLoc.Query().Get("state") != "xyz123" {
		t.Errorf("expected state 'xyz123', got %s", parsedLoc.Query().Get("state"))
	}

	// 3. POST /token with invalid verifier fails
	badTokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"client_secret": {api.DefaultOwnCloudClientSecret},
		"code":          {authCode},
		"redirect_uri":  {redirectURI},
		"code_verifier": {"wrong_verifier"},
	}
	badReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth2/token", strings.NewReader(badTokenForm.Encode()))
	badReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badRec := httptest.NewRecorder()
	handler.TokenHandler(badRec, badReq)

	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for bad PKCE verifier, got %d", badRec.Code)
	}

	// Because code is single-use, re-authorize to get a fresh code
	postRec2 := httptest.NewRecorder()
	postReq2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth2/authorize", strings.NewReader(form.Encode()))
	postReq2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.AuthorizeHandler(postRec2, postReq2)
	parsedLoc2, _ := url.Parse(postRec2.Header().Get("Location"))
	freshCode := parsedLoc2.Query().Get("code")

	// 4. POST /token with valid verifier succeeds
	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"client_secret": {api.DefaultOwnCloudClientSecret},
		"code":          {freshCode},
		"redirect_uri":  {redirectURI},
		"code_verifier": {codeVerifier},
	}
	tokenReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth2/token", strings.NewReader(tokenForm.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	handler.TokenHandler(tokenRec, tokenReq)

	if tokenRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on token exchange, got %d: %s", tokenRec.Code, tokenRec.Body.String())
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
		UserID       string `json:"user_id"`
	}
	if err := json.Unmarshal(tokenRec.Body.Bytes(), &tokenResp); err != nil {
		t.Fatalf("failed to decode token response: %v", err)
	}

	if tokenResp.AccessToken == "" || tokenResp.RefreshToken == "" || tokenResp.UserID != "alice" {
		t.Fatalf("expected non-empty tokens and user_id alice, got: %+v", tokenResp)
	}
	if tokenResp.TokenType != "Bearer" {
		t.Errorf("expected Bearer token_type, got %s", tokenResp.TokenType)
	}

	// Verify access token is also created in sessionRepo
	sess, err := sessionRepo.GetByToken(context.Background(), tokenResp.AccessToken)
	if err != nil || sess == nil || sess.UserID != user.ID {
		t.Errorf("expected session for access token to exist in sessionRepo")
	}

	// 5. POST /token with refresh_token succeeds
	refreshForm := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"client_secret": {api.DefaultOwnCloudClientSecret},
		"refresh_token": {tokenResp.RefreshToken},
	}
	refreshReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth2/token", strings.NewReader(refreshForm.Encode()))
	refreshReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	refreshRec := httptest.NewRecorder()
	handler.TokenHandler(refreshRec, refreshReq)

	if refreshRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on refresh token exchange, got %d: %s", refreshRec.Code, refreshRec.Body.String())
	}

	var refreshResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(refreshRec.Body.Bytes(), &refreshResp); err != nil {
		t.Fatalf("failed to decode refresh response: %v", err)
	}

	if refreshResp.AccessToken == "" || refreshResp.RefreshToken == "" {
		t.Fatalf("expected fresh tokens, got: %+v", refreshResp)
	}
	if refreshResp.AccessToken == tokenResp.AccessToken {
		t.Errorf("expected access token rotation")
	}

	// Verify old access token was deleted from sessionRepo
	oldSess, _ := sessionRepo.GetByToken(context.Background(), tokenResp.AccessToken)
	if oldSess != nil {
		t.Errorf("expected old session to be deleted")
	}
}

func TestOAuth2CancelAuthorization(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	userRepo := &mockUserRepoForOAuth{}
	sessionRepo := newMockSessionRepoForOAuth()
	handler := api.NewOAuthHandler(oauthRepo, userRepo, sessionRepo, nil)

	redirectURI := "http://127.0.0.1:54321"
	form := url.Values{
		"action":       {"cancel"},
		"client_id":    {api.DefaultOwnCloudClientID},
		"redirect_uri": {redirectURI},
		"state":        {"state456"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth2/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.AuthorizeHandler(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 Found, got %d", rec.Code)
	}

	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Query().Get("error") != "access_denied" {
		t.Errorf("expected error=access_denied, got %s", loc.Query().Get("error"))
	}
	if loc.Query().Get("state") != "state456" {
		t.Errorf("expected state=state456, got %s", loc.Query().Get("state"))
	}
}

func TestWebFingerHandler(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	userRepo := &mockUserRepoForOAuth{}
	sessionRepo := newMockSessionRepoForOAuth()
	handler := api.NewOAuthHandler(oauthRepo, userRepo, sessionRepo, nil)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/webfinger?rel=http%3A%2F%2Fopenid.net%2Fspecs%2Fconnect%2F1.0%2Fissuer&resource=https%3A%2F%2Fcloud.example.com", nil)
	req.Host = "cloud.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()

	handler.WebFingerHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from WebFinger, got %d", rec.Code)
	}

	var data struct {
		Subject string `json:"subject"`
		Links   []struct {
			Rel  string `json:"rel"`
			Href string `json:"href"`
		} `json:"links"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
		t.Fatalf("failed to decode WebFinger response: %v", err)
	}
	if data.Subject != "https://cloud.example.com" {
		t.Errorf("expected subject https://cloud.example.com, got %s", data.Subject)
	}
	if len(data.Links) == 0 || data.Links[0].Href != "https://cloud.example.com" {
		t.Errorf("expected issuer link https://cloud.example.com, got %+v", data.Links)
	}
}

func TestOAuth2AndroidMobileFlow(t *testing.T) {
	passwordHash, _ := domain.HashPassword("androidsecret")
	user := &domain.User{
		ID:           42,
		Username:     "androiduser",
		PasswordHash: passwordHash,
	}

	oauthRepo := newMockOAuthRepo()
	userRepo := &mockUserRepoForOAuth{user: user}
	sessionRepo := newMockSessionRepoForOAuth()
	handler := api.NewOAuthHandler(oauthRepo, userRepo, sessionRepo, nil)

	redirectURI := "oc://android.owncloud.com"
	clientID := api.AndroidOwnCloudClientID
	clientSecret := api.AndroidOwnCloudClientSecret

	// 1. GET /index.php/apps/oauth2/authorize with Android redirect_uri
	authURL := "/index.php/apps/oauth2/authorize?response_type=code&client_id=" + clientID +
		"&redirect_uri=" + url.QueryEscape(redirectURI) +
		"&state=andState77"
	getReq := httptest.NewRequest(http.MethodGet, authURL, nil)
	getRec := httptest.NewRecorder()
	handler.AuthorizeHandler(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for Android authorize page, got %d (body: %s)", getRec.Code, getRec.Body.String())
	}
	if !strings.Contains(getRec.Body.String(), "ownCloud Android App") {
		t.Errorf("expected page to display 'ownCloud Android App'")
	}

	// 2. POST /index.php/apps/oauth2/authorize with credentials
	form := url.Values{
		"action":       {"authorize"},
		"client_id":    {clientID},
		"redirect_uri": {redirectURI},
		"state":        {"andState77"},
		"username":     {"androiduser"},
		"password":     {"androidsecret"},
	}
	postReq := httptest.NewRequest(http.MethodPost, "/index.php/apps/oauth2/authorize", strings.NewReader(form.Encode()))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postRec := httptest.NewRecorder()
	handler.AuthorizeHandler(postRec, postReq)

	if postRec.Code != http.StatusFound {
		t.Fatalf("expected 302 Found redirect, got %d", postRec.Code)
	}

	loc := postRec.Header().Get("Location")
	if !strings.HasPrefix(loc, "oc://android.owncloud.com") {
		t.Fatalf("expected redirect to oc://android.owncloud.com, got %s", loc)
	}
	parsedLoc, _ := url.Parse(loc)
	authCode := parsedLoc.Query().Get("code")
	if authCode == "" {
		t.Fatalf("missing authorization code in redirect: %s", loc)
	}
	if parsedLoc.Query().Get("state") != "andState77" {
		t.Errorf("expected state=andState77, got %s", parsedLoc.Query().Get("state"))
	}

	// 3. POST /index.php/apps/oauth2/api/v1/token to exchange code
	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code":          {authCode},
		"redirect_uri":  {redirectURI},
	}
	tokenReq := httptest.NewRequest(http.MethodPost, "/index.php/apps/oauth2/api/v1/token", strings.NewReader(tokenForm.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	handler.TokenHandler(tokenRec, tokenReq)

	if tokenRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from token endpoint, got %d: %s", tokenRec.Code, tokenRec.Body.String())
	}

	var tokenData struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		UserID      string `json:"user_id"`
	}
	if err := json.Unmarshal(tokenRec.Body.Bytes(), &tokenData); err != nil {
		t.Fatalf("failed to decode token response: %v", err)
	}
	if tokenData.AccessToken == "" || tokenData.TokenType != "Bearer" || tokenData.UserID != "androiduser" {
		t.Fatalf("unexpected token response: %+v", tokenData)
	}
}
