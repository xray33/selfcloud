# AGENTS.md - AI Agent Guidelines for SelfCloud Web UI

## 1. Subproject Overview
`web/` contains the standalone React 19 + TypeScript frontend application for SelfCloud.
It provides a modern, fast file management Web UI (file browsing, breadcrumb navigation, drag-and-drop file upload, folder creation, inline rename/delete, public tokenized sharing, and dynamic ZIP archive streaming).

## 2. Tech Stack & Dependencies
- **Framework**: React 19 + TypeScript 7
- **Build Tool**: Vite 8
- **Styling**: Tailwind CSS v3
- **Icons**: Lucide React
- **HTTP Client**: Native `fetch` wrapper in `src/api/client.ts` with credentials (`credentials: 'include'` for HttpOnly session cookies)

## 3. Directory Layout
```
web/
├── Makefile                # Subproject targets (install, dev, build, clean)
├── package.json            # npm dependencies & build scripts
├── vite.config.ts          # Vite config with API dev proxy to localhost:8080
├── tsconfig.json           # TypeScript compilation config
├── tailwind.config.js      # Tailwind theme tokens & color palette
├── nginx.conf              # Nginx reverse proxy configuration for Web UI container
├── index.html              # Single Page Application entry HTML
└── src/
    ├── main.tsx            # React DOM mounting entry point
    ├── App.tsx             # Root app router & auth initialization
    ├── index.css           # Tailwind base & global styles
    ├── types/              # TypeScript interfaces (User, NodeItem, Share)
    ├── api/                # API client modules (auth.ts, files.ts, shares.ts)
    ├── components/         # UI Components (Navbar, FileList, ActionToolbar, Modals)
    └── pages/              # Top-level pages (LoginPage, FilesPage, PublicSharePage)
```

## 4. Key API Integration Endpoints
- **Auth**: `POST /api/v1/auth/login`, `POST /api/v1/auth/logout`, `GET /api/v1/auth/me`
- **Files**: `GET /api/v1/files?path=...`, `POST /api/v1/files/upload`, `POST /api/v1/files/mkdir`, `DELETE /api/v1/files?path=...`, `POST /api/v1/files/rename`, `GET /api/v1/files/download`, `GET /api/v1/files/download-zip`
- **Shares**: `POST /api/v1/shares`, `GET /api/v1/shares`, `DELETE /api/v1/shares?token=...`, `GET /api/v1/public/shares?token=...`, `GET /api/v1/public/shares/download?token=...`
- **SSO**: `GET /api/v1/auth/sso/google/login`, `GET /api/v1/auth/sso/google/callback`

## 5. Development & Build Commands
- `make install`: Installs Node dependencies (`npm install`).
- `make dev`: Starts Vite dev server with proxy to backend (`http://localhost:5173`).
- `make build`: Typechecks and compiles production build to `web/dist`.
- `make clean`: Deletes `web/dist` and node modules cache.

## 6. Coding Guidelines & Practices
1. **API Clients**: Keep fetch calls inside `src/api/` modules. Always handle non-OK responses with typed errors.
2. **State & URL Syncing**: Sync active directory paths with `window.location.search` (`?path=...`) and handle `popstate` events for browser Back/Forward navigation.
3. **Styling**: Use predefined Tailwind utility classes with consistent dark mode color tokens (`slate-950`, `slate-900`, `brand-600`, `purple-500`).
4. **Icons**: Use Lucide React icons (`<Cloud />`, `<Folder />`, `<Share2 />`, `<Link2 />`).
