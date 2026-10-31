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

	var err error
	switch cmd {
	case "serve":
		err = cmdServe()
	case "migrate":
		err = cmdMigrate()
	case "user":
		err = cmdUser(os.Args[2:])
	case "reindex":
		err = cmdReindex(os.Args[2:])
	case "help", "--help", "-h":
		printUsage()
		return
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
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

func cmdMigrate() error {
	cfg := config.LoadConfig()
	logger := makeLogger(cfg)

	logger.Info("Connecting to database", slog.String("driver", cfg.DBDriver))
	sqlDB, err := connectDB(cfg)
	if err != nil {
		logger.Error("Database connection failed", slog.Any("error", err))
		return err
	}
	defer sqlDB.Close()

	if err := db.Migrate(sqlDB); err != nil {
		logger.Error("Migration failed", slog.Any("error", err))
		return err
	}
	logger.Info("Database migration completed successfully")
	return nil
}

type reindexFlags struct {
	targetUser    string
	dryRun        bool
	removeOrphans bool
}

func parseReindexFlags(args []string) reindexFlags {
	var flags reindexFlags
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--user":
			if i+1 < len(args) {
				i++
				flags.targetUser = args[i]
			}
		case "--dry-run":
			flags.dryRun = true
		case "--remove-orphans":
			flags.removeOrphans = true
		}
	}
	return flags
}

func resolveReindexUsers(ctx context.Context, userRepo repository.UserRepository, targetUser string, logger *slog.Logger) ([]*domain.User, error) {
	if targetUser != "" {
		u, err := userRepo.GetByUsername(ctx, targetUser)
		if err != nil || u == nil {
			logger.Error("User not found", slog.String("username", targetUser))
			return nil, fmt.Errorf("user %q not found", targetUser)
		}
		return []*domain.User{u}, nil
	}
	users, err := userRepo.ListAll(ctx)
	if err != nil {
		logger.Error("Failed to list users", slog.Any("error", err))
		return nil, err
	}
	return users, nil
}

func reindexAllUsers(ctx context.Context, idx *indexer.Reindexer, users []*domain.User, removeOrphans, dryRun bool, logger *slog.Logger) error {
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
		return fmt.Errorf("reindex completed with %d errors", totalErrors)
	}
	return nil
}

func cmdReindex(args []string) error {
	flags := parseReindexFlags(args)

	cfg := config.LoadConfig()
	logger := makeLogger(cfg)

	sqlDB, err := connectDB(cfg)
	if err != nil {
		logger.Error("Database connection failed", slog.Any("error", err))
		return err
	}
	defer sqlDB.Close()

	userRepo := repository.NewUserRepository(sqlDB)
	nodeRepo := repository.NewNodeRepository(sqlDB)
	ctx := context.Background()

	if flags.dryRun {
		logger.Info("Dry-run mode enabled: no changes will be written to the database")
	}

	users, err := resolveReindexUsers(ctx, userRepo, flags.targetUser, logger)
	if err != nil {
		return err
	}

	idx := indexer.New(nodeRepo, cfg.StorageDir, logger, flags.dryRun)
	return reindexAllUsers(ctx, idx, users, flags.removeOrphans, flags.dryRun, logger)
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

func cmdUser(args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: selfcloud user <create|list|delete|update|set-password|set-email> [args...]")
		return errors.New("missing user subcommand")
	}

	cfg := config.LoadConfig()
	logger := makeLogger(cfg)

	sqlDB, err := connectDB(cfg)
	if err != nil {
		logger.Error("Database connection failed", slog.Any("error", err))
		return err
	}
	defer sqlDB.Close()

	repos := repository.NewRepositories(sqlDB)
	transactor := repository.NewTransactor(sqlDB)
	ctx := context.Background()

	switch args[0] {
	case "create":
		return cmdUserCreate(ctx, logger, transactor, args[1:])
	case "list":
		return cmdUserList(ctx, logger, repos)
	case "delete":
		return cmdUserDelete(ctx, logger, transactor, cfg.StorageDir, args[1:])
	case "update":
		return cmdUserUpdate(ctx, logger, repos, args[1:])
	case "set-password":
		return cmdUserSetPassword(ctx, logger, repos, args[1:])
	case "set-email":
		return cmdUserSetEmail(ctx, logger, repos, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown user subcommand: %s\n", args[0])
		return fmt.Errorf("unknown user subcommand: %s", args[0])
	}
}

func upsertUser(ctx context.Context, tx *repository.Repositories, username, hash, password, email string, updateIfExists bool) (*domain.User, string, error) {
	existing, err := tx.Users.GetByUsername(ctx, username)
	if err != nil {
		return nil, "", fmt.Errorf("failed to check existing user: %w", err)
	}
	if existing == nil {
		user := &domain.User{Username: username, PasswordHash: hash, Email: email}
		if err := tx.Users.Create(ctx, user); err != nil {
			return nil, "", fmt.Errorf("failed to create user: %w", err)
		}

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
			return nil, "", fmt.Errorf("failed to initialize user root node: %w", err)
		}
		return user, "created", nil
	}

	if !updateIfExists {
		return nil, "", fmt.Errorf("user %q already exists", username)
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
			return nil, "", fmt.Errorf("failed to update user: %w", err)
		}
		return existing, "updated", nil
	}
	return existing, "unchanged", nil
}

