package functional

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"selfcloud/internal/api"
	"selfcloud/internal/db"
	"selfcloud/internal/domain"
	"selfcloud/internal/middleware"
	"selfcloud/internal/ocs"
	"selfcloud/internal/repository"
	"selfcloud/internal/storage"
	"selfcloud/internal/webdav"
)

func setupTestServer(t *testing.T) (*httptest.Server, func()) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		dsn = "selfcloud:selfcloud_pass@tcp(127.0.0.1:3306)/selfcloud?parseTime=true"
	}

	sqlDB, err := db.Connect("mysql", dsn)
	if err != nil {
		t.Skipf("Skipping functional test: MariaDB connection failed (%v)", err)
		return nil, func() {}
	}

	if err := db.Migrate(sqlDB); err != nil {
		t.Fatalf("Migration failed: %v", err)
	}

	ctx := context.Background()
	userRepo := repository.NewUserRepository(sqlDB)
	nodeRepo := repository.NewNodeRepository(sqlDB)

	hash, _ := domain.HashPassword("admin")
	adminUser, _ := userRepo.GetByUsername(ctx, "admin")
	if adminUser == nil {
		adminUser = &domain.User{Username: "admin", PasswordHash: hash, Email: "admin@selfcloud.local"}
		_ = userRepo.Create(ctx, adminUser)
		_ = nodeRepo.Create(ctx, &domain.Node{
			UserID: adminUser.ID, Path: "/", ParentPath: "", Name: "", IsDir: true,
			ETag: "root_etag", FileID: "00000001oc_root", MTime: time.Now(), ContentType: "httpd/unix-directory",
		})
	} else {
		adminUser.PasswordHash = hash
		_ = userRepo.Update(ctx, adminUser)
	}
	tempStorage, err := os.MkdirTemp("", "selfcloud_fn_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp storage: %v", err)
	}

	localStorage, err := storage.NewLocalStorage(tempStorage)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ocsHandler := ocs.NewOCSHandler()
	transactor := repository.NewTransactor(sqlDB)
	webdavHandler := webdav.NewWebDAVHandler(transactor, nodeRepo, localStorage, logger)

	sessionRepo := repository.NewSessionRepository(sqlDB)
	authHandler := api.NewAuthHandler(userRepo, sessionRepo, logger)
	sessionAuthWrapper := middleware.SessionAuthMiddleware(sessionRepo, userRepo)

	shareRepo := repository.NewShareRepository(sqlDB)
	filesHandler := api.NewFilesHandler(transactor, nodeRepo, localStorage, logger)
	sharesHandler := api.NewSharesHandler(transactor, shareRepo, nodeRepo, userRepo, localStorage, logger)

	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.HandleFunc("GET /status.php", ocsHandler.StatusHandler)

	mux.HandleFunc("POST /api/v1/auth/login", authHandler.LoginHandler)
	mux.Handle("POST /api/v1/auth/logout", sessionAuthWrapper(http.HandlerFunc(authHandler.LogoutHandler)))
	mux.Handle("GET /api/v1/auth/me", sessionAuthWrapper(http.HandlerFunc(authHandler.MeHandler)))

	mux.Handle("GET /api/v1/files", sessionAuthWrapper(http.HandlerFunc(filesHandler.GetFilesHandler)))
	mux.Handle("POST /api/v1/files/upload", sessionAuthWrapper(http.HandlerFunc(filesHandler.UploadHandler)))
	mux.Handle("POST /api/v1/files/mkdir", sessionAuthWrapper(http.HandlerFunc(filesHandler.MkdirHandler)))
	mux.Handle("DELETE /api/v1/files", sessionAuthWrapper(http.HandlerFunc(filesHandler.DeleteHandler)))
	mux.Handle("POST /api/v1/files/rename", sessionAuthWrapper(http.HandlerFunc(filesHandler.RenameHandler)))
	mux.Handle("GET /api/v1/files/download", sessionAuthWrapper(http.HandlerFunc(filesHandler.DownloadHandler)))
	mux.Handle("GET /api/v1/files/download-zip", sessionAuthWrapper(http.HandlerFunc(filesHandler.DownloadZipHandler)))
	mux.Handle("POST /api/v1/files/download-zip", sessionAuthWrapper(http.HandlerFunc(filesHandler.DownloadZipHandler)))

	mux.Handle("POST /api/v1/shares", sessionAuthWrapper(http.HandlerFunc(sharesHandler.CreateShareHandler)))
	mux.Handle("GET /api/v1/shares", sessionAuthWrapper(http.HandlerFunc(sharesHandler.ListSharesHandler)))
	mux.Handle("DELETE /api/v1/shares", sessionAuthWrapper(http.HandlerFunc(sharesHandler.RevokeShareHandler)))

	mux.HandleFunc("GET /api/v1/public/shares/download", sharesHandler.PublicShareDownloadHandler)
	mux.HandleFunc("GET /api/v1/public/shares", sharesHandler.PublicShareInfoHandler)

	oauthRepo := repository.NewOAuthRepository(sqlDB)
	oauthHandler := api.NewOAuthHandler(oauthRepo, userRepo, sessionRepo, logger)
	graphHandler := api.NewGraphHandler(userRepo, nodeRepo)

	mux.HandleFunc("GET /.well-known/webfinger", oauthHandler.WebFingerHandler)
	mux.HandleFunc("GET /.well-known/openid-configuration", oauthHandler.OpenIDConfigurationHandler)
	mux.HandleFunc("GET /api/v1/auth/oauth2/authorize", oauthHandler.AuthorizeHandler)
	mux.HandleFunc("POST /api/v1/auth/oauth2/authorize", oauthHandler.AuthorizeHandler)
	mux.HandleFunc("GET /index.php/apps/oauth2/authorize", oauthHandler.AuthorizeHandler)
	mux.HandleFunc("POST /index.php/apps/oauth2/authorize", oauthHandler.AuthorizeHandler)
	mux.HandleFunc("GET /apps/oauth2/authorize", oauthHandler.AuthorizeHandler)
	mux.HandleFunc("POST /apps/oauth2/authorize", oauthHandler.AuthorizeHandler)
	mux.HandleFunc("POST /api/v1/auth/oauth2/token", oauthHandler.TokenHandler)
	mux.HandleFunc("POST /index.php/apps/oauth2/api/v1/token", oauthHandler.TokenHandler)
	mux.HandleFunc("POST /apps/oauth2/api/v1/token", oauthHandler.TokenHandler)

	authWrapper := middleware.DualAuthMiddleware(userRepo, sessionRepo, nil)
	mux.Handle("GET /graph/v1.0/me/drives", authWrapper(http.HandlerFunc(graphHandler.MeDrivesHandler)))
	mux.Handle("GET /ocs/v1.php/cloud/capabilities", authWrapper(http.HandlerFunc(ocsHandler.CapabilitiesHandler)))
	mux.Handle("GET /ocs/v2.php/cloud/capabilities", authWrapper(http.HandlerFunc(ocsHandler.CapabilitiesHandler)))
	mux.Handle("GET /ocs/v1.php/cloud/user", authWrapper(http.HandlerFunc(ocsHandler.UserHandler)))
	mux.Handle("GET /ocs/v2.php/cloud/user", authWrapper(http.HandlerFunc(ocsHandler.UserHandler)))
	mux.Handle("/remote.php/webdav/", authWrapper(webdavHandler))
	mux.Handle("/remote.php/webdav", authWrapper(webdavHandler))

	handler := middleware.LoggingMiddleware(logger)(middleware.MetricsMiddleware(mux))
	server := httptest.NewServer(handler)

	cleanup := func() {
		server.Close()
		sqlDB.Close()
		_ = os.RemoveAll(tempStorage)
	}

	return server, cleanup
}

