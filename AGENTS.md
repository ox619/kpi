# AGENTS.md

DooTask KPI 绩效考核系统 — a single-repo app that ships as a DooTask platform plugin.

## Architecture

- **Frontend**: Next.js 15 (App Router) + React 19 + Tailwind v4, at repo root. Path alias `@/*` → repo root. All pages are client components under `app/`; shared state lives in `lib/*-context.tsx` (auth, dootask, theme, notifications, etc.).
- **Backend**: Go 1.23 + Gin + GORM/SQLite in `server/`. Entry: `server/main.go`, routes: `server/routes/routes.go`, handlers: `server/handlers/`.
- **Plugin packaging**: `dootask-plugin/` holds the DooTask App Store manifest (`config.yml`), menu/hooks config and nginx routing (`version/`), the CLI API spec (`openapi.yaml`), and the AI knowledge base (`ai-kb/`).
- There is a single Go module (`server/go.mod`, module `dootask-kpi-server`) and a single npm package. No monorepo tooling.

## Commands

```bash
# Frontend only (port 3000)
npm run dev

# Frontend + backend together (requires bash + air: go install github.com/air-verse/air@latest)
npm run dev:all          # runs scripts/dev.sh; waits for backend /health on :8080

# Backend only (port 8080) — must be run FROM server/ (relative SQLite path db/kpi.db)
cd server && CGO_ENABLED=1 go run main.go

npm run build            # next build
npm run lint             # next lint (eslint-config-next; no typecheck or test scripts exist)
```

- `CGO_ENABLED=1` is required for any `go build`/`go run` (mattn/go-sqlite3).
- SQLite DB path is relative (`db/kpi.db`); always start the backend with CWD = `server/`.
- There are **no tests** anywhere. Verify changes with `npm run lint` and `npm run build`; for Go changes also compile via `go build ./...` in `server/`.
- `npm run dev:all` uses bash and air — on Windows use Git Bash/WSL, or run the two processes separately.

## Deployment / Docker

- `Dockerfile` builds backend and frontend into one image; the container runs both (`start.sh`: Go server + `node server.js`). Frontend served standalone (`NEXT_OUTPUT_MODE=standalone`).
- In Docker, the app lives under base path `/apps/kpi`: `NEXT_PUBLIC_BASE_PATH=/apps/kpi` and `NEXT_PUBLIC_API_URL=/apps/kpi/api` (nginx proxies `/apps/kpi/api` → `kpi:8080/api`, `/apps/kpi` → `kpi:3000`). Local dev has no `NEXT_PUBLIC_API_URL`, so `lib/api.ts` derives it from the page host: `http://<host>:8080/api` (falls back to `http://localhost:8080/api` during SSR), which keeps LAN access working. The derived port can be overridden at build time with `NEXT_PUBLIC_API_PORT` (used by the Linux direct-connect package, see `deploy/linux/README.md`).
- `SYSTEM_MODE=integrated` (set in the Docker image) switches behavior: seed "test users" are NOT created (only templates/rules), and some settings endpoints change. Local dev runs in standalone mode with full test data.
- The `theme` query param is forwarded to the server as an `x-theme` header by `middleware.ts` (iframe theme sync with DooTask).

## Backend conventions

- All API routes are under `/api` (see `server/routes/routes.go`); JWT auth middleware on the protected group.
- Frontend sends two auth headers: `Authorization: Bearer <jwt>` and `DooTaskAuth: <dootask token>` (both auto-attached in `lib/api.ts` interceptors). The backend uses the DooTask token to call the DooTask API (`server/utils/dootask.go`, bot messages, user sync) — never strip it.
- Auto-migration via GORM `AutoMigrate` in `server/models/database.go`; no migration files. Adding a model field = update struct, migration runs on next start. New DB files/`server/db/` are gitignored.
- Test/seed data is created at startup (`models.CreateTestData`) when tables are empty.

## Release process (from PUBLISHING_GUIDE.md + .github/workflows/release.yml)

1. Push a tag in `X.Y.Z` format — **no `v` prefix** (gitflow). Tag push triggers CI.
2. CI builds/pushes a multi-arch Docker image (`dootask/kpi:<tag>`, plus `latest` on default branch) and publishes `dootask-plugin/` to the DooTask App Store (appid `kpi`). During packaging, `dootask-plugin/version/` is renamed to the tag name inside the tarball.
3. Required repo secrets: `DOOTASK_USERNAME`, `DOOTASK_PASSWORD`, `DOCKER_USERNAME`, `DOCKER_PASSWORD`.

## Keep in sync when changing the API

- `dootask-plugin/openapi.yaml` mirrors the Gin routes and powers `doo app call kpi <operationId>`. If you add/change an API endpoint, update the matching path/operationId there (internal endpoints like hooks/SSE/download/backup are marked `x-doo-cli: false`).
- `dootask-plugin/version/config.yml` defines DooTask hooks (`/api/hooks/user/onboard|offboard`) and requires DooTask > 1.4.67 — don't break those endpoints.
- `dootask-plugin/ai-kb/` is end-user documentation shipped with the plugin (YAML frontmatter with `id`, `type`, `last_verified`, etc.). Update it when user-facing behavior changes.

## Frontend conventions

- All API types and clients are centralized in `lib/api.ts`; use its axios instance (token handling included), not bare axios/fetch.
- Local persistence must go through `lib/storage.ts` (keys get the `__dootask_kpi__` prefix) — don't touch `localStorage` directly.
- DooTask integration is gated on `isMicroApp()` from `@dootask/tools` (`lib/dootask-context.tsx`); the app must keep working standalone (direct JWT login) as well as embedded.
- UI: Radix UI + shadcn-style components in `components/ui/`; Prettier config exists (`.prettierrc`: no semicolons, double quotes) but prettier is not a dependency — match existing style manually.

## User-facing docs

- Roles: `employee` < `manager` < `hr`. Evaluation flow: create → self-review → manager review → (optional invited scoring) → HR review → employee confirm → (optional objection). Final scores are computed from configurable performance-rule weights (`performance-rules`).
- Standalone mode seeds accounts (all password `123456`): HR `sunba@company.com`; managers `zhangsan@company.com`, `zhaoliu@company.com`, `zhoujiu@company.com`; employees `lisi@company.com`, `wangwu@company.com`, `qianqi@company.com` — useful for manual verification.
