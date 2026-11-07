# WATCH-ANIME_v2.5

> MoveX (V2.5, after part 33) imported from the Google Drive archive
> `V2.5_movex-after-part33.zip` on 2026-11-07. Original project README follows.

---

# MoveX

- `frontend/`: user app (static, deployed to GitHub Pages)
- `backend/`: Go API + admin panel + SQLite + Telegram backup + replication
- `legacy/`: the old Node.js project, kept only as reference during the migration
- `docs/`: work plan. Progress: `PROGRESS.md`

Full documentation (Bangla) is written in the last part (P20).

Quick start (backend):
```
cd backend
cp .env.example .env      # fill in FIREBASE_PROJECT_ID, FIREBASE_API_KEY, ADMIN_UIDS; COOKIE_SECURE=0 for local http
go get modernc.org/sqlite@latest && go mod tidy
set -a; . ./.env; set +a
go run ./cmd/movex
curl localhost:3000/api/v1/health
```

## Worker replication status

Worker catalog replication includes a scheduled puller, atomic snapshot/change application, and post-commit acknowledgements. The main server can send debounced worker wake-ups when the optional shared `SYNC_NOTIFY_KEY` is configured on both main and worker instances; otherwise workers continue polling. Per-worker `WORKER_KEY` values are stored only as hashes on the main server and are not reused for push notifications. See `PROGRESS.md` for the exact replication status and outstanding verification.

## Frontend migration status

The active user frontend uses Firebase Authentication and the Go `/api/v1` API for core user data, search results, and category browse filters. Category browse filtering and ordering are server-backed. Search autocomplete and Explore-page facet counts still use the already-loaded catalog cache. Do not treat the migration as complete until browser E2E and deployment checks pass; see `PROGRESS.md`.

### Part 24 additions

- Explore category/industry/language/type/year counts are served by `GET /api/v1/catalog/facets` and exclude private catalog items.
- Search autocomplete uses a small relevance-ranked Go catalog request, with popular-search phrases retained and a local fallback if the request fails.
- See `PROGRESS.md` for exact validation results and remaining environment-dependent checks.

### Latest continuation

Part 25 adds typed movie/episode download-source selection, keeps download URLs separate from stream playback, and hardens notification/social-link rendering against unsafe protocols and unescaped content. See `PROGRESS.md` for validation results and outstanding live-browser checks.

### Part 32 additions

- Worker sync status now advances only after an authenticated post-commit acknowledgement; a delivered batch alone is not treated as applied.
- Added replication epoch tracking to worker status so workers with stale snapshots are excluded from public server discovery after a main reset/restore.
- Added optional debounced main-to-worker notifications using a separate shared `SYNC_NOTIFY_KEY` (32+ characters). Periodic polling remains the fallback.
- See `docs/FRONTEND_DEPLOYMENT.md` for the main/worker environment setup.
