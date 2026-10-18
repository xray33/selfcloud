# AGENTS.md - Guide for AI Agents working on SelfCloud

## 1. Project Overview
SelfCloud is a lightweight, 100% ownCloud-compatible WebDAV backend service and Web UI written in Go (Go 1.26) and React 19.
It allows ownCloud desktop sync clients, mobile apps, and modern web browsers to log in, browse files, upload, download, move, copy, share, and delete files seamlessly.

> [!NOTE]
> For AI Agent instructions specific to the React Web UI frontend, see [`web/AGENTS.md`](web/AGENTS.md).
> For AI Agent instructions specific to production Ansible deployment, see [`deploy/AGENTS.md`](deploy/AGENTS.md).

## 2. Tech Stack & Dependencies
- **Backend Language**: Go 1.26 (using standard library `net/http` native routing).
- **Frontend Subproject**: React 19 + TypeScript + Vite + Tailwind CSS in [`web/`](web/AGENTS.md).
- **Metadata Database**: MariaDB / MySQL compatible (DB-agnostic design using `doug-martin/goqu/v9` SQL builder; versioned schema migrations via `schema_migrations` table).
- **File Storage**: Local filesystem (`./data` or configured path).
- **Authentication**: Configurable Dual Auth via `AUTH_METHODS` (`basic,oauth`, `basic`, or `oauth`): HTTP Basic Auth for legacy sync clients; OAuth2 authorization code with PKCE & Bearer tokens for ownCloud desktop sync client v7+ and REST API; HttpOnly session cookies; Google OAuth2 SSO; per-IP sliding window brute-force rate limiting and CSRF origin validation.
- **Logging & Audit**: Standard `log/slog` structured logging with cryptographically random request correlation IDs (`request_id`, `int_request_id`), reverse proxy IP resolution guarded by `TRUST_PROXY`, and context-carried `[AUDIT]` event logging.
- **Security & Storage**: Symlink escape detection via `filepath.EvalSymlinks`, application-level path length validation (max 700 chars for InnoDB index constraints), CSRF validation on mutating cookie requests, and RFC 4918 WebDAV compliance (`Overwrite`, `Depth`).
- **Metrics**: Prometheus HTTP & WebDAV metrics exposed at `/metrics` (supports optional `METRICS_AUTH_TOKEN` protection).

## 3. Directory Layout
```
├── .golangci.yml           # Linter configuration
├── AGENTS.md               # AI Agent Guidelines (this file)
├── Dockerfile              # Multi-stage Docker build
├── Makefile                # Build, test, lint, and UI targets
├── docker-compose.yml      # MariaDB setup for functional testing
├── cmd/
│   └── selfcloud/
│       └── main.go         # Application CLI entry point (serve, migrate, user, reindex)
├── internal/
│   ├── config/             # Config loader (env vars & defaults)
│   ├── db/                 # DB connector & schema auto-migrations
│   ├── domain/             # Core models (User, Node, Session, Share, Identity)
│   ├── repository/         # Data access interfaces & goqu MariaDB implementation
│   ├── storage/            # Local FS blob storage driver
│   ├── middleware/         # Auth, SessionAuth, slog access logs, Audit logging, Prometheus metrics
│   ├── metrics/            # Prometheus counters & histograms
│   ├── audit/              # Context-carried audit logger
│   ├── indexer/            # Filesystem-to-DB reconciliation engine
│   ├── api/                # REST API handlers for Auth, Files, Shares, and Google SSO
│   ├── ocs/                # ownCloud status & capabilities handlers
│   └── webdav/             # WebDAV method handlers (PROPFIND, PUT, GET, DELETE, MKCOL, MOVE, etc.)
└── web/                    # React 19 frontend subproject (see web/AGENTS.md)
```