func cmdUserCreate(ctx context.Context, logger *slog.Logger, transactor repository.Transactor, args []string) error {
	updateIfExists := false
	var positional []string
	for _, arg := range args {
		if arg == "--update-if-exists" || arg == "--upsert" {
			updateIfExists = true
		} else {
			positional = append(positional, arg)
		}
	}

	if len(positional) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: selfcloud user create <username> [password] [email] [--update-if-exists]")
		return errors.New("missing username")
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
		return err
	}

	hash, err := domain.HashPassword(password)
	if err != nil {
		logger.Error("Failed to hash password", slog.Any("error", err))
		return err
	}

	var createdUser *domain.User
	var status string

	err = transactor.WithTx(ctx, func(tx *repository.Repositories) error {
		u, st, err := upsertUser(ctx, tx, username, hash, password, email, updateIfExists)
		if err != nil {
			return err
		}
		createdUser = u
		status = st
		return nil
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		logger.Error("Failed to create user", slog.Any("error", err))
		return err
	}

	switch status {
	case "created":
		fmt.Printf("User %q created successfully (id=%d)\n", username, createdUser.ID)
	case "updated":
		fmt.Printf("User %q already exists, updated successfully (id=%d)\n", username, createdUser.ID)
	case "unchanged":
		fmt.Printf("User %q already exists and is up to date (id=%d)\n", username, createdUser.ID)
	}
	return nil
}

func cmdUserList(ctx context.Context, logger *slog.Logger, repos *repository.Repositories) error {
	users, err := repos.Users.ListAll(ctx)
	if err != nil {
		logger.Error("Failed to list users", slog.Any("error", err))
		return err
	}
	fmt.Printf("%-5s  %-30s  %-30s  %s\n", "ID", "Username", "Email", "Created")
	fmt.Printf("%-5s  %-30s  %-30s  %s\n", "---", "--------", "-----", "-------")
	for _, u := range users {
		fmt.Printf("%-5d  %-30s  %-30s  %s\n", u.ID, u.Username, u.Email, u.CreatedAt.Format(time.RFC3339))
	}
	return nil
}

func cmdUserDelete(ctx context.Context, logger *slog.Logger, transactor repository.Transactor, storageDir string, args []string) error {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: selfcloud user delete <username>")
		return errors.New("missing username")
	}
	username := args[0]
	err := transactor.WithTx(ctx, func(tx *repository.Repositories) error {
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
		return err
	}
	// Clean up user's storage directory if it exists
	userStorageDir := filepath.Join(storageDir, username)
	if err := os.RemoveAll(userStorageDir); err != nil {
		logger.Warn("Failed to clean up user storage directory", slog.String("dir", userStorageDir), slog.Any("error", err))
	}
	fmt.Printf("User %q deleted successfully\n", username)
	return nil
}