func TestFullSelfCloudWorkflow(t *testing.T) {
	server, cleanup := setupTestServer(t)
	if server == nil {
		return
	}
	defer cleanup()

	client := server.Client()

	// 1. Status.php
	resp, err := client.Get(server.URL + "/status.php")
	if err != nil {
		t.Fatalf("GET /status.php failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK from /status.php, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 2. Metrics
	resp, err = client.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK from /metrics, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 3. Capabilities with Basic Auth admin:admin
	req, _ := http.NewRequest("GET", server.URL+"/ocs/v1.php/cloud/capabilities", nil)
	req.SetBasicAuth("admin", "admin")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("GET capabilities failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK from capabilities, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 4. PROPFIND /remote.php/webdav/
	req, _ = http.NewRequest("PROPFIND", server.URL+"/remote.php/webdav/", nil)
	req.SetBasicAuth("admin", "admin")
	req.Header.Set("Depth", "1")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("PROPFIND /remote.php/webdav/ failed: %v", err)
	}
	if resp.StatusCode != http.StatusMultiStatus {
		t.Errorf("expected 207 Multi-Status, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 5. PUT /remote.php/webdav/hello.txt
	fileContent := "Hello SelfCloud WebDAV client!"
	req, _ = http.NewRequest("PUT", server.URL+"/remote.php/webdav/hello.txt", strings.NewReader(fileContent))
	req.SetBasicAuth("admin", "admin")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("PUT /remote.php/webdav/hello.txt failed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 201 Created or 204 No Content, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 6. GET /remote.php/webdav/hello.txt
	req, _ = http.NewRequest("GET", server.URL+"/remote.php/webdav/hello.txt", nil)
	req.SetBasicAuth("admin", "admin")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("GET /remote.php/webdav/hello.txt failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(bodyBytes) != fileContent {
		t.Errorf("expected file content %q, got %q", fileContent, string(bodyBytes))
	}

	// 7. MKCOL /remote.php/webdav/myfolder
	req, _ = http.NewRequest("MKCOL", server.URL+"/remote.php/webdav/myfolder", nil)
	req.SetBasicAuth("admin", "admin")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("MKCOL failed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201 Created for MKCOL, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 8. MOVE /remote.php/webdav/hello.txt -> /remote.php/webdav/myfolder/hello_moved.txt
	req, _ = http.NewRequest("MOVE", server.URL+"/remote.php/webdav/hello.txt", nil)
	req.SetBasicAuth("admin", "admin")
	req.Header.Set("Destination", server.URL+"/remote.php/webdav/myfolder/hello_moved.txt")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("MOVE failed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 201 or 204 for MOVE, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 8b. OPTIONS /remote.php/webdav/
	req, _ = http.NewRequest("OPTIONS", server.URL+"/remote.php/webdav/", nil)
	req.SetBasicAuth("admin", "admin")
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK from OPTIONS, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 8c. PROPPATCH /remote.php/webdav/myfolder/hello_moved.txt
	req, _ = http.NewRequest("PROPPATCH", server.URL+"/remote.php/webdav/myfolder/hello_moved.txt", strings.NewReader("<propertyupdate/>"))
	req.SetBasicAuth("admin", "admin")
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusMultiStatus {
		t.Errorf("expected 207 Multi-Status from PROPPATCH, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 9. DELETE /remote.php/webdav/myfolder
	req, _ = http.NewRequest("DELETE", server.URL+"/remote.php/webdav/myfolder", nil)
	req.SetBasicAuth("admin", "admin")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("DELETE failed: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204 No Content for DELETE, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAuthAPIWorkflow(t *testing.T) {
	server, cleanup := setupTestServer(t)
	if server == nil {
		return
	}
	defer cleanup()

	jar, _ := cookiejar.New(nil)
	client := server.Client()
	client.Jar = jar

	// 1. GET /api/v1/auth/me -> 401 Unauthorized (not logged in)
	resp, err := client.Get(server.URL + "/api/v1/auth/me")
	if err != nil {
		t.Fatalf("GET /api/v1/auth/me failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized for unauthenticated /me, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 2. POST /api/v1/auth/login with wrong password -> 401
	badLoginJSON := `{"username":"admin","password":"wrongpassword"}`
	resp, err = client.Post(server.URL+"/api/v1/auth/login", "application/json", strings.NewReader(badLoginJSON))
	if err != nil {
		t.Fatalf("POST /api/v1/auth/login failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for bad password, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 3. POST /api/v1/auth/login with admin:admin -> 200 OK & session cookie set
	loginJSON := `{"username":"admin","password":"admin"}`
	resp, err = client.Post(server.URL+"/api/v1/auth/login", "application/json", strings.NewReader(loginJSON))
	if err != nil {
		t.Fatalf("POST /api/v1/auth/login failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK for valid login, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(body), `"username":"admin"`) {
		t.Errorf("expected response to contain user profile, got %s", string(body))
	}

	// 4. GET /api/v1/auth/me with session cookie -> 200 OK
	resp, err = client.Get(server.URL + "/api/v1/auth/me")
	if err != nil {
		t.Fatalf("GET /api/v1/auth/me failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK for authenticated /me, got %d", resp.StatusCode)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"username":"admin"`) {
		t.Errorf("expected /me response to contain user profile, got %s", string(body))
	}

	// 5. POST /api/v1/auth/logout -> 200 OK
	resp, err = client.Post(server.URL+"/api/v1/auth/logout", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/v1/auth/logout failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK for logout, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 6. GET /api/v1/auth/me after logout -> 401 Unauthorized
	resp, err = client.Get(server.URL + "/api/v1/auth/me")
	if err != nil {
		t.Fatalf("GET /api/v1/auth/me failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized after logout, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 7. Test Bearer token login, access, and logout revocation
	bearerClient := &http.Client{} // clean client without cookies
	loginReq, _ := http.NewRequest("POST", server.URL+"/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"admin"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("X-Forwarded-Proto", "https") // test secure cookie detection
	resp, err = bearerClient.Do(loginReq)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Bearer login failed: %v, status: %d", err, resp.StatusCode)
	}
	// Verify Secure flag is set on cookie when HTTPS is detected
	cookies := resp.Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == middleware.SessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil || !sessionCookie.Secure {
		t.Errorf("expected Secure=true on session cookie for HTTPS request, got: %+v", sessionCookie)
	}

	var loginData struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&loginData)
	resp.Body.Close()
	if loginData.Token == "" {
		t.Fatalf("expected token in login response, got empty")
	}

	// 7a. Query /me with Bearer token -> 200 OK
	meReq, _ := http.NewRequest("GET", server.URL+"/api/v1/auth/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+loginData.Token)
	resp, err = bearerClient.Do(meReq)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/auth/me with Bearer failed: %v, status: %d", err, resp.StatusCode)
	}
	resp.Body.Close()

	// 7b. Logout with Bearer token -> 200 OK
	logoutReq, _ := http.NewRequest("POST", server.URL+"/api/v1/auth/logout", nil)
	logoutReq.Header.Set("Authorization", "Bearer "+loginData.Token)
	resp, err = bearerClient.Do(logoutReq)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/logout with Bearer failed: %v, status: %d", err, resp.StatusCode)
	}
	resp.Body.Close()

	// 7c. Query /me with revoked Bearer token -> 401 Unauthorized
	meRevokedReq, _ := http.NewRequest("GET", server.URL+"/api/v1/auth/me", nil)
	meRevokedReq.Header.Set("Authorization", "Bearer "+loginData.Token)
	resp, err = bearerClient.Do(meRevokedReq)
	if err != nil {
		t.Fatalf("GET /api/v1/auth/me after Bearer logout failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized after Bearer logout revocation, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestFilesAndSharesAPIWorkflow(t *testing.T) {
	server, cleanup := setupTestServer(t)
	if server == nil {
		return
	}
	defer cleanup()

	jar, _ := cookiejar.New(nil)
	client := server.Client()
	client.Jar = jar

	// Login
	loginJSON := `{"username":"admin","password":"admin"}`
	resp, err := client.Post(server.URL+"/api/v1/auth/login", "application/json", strings.NewReader(loginJSON))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Login failed: %v, status: %d", err, resp.StatusCode)
	}
	resp.Body.Close()

	// Clean up any leftover test node from prior runs
	delReq, _ := http.NewRequest("DELETE", server.URL+"/api/v1/files?path=/documents", nil)
	_, _ = client.Do(delReq)

	// 1. GET /api/v1/files?path=/ -> list root directory
	resp, err = client.Get(server.URL + "/api/v1/files?path=/")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/files failed: %v, status: %d", err, resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"path":"/"`) {
		t.Errorf("expected root directory response, got %s", string(body))
	}

	// 2. POST /api/v1/files/mkdir -> create /documents
	mkdirJSON := `{"path":"/documents"}`
	resp, err = client.Post(server.URL+"/api/v1/files/mkdir", "application/json", strings.NewReader(mkdirJSON))
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/v1/files/mkdir failed: %v, status: %d", err, resp.StatusCode)
	}
	resp.Body.Close()

	// 3. POST /api/v1/files/upload -> upload /documents/sample.txt
	var bodyBuf bytes.Buffer
	mw := multipart.NewWriter(&bodyBuf)
	_ = mw.WriteField("path", "/documents/sample.txt")
	fw, _ := mw.CreateFormFile("file", "sample.txt")
	_, _ = fw.Write([]byte("SelfCloud Web UI REST API File Upload Content"))
	_ = mw.Close()

	req, _ := http.NewRequest("POST", server.URL+"/api/v1/files/upload", &bodyBuf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/v1/files/upload failed: %v, status: %d", err, resp.StatusCode)
	}
	resp.Body.Close()

	// 3b. POST /api/v1/files/upload with path="/documents" (folder path without trailing slash)
	var bodyBuf2 bytes.Buffer
	mw2 := multipart.NewWriter(&bodyBuf2)
	_ = mw2.WriteField("path", "/documents")
	fw2, _ := mw2.CreateFormFile("file", "book.pdf")
	_, _ = fw2.Write([]byte("PDF Book Content"))
	_ = mw2.Close()

	req2, _ := http.NewRequest("POST", server.URL+"/api/v1/files/upload", &bodyBuf2)
	req2.Header.Set("Content-Type", mw2.FormDataContentType())
	resp, err = client.Do(req2)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/v1/files/upload to directory path failed: %v, status: %d", err, resp.StatusCode)
	}
	resp.Body.Close()

	// 4. GET /api/v1/files/download?path=/documents/sample.txt
	resp, err = client.Get(server.URL + "/api/v1/files/download?path=/documents/sample.txt")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET download failed: %v, status: %d", err, resp.StatusCode)
	}
	content, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(content) != "SelfCloud Web UI REST API File Upload Content" {
		t.Errorf("unexpected downloaded content: %s", string(content))
	}

	// 5. GET /api/v1/files/download-zip?path=/documents -> dynamic ZIP stream
	resp, err = client.Get(server.URL + "/api/v1/files/download-zip?path=/documents")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET download-zip failed: %v, status: %d", err, resp.StatusCode)
	}
	zipBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if len(zipBytes) == 0 || resp.Header.Get("Content-Type") != "application/zip" {
		t.Errorf("expected ZIP stream, got content-type %s, size %d", resp.Header.Get("Content-Type"), len(zipBytes))
	}

	// 5b. POST /api/v1/files/rename -> rename /documents/book.pdf to /documents/renamed_book.pdf
	renameJSON := `{"old_path":"/documents/book.pdf","new_path":"/documents/renamed_book.pdf"}`
	resp, err = client.Post(server.URL+"/api/v1/files/rename", "application/json", strings.NewReader(renameJSON))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/v1/files/rename failed: %v, status: %d", err, resp.StatusCode)
	}
	resp.Body.Close()

	// Verify name and path updated in directory listing
	resp, err = client.Get(server.URL + "/api/v1/files?path=/documents")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET list after rename failed: %v, status: %d", err, resp.StatusCode)
	}
	listBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(listBody), `"name":"renamed_book.pdf"`) {
		t.Errorf("expected directory listing to contain renamed_book.pdf, got: %s", string(listBody))
	}

	// 6. POST /api/v1/shares -> create public share link for /documents/sample.txt
	shareReqJSON := `{"path":"/documents/sample.txt"}`
	resp, err = client.Post(server.URL+"/api/v1/shares", "application/json", strings.NewReader(shareReqJSON))
	if err != nil || (resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK) {
		t.Fatalf("POST /api/v1/shares failed: %v, status: %d", err, resp.StatusCode)
	}
	shareBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var shareRes struct {
		Share struct {
			Token    string `json:"token"`
			ShareURL string `json:"share_url"`
		} `json:"share"`
	}
	_ = json.Unmarshal(shareBody, &shareRes)
	if shareRes.Share.Token == "" {
		t.Fatalf("failed to parse share token from %s", string(shareBody))
	}

	// 7. Unauthenticated public access: GET /api/v1/public/shares?token=...
	anonClient := &http.Client{}
	resp, err = anonClient.Get(server.URL + "/api/v1/public/shares?token=" + shareRes.Share.Token)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Public share info failed: %v, status: %d", err, resp.StatusCode)
	}
	resp.Body.Close()

	// 8. Unauthenticated public download: GET /api/v1/public/shares/download?token=...
	resp, err = anonClient.Get(server.URL + "/api/v1/public/shares/download?token=" + shareRes.Share.Token)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Public share download failed: %v, status: %d", err, resp.StatusCode)
	}
	publicContent, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(publicContent) != "SelfCloud Web UI REST API File Upload Content" {
		t.Errorf("unexpected public shared content: %s", string(publicContent))
	}

	// 8b. Authenticated share listing: GET /api/v1/shares
	resp, err = client.Get(server.URL + "/api/v1/shares")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/shares failed: %v, status: %d", err, resp.StatusCode)
	}
	sharesListBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(sharesListBody), shareRes.Share.Token) {
		t.Errorf("expected GET /api/v1/shares to include token %s, got: %s", shareRes.Share.Token, string(sharesListBody))
	}

	// 8c. Authenticated share revocation: DELETE /api/v1/shares?token=...
	revokeReq, _ := http.NewRequest("DELETE", server.URL+"/api/v1/shares?token="+shareRes.Share.Token, nil)
	resp, err = client.Do(revokeReq)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE /api/v1/shares failed: %v, status: %d", err, resp.StatusCode)
	}
	resp.Body.Close()

	// Verify share is gone from public info
	resp, err = anonClient.Get(server.URL + "/api/v1/public/shares?token=" + shareRes.Share.Token)
	if err != nil || resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for revoked share, got status %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 9. DELETE /api/v1/files?path=/documents -> recursive delete
	req, _ = http.NewRequest("DELETE", server.URL+"/api/v1/files?path=/documents", nil)
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE /api/v1/files failed: %v, status: %d", err, resp.StatusCode)
	}
	resp.Body.Close()
}

func TestOwnCloudSyncClientOAuthWorkflow(t *testing.T) {
	server, cleanup := setupTestServer(t)
	if server == nil {
		return
	}
	defer cleanup()

	client := server.Client()

	// 1. Probe: Client performs unauthenticated PROPFIND /remote.php/webdav/
	// Must return 401 and WWW-Authenticate containing "bearer "
	probeReq, _ := http.NewRequest("PROPFIND", server.URL+"/remote.php/webdav/", nil)
	probeResp, err := client.Do(probeReq)
	if err != nil {
		t.Fatalf("PROPFIND probe failed: %v", err)
	}
	if probeResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for unauthenticated PROPFIND, got %d", probeResp.StatusCode)
	}
	authHeader := probeResp.Header.Get("WWW-Authenticate")
	probeResp.Body.Close()
	if !strings.Contains(strings.ToLower(authHeader), "bearer ") {
		t.Fatalf("expected WWW-Authenticate to contain 'bearer ', got %q", authHeader)
	}

	// 2. Discovery: Client fetches /.well-known/openid-configuration
	discoveryResp, err := client.Get(server.URL + "/.well-known/openid-configuration")
	if err != nil || discoveryResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /.well-known/openid-configuration failed: %v, status: %d", err, discoveryResp.StatusCode)
	}
	var oidcDoc struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
	}
	if err := json.NewDecoder(discoveryResp.Body).Decode(&oidcDoc); err != nil {
		t.Fatalf("failed to decode OIDC config: %v", err)
	}
	discoveryResp.Body.Close()
	if oidcDoc.AuthorizationEndpoint == "" || oidcDoc.TokenEndpoint == "" {
		t.Fatalf("missing authorization or token endpoint in OIDC doc: %+v", oidcDoc)
	}

	// 3. OAuth2 PKCE Flow: Client opens browser to authorize
	codeVerifier := "an_arbitrary_random_verifier_string_of_sufficient_length_for_pkce_test"
	h := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(h[:])
	redirectURI := "http://127.0.0.1:45678"

	// Simulate user submitting credentials on the authorization page
	authForm := url.Values{
		"action":                {"authorize"},
		"client_id":             {api.DefaultOwnCloudClientID},
		"redirect_uri":          {redirectURI},
		"state":                 {"random_state_val"},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
		"username":              {"admin"},
		"password":              {"admin"},
	}

	// Disable auto-redirect to inspect 302 Location header
	noRedirectClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	authResp, err := noRedirectClient.PostForm(server.URL+"/index.php/apps/oauth2/authorize", authForm)
	if err != nil {
		t.Fatalf("POST /authorize failed: %v", err)
	}
	if authResp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 Found redirect, got %d", authResp.StatusCode)
	}
	redirectLocation := authResp.Header.Get("Location")
	authResp.Body.Close()

	parsedRedirect, err := url.Parse(redirectLocation)
	if err != nil {
		t.Fatalf("failed to parse redirect URL: %v", err)
	}
	authCode := parsedRedirect.Query().Get("code")
	if authCode == "" {
		t.Fatalf("missing authorization code in redirect location: %s", redirectLocation)
	}

	// 4. Token Exchange: Client exchanges code for access_token and refresh_token
	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {api.DefaultOwnCloudClientID},
		"client_secret": {api.DefaultOwnCloudClientSecret},
		"code":          {authCode},
		"redirect_uri":  {redirectURI},
		"code_verifier": {codeVerifier},
	}
	tokenResp, err := client.PostForm(server.URL+"/index.php/apps/oauth2/api/v1/token", tokenForm)
	if err != nil || tokenResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /token failed: %v, status: %d", err, tokenResp.StatusCode)
	}
	var tokenData struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		RefreshToken string `json:"refresh_token"`
		UserID       string `json:"user_id"`
	}
	if err := json.NewDecoder(tokenResp.Body).Decode(&tokenData); err != nil {
		t.Fatalf("failed to decode token response: %v", err)
	}
	tokenResp.Body.Close()
	if tokenData.AccessToken == "" || tokenData.TokenType != "Bearer" || tokenData.UserID != "admin" {
		t.Fatalf("invalid token response: %+v", tokenData)
	}

	// 5. User Info: Client calls /ocs/v2.php/cloud/user with Bearer token
	userReq, _ := http.NewRequest("GET", server.URL+"/ocs/v2.php/cloud/user?format=json", nil)
	userReq.Header.Set("Authorization", "Bearer "+tokenData.AccessToken)
	userResp, err := client.Do(userReq)
	if err != nil || userResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ocs/v2.php/cloud/user failed: %v, status: %d", err, userResp.StatusCode)
	}
	var userData struct {
		OCS struct {
			Data struct {
				ID          string `json:"id"`
				DisplayName string `json:"display-name"`
			} `json:"data"`
		} `json:"ocs"`
	}
	if err := json.NewDecoder(userResp.Body).Decode(&userData); err != nil {
		t.Fatalf("failed to decode user response: %v", err)
	}
	userResp.Body.Close()
	if userData.OCS.Data.ID != "admin" {
		t.Errorf("expected user ID 'admin', got %q", userData.OCS.Data.ID)
	}

	// 6. Capabilities: Client checks capabilities
	capReq, _ := http.NewRequest("GET", server.URL+"/ocs/v2.php/cloud/capabilities?format=json", nil)
	capReq.Header.Set("Authorization", "Bearer "+tokenData.AccessToken)
	capResp, err := client.Do(capReq)
	if err != nil || capResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ocs/v2.php/cloud/capabilities failed: %v, status: %d", err, capResp.StatusCode)
	}
	var capData struct {
		OCS struct {
			Data struct {
				Capabilities struct {
					OAuth2 struct {
						Enabled bool `json:"enabled"`
					} `json:"oauth2"`
					Spaces *struct {
						Enabled bool `json:"enabled"`
					} `json:"spaces"`
				} `json:"capabilities"`
			} `json:"data"`
		} `json:"ocs"`
	}
	if err := json.NewDecoder(capResp.Body).Decode(&capData); err != nil {
		t.Fatalf("failed to decode capabilities: %v", err)
	}
	capResp.Body.Close()
	if !capData.OCS.Data.Capabilities.OAuth2.Enabled {
		t.Fatalf("expected capabilities.oauth2.enabled == true")
	}
	if capData.OCS.Data.Capabilities.Spaces != nil && capData.OCS.Data.Capabilities.Spaces.Enabled {
		t.Fatalf("expected capabilities.spaces to be omitted/disabled by default")
	}

	// 7. Graph API: Client queries /graph/v1.0/me/drives
	graphReq, _ := http.NewRequest("GET", server.URL+"/graph/v1.0/me/drives", nil)
	graphReq.Header.Set("Authorization", "Bearer "+tokenData.AccessToken)
	graphResp, err := client.Do(graphReq)
	if err != nil || graphResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /graph/v1.0/me/drives failed: %v, status: %d", err, graphResp.StatusCode)
	}
	var drivesData struct {
		Value []struct {
			ID        string `json:"id"`
			DriveType string `json:"driveType"`
			Root      struct {
				WebDavURL string `json:"webDavUrl"`
			} `json:"root"`
		} `json:"value"`
	}
	if err := json.NewDecoder(graphResp.Body).Decode(&drivesData); err != nil {
		t.Fatalf("failed to decode drives response: %v", err)
	}
	graphResp.Body.Close()
	if len(drivesData.Value) == 0 || drivesData.Value[0].DriveType != "personal" {
		t.Fatalf("expected personal drive in drives response: %+v", drivesData)
	}

	// 8. WebDAV Operations with Bearer token
	type davMultiStatus struct {
		XMLName   xml.Name `xml:"multistatus"`
		Responses []struct {
			HRef     string `xml:"href"`
			PropStat struct {
				Prop struct {
					GetETag string `xml:"getetag"`
					OCSize  int64  `xml:"size"`
				} `xml:"prop"`
			} `xml:"propstat"`
		} `xml:"response"`
	}

	// 8a. PROPFIND /remote.php/webdav/
	davPropReq, _ := http.NewRequest("PROPFIND", server.URL+"/remote.php/webdav/", nil)
	davPropReq.Header.Set("Authorization", "Bearer "+tokenData.AccessToken)
	davPropReq.Header.Set("Depth", "0")
	davPropResp, err := client.Do(davPropReq)
	if err != nil || davPropResp.StatusCode != http.StatusMultiStatus {
		t.Fatalf("PROPFIND /remote.php/webdav/ failed: %v, status: %d", err, davPropResp.StatusCode)
	}
	var msBefore davMultiStatus
	if err := xml.NewDecoder(davPropResp.Body).Decode(&msBefore); err != nil || len(msBefore.Responses) == 0 {
		t.Fatalf("failed to decode MultiStatus XML: %v", err)
	}
	davPropResp.Body.Close()
	rootETagBefore := msBefore.Responses[0].PropStat.Prop.GetETag

	// 8b. PUT /remote.php/webdav/client_sync_test.txt
	putContent := "Hello from ownCloud Desktop Client sync engine!"
	putReq, _ := http.NewRequest("PUT", server.URL+"/remote.php/webdav/client_sync_test.txt", strings.NewReader(putContent))
	putReq.Header.Set("Authorization", "Bearer "+tokenData.AccessToken)
	putResp, err := client.Do(putReq)
	if err != nil || (putResp.StatusCode != http.StatusCreated && putResp.StatusCode != http.StatusNoContent) {
		t.Fatalf("PUT /remote.php/webdav/client_sync_test.txt failed: %v, status: %d", err, putResp.StatusCode)
	}
	putResp.Body.Close()

	// 8c. Verify root ETag changed after file creation so sync client detects update
	davPropReq2, _ := http.NewRequest("PROPFIND", server.URL+"/remote.php/webdav/", nil)
	davPropReq2.Header.Set("Authorization", "Bearer "+tokenData.AccessToken)
	davPropReq2.Header.Set("Depth", "0")
	davPropResp2, err := client.Do(davPropReq2)
	if err != nil || davPropResp2.StatusCode != http.StatusMultiStatus {
		t.Fatalf("PROPFIND /remote.php/webdav/ after PUT failed: %v", err)
	}
	var msAfter davMultiStatus
	if err := xml.NewDecoder(davPropResp2.Body).Decode(&msAfter); err != nil || len(msAfter.Responses) == 0 {
		t.Fatalf("failed to decode MultiStatus XML after PUT: %v", err)
	}
	davPropResp2.Body.Close()
	rootETagAfter := msAfter.Responses[0].PropStat.Prop.GetETag

	if rootETagBefore == "" || rootETagAfter == "" || rootETagBefore == rootETagAfter {
		t.Fatalf("expected root ETag to change after PUT file (to notify sync client), before=%q, after=%q", rootETagBefore, rootETagAfter)
	}

	if rootSizeAfter := msAfter.Responses[0].PropStat.Prop.OCSize; rootSizeAfter < int64(len(putContent)) {
		t.Fatalf("expected root directory oc:size to reflect uploaded file, got %d, expected at least %d", rootSizeAfter, len(putContent))
	}

	// 8d. GET /remote.php/webdav/client_sync_test.txt
	getReq, _ := http.NewRequest("GET", server.URL+"/remote.php/webdav/client_sync_test.txt", nil)
	getReq.Header.Set("Authorization", "Bearer "+tokenData.AccessToken)
	getResp, err := client.Do(getReq)
	if err != nil || getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /remote.php/webdav/client_sync_test.txt failed: %v, status: %d", err, getResp.StatusCode)
	}
	getBody, _ := io.ReadAll(getResp.Body)
	getResp.Body.Close()
	if string(getBody) != putContent {
		t.Errorf("expected content %q, got %q", putContent, string(getBody))
	}
}



