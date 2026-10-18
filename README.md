# SelfCloud

[![Go Version](https://img.shields.io/badge/Go-1.26-00ADD8?style=flat&logo=go)](https://golang.org/)
[![React Version](https://img.shields.io/badge/React-19.0-61DAFB?style=flat&logo=react)](https://react.dev/)
[![CI](https://github.com/xray33/selfcloud/actions/workflows/ci.yml/badge.svg)](https://github.com/xray33/selfcloud/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/xray33/selfcloud/branch/main/graph/badge.svg)](https://codecov.io/gh/xray33/selfcloud)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**SelfCloud** is a lightweight, high-performance, 100% ownCloud-compatible WebDAV backend server and modern Web UI written in Go and React.

It enables official **ownCloud Desktop Sync Clients**, **Mobile Apps**, and **Web Browsers** to seamlessly log in, browse, upload, download, move, rename, delete, share, and stream files with zero disk overhead.

---

## ✨ Features

- **100% ownCloud WebDAV & Sync Client Support**: Fully compatible with official ownCloud Desktop Sync Clients (v2.x–v6.x with Basic Auth or OAuth2 PKCE), mobile apps (Android & iOS), and WebDAV tools (`/status.php`, `/.well-known/openid-configuration`, `/.well-known/webfinger`, `/ocs/v1.php/*`, `/remote.php/webdav/*`).
  > [!NOTE]
  > Desktop Client 7.x dropped support for ownCloud 10 servers and communicates exclusively with ownCloud Infinite Scale (oCIS). For desktop synchronization with SelfCloud, use the official **ownCloud Desktop Client 6.x series** (e.g. 6.0.0).
- **Modern React 19 Web UI**: Fast, responsive Single Page Application built with Vite, TypeScript, and Tailwind CSS.
  - Interactive file explorer with breadcrumb navigation and `..` parent directory row.
  - Client-side column sorting by **Name**, **Date Modified**, and **Size** (ascending/descending).
  - Drag-and-drop file upload zone and modal folder creation.
  - Dedicated **Shared Links** manager modal and visual **`Shared`** badges on files/folders.
- **On-The-Fly ZIP Archive Streaming**: Downloads directories as `.zip` archives with **0 temporary disk files** by piping `archive/zip` directly into HTTP response streams.
- **Google OAuth2 SSO**: Integrated multi-identity authentication allowing users to log in via local password or Google SSO (`user_identities` schema).
- **Public Share Links**: Generates unauthenticated public download links (`/s/{token}`) with a standalone download landing page.
- **Context-Carried Audit Logging**: Emits structured `[AUDIT]` events *before* generic HTTP access logs, recording user actions, IP addresses, and request correlation IDs (`request_id`, `int_request_id`).
- **Filesystem-to-DB Reindexer CLI**: Includes `./bin/selfcloud reindex` to automatically reconcile database metadata with files on disk and clean up orphaned entries.
- **Prometheus Metrics**: Exposes HTTP and WebDAV counters/histograms at `/metrics`.
- **Single-Binary Packaging**: Serves compiled static frontend assets (`web/dist`) directly from the Go executable.

---

## 🏗 Architecture & Tech Stack

- **Backend**: Go 1.26 (Native `net/http` routing)
- **Frontend**: React 19, TypeScript 5, Vite 6, Tailwind CSS v3, Lucide React (`web/`)
- **Metadata Database**: MariaDB / MySQL (Vendor-agnostic SQL generation via `doug-martin/goqu/v9`)
- **File Storage**: Local filesystem (`./data` or configured path)
- **Logging**: Standard `log/slog` structured logging

```
├── cmd/selfcloud/main.go       # Multi-command CLI (serve, migrate, user, reindex)
├── internal/
│   ├── api/                    # REST API Handlers (Auth, Files, Shares, Google SSO)
│   ├── audit/                  # Context-carried audit logger
│   ├── config/                 # Environment configuration loader
│   ├── db/                     # DB connector & schema auto-migrations
│   ├── domain/                 # Core domain models (User, Node, Session, Share, Identity)
│   ├── indexer/                # Filesystem-to-DB reconciliation engine
│   ├── metrics/                # Prometheus counters & histograms
│   ├── middleware/             # Auth, SessionAuth, Audit logging, slog access logs
│   ├── ocs/                    # ownCloud discovery & capability handlers
│   ├── repository/             # Data access interfaces & goqu MariaDB implementation
│   ├── storage/                # Local FS blob storage driver
│   └── webdav/                 # WebDAV protocol handlers (PROPFIND, PUT, GET, MOVE, etc.)
└── web/                        # React 19 frontend subproject (see web/AGENTS.md)
```

---

## 🚀 Running SelfCloud

SelfCloud supports three execution modes depending on your use case:

### Tier 1: Local Binary Development (Fast Iteration)
For active Go backend or React UI development with hot reloading:

```bash
# 1. Start local MariaDB container
make up-db

# 2. Build Web UI assets & Go binary
make ui-build build

# 3. Create an admin user
./bin/selfcloud user create admin admin_pass admin@example.com

# 4. Start Go server (serves static UI at http://localhost:8080)
./bin/selfcloud serve

# (Optional) Start Vite dev server for instant React HMR at http://localhost:5173
make ui-dev
```

---

### Tier 2: Containerized Docker Compose (Local Testing & Home Lab)
Runs the entire 3-container production stack (MariaDB + Go Backend + Nginx Web UI) locally in Docker.

```bash
# 1. (Optional) Customize environment variables
cp .env.example .env

# 2. Build container images and start all services
make docker-up

# 3. Create your user account inside the running backend container
docker exec -it selfcloud-backend /app/selfcloud user create xray33 secret123 xray33@example.com

# 4. View live logs or stop services
make docker-logs
make docker-down
```

> [!TIP]
> **Running Multiple Instances**: You can run multiple isolated SelfCloud stacks on the same host by providing a custom project name and port in `.env`:
> ```bash
> docker compose -p team-cloud -f docker-compose.yml up -d
> ```

---

### Tier 3: Production Linux VPS Deployment (Ansible)
Full automated deployment targeting Ubuntu 24.04/22.04 LTS servers. Manages Docker CE, host Nginx with Let's Encrypt SSL, unprivileged containers under independent systemd units (`selfcloud-backend.service`, `selfcloud-web.service`), and automated user provisioning.

```bash
# 1. Create a dedicated inventory directory for your VPS (ignored by git)
cp -r deploy/inventories.local/sample deploy/inventories.local/my-vps

# 2. Edit host connection & domain settings
# deploy/inventories.local/my-vps/inventory.ini
# deploy/inventories.local/my-vps/group_vars/all.yml

# 3. Run the automated deployment
make deploy-ansible INVENTORY=my-vps
```
For full details, see the [`deploy/AGENTS.md`](deploy/AGENTS.md) deployment guide.

---

## 🛠 Command-Line Interface (CLI)

SelfCloud features a unified subcommand CLI:

```bash
# Start the HTTP/WebDAV server (default command)
./bin/selfcloud serve

# Run database schema migrations
./bin/selfcloud migrate

# Manage user accounts (password can be prompted interactively, passed via SELFCLOUD_USER_PASSWORD, or as argument)
./bin/selfcloud user create <username> [password] [email]
./bin/selfcloud user list
./bin/selfcloud user delete <username>
./bin/selfcloud user update <username> [--password <newpass>] [--email <newemail>]
./bin/selfcloud user set-password <username> [new_password]
./bin/selfcloud user set-email <username> <new_email>

# Reconcile filesystem storage with database metadata
./bin/selfcloud reindex [--user <username>] [--remove-orphans] [--dry-run]
```

---

## ⚙️ Environment Variables

| Variable | Default Value | Description |
| :--- | :--- | :--- |
| `SERVER_PORT` | `8080` | HTTP server listening port |
| `STORAGE_DIR` | `./data` | Local filesystem storage path |
| `DB_DRIVER` | `mysql` | Database driver (`mysql`) |
| `DB_DSN` | `selfcloud:selfcloud_pass@tcp(localhost:3306)/selfcloud?parseTime=true` | MariaDB DSN |
| `LOG_LEVEL` | `info` | Structured logging level (`debug`, `info`, `warn`, `error`) |
| `SERVE_STATIC` | `true` | Serve static Web UI from `./web/dist` (`false` in production Docker) |
| `AUTO_MIGRATE` | `true` | Run DB schema migrations automatically on server startup |
| `MAX_UPLOAD_SIZE_BYTES` | `10737418240` (10GB) | Maximum upload payload size for REST and WebDAV PUT |
| `TRUST_PROXY` | `true` | Trust reverse proxy headers (`X-Forwarded-Proto`, `X-Forwarded-For`, `X-Forwarded-Host`) |
| `COOKIE_SECURE` | `auto` | Cookie `Secure` flag policy (`auto`, `true`, `false`) |
| `SERVER_READ_TIMEOUT` | `30m` | HTTP server read timeout (Slowloris protection) |
| `RATE_LIMIT_LOGIN_REQUESTS` | `15` | Maximum login/SSO/WebDAV failed requests per window per IP (`0` to disable) |
| `RATE_LIMIT_LOGIN_WINDOW` | `1m` | Sliding window duration for login and auth rate limiting |
| `AUTH_METHODS` | `basic,oauth` | Allowed auth schemes: `basic,oauth`, `basic`, or `oauth`. Use `basic` for plain HTTP setups without TLS |
| `METRICS_AUTH_TOKEN` | `""` | Optional Bearer token required to access `/metrics` (`""` for unauthenticated) |
| `SSO_GOOGLE_CLIENT_ID` | `""` | Google OAuth2 Client ID |
| `SSO_GOOGLE_CLIENT_SECRET` | `""` | Google OAuth2 Client Secret |
| `SSO_REDIRECT_BASE_URL` | `http://localhost:8080` | Base URL for OAuth2 callbacks |

---

## 🔌 API & Endpoint Reference

### WebDAV & Desktop Sync Protocol (Sync Clients)
- `GET /.well-known/openid-configuration`: OpenID Connect discovery for ownCloud sync clients (Desktop 2.x–6.x and Android/iOS) OAuth2 PKCE login.
- `GET /.well-known/webfinger`: WebFinger issuer discovery for mobile and desktop clients.
- `GET /api/v1/auth/oauth2/authorize`, `POST /api/v1/auth/oauth2/token`: OAuth2 authorization code with PKCE flow and token issuance.
- `GET /status.php`: ownCloud version check (`10.11.0.3`).
- `GET /ocs/v1.php/cloud/capabilities`, `GET /ocs/v2.php/cloud/capabilities`: ownCloud capabilities (core, files, and oauth2; spaces opt-in via `ENABLE_SPACES`).
- `GET /graph/v1.0/me/drives`: Microsoft Graph personal drive spaces endpoint (available when `ENABLE_SPACES=true`).
- `/remote.php/webdav/*`, `/remote.php/dav/files/{username}/*`: Standard WebDAV (`PROPFIND`, `PUT`, `GET`, `DELETE`, `MKCOL`, `MOVE`, `COPY`). Fully compliant with RFC 4918 (`Overwrite: T/F`, `Depth: 0/infinity`).

### REST API (Web UI)
- **Auth**:
  - `POST /api/v1/auth/login`: Local username/password authentication (rate-limited).
  - `POST /api/v1/auth/logout`: Invalidate session token & clear cookie.
  - `GET /api/v1/auth/me`: Get current authenticated user details.
  - `GET /api/v1/auth/sso/google/login`: Initiate Google OAuth2 flow (rate-limited).
  - `GET /api/v1/auth/sso/google/callback`: Handle Google OAuth2 callback. Account linking for existing users requires an active authenticated session.
- **Files**: `GET /api/v1/files`, `POST /api/v1/files/upload`, `POST /api/v1/files/mkdir`, `DELETE /api/v1/files` (strictly requires `DELETE`), `POST /api/v1/files/rename`, `GET /api/v1/files/download`, `GET /api/v1/files/download-zip`
- **Shares**: `POST /api/v1/shares`, `GET /api/v1/shares`, `DELETE /api/v1/shares?token=...` (strictly requires `DELETE`)
- **Public Shares**: `GET /s/{token}` (UI Landing page), `GET /api/v1/public/shares?token=...`, `GET /api/v1/public/shares/download?token=...`

---

## 📜 License

Distributed under the MIT License. See `LICENSE` for details.

---

## ⚖️ Disclaimer

ownCloud is a registered trademark of ownCloud GmbH / Kiteworks. **SelfCloud** is an independent, clean-room open-source implementation and is not affiliated with, endorsed by, or sponsored by ownCloud GmbH or Kiteworks.