func parseUserUpdateFlags(args []string) (password, email string) {
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--password" && i+1 < len(args):
			password = args[i+1]
			i++
		case args[i] == "--email" && i+1 < len(args):
			email = args[i+1]
			i++
		case i == 0 && !strings.HasPrefix(args[i], "--"):
			password = args[i]
		case i == 1 && !strings.HasPrefix(args[i], "--"):
			email = args[i]
		}
	}
	return password, email
}

func cmdUserUpdate(ctx context.Context, logger *slog.Logger, repos *repository.Repositories, args []string) error {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: selfcloud user update <username> [--password <newpass>] [--email <newemail>]")
		return errors.New("missing username")
	}
	username := args[0]
	user, err := repos.Users.GetByUsername(ctx, username)
	if err != nil || user == nil {
		fmt.Fprintf(os.Stderr, "User %q not found\n", username)
		return fmt.Errorf("user %q not found", username)
	}

	newPassword, newEmail := parseUserUpdateFlags(args[1:])
	if newPassword == "" && newEmail == "" {
		fmt.Fprintln(os.Stderr, "Error: Specify at least --password or --email to update")
		return errors.New("specify at least --password or --email")
	}

	if newPassword != "" {
		hash, err := domain.HashPassword(newPassword)
		if err != nil {
			logger.Error("Failed to hash password", slog.Any("error", err))
			return err
		}
		user.PasswordHash = hash
	}
	if newEmail != "" {
		user.Email = newEmail
	}

	if err := repos.Users.Update(ctx, user); err != nil {
		logger.Error("Failed to update user", slog.Any("error", err))
		return err
	}

	fmt.Printf("User %q updated successfully\n", username)
	return nil
}

func cmdUserSetPassword(ctx context.Context, logger *slog.Logger, repos *repository.Repositories, args []string) error {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: selfcloud user set-password <username> [new_password]")
		return errors.New("missing username")
	}
	username := args[0]
	passArg := ""
	if len(args) >= 2 {
		passArg = args[1]
	}
	user, err := repos.Users.GetByUsername(ctx, username)
	if err != nil || user == nil {
		fmt.Fprintf(os.Stderr, "User %q not found\n", username)
		return fmt.Errorf("user %q not found", username)
	}
	newPassword, err := resolvePassword(passArg, fmt.Sprintf("Enter new password for user %q", username))
	if err != nil {
		logger.Error("Password error", slog.Any("error", err))
		return err
	}
	hash, err := domain.HashPassword(newPassword)
	if err != nil {
		logger.Error("Failed to hash password", slog.Any("error", err))
		return err
	}
	user.PasswordHash = hash
	if err := repos.Users.Update(ctx, user); err != nil {
		logger.Error("Failed to update user password", slog.Any("error", err))
		return err
	}
	fmt.Printf("Password for user %q updated successfully\n", username)
	return nil
}

func cmdUserSetEmail(ctx context.Context, logger *slog.Logger, repos *repository.Repositories, args []string) error {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: selfcloud user set-email <username> <new_email>")
		return errors.New("missing arguments")
	}
	username, newEmail := args[0], args[1]
	user, err := repos.Users.GetByUsername(ctx, username)
	if err != nil || user == nil {
		fmt.Fprintf(os.Stderr, "User %q not found\n", username)
		return fmt.Errorf("user %q not found", username)
	}
	user.Email = newEmail
	if err := repos.Users.Update(ctx, user); err != nil {
		logger.Error("Failed to update user email", slog.Any("error", err))
		return err
	}
	fmt.Printf("Email for user %q updated successfully to %q\n", username, newEmail)
	return nil
}

