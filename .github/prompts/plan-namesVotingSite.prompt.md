# Plan: Names voting site (names.thomasjbarlow.com)

Anonymous name-voting site (👍/👎 + submit) with admin moderation, built to match BarlowFabrication's CI/CD: Go + Gin + GORM + Postgres backend, deployed as Docker images to the existing Docker Swarm node `mediapc` via the self-hosted GitHub Actions runner, fronted by the existing Traefik + Cloudflare cert resolver. Frontend is intentionally minimal — HTMX over Go `html/template` — so there is no Node build step and the production image is a single small Go binary + static assets.

## Steps

### Phase 1 — Repo scaffold
1. `go mod init` and create directory layout mirroring BarlowFabrication: `backend/`, `shared/{database,models,secrets}/`, `secrets/`.
2. Add `.gitignore`, `README.md`, and a `Taskfile.yml` with the same task names used in BarlowFabrication (`up:local:backend`, `build:prod:backend:tag`, `deploy:prod:tag`).

### Phase 2 — Backend (Go + Gin + GORM)
3. `backend/main.go` bootstraps Gin, loads file-based secrets, connects to Postgres with retry, runs `AutoMigrate`, serves on `BACKEND_PORT=8104`.
4. Models: `Name{ID, Text(unique), Status, CreatedAt, SubmitterHash}`, `Vote{ID, NameID, VoterHash, Value, CreatedAt}` with unique `(NameID, VoterHash)`.
5. Middleware: `VoterIdentity` (reads `X-Voter-Fingerprint` header from FingerprintJS, combines with hashed IP + `VOTER_SALT`), per-voter `RateLimit` via `x/time/rate`, `AdminAuth` (HS256 JWT).
6. Handlers: `GET /api/names` (sort=top|controversial|new, search `q`), `POST /api/names` (dedupe + `go-away` profanity check + rate limit), `POST /api/names/:id/vote` (upsert/delete), `POST /api/admin/login` (bcrypt + JWT), `GET /api/admin/names`, `DELETE /api/admin/names/:id` (soft delete), `GET /healthz`.
7. Templates: `index.html`, `admin.html`, plus HTMX partials; static `app.js` attaches fingerprint header to every fetch; FingerprintJS OSS v3 vendored.

### Phase 3 — Local dev
8. `backend/Dockerfile` (multi-stage `golang:1.25` → `alpine:3.22`) and `backend/dev.Dockerfile` (air + delve) plus `backend/.air.toml`, modeled on BarlowFabrication's.
9. `docker-compose.yml` with only `names-db` (postgres:16.1-alpine) and `names-backend` (hot-reload), single bridge network, named volume `names-data`.

### Phase 4 — Production stack & CI/CD (parallel to Phase 3 once Phase 2 compiles)
10. `stack.yml` for Docker Swarm: postgres + backend, constrained to `node.hostname == mediapc`, versioned external secrets (`names_app_secrets_v${NAMES_VERSION}` etc.), Traefik labels routing `` Host(`names.thomasjbarlow.com`) `` → backend port 8104 with `cloudflare` cert resolver, joins existing `traefik-public` overlay network.
11. `.github/workflows/Build.yml` — PR-triggered on self-hosted runner, builds & pushes `registry.thomasjbarlow.com/names-backend:${PR_NUMBER}`.
12. `.github/workflows/Deploy.yml` — on PR merge, pulls image, materializes versioned Docker secrets from GitHub repository secrets, runs `task deploy:prod:tag -- ${PR_NUMBER}`, waits for replicas to converge.
13. Add minimal GitHub secrets: `DATABASE_USERNAME/PASSWORD/NAME`, `ADMIN_USERNAME`, `ADMIN_PASSWORD_BCRYPT`, `ADMIN_JWT_SECRET`, `VOTER_SALT`.

### Phase 5 — DNS + first deploy
14. Add Cloudflare DNS `names.thomasjbarlow.com` (proxied) → swarm ingress.
15. Pre-create external volume `names-data-drive` on `mediapc`.
16. Open + merge first PR; verify site loads at `https://names.thomasjbarlow.com`.

## Relevant files (all new)
- `backend/main.go` — Gin bootstrap + template loading.
- `backend/handlers/{names,votes,admin,health}.go` — HTTP layer.
- `backend/middlewares/{voter,ratelimit,admin_auth}.go` — model after BarlowFabrication's `backend/middlewares/`.
- `backend/templates/{index,admin,_name_row,_name_list}.html` + `backend/static/{app.js,styles.css,fingerprint.js}`.
- `shared/database/database.go` — copy retry + AutoMigrate pattern from BarlowFabrication's equivalent.
- `shared/models/{name,vote}.go` — GORM models w/ unique indexes.
- `shared/secrets/secrets.go` — copy file-based loader.
- `backend/Dockerfile`, `backend/dev.Dockerfile`, `backend/.air.toml`, `docker-compose.yml`, `stack.yml`, `Taskfile.yml`, `.github/workflows/Build.yml`, `.github/workflows/Deploy.yml` — modeled on BarlowFabrication equivalents, Node/Angular stage removed.

## Verification
1. `task up:local:backend` brings up db + backend; `curl localhost:8104/healthz` returns 200.
2. Submit a name → appears; submit same name → rejected; submit profanity → rejected.
3. Vote 👍 then 👎 from same browser → tally adjusts, no duplicate row; clear cookies, vote again → still blocked (fingerprint match); different browser → new vote allowed.
4. 20 rapid submits → 429 from rate limiter.
5. Admin login with wrong password → 401; correct → JWT; `DELETE /api/admin/names/:id` with JWT → name disappears from public list.
6. PR opened → Build workflow green, image in `registry.thomasjbarlow.com`. PR merged → Deploy workflow green, `docker service ls` on `mediapc` shows `names_names-backend` 1/1.
7. `https://names.thomasjbarlow.com` loads with a valid Cloudflare-issued cert; `/api/names` returns JSON.

## Decisions
- Frontend: HTMX + `html/template` (no Node, single-stage Go build).
- Voter ID: FingerprintJS OSS v3 visitorId hashed with `VOTER_SALT` + client IP; only the hash is stored.
- Submissions: publish immediately, gated by `go-away` profanity filter + per-voter rate limit. No moderation queue.
- Admin: single account; bcrypt-hashed password in Docker secret; HS256 JWT (~12h).
- DB: dedicated Postgres container/volume — not shared with BarlowFabrication.
- Sort scores: `top` = sum(value) desc; `controversial` = `min(👍,👎) * log(👍+👎+1)`; `new` = CreatedAt desc.
- Search: `ILIKE '%q%'` on `Name.Text` — fine for expected volume.
- Out of scope: comments, categories, separate leaderboard, OAuth admin, multi-admin.

## Further Considerations
1. **Bot abuse**: FingerprintJS OSS is bypassable. Options: A) ship as-is and add hCaptcha later if abuse appears (recommended); B) add hCaptcha on submit now; C) skip fingerprint and rely on IP-hash + cookie only.
2. **Backend port**: plan uses `8104` (8102/8103 already used on `mediapc`). A) keep 8104, B) pick another.
3. **Go module path**: assumes `github.com/barlowtj48/names`. A) confirm, B) provide a different path.
