package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"selfcloud/internal/api"
	"selfcloud/internal/config"
	"selfcloud/internal/db"
	"selfcloud/internal/domain"
	"selfcloud/internal/indexer"
	"selfcloud/internal/middleware"
	"selfcloud/internal/ocs"
	"selfcloud/internal/repository"
	"selfcloud/internal/storage"
	"selfcloud/internal/webdav"
)

// Build information, injected via -ldflags at build time.
var (
	buildVersion = "dev"
	buildCommit  = "unknown"
	buildDate    = "unknown"
)

func main() {
	cmd := "serve"
	if len(os.Args) >= 2 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		cmdServe()
	case "migrate":
		cmdMigrate()
	case "user":
		cmdUser(os.Args[2:])
	case "reindex":
		cmdReindex(os.Args[2:])
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`SelfCloud - Lightweight, high-performance WebDAV backend

Usage:
  selfcloud <command> [arguments]

Commands:
  serve                                                    Start the HTTP server (default)
  migrate                                                  Run database schema migrations
  user create <username> [password] [email] [--update-if-exists] Create a new user (or update properties if exists)
  user list                                                List all users
  user delete <username>                                   Delete a user
  user update <username> [--password <p>] [--email <e>]    Update user password or email
  user set-password <username> [password]                  Update user password (prompted/stdin/env if omitted)
  user set-email <username> <email>                        Update user email
  reindex [flags]                                          Sync filesystem storage to database
    --user <username>                                      Only reindex a specific user (default: all)
    --dry-run                                              Print planned changes without writing to DB
    --remove-orphans                                       Delete DB entries with no corresponding file

Environment Variables:
  SERVER_PORT     HTTP port to listen on (default: 8080)
  STORAGE_DIR     File storage root directory (default: ./data)
  DB_DRIVER       Database driver (default: mysql)
  DB_DSN          Database connection string (default: selfcloud:selfcloud_pass@tcp(127.0.0.1:3306)/selfcloud?parseTime=true)
  LOG_LEVEL       Logging level: debug, info, warn, error (default: info)
  SERVE_STATIC    Serve Web UI from ./web/dist (default: true)
  AUTO_MIGRATE    Run schema migrations automatically on startup (default: true)
  MAX_UPLOAD_SIZE_BYTES Max upload payload size in bytes (default: 10737418240 [10GB])`)
}

func makeLogger(cfg *config.Config) *slog.Logger {
	var handler slog.Handler
	if cfg.LogLevel == "debug" {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	} else {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	}
	return slog.New(handler)
}

func connectDB(cfg *config.Config) (*sql.DB, error) {
	return db.Connect(cfg.DBDriver, cfg.DBDSN, db.PoolConfig{
		MaxOpenConns:    cfg.DBMaxOpenConns,
		MaxIdleConns:    cfg.DBMaxIdleConns,
		ConnMaxLifetime: cfg.DBConnMaxLifetime,
		ConnMaxIdleTime: cfg.DBConnMaxIdleTime,
	})
}

func cmdMigrate() {
	cfg := config.LoadConfig()
	logger := makeLogger(cfg)

	logger.Info("Connecting to database", slog.String("driver", cfg.DBDriver))
	sqlDB, err := connectDB(cfg)
	if err != nil {
		logger.Error("Database connection failed", slog.Any("error", err))
		os.Exit(1)
	}
	defer sqlDB.Close()

	if err := db.Migrate(sqlDB); err != nil {
		logger.Error("Migration failed", slog.Any("error", err))
		os.Exit(1)
	}
	logger.Info("Database migration completed successfully")
}