type serverDependencies struct {
	cfg              *config.Config
	repos            *repository.Repositories
	transactor       repository.Transactor
	storage          storage.Storage
	loginRateLimiter *middleware.IPRateLimiter
	logger           *slog.Logger
}

func cmdServe() error {
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
		return err
	}
	defer sqlDB.Close()

	if cfg.AutoMigrate {
		logger.Info("Auto-migration enabled: running database migrations")
		if err := db.Migrate(sqlDB); err != nil {
			logger.Error("Auto-migration failed", slog.Any("error", err))
			return err
		}
		logger.Info("Database auto-migration completed successfully")
	}

	repos := repository.NewRepositories(sqlDB)
	transactor := repository.NewTransactor(sqlDB)

	localStorage, err := storage.NewLocalStorage(cfg.StorageDir)
	if err != nil {
		logger.Error("Failed to initialize storage driver", slog.Any("error", err))
		return err
	}

	middleware.ConfigureSecurity(cfg.TrustProxy, cfg.CookieSecure)
	loginRateLimiter := middleware.NewIPRateLimiter(cfg.RateLimitLoginRequests, cfg.RateLimitLoginWindow)
	defer loginRateLimiter.Close()

	deps := &serverDependencies{
		cfg:              cfg,
		repos:            repos,
		transactor:       transactor,
		storage:          localStorage,
		loginRateLimiter: loginRateLimiter,
		logger:           logger,
	}

	mux := buildServerMux(deps)

	reaperCtx, cancelReaper := context.WithCancel(context.Background())
	defer cancelReaper()
	go startSessionReaper(reaperCtx, repos, logger)

	rootHandler := middleware.LoggingMiddleware(logger)(middleware.MetricsMiddleware(mux))
	server := &http.Server{
		Addr:              ":" + cfg.ServerPort,
		Handler:           rootHandler,
		ReadHeaderTimeout: 30 * time.Second,
		ReadTimeout:       cfg.ServerReadTimeout,
		WriteTimeout:      cfg.ServerReadTimeout,
		IdleTimeout:       120 * time.Second,
	}

	return runHTTPServer(server, logger)
}

func buildServerMux(deps *serverDependencies) *http.ServeMux {
	cfg := deps.cfg
	logger := deps.logger

	ocsHandler := ocs.NewOCSHandler(cfg)
	webdavHandler := webdav.NewWebDAVHandler(deps.transactor, deps.repos.Nodes, deps.storage, logger).
		WithMaxUploadSize(cfg.MaxUploadSize).
		WithWWWAuthenticateHeader(cfg.WWWAuthenticateHeader())
	authHandler := api.NewAuthHandler(deps.repos.Users, deps.repos.Sessions, logger).
		WithIdentityRepo(deps.repos.Identities).
		WithConfig(cfg)
	filesHandler := api.NewFilesHandler(deps.transactor, deps.repos.Nodes, deps.storage, logger).WithMaxUploadSize(cfg.MaxUploadSize)
	sharesHandler := api.NewSharesHandler(deps.transactor, deps.repos.Shares, deps.repos.Nodes, deps.repos.Users, deps.storage, logger)
	systemHandler := api.NewSystemHandler(buildVersion, buildCommit, buildDate)
	ssoHandler := api.NewSSOHandler(cfg, deps.transactor, deps.repos.Users, deps.repos.Sessions, deps.repos.Nodes, deps.repos.Identities, logger)
	oauthHandler := api.NewOAuthHandler(deps.repos.OAuth, deps.repos.Users, deps.repos.Sessions, logger)
	graphHandler := api.NewGraphHandler(deps.repos.Users, deps.repos.Nodes)

	mux := http.NewServeMux()
	mux.Handle("GET /metrics", buildMetricsHandler(cfg))

	registerDiscoveryRoutes(mux, ocsHandler, oauthHandler)
	registerAPIRoutes(mux, deps, authHandler, filesHandler, sharesHandler, ssoHandler, systemHandler, oauthHandler)
	registerWebDAVRoutes(mux, deps, webdavHandler, ocsHandler, graphHandler)
	registerStaticRoutes(mux, cfg, logger)

	return mux
}

