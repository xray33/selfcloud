# AGENTS.md - AI Agent Guidelines for SelfCloud Deployment

## 1. Subproject Overview
`deploy/` contains the production Ansible deployment automation for SelfCloud targeting modern LTS Ubuntu Linux servers.
It manages container installation, systemd unit generation, host Nginx reverse proxy with Let's Encrypt SSL, and initial user provisioning.

## 2. Directory Layout
```
deploy/
├── AGENTS.md                           # AI Agent guidelines (this file)
├── requirements.txt                    # Python requirements for Ansible controller
├── playbook.yml                        # Main deployment playbook
├── inventories.local/                  # Private local inventories (ignored by git)
│   └── sample/                         # Sample template inventory (tracked in git)
│       ├── inventory.ini               # Host definitions & connection variables
│       └── group_vars/
│           └── all.yml                 # Overrides (domain, ports, DB passwords, users)
└── roles/
    ├── docker/                         # Installs Docker CE & creates selfcloud-net network
    ├── mariadb/                        # Optional: containerized MariaDB via systemd unit
    ├── selfcloud/                      # Backend & Web UI containers via systemd units
    └── nginx/                          # Host Nginx, SSL Certbot, and WebDAV proxy
```

## 3. Inventory Pattern (`inventories.local`)
Like Kubespray, each cluster or VPS deployment is completely isolated in its own directory:
- To create a new deployment environment:
  ```bash
  cp -r deploy/inventories.local/sample deploy/inventories.local/my-vps
  ```
- Edit `deploy/inventories.local/my-vps/inventory.ini` and `group_vars/all.yml`.
- Run playbook:
  ```bash
  ansible-playbook -i deploy/inventories.local/my-vps/inventory.ini deploy/playbook.yml
  # or via Makefile
  make deploy-ansible INVENTORY=my-vps
  ```

## 4. Key Architecture Principles
1. **Systemd over individual `docker run`**:
   - `selfcloud-backend.service`: Runs Go backend container with `SERVE_STATIC=false`.
   - `selfcloud-web.service`: Runs Nginx + React UI container publishing to `127.0.0.1:{{ selfcloud_internal_port }}`.
   - `selfcloud-mariadb.service`: Optional local MariaDB container running on `selfcloud-net`.
2. **Host Nginx**:
   - Runs directly on host Ubuntu to allow coexistence with other web apps and Let's Encrypt Certbot validators.
   - Configured with `client_max_body_size 0` and `proxy_request_buffering off` for large WebDAV file streams.
3. **Git-Aware Image Versioning & Idempotent Deployments**:
   - Images are tagged with the short Git commit hash (and `-dirty` if uncommitted changes exist, e.g. `selfcloud-backend:6ee7720`).
   - `make docker-export` builds and archives images to `/tmp/selfcloud-build/selfcloud-<tag>.tar.gz` with deterministic timestamps (`gzip -n`).
   - If git is on a clean commit and the archive already exists, `make docker-export` skips rebuilding.
   - Ansible copies `/tmp/selfcloud-build/selfcloud-<tag>.tar.gz` to the VPS. If the commit has not changed, Ansible's SHA256 checksum match reports `OK` and avoids unnecessary restarts.
   - On new commits or code changes, Ansible uploads the new archive, updates systemd unit image tags, and triggers service restarts.
4. **User Provisioning**:
   - `db.Migrate()` does NOT seed any hardcoded admin users.
   - Initial users defined in `selfcloud_users` are created idempotently via `docker exec selfcloud-backend /app/selfcloud user create ... --update-if-exists`.
5. **Reverse Proxy Header Propagation & Cookie Security**:
   - Host Nginx sends `X-Forwarded-Proto $scheme` and `X-Forwarded-For`.
   - `selfcloud-web` container preserves incoming `X-Forwarded-Proto` when proxying to `selfcloud-backend`.
   - `selfcloud.env.j2` automatically sets `COOKIE_SECURE=true` when `enable_ssl: true` and defaults `TRUST_PROXY=true` for reverse-proxied production deployments.
