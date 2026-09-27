package functional

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
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

	adminUser, _ := userRepo.GetByUsername(ctx, "admin")
	if adminUser == nil {
		hash, _ := domain.HashPassword("admin")
		adminUser = &domain.User{Username: "admin", PasswordHash: hash, Email: "admin@selfcloud.local"}
		_ = userRepo.Create(ctx, adminUser)
		_ = nodeRepo.Create(ctx, &domain.Node{
			UserID: adminUser.ID, Path: "/", ParentPath: "", Name: "", IsDir: true,
			ETag: "root_etag", FileID: "00000001oc_root", MTime: time.Now(), ContentType: "httpd/unix-directory",
		})
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

	authWrapper := middleware.BasicAuthMiddleware(userRepo)
	mux.Handle("GET /ocs/v1.php/cloud/capabilities", authWrapper(http.HandlerFunc(ocsHandler.CapabilitiesHandler)))
	mux.Handle("GET /ocs/v1.php/cloud/user", authWrapper(http.HandlerFunc(ocsHandler.UserHandler)))
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

	// 9. DELETE /api/v1/files?path=/documents -> recursive delete
	req, _ = http.NewRequest("DELETE", server.URL+"/api/v1/files?path=/documents", nil)
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE /api/v1/files failed: %v, status: %d", err, resp.StatusCode)
	}
	resp.Body.Close()
}