func buildMetricsHandler(cfg *config.Config) http.Handler {
	if cfg.MetricsAuthToken == "" {
		return promhttp.Handler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ""
		authHeader := r.Header.Get("Authorization")
		if after, ok := strings.CutPrefix(authHeader, "Bearer "); ok {
			token = after
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

func registerDiscoveryRoutes(mux *http.ServeMux, ocsHandler *ocs.OCSHandler, oauthHandler *api.OAuthHandler) {
	mux.HandleFunc("GET /status.php", ocsHandler.StatusHandler)
	mux.HandleFunc("GET /index.php/status.php", ocsHandler.StatusHandler)
	mux.HandleFunc("GET /.well-known/webfinger", oauthHandler.WebFingerHandler)
	mux.HandleFunc("GET /.well-known/openid-configuration", oauthHandler.OpenIDConfigurationHandler)
	mux.HandleFunc("GET /api/v1/auth/oauth2/authorize", oauthHandler.AuthorizeHandler)
	mux.HandleFunc("POST /api/v1/auth/oauth2/authorize", oauthHandler.AuthorizeHandler)
	mux.HandleFunc("GET /index.php/apps/oauth2/authorize", oauthHandler.AuthorizeHandler)
	mux.HandleFunc("POST /index.php/apps/oauth2/authorize", oauthHandler.AuthorizeHandler)
	mux.HandleFunc("GET /apps/oauth2/authorize", oauthHandler.AuthorizeHandler)
	mux.HandleFunc("POST /apps/oauth2/authorize", oauthHandler.AuthorizeHandler)
}

func registerAPIRoutes(
	mux *http.ServeMux,
	deps *serverDependencies,
	authHandler *api.AuthHandler,
	filesHandler *api.FilesHandler,
	sharesHandler *api.SharesHandler,
	ssoHandler *api.SSOHandler,
	systemHandler *api.SystemHandler,
	oauthHandler *api.OAuthHandler,
) {
	sessionAuth := middleware.RequireSessionAuthMiddleware(deps.repos.Sessions, deps.repos.Users)

	// OAuth Token Endpoints
	mux.Handle("POST /api/v1/auth/oauth2/token", deps.loginRateLimiter.LimitMiddleware(http.HandlerFunc(oauthHandler.TokenHandler)))
	mux.Handle("POST /index.php/apps/oauth2/api/v1/token", deps.loginRateLimiter.LimitMiddleware(http.HandlerFunc(oauthHandler.TokenHandler)))
	mux.Handle("POST /apps/oauth2/api/v1/token", deps.loginRateLimiter.LimitMiddleware(http.HandlerFunc(oauthHandler.TokenHandler)))

	// System info
	mux.HandleFunc("GET /api/v1/system/info", systemHandler.SystemInfoHandler)

	// Auth & SSO
	mux.Handle("POST /api/v1/auth/login", deps.loginRateLimiter.LimitMiddleware(http.HandlerFunc(authHandler.LoginHandler)))
	mux.Handle("POST /api/v1/auth/logout", sessionAuth(http.HandlerFunc(authHandler.LogoutHandler)))
	mux.Handle("GET /api/v1/auth/me", sessionAuth(http.HandlerFunc(authHandler.MeHandler)))
	mux.Handle("GET /api/v1/auth/sso/google/login", deps.loginRateLimiter.LimitMiddleware(http.HandlerFunc(ssoHandler.GoogleLoginHandler)))
	mux.Handle("GET /api/v1/auth/sso/google/callback", deps.loginRateLimiter.LimitMiddleware(http.HandlerFunc(ssoHandler.GoogleCallbackHandler)))

	// Files
	mux.Handle("GET /api/v1/files", sessionAuth(http.HandlerFunc(filesHandler.GetFilesHandler)))
	mux.Handle("POST /api/v1/files/upload", sessionAuth(http.HandlerFunc(filesHandler.UploadHandler)))
	mux.Handle("POST /api/v1/files/mkdir", sessionAuth(http.HandlerFunc(filesHandler.MkdirHandler)))
	mux.Handle("DELETE /api/v1/files", sessionAuth(http.HandlerFunc(filesHandler.DeleteHandler)))
	mux.Handle("POST /api/v1/files/rename", sessionAuth(http.HandlerFunc(filesHandler.RenameHandler)))
	mux.Handle("GET /api/v1/files/download", sessionAuth(http.HandlerFunc(filesHandler.DownloadHandler)))
	mux.Handle("GET /api/v1/files/download-zip", sessionAuth(http.HandlerFunc(filesHandler.DownloadZipHandler)))
	mux.Handle("POST /api/v1/files/download-zip", sessionAuth(http.HandlerFunc(filesHandler.DownloadZipHandler)))

	// Shares
	mux.Handle("POST /api/v1/shares", sessionAuth(http.HandlerFunc(sharesHandler.CreateShareHandler)))
	mux.Handle("GET /api/v1/shares", sessionAuth(http.HandlerFunc(sharesHandler.ListSharesHandler)))
	mux.Handle("DELETE /api/v1/shares", sessionAuth(http.HandlerFunc(sharesHandler.RevokeShareHandler)))
	mux.HandleFunc("GET /api/v1/public/shares/download", sharesHandler.PublicShareDownloadHandler)
	mux.HandleFunc("GET /api/v1/public/shares", sharesHandler.PublicShareInfoHandler)
	mux.HandleFunc("GET /s/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/download") {
			sharesHandler.PublicShareDownloadHandler(w, r)
			return
		}
		if deps.cfg.ServeStatic {
			uiDistDir := "./web/dist"
			if _, err := os.Stat(uiDistDir); err == nil {
				http.ServeFile(w, r, filepath.Join(uiDistDir, "index.html"))
				return
			}
		}
		sharesHandler.PublicShareInfoHandler(w, r)
	})
}

func registerWebDAVRoutes(
	mux *http.ServeMux,
	deps *serverDependencies,
	webdavHandler *webdav.WebDAVHandler,
	ocsHandler *ocs.OCSHandler,
	graphHandler *api.GraphHandler,
) {
	authWrapper := middleware.DualAuthMiddleware(deps.repos.Users, deps.repos.Sessions, deps.cfg, deps.loginRateLimiter)

	if deps.cfg.EnableSpaces {
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
}

func registerStaticRoutes(mux *http.ServeMux, cfg *config.Config, logger *slog.Logger) {
	uiDistDir := "./web/dist"
	if !cfg.ServeStatic {
		logger.Info("Static Web UI file serving disabled (SERVE_STATIC=false)")
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			logger.Debug("Static Web UI requested while SERVE_STATIC=false", slog.String("path", r.URL.Path))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Static Web UI file serving is disabled on this server"}`))
		})
		return
	}

	if _, err := os.Stat(uiDistDir); err != nil {
		logger.Warn("Static Web UI directory not found", slog.String("dir", uiDistDir))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			logger.Warn("Static Web UI requested but dist directory is missing", slog.String("path", r.URL.Path))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Static Web UI dist directory not found"}`))
		})
		return
	}

	logger.Info("Static Web UI file serving enabled", slog.String("dir", uiDistDir))
	fileServer := http.FileServer(http.Dir(uiDistDir))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/login" || strings.HasPrefix(r.URL.Path, "/s/") {
			http.ServeFile(w, r, filepath.Join(uiDistDir, "index.html"))
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func startSessionReaper(ctx context.Context, repos *repository.Repositories, logger *slog.Logger) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if count, err := repos.Sessions.DeleteExpired(ctx); err == nil && count > 0 {
				logger.Debug("Cleaned up expired sessions", slog.Int64("count", count))
			}
		}
	}
}

func runHTTPServer(server *http.Server, logger *slog.Logger) error {
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
	return nil
}