func cmdReindex(args []string) {
	// Parse flags manually to avoid importing flag package complexity.
	var targetUser string
	dryRun := false
	removeOrphans := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--user":
			if i+1 < len(args) {
				i++
				targetUser = args[i]
			}
		case "--dry-run":
			dryRun = true
		case "--remove-orphans":
			removeOrphans = true
		}
	}

	cfg := config.LoadConfig()
	logger := makeLogger(cfg)

	sqlDB, err := connectDB(cfg)
	if err != nil {
		logger.Error("Database connection failed", slog.Any("error", err))
		os.Exit(1)
	}
	defer sqlDB.Close()

	userRepo := repository.NewUserRepository(sqlDB)
	nodeRepo := repository.NewNodeRepository(sqlDB)
	ctx := context.Background()

	if dryRun {
		logger.Info("Dry-run mode enabled: no changes will be written to the database")
	}

	// Collect users to reindex.
	var users []*domain.User
	if targetUser != "" {
		u, err := userRepo.GetByUsername(ctx, targetUser)
		if err != nil || u == nil {
			logger.Error("User not found", slog.String("username", targetUser))
			os.Exit(1)
		}
		users = []*domain.User{u}
	} else {
		users, err = userRepo.ListAll(ctx)
		if err != nil {
			logger.Error("Failed to list users", slog.Any("error", err))
			os.Exit(1)
		}
	}

	idx := indexer.New(nodeRepo, cfg.StorageDir, logger, dryRun)

	totalCreated, totalUpdated, totalDeleted, totalErrors := 0, 0, 0, 0
	for _, u := range users {
		logger.Info("Reindexing user", slog.String("username", u.Username))
		stats, err := idx.ReindexUser(ctx, u, removeOrphans)
		if err != nil {
			logger.Error("Reindex failed", slog.String("username", u.Username), slog.Any("error", err))
			totalErrors++
			continue
		}
		logger.Info("Reindex complete for user",
			slog.String("username", u.Username),
			slog.Int("created", stats.Created),
			slog.Int("updated", stats.Updated),
			slog.Int("deleted", stats.Deleted),
			slog.Int("errors", stats.Errors),
		)
		totalCreated += stats.Created
		totalUpdated += stats.Updated
		totalDeleted += stats.Deleted
		totalErrors += stats.Errors
	}

	logger.Info("Reindex finished",
		slog.Int("total_created", totalCreated),
		slog.Int("total_updated", totalUpdated),
		slog.Int("total_deleted", totalDeleted),
		slog.Int("total_errors", totalErrors),
		slog.Bool("dry_run", dryRun),
	)

	if totalErrors > 0 {
		os.Exit(1)
	}
}

func resolvePassword(explicitPassword, prompt string) (string, error) {
	if explicitPassword != "" {
		return explicitPassword, nil
	}
	if envPass := os.Getenv("SELFCLOUD_USER_PASSWORD"); envPass != "" {
		return envPass, nil
	}
	stat, err := os.Stdin.Stat()
	if err == nil && (stat.Mode()&os.ModeCharDevice) != 0 {
		fmt.Fprintf(os.Stderr, "%s: ", prompt)
	}
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("failed to read password from stdin: %w", err)
	}
	pass := strings.TrimRight(line, "\r\n")
	if pass == "" {
		return "", errors.New("empty password provided")
	}
	return pass, nil
}