## 4. Key Endpoints & API Compliance
- `GET /.well-known/openid-configuration`: OpenID Connect discovery for desktop sync client v7+.
- `GET /api/v1/auth/oauth2/authorize`, `POST /api/v1/auth/oauth2/token`: OAuth2 PKCE login & token issuance for sync clients.
- `GET /graph/v1.0/me/drives`: Microsoft Graph / oCIS personal drive Spaces discovery for sync client v7+.
- `GET /status.php`: Returns ownCloud version JSON (`"installed": true`, `"version": "10.11.0.3"`).
- `GET /ocs/v1.php/cloud/capabilities`, `/ocs/v2.php/cloud/capabilities`: Discovery API for desktop sync client (conditionally advertises `"spaces"` based on `AUTH_METHODS`).
- `/remote.php/webdav/*`, `/remote.php/dav/files/{username}/*`: WebDAV endpoints supporting `OPTIONS`, `PROPFIND`, `GET`, `HEAD`, `PUT`, `DELETE`, `MKCOL`, `MOVE`, `COPY`, `PROPPATCH`.
- `/api/v1/auth/*`: REST authentication (`POST /login`, `POST /logout`, `GET /me`).
- `/api/v1/auth/sso/google/*`: Google OAuth2 authorization code flow & account linking.
- `/api/v1/files/*`: File explorer, upload, mkdir, delete, rename, download, dynamic ZIP streaming.
- `/api/v1/shares/*` & `/s/{token}`: Tokenized public share links & unauthenticated downloads.
- `/`: Single-binary serving of compiled React Web UI static assets (`web/dist`).

## 5. Development & Testing Commands
- **CI & Pre-Commit Check**: Run `make ci` (runs Go lint, Go tests, UI lint, UI build, and backend build).
- **Linting**: Run `make lint` (Go linter) and `make ui-lint` / `make ui-build` (React typecheck & build).
- **Tests**: Run `make test` (runs unit & functional tests against MariaDB configured via `DB_DSN`).
- **Build**: Run `make build` (compiles `bin/selfcloud`) and `make ui-build` (builds `web/dist`).
- **Docker Targets**:
  - `make docker-build` (builds both backend and frontend images)
  - `make docker-export` (builds and exports Docker images to `/tmp/selfcloud-images.tar.gz`)
  - `make docker-up` (starts full stack in Docker using repository ./data)
  - `make docker-down` (stops full stack in Docker)
  - `make docker-logs` (streams logs from all Docker containers)
  - `make deploy-ansible [INVENTORY=<name>]` (exports images and deploys stack to remote VPS using Ansible)
- **UI Commands**: Run `make ui-dev` (starts Vite dev server) or `make ui-install` (installs node modules).
- **Local DB**: Run `make up-db` to start MariaDB container.
- **CLI Commands**:
  - `./bin/selfcloud serve` (starts HTTP server)
  - `./bin/selfcloud migrate` (runs DB schema migrations)
  - `./bin/selfcloud user <create|list|delete|update|set-password|set-email>` (user management)
  - `./bin/selfcloud reindex [--user <name>] [--remove-orphans]` (reconciles disk & DB)

## 6. Coding Guidelines & Practices
1. **Never guess API signatures**: Check standard library or existing domain models before modifying signatures.
2. **Logging & Audit**: Use `slog` for structured logging. Always include request context / request ID when handling HTTP calls and record audit events via `audit.Record(ctx, ...)` before emitting access logs.
3. **Database**: Use `goqu` for building SQL queries to keep DB operations clean and vendor-agnostic. Always update `name`, `path`, `parent_path`, and `updated_at` on node updates/moves. Apply schema changes via versioned migrations in `internal/db/db.go`.
4. **Security**: Validate paths in storage driver to prevent directory traversal (`..`) and symlink escapes (`checkSymlinkEscape`). Enforce path length boundaries (`pathutil.ValidatePathLength`) to respect database index limits. Verify CSRF origins on mutating cookie endpoints and guard against reverse proxy header spoofing via `TRUST_PROXY`.
5. **Frontend Reference**: See [`web/AGENTS.md`](web/AGENTS.md) when making changes inside the `web/` subproject.