func cmdUser(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: selfcloud user <create|list|delete|update|set-password|set-email> [args...]")
		os.Exit(1)
	}

	cfg := config.LoadConfig()
	logger := makeLogger(cfg)

	sqlDB, err := connectDB(cfg)
	if err != nil {
		logger.Error("Database connection failed", slog.Any("error", err))
		os.Exit(1)
	}
	defer sqlDB.Close()

	repos := repository.NewRepositories(sqlDB)
	transactor := repository.NewTransactor(sqlDB)
	ctx := context.Background()

	switch args[0] {
	case "create":
		updateIfExists := false
		var positional []string
		for _, arg := range args[1:] {
			if arg == "--update-if-exists" || arg == "--upsert" {
				updateIfExists = true
			} else {
				positional = append(positional, arg)
			}
		}

		if len(positional) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: selfcloud user create <username> [password] [email] [--update-if-exists]")
			os.Exit(1)
		}
		username := positional[0]
		passArg := ""
		email := ""
		if len(positional) >= 2 {
			passArg = positional[1]
		}
		if len(positional) >= 3 {
			email = positional[2]
		}

		password, err := resolvePassword(passArg, fmt.Sprintf("Enter password for user %q", username))
		if err != nil {
			logger.Error("Password error", slog.Any("error", err))
			os.Exit(1)
		}

		hash, err := domain.HashPassword(password)
		if err != nil {
			logger.Error("Failed to hash password", slog.Any("error", err))
			os.Exit(1)
		}

		var createdUser *domain.User
		status := "created" // "created", "updated", "unchanged"

		err = transactor.WithTx(ctx, func(tx *repository.Repositories) error {
			existing, err := tx.Users.GetByUsername(ctx, username)
			if err != nil {
				return fmt.Errorf("failed to check existing user: %w", err)
			}
			if existing != nil {
				if !updateIfExists {
					return fmt.Errorf("user %q already exists", username)
				}
				needsUpdate := false
				if !existing.CheckPassword(password) {
					existing.PasswordHash = hash
					needsUpdate = true
				}
				if email != "" && existing.Email != email {
					existing.Email = email
					needsUpdate = true
				}
				if needsUpdate {
					if err := tx.Users.Update(ctx, existing); err != nil {
						return fmt.Errorf("failed to update user: %w", err)
					}
					status = "updated"
				} else {
					status = "unchanged"
				}
				createdUser = existing
				return nil
			}

			user := &domain.User{Username: username, PasswordHash: hash, Email: email}
			if err := tx.Users.Create(ctx, user); err != nil {
				return fmt.Errorf("failed to create user: %w", err)
			}
			createdUser = user

			// Seed root directory node for new user.
			rootNode := &domain.Node{
				UserID:      user.ID,
				Path:        "/",
				ParentPath:  "",
				Name:        "",
				IsDir:       true,
				ETag:        fmt.Sprintf("root_%s", username),
				FileID:      fmt.Sprintf("%08doc_root", user.ID),
				MTime:       time.Now(),
				ContentType: "httpd/unix-directory",
			}
			if err := tx.Nodes.Create(ctx, rootNode); err != nil {
				return fmt.Errorf("failed to initialize user root node: %w", err)
			}
			return nil
		})

		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			logger.Error("Failed to create user", slog.Any("error", err))
			os.Exit(1)
		}

		switch status {
		case "created":
			fmt.Printf("User %q created successfully (id=%d)\n", username, createdUser.ID)
		case "updated":
			fmt.Printf("User %q already exists, updated successfully (id=%d)\n", username, createdUser.ID)
		case "unchanged":
			fmt.Printf("User %q already exists and is up to date (id=%d)\n", username, createdUser.ID)
		}

	case "list":
		users, err := repos.Users.ListAll(ctx)
		if err != nil {
			logger.Error("Failed to list users", slog.Any("error", err))
			os.Exit(1)
		}
		fmt.Printf("%-5s  %-30s  %-30s  %s\n", "ID", "Username", "Email", "Created")
		fmt.Printf("%-5s  %-30s  %-30s  %s\n", "---", "--------", "-----", "-------")
		for _, u := range users {
			fmt.Printf("%-5d  %-30s  %-30s  %s\n", u.ID, u.Username, u.Email, u.CreatedAt.Format(time.RFC3339))
		}

	case "delete":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: selfcloud user delete <username>")
			os.Exit(1)
		}
		username := args[1]
		err = transactor.WithTx(ctx, func(tx *repository.Repositories) error {
			u, err := tx.Users.GetByUsername(ctx, username)
			if err != nil || u == nil {
				return fmt.Errorf("user %q not found", username)
			}
			if err := tx.Nodes.DeletePrefix(ctx, u.ID, "/"); err != nil {
				return fmt.Errorf("failed to clean up user files: %w", err)
			}
			return tx.Users.Delete(ctx, username)
		})
		if err != nil {
			logger.Error("Failed to delete user", slog.Any("error", err))
			os.Exit(1)
		}
		// Clean up user's storage directory if it exists
		userStorageDir := filepath.Join(cfg.StorageDir, username)
		if err := os.RemoveAll(userStorageDir); err != nil {
			logger.Warn("Failed to clean up user storage directory", slog.String("dir", userStorageDir), slog.Any("error", err))
		}
		fmt.Printf("User %q deleted successfully\n", username)

	case "update":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: selfcloud user update <username> [--password <newpass>] [--email <newemail>]")
			os.Exit(1)
		}
		username := args[1]
		user, err := repos.Users.GetByUsername(ctx, username)
		if err != nil || user == nil {
			fmt.Fprintf(os.Stderr, "User %q not found\n", username)
			os.Exit(1)
		}

		newPassword := ""
		newEmail := ""

		for i := 2; i < len(args); i++ {
			if args[i] == "--password" && i+1 < len(args) {
				newPassword = args[i+1]
				i++
			} else if args[i] == "--email" && i+1 < len(args) {
				newEmail = args[i+1]
				i++
			} else if i == 2 && !strings.HasPrefix(args[i], "--") {
				newPassword = args[i]
			} else if i == 3 && !strings.HasPrefix(args[i], "--") {
				newEmail = args[i]
			}
		}

		if newPassword == "" && newEmail == "" {
			fmt.Fprintln(os.Stderr, "Error: Specify at least --password or --email to update")
			os.Exit(1)
		}

		if newPassword != "" {
			hash, err := domain.HashPassword(newPassword)
			if err != nil {
				logger.Error("Failed to hash password", slog.Any("error", err))
				os.Exit(1)
			}
			user.PasswordHash = hash
		}
		if newEmail != "" {
			user.Email = newEmail
		}

		if err := repos.Users.Update(ctx, user); err != nil {
			logger.Error("Failed to update user", slog.Any("error", err))
			os.Exit(1)
		}

		fmt.Printf("User %q updated successfully\n", username)

	case "set-password":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: selfcloud user set-password <username> [new_password]")
			os.Exit(1)
		}
		username := args[1]
		passArg := ""
		if len(args) >= 3 {
			passArg = args[2]
		}
		user, err := repos.Users.GetByUsername(ctx, username)
		if err != nil || user == nil {
			fmt.Fprintf(os.Stderr, "User %q not found\n", username)
			os.Exit(1)
		}
		newPassword, err := resolvePassword(passArg, fmt.Sprintf("Enter new password for user %q", username))
		if err != nil {
			logger.Error("Password error", slog.Any("error", err))
			os.Exit(1)
		}
		hash, err := domain.HashPassword(newPassword)
		if err != nil {
			logger.Error("Failed to hash password", slog.Any("error", err))
			os.Exit(1)
		}
		user.PasswordHash = hash
		if err := repos.Users.Update(ctx, user); err != nil {
			logger.Error("Failed to update user password", slog.Any("error", err))
			os.Exit(1)
		}
		fmt.Printf("Password for user %q updated successfully\n", username)

	case "set-email":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: selfcloud user set-email <username> <new_email>")
			os.Exit(1)
		}
		username, newEmail := args[1], args[2]
		user, err := repos.Users.GetByUsername(ctx, username)
		if err != nil || user == nil {
			fmt.Fprintf(os.Stderr, "User %q not found\n", username)
			os.Exit(1)
		}
		user.Email = newEmail
		if err := repos.Users.Update(ctx, user); err != nil {
			logger.Error("Failed to update user email", slog.Any("error", err))
			os.Exit(1)
		}
		fmt.Printf("Email for user %q updated successfully to %q\n", username, newEmail)

	default:
		fmt.Fprintf(os.Stderr, "Unknown user subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func cmdServe() {
	cfg := config.LoadConfig()
	logger := makeLogger(cfg)
	slog.SetDefault(logger)

	logger.Info("Starting SelfCloud backend service",
		slog.String("port", cfg.ServerPort),
		slog.String("storage_dir", cfg.StorageDir),
		slog.String("db_driver", cfg.DBDriver),
	)

	sqlDB, err := connectDB(cfg)
	if err != nil {
		logger.Error("Database connection failed", slog.Any("error", err))
		os.Exit(1)
	}
	defer sqlDB.Close()

	if cfg.AutoMigrate {
		logger.Info("Auto-migration enabled: running database migrations")
		if err := db.Migrate(sqlDB); err != nil {
			logger.Error("Auto-migration failed", slog.Any("error", err))
			os.Exit(1)
		}
		logger.Info("Database auto-migration completed successfully")
	}

	repos := repository.NewRepositories(sqlDB)
	transactor := repository.NewTransactor(sqlDB)

	localStorage, err := storage.NewLocalStorage(cfg.StorageDir)
	if err != nil {
		logger.Error("Failed to initialize storage driver", slog.Any("error", err))
		os.Exit(1)
	}

	ocsHandler := ocs.NewOCSHandler(cfg)
	webdavHandler := webdav.NewWebDAVHandler(transactor, repos.Nodes, localStorage, logger).
		WithMaxUploadSize(cfg.MaxUploadSize).
		WithWWWAuthenticateHeader(cfg.WWWAuthenticateHeader())
	authHandler := api.NewAuthHandler(repos.Users, repos.Sessions, logger).
		WithIdentityRepo(repos.Identities).
		WithConfig(cfg)
	filesHandler := api.NewFilesHandler(transactor, repos.Nodes, localStorage, logger).WithMaxUploadSize(cfg.MaxUploadSize)
	sharesHandler := api.NewSharesHandler(transactor, repos.Shares, repos.Nodes, repos.Users, localStorage, logger)
	systemHandler := api.NewSystemHandler(buildVersion, buildCommit, buildDate)
	ssoHandler := api.NewSSOHandler(cfg, transactor, repos.Users, repos.Sessions, repos.Nodes, repos.Identities, logger)
	oauthHandler := api.NewOAuthHandler(repos.OAuth, repos.Users, repos.Sessions, logger)
	graphHandler := api.NewGraphHandler(repos.Users, repos.Nodes)

	mux := http.NewServeMux()

	metricsHandler := promhttp.Handler()
	if cfg.MetricsAuthToken != "" {
		metricsHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := ""
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				token = strings.TrimPrefix(authHeader, "Bearer ")
			} else if qToken := r.URL.Query().Get("token"); qToken != "" {
				token = qToken
			}
			if token != cfg.MetricsAuthToken {
				w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			promhttp.Handler().ServeHTTP(w, r)
		})
	}
	mux.Handle("GET /metrics", metricsHandler)
	mux.HandleFunc("GET /status.php", ocsHandler.StatusHandler)

	// OpenID Connect Discovery & OAuth2 Endpoints for ownCloud Sync Clients (Desktop and Mobile)
	mux.HandleFunc("GET /.well-known/webfinger", oauthHandler.WebFingerHandler)
	mux.HandleFunc("GET /.well-known/openid-configuration", oauthHandler.OpenIDConfigurationHandler)
	mux.HandleFunc("GET /api/v1/auth/oauth2/authorize", oauthHandler.AuthorizeHandler)
	mux.HandleFunc("POST /api/v1/auth/oauth2/authorize", oauthHandler.AuthorizeHandler)
	mux.HandleFunc("GET /index.php/apps/oauth2/authorize", oauthHandler.AuthorizeHandler)
	mux.HandleFunc("POST /index.php/apps/oauth2/authorize", oauthHandler.AuthorizeHandler)
	mux.HandleFunc("GET /apps/oauth2/authorize", oauthHandler.AuthorizeHandler)
	mux.HandleFunc("POST /apps/oauth2/authorize", oauthHandler.AuthorizeHandler)
	mux.HandleFunc("GET /index.php/status.php", ocsHandler.StatusHandler)

	sessionAuthWrapper := middleware.RequireSessionAuthMiddleware(repos.Sessions, repos.Users)
	middleware.ConfigureSecurity(cfg.TrustProxy, cfg.CookieSecure)

	loginRateLimiter := middleware.NewIPRateLimiter(cfg.RateLimitLoginRequests, cfg.RateLimitLoginWindow)
	defer loginRateLimiter.Close()

	mux.Handle("POST /api/v1/auth/oauth2/token", loginRateLimiter.LimitMiddleware(http.HandlerFunc(oauthHandler.TokenHandler)))
	mux.Handle("POST /index.php/apps/oauth2/api/v1/token", loginRateLimiter.LimitMiddleware(http.HandlerFunc(oauthHandler.TokenHandler)))
	mux.Handle("POST /apps/oauth2/api/v1/token", loginRateLimiter.LimitMiddleware(http.HandlerFunc(oauthHandler.TokenHandler)))

	// REST System Info Endpoint
	mux.HandleFunc("GET /api/v1/system/info", systemHandler.SystemInfoHandler)

	// REST Auth & SSO API Endpoints for Web UI
	mux.Handle("POST /api/v1/auth/login", loginRateLimiter.LimitMiddleware(http.HandlerFunc(authHandler.LoginHandler)))
	mux.Handle("POST /api/v1/auth/logout", sessionAuthWrapper(http.HandlerFunc(authHandler.LogoutHandler)))
	mux.Handle("GET /api/v1/auth/me", sessionAuthWrapper(http.HandlerFunc(authHandler.MeHandler)))
	mux.Handle("GET /api/v1/auth/sso/google/login", loginRateLimiter.LimitMiddleware(http.HandlerFunc(ssoHandler.GoogleLoginHandler)))
	mux.Handle("GET /api/v1/auth/sso/google/callback", loginRateLimiter.LimitMiddleware(http.HandlerFunc(ssoHandler.GoogleCallbackHandler)))

	// REST Files API Endpoints for Web UI
	mux.Handle("GET /api/v1/files", sessionAuthWrapper(http.HandlerFunc(filesHandler.GetFilesHandler)))
	mux.Handle("POST /api/v1/files/upload", sessionAuthWrapper(http.HandlerFunc(filesHandler.UploadHandler)))
	mux.Handle("POST /api/v1/files/mkdir", sessionAuthWrapper(http.HandlerFunc(filesHandler.MkdirHandler)))
	mux.Handle("DELETE /api/v1/files", sessionAuthWrapper(http.HandlerFunc(filesHandler.DeleteHandler)))
	mux.Handle("POST /api/v1/files/rename", sessionAuthWrapper(http.HandlerFunc(filesHandler.RenameHandler)))
	mux.Handle("GET /api/v1/files/download", sessionAuthWrapper(http.HandlerFunc(filesHandler.DownloadHandler)))
	mux.Handle("GET /api/v1/files/download-zip", sessionAuthWrapper(http.HandlerFunc(filesHandler.DownloadZipHandler)))
	mux.Handle("POST /api/v1/files/download-zip", sessionAuthWrapper(http.HandlerFunc(filesHandler.DownloadZipHandler)))

	// REST Shares API Endpoints for Web UI
	mux.Handle("POST /api/v1/shares", sessionAuthWrapper(http.HandlerFunc(sharesHandler.CreateShareHandler)))
	mux.Handle("GET /api/v1/shares", sessionAuthWrapper(http.HandlerFunc(sharesHandler.ListSharesHandler)))
	mux.Handle("DELETE /api/v1/shares", sessionAuthWrapper(http.HandlerFunc(sharesHandler.RevokeShareHandler)))

	// Public Unauthenticated Share Endpoints
	mux.HandleFunc("GET /api/v1/public/shares/download", sharesHandler.PublicShareDownloadHandler)
	mux.HandleFunc("GET /api/v1/public/shares", sharesHandler.PublicShareInfoHandler)
	mux.HandleFunc("GET /s/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/download") {
			sharesHandler.PublicShareDownloadHandler(w, r)
			return
		}
		if cfg.ServeStatic {
			uiDistDir := "./web/dist"
			if _, err := os.Stat(uiDistDir); err == nil {
				http.ServeFile(w, r, filepath.Join(uiDistDir, "index.html"))
				return
			}
		}
		sharesHandler.PublicShareInfoHandler(w, r)
	})

	authWrapper := middleware.DualAuthMiddleware(repos.Users, repos.Sessions, cfg, loginRateLimiter)

	// Microsoft Graph API for ownCloud Infinite Scale Spaces discovery (opt-in)
	if cfg.EnableSpaces {
		mux.Handle("GET /graph/v1.0/me/drives", authWrapper(http.HandlerFunc(graphHandler.MeDrivesHandler)))
	}

	mux.Handle("GET /ocs/v1.php/cloud/capabilities", authWrapper(http.HandlerFunc(ocsHandler.CapabilitiesHandler)))
	mux.Handle("GET /ocs/v2.php/cloud/capabilities", authWrapper(http.HandlerFunc(ocsHandler.CapabilitiesHandler)))
	mux.Handle("GET /ocs/v1.php/cloud/user", authWrapper(http.HandlerFunc(ocsHandler.UserHandler)))
	mux.Handle("GET /ocs/v2.php/cloud/user", authWrapper(http.HandlerFunc(ocsHandler.UserHandler)))

	mux.Handle("/remote.php/webdav/", authWrapper(webdavHandler))
	mux.Handle("/remote.php/webdav", authWrapper(webdavHandler))
	mux.Handle("/remote.php/dav/files/", authWrapper(webdavHandler))
	mux.Handle("/dav/files/", authWrapper(webdavHandler))
	mux.Handle("/webdav/", authWrapper(webdavHandler))
	mux.Handle("/webdav", authWrapper(webdavHandler))

	uiDistDir := "./web/dist"
	if cfg.ServeStatic {
		if _, err := os.Stat(uiDistDir); err == nil {
			logger.Info("Static Web UI file serving enabled", slog.String("dir", uiDistDir))
			fileServer := http.FileServer(http.Dir(uiDistDir))
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/" || r.URL.Path == "/login" || strings.HasPrefix(r.URL.Path, "/s/") {
					http.ServeFile(w, r, filepath.Join(uiDistDir, "index.html"))
					return
				}
				fileServer.ServeHTTP(w, r)
			})
		} else {
			logger.Warn("Static Web UI directory not found", slog.String("dir", uiDistDir))
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				logger.Warn("Static Web UI requested but dist directory is missing", slog.String("path", r.URL.Path))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"Static Web UI dist directory not found"}`))
			})
		}
	} else {
		logger.Info("Static Web UI file serving disabled (SERVE_STATIC=false)")
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			logger.Debug("Static Web UI requested while SERVE_STATIC=false", slog.String("path", r.URL.Path))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Static Web UI file serving is disabled on this server"}`))
		})
	}

	// Periodic background cleanup of expired sessions
	reaperCtx, cancelReaper := context.WithCancel(context.Background())
	defer cancelReaper()
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-reaperCtx.Done():
				return
			case <-ticker.C:
				if count, err := repos.Sessions.DeleteExpired(reaperCtx); err == nil && count > 0 {
					logger.Debug("Cleaned up expired sessions", slog.Int64("count", count))
				}
			}
		}
	}()

	rootHandler := middleware.LoggingMiddleware(logger)(middleware.MetricsMiddleware(mux))

	server := &http.Server{
		Addr:              ":" + cfg.ServerPort,
		Handler:           rootHandler,
		ReadHeaderTimeout: 30 * time.Second,
		ReadTimeout:       cfg.ServerReadTimeout,
		WriteTimeout:      cfg.ServerReadTimeout,
		IdleTimeout:       120 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		logger.Info("HTTP server listening", slog.String("addr", server.Addr))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server error", slog.Any("error", err))
		}
	}()

	<-stop
	logger.Info("Shutting down HTTP server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("Graceful shutdown failed", slog.Any("error", err))
	}
	logger.Info("Server stopped cleanly")
}
