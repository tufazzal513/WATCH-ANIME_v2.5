# PROGRESS

Work is split into 20 parts (see `docs/MOVEX_WORK_PLAN.md`). One part per session. Follow the numeric order.
Spec = `MOVEX_GO_MIGRATION_PROMPT.md` (not stored in the repo; the user attaches it each session).

## Status
| Part | Title | Status |
|---|---|---|
| P1 | Foundation | DONE |
| P2 | Schema + catalog repo | DONE |
| P3 | Private data repo | DONE |
| P4 | Auth engine + middleware | DONE |
| P5 | User API: catalog | DONE |
| P6 | User API: private | DONE |
| P7 | Admin infra | DONE |
| P8 | Dashboard + broken links | DONE* |
| P9 | Movie form | DONE* |
| P10 | Seasons/episodes/links + All Movies | DONE* |
| P11 | Categories, banners, users | DONE* |
| P12 | Banners + settings | DONE* |
| P13 | Telegram backup core | PARTIAL* |
| P14 | Restore safety core | PARTIAL* |
| P13 | Telegram backup core | PARTIAL* |
| P14 | Restore safety core | PARTIAL* |
| P15 | Main-side replication | PARTIAL* |
| P16 | Worker-side replication | PARTIAL* |
| P17..P20 | see plan | todo |

## P1: Foundation (DONE)
Done:
- Repo layout: `frontend/` (user app only: index, pages, partials, user+shared JS, user CSS), `backend/` (Go), `legacy/` (untouched full copy of the old project, used as reference; deleted in P20 with the user's consent).
- `backend/go.mod` (module `movex`, `go 1.22`), `cmd/movex` (serve | version), graceful shutdown (SIGINT/SIGTERM).
- `internal/config`: all env vars from spec §4, validated together, `ResolveSessionSecret` (auto `DATA_DIR/session.key`, 0600), `Summary()` (secret-free, for the future /admin/server page).
- `internal/db`: `Open` (WAL, busy_timeout, foreign_keys, synchronous=NORMAL), embedded migration runner (`Migrate`, `Apply`, `LoadMigrations`), refuses a DB newer than the binary (`ErrDBNewer`; matters for restore).
- `internal/presence`: in-memory presence (live window 90 s, TTL 10 min, same as old UI).
- `internal/server`: ServeMux, timeouts, `GET /api/v1/health` ({ok, role, version, seq}), `Deps.Seq` hook for the repo layer.
- `.env.example` (all vars), `.gitignore`.
- Tests: config, migration loader, presence, health/shutdown pass (`-race`). Real-SQLite migration tests exist (`migrate_sqlite_test.go`, tag `!nodriver`).

Decisions:
- `DEV_INSECURE_AUTH=1` is refused (config error, process exits) while `COOKIE_SECURE=1`. "Production" == COOKIE_SECURE=1 (the default). Local dev: COOKIE_SECURE=0.
- `ALLOWED_ORIGINS` defaults to `*` with a startup warning (API uses Bearer tokens, no cookies).
- Worker also requires FIREBASE_PROJECT_ID. ADMIN_UIDS / FIREBASE_API_KEY / TG_* / BACKUP_* are main-only.
- TG_API_ID/TG_API_HASH/TG_CHAT_ID must be all set or all empty.
- Migration numbering: `001_meta.sql` already exists (meta table). **P2 adds 
- `go.mod` has NO `require` yet and there is no `go.sum` (modules could not be downloaded in the first sandbox). First thing on a networked machine:
  `cd backend && go get modernc.org/sqlite@latest github.com/gotd/td@latest && go mod tidy`
  (gotd is only needed from P13; modernc is needed now for `go build` without tags.)

## P2: Schema + catalog repo (DONE)
Done:
- 
- `internal/taxonomy`: exact Go port of taxonomy.js (69 genres, 5 types, 36 industries, 19 languages, popular lists).
- `internal/repo` (repo.go, movies.go, categories.go, site.go): all catalog CRUD with validation; `MaxSeq/SeqFunc`; wired into main (seed categories on ROLE=main, `/health` shows real seq).
- `scripts/sandbox-test.sh`: runs any `go test` args on REAL SQLite in the restricted sandbox (see recipes). 40+ repo tests pass with `-race`.
- `db.DSN` now adds `_txlock=immediate` (write transactions take the lock up front; avoids SQLITE_BUSY on read-then-write).

Decisions (later parts MUST follow these):
- **Ids** are 20-char sortable strings (`newID`: 10 chars ms time + 10 random, Crockford base32). Old Firebase push ids are not kept (dummy data, no migration).
- **Timestamps** created_at/updated_at/sent_at/release_ts = unix ms; `changes.ts` = unix seconds.
- **Genres are DB rows** (`categories`, id = slug, seeded once from taxonomy via `SeedCategories`, then admin-managed; spec §8 `/admin/categories`). Movie genres keep admin order (`pos`; first = primary/old `category`). **Languages, type, industry stay fixed lists** validated against `taxonomy` (not tables). Old code never had category CRUD (only the static list), so P11 is a new admin page.
- **Search**: no FTS. `movies.search_text` (lower-cased title+cast+genres+industry+languages+type+year, rebuilt by the repo on every change incl. category rename/delete) + `ListOpts.Query` = every word must be a substring. This reproduces the old client search exactly. Ranking (title match first, then "hot") is done by the API layer in P5.
- **No download links existed in the old data** (only one `server1` stream URL per movie/episode; `server2` was already removed). Table `links(kind stream|download, quality, label, url, sort)` supports both; old `server1` == `FirstStream(links)`. P5 API must expose `server1` for the old frontend shape if P17-19 keep it, or P18/19 adapt to a links list.
- Links only accept http(s) URLs (no javascript:/data:). Banner image/link, social URL (+mailto:/tel:), poster/cover too.
- **changes log is written by triggers.** Child tables (seasons, episodes, links, movie_categories, movie_languages) log an `upsert` of their **movie**; direct tables log their own row (`categories`, `banners`, `social_links`, `notifications`, `settings` (row_id = key)). A whole-movie cascade delete logs exactly one `delete`. **P15 sync must resolve every (tbl,row_id) by current existence** (exists -> send full row/aggregate as upsert; missing -> delete), so log order inside one transaction never matters. For `movies` the payload is the whole aggregate (movie + genres + languages + movie links + seasons + episodes + episode links).
- Not replicated by design: `movie_stats` (plays/lists counters), `search_stats`, users, etc. (P3). The user app reads `stats` for every movie (ranking); P5 must serve it from the main (workers proxy that endpoint) or P3 can decide to replicate a snapshot.
- `MovieInput` update rule: `Links`/`Seasons` nil = keep, non-nil (even empty) = replace; switching content_type to "movie" deletes seasons. Repo allows empty seasons; the admin handler (P9/P10) should drop empty ones like the old form did (old form skipped seasons with no episodes, after a confirm).
- `release_ts`: pass the browser-local timestamp (ms) when known; if 0 it is derived from `release_date` (form value `2006-01-02T15:04`) read as UTC. P9 should add a tiny inline script/hidden field with the admin's local ms.
- Settings keys allowed: appName, appLogo, themeMode(static|auto), themeColor(#rrggbb), themeInterval(5..86400), pwdResetLimit(1..50). `adminUIDs` is gone (env). Public keys: all except pwdResetLimit.
- Reorder helpers (`ReorderCategories`, `ReorderBanners`): listed ids first, others follow in current order, sort renumbered 10,20,30.
- Old `progress` records have `season` (name), `ep` (index), `t`, `d`, `at`: P3's `watch_progress` should store season TEXT + ep INTEGER (+ t, d, updated_at), not an episode FK.
- Old `searchStats/<key>` = {t (term), c (count), at}; key = normalized term (<=40 chars). Old `stats/<movieId>` = {plays, lists}; `lists` +/-1 on My List add/remove (never below 0), `plays` +1 per view.

## P3: Private data repo (DONE)
Done:
- `003_private.sql`: users, my_list, watch_progress, broken_links, error_logs, search_stats, movie_stats, pwd_reset_attempts, audit_log, workers. **No change-log triggers** (main-only); a test proves private writes never reach `changes`.
- `internal/repo/users.go`: EnsureUser/GetUser/UserStatus/UpdateProfile/TouchLastSeen/SetUserStatus/PurgeUser/ListUsers/CountUsers/UserGrowth, My List (server-side counters), progress.
- `internal/repo/ops.go`: movie stats, search stats, broken links, write-once error logs, password-reset limiter, audit log, workers (hashed keys), meta get/set.
- ~45 more tests; whole suite passes with `-race` on real SQLite (`scripts/sandbox-test.sh -race ./...`).

Decisions (later parts MUST follow these):
- **User status values are `active | frozen | deleted`** (the old names; spec wrote "blocked"). `repo.Blocked(status)` is true for frozen AND deleted -> API answers 403. Admin "delete" only marks `deleted` (+ deleted_at) and keeps the row so the user's own client can see it and remove the Firebase account; `PurgeUser` hard-deletes (cascades list/progress). A login never revives a frozen/deleted user and does not touch their last_login_at.
- `EnsureUser` (call after token verification on `GET /me`): first login creates an active record (name default "User"); later logins only refresh `last_login_at`. Profile fields are NOT overwritten from the token afterwards; users change name/photo via `UpdateProfile` (`PUT /me`). Users cannot change `status` or email through any repo call.
- **Presence** stays in memory (P1). `TouchLastSeen(uid, every)` persists `users.last_seen` at most every `every` (suggest 5 min) for the "last seen" column when someone is offline. "Live" = presence.IsLive (90 s). "New user" filter: use `ListOpts.JoinedSince` (old UI treated recent joiners as NEW; check `getUserStatus` in legacy users.js for the exact window when building P11).
- **My List**: `AddToList` / `RemoveFromList` are idempotent and update `movie_stats.lists` in the same transaction (+1 only when really added, -1 only when really removed, never below 0). So the client must NOT touch stats; P6 simply calls these. Unknown movie -> `ErrNotFound`. Max 2000 items per user. Old `myList` value was the added-at timestamp -> `ListItem.AddedAt` (ms).
- **Progress**: `SetProgress(uid, movieID, season, ep*, t, d)`: d>0, t>=0 (t capped to d), season = season NAME, ep = episode index; both empty for movies. Keeps only the newest 300 titles per user. The "finished (>95%) -> delete" rule stays in the client (calls `DeleteProgress`); P6 should expose DELETE or treat t/d>0.95 as delete. Old record also had `at` -> `Progress.At`.
- **Play counting**: `IncPlays(movieID)`; the 6h-per-user-per-title de-dupe and the "only after 30 s of real watching" rule are client-side in the old app; P6 must add a server-side rate limit per (uid, movie).
- **Stats for the user app**: `AllMovieStats()` -> {plays, lists} for every title (old `stats` node, used for the "hot" ranking). Dashboard "total views" = `TotalPlays()`. There is **no `site_stats` table** (not needed).
- **Search stats**: `HitSearch(term)` (normalized: lower-case, collapsed spaces, 2-40 chars) and `TopSearches(limit)` (count desc). Old client showed the top 12.
- **Broken links**: ONE row per title (old `broken_links/<movieId>`); a new report refreshes ep_info/server_type/url/reporter, bumps `reports` and re-opens it. `status` open|resolved (spec: "resolve/delete"). `ClearBrokenLinks(movieID)` must be called by the admin movie-save handler (old behaviour: saving an edited title removed its report). `CountOpenBrokenLinks` feeds the sidebar badge.
- **Error logs**: write-once rows; whole JSON (payload + ts + panel) <= 4000 BYTES (also a DB CHECK). Fields come from the client: `message`, `source`, ... + `panel` user|admin. `PruneErrors(keep)` should run from a periodic job (P20/P13 scheduler) to keep ~5000.
- **Password reset limiter**: `ResetKey(email)` = sha256(lower(trim(email))) (addresses are never stored); `CountResetAttempts(key, 24h)` vs `settings.pwdResetLimit` (default 3), `AddResetAttempt` only after Firebase accepted the request; `ResetStats()` -> dashboard totals; `PruneResetAttempts`.
- **Workers**: `CreateWorker(name,url)` returns the secret key ONCE (`mxw_` + 64 hex); only sha256 is stored. `WorkerByKey`, `TouchWorker(id, seq)`, `ListWorkers`, `DeleteWorker`. P15 builds the endpoints.
- **Audit**: `Audit(ctx, actorUID, action, target, detail)`; details are cut at 1000 chars, never rejected. Suggested action names: `movie.create|update|delete`, `user.freeze|unfreeze|delete`, `worker.add|delete`, `backup.run`, `restore.run`.
- `meta` helpers `GetMeta/SetMeta` exist (P15 will store `epoch` here).

## P4: Auth engine + middleware (DONE)
Done:
- `internal/auth`: `Verifier` (RS256 only, kid required, Google JWKS cached by `Cache-Control max-age` clamped 1 min..24 h, unknown kid -> at most 1 refetch/min, stale keys used up to 12 h during a Google outage, `exp`, `iat <= now+300s`, `aud == project`, `iss == https://securetoken.google.com/<project>`, `sub` 1..128, result cache <= 10 min or token exp, 5000 entries), `dev:<uid>` tokens only when `DevInsecure`. Errors: `ErrInvalidToken` (-> 401), `ErrKeysUnavailable` (-> 503 + Retry-After). 25 attack/edge cases tested against a fake JWKS server (alg none, HS256 confusion, wrong key/aud/iss/kid, tampered payload, expiry, rotation, outage, recovery).
- `internal/httpx`: `WriteJSON/WriteError` (`{"error":code,"message","field","status"}`), `WriteJSONETag` + `NotModified` (weak ETag, `If-None-Match` list and `*`), `DecodeJSON(w,r,&dst,max)` (415/400/413 handled, rejects trailing values), `ClientIP` (TRUST_PROXY uses the RIGHT-most X-Forwarded-For entry), `Chain`, `AccessLog` (request id, no query string/headers/tokens, uid via `SetUID`), `Recover` (generic 500, stack only in log), `APISecurityHeaders`, `CORS` (listed origins or `*`, no credentials, preflight answered, `Vary: Origin`), `Gzip` (only JSON/text > 1 KB, buffered decision, handles 304/204/HEAD/own Content-Encoding), `LimitBody`, token-bucket `Limiter`, `RateLimitIP`, `TooMany` (429 + Retry-After).
- `internal/server`: API chain = security headers -> CORS -> per-IP rate limit (900/min) -> gzip -> 1 MB body limit (CORS is outside the limiter so 429s are readable by browsers). Route helpers: **`s.Public(pattern, fn)`, `s.Private(pattern, fn(w,r,*Principal))`, `s.AdminAPI(...)`**, `s.AnonWriteAllowed(w,r)` (20/min per IP for token-less writes), `s.fail(w,r,err)` (ValidationError -> 400 {"error":"validation","field","message"}, ErrNotFound -> 404, MaxBytes -> 413, anything else -> generic 500 + log). Unknown `/api/*` -> JSON 404, wrong method -> JSON 405 + `Allow`. Preflight `OPTIONS /api/*` -> 204.
- `Principal{UID,Email,Name,Picture,Status,IsAdmin}`. `authenticate`: Bearer token (case-insensitive scheme, never from the query string) -> verify -> per-uid limit (300/min) -> on ROLE=main: `repo.UserStatus` -> frozen/deleted => **403 `{"error":"account_blocked","status":"frozen|deleted"}`**; a user with no record yet is allowed (`Status==""`; P6 `GET /me` calls `EnsureUser`).
- `cmd/movex` builds the verifier from env. Real binary smoke-tested (startup, migrations 1-3, 69 genres seeded, health seq=69, preflight, JSON 404, SIGTERM shutdown, config errors).
- Whole suite passes with `-race` on real SQLite.

Decisions (later parts MUST follow these):
- All user-API handlers must be registered through `Public/Private/AdminAPI` (never `mux.Handle` directly) so CORS, rate limit, gzip, body limit and security headers always apply. Admin SSR routes (P7) are NOT wrapped with `api()` (no CORS there) and get their own chain.
- On **workers** the status check is skipped (their local `users` table is empty, private routes are proxied to main). Consequence: a frozen user can still read the public catalog from a worker; private data stays protected on main. Document in P16/P20 README.
- Rate limits are constants in `server` (`IPPerMinute=900`, `UIDPerMinute=300`, `AnonWritesPerMin=20`); no env vars (spec §4 has none).
- `ErrKeysUnavailable` => 503 (not 401) so a Google outage does not log everybody out in the client.
- Error `message` texts are English and generic; the frontend decides what to show. Internal errors never leak details.
- ETag/304: use `httpx.WriteJSONETag(w, r, v)` for GET list/detail endpoints (P5). Cache-Control is `private, no-cache`.
- `X-Request-Id` is returned on every response and logged; useful for support.

## P5: User API, catalog part (DONE)
Routes (all in `internal/server/api_catalog.go`, registered when `Repo != nil`; every one goes through Public/Private so the API chain applies):
| Route | Auth | Notes |
|---|---|---|
| `GET /api/v1/settings/public` | none | stored keys appName, appLogo, themeMode, themeColor, themeInterval (never pwdResetLimit) |
| `GET /api/v1/servers` | none, **main only** | `{servers:[{name,url}]}` of workers seen in the last 10 min |
| `GET /api/v1/movies` | token | lite cards; params below |
| `GET /api/v1/movies/{id}` | token | full title; private => 404 |
| `POST /api/v1/movies/{id}/view` | token | `{counted:bool}`; one counted view per (uid,title) per 6 h |
| `GET /api/v1/categories` | token | `{items:[{id,name,popular,sort,count}]}` count = non-private titles |
| `GET /api/v1/banners` | token | active only, hides banners of private titles; `{id,imageUrl,movieId,movieTitle,linkUrl}` |
| `GET /api/v1/social-links` | token | `{id,name,url,icon}` |
| `GET /api/v1/notifications?limit` | token | newest first; `{id,title,message,timestamp(ms)}` |
| `GET /api/v1/search/suggest?q&limit` | token | terms searched by >= 2 people, prefix filter |
| `POST /api/v1/search/hit` | token | `{"term"}` -> 204; 30/min per user |
All GET list/detail responses carry a weak ETag and answer 304 on `If-None-Match` (`Cache-Control: private, no-cache`).

`GET /movies` params: `offset` (0..100000), `limit` (1..100, default **24**; the search screen should send 20), `q` (<=100 chars), `category` (genre name), `type`, `contentType` (movie|series), `industry`, `language`, `year`, `upcoming` (yes|no), `sort` (new|old|title|year|rating|hot|relevance), `ids` (<=100 comma-separated ids; result keeps the requested order, ignores offset/limit; used for My List / continue watching). `sort` defaults to `relevance` when `q` is set, else `new`. Response `{items, total, offset, limit, hasMore}`. Bad params => 400 `{"error":"validation","field","message"}`. There is no `lite` param: the list is always lite.

Card shape (camelCase, close to the old field names): `id,title,contentType,type,industry,year,rating(number|null),posterUrl,coverUrl,dubbed,isUpcoming,releaseDate,releaseTs(ms|null),genres[],category(=genres[0]),languages[],createdAt`. Detail adds `description,cast,server1,links[{kind,quality,label,url}],seasons[{name,episodes[{name,server1,links[]}]}]`. **`seasons` is now an ordered ARRAY** (the old DB used an object keyed by season name); `server1` = first stream link (`repo.FirstStream`), kept so the player code can stay almost unchanged.

Decisions (P6, P17-P19 MUST follow these):
- **Private titles are invisible to the user app** (list, search, ids, detail 404, view 404, banners, category counts). Same as the old `user-data.js` filter.
- **Upcoming titles are listed** (the app shows a countdown). The server only returns the raw `isUpcoming/releaseDate/releaseTs`; the client decides "released" like old `mxReleased` (isUpcoming && releaseTs <= now). The `upcoming=yes|no` filter does that comparison server-side.
- **Ranking is server-side.** `hot` = 50% rating/10 + 30% plays/maxPlays + 20% lists/maxLists, ties -> newer (`COALESCE(release_ts, created_at)`); `relevance` = title exact (3) > starts with (2) > contains (1) > no title match (0, matched via cast/genre/...), then hot. Counters are NOT in card JSON (they would change every list ETag on every view) and there is no `/stats` endpoint. Old client rows map to: "Top 10 today" = `sort=hot&limit=10`, "Popular in <industry>" = `industry=X&sort=hot&limit=20`, "Watch in <language>" = `language=X&sort=hot`, genre rows = `category=X&sort=hot&limit=20`, latest = `sort=new`, upcoming row = `upcoming=yes`. Search tabs map to `contentType`/`type` params. Old special queries ("trending", "popular", "hot" -> rating >= 7) should become `sort=hot` in the client.
- "Similar titles" (watch page): no endpoint; client calls `/movies?category=<primary genre>&sort=hot&limit=13` and drops the current id.
- `suggest` hides terms searched by fewer than 2 people (privacy). The old client fell back to the hottest titles when nothing is there: call `/movies?sort=hot&limit=8` in that case.
- Workers: `/servers` is main-only (404 elsewhere). `sort=hot|relevance` need `movie_stats`, which workers do NOT have -> **P16: workers must proxy requests with `sort=hot` or `sort=relevance` (and `/search/*`, `/view`) to main**, or hot/relevance would fall back to rating-only on a worker.
- `repo` additions: `ListOpts.IDs`, `ListOpts.Sort` hot/relevance, `ListPublicCategories`, `Banner.MoviePrivate`, `SuggestSearches`.

## P6: User API, private part (DONE)
Routes (`internal/server/api_private.go`, registered on **ROLE=main only**; all go through `Private`/`Public`):
| Route | Notes |
|---|---|
| `GET /api/v1/me` | the "I just signed in" call: `EnsureUser` (creates the record on first login from the token's email/name/picture, status active; later only refreshes lastLoginAt). Returns `{uid,email,name,photoURL,status,createdAt,lastLoginAt}` (no isAdmin) |
| `PUT /api/v1/me` | `{name,photoURL}` only; any other body field (status, email, uid...) is ignored => users cannot change their status |
| `DELETE /api/v1/me` | **only when an admin marked the account `deleted`** (204), otherwise 403. The ONE route that lets deleted accounts through (`authenticateOpts(allowDeleted)`). Client flow: see 403 `account_blocked` + `status:"deleted"` -> call `DELETE /me` -> then delete the Firebase account |
| `GET /me/list` | `{items:[{movieId,addedAt}]}` newest first; fetch cards with `GET /movies?ids=` (max 100 per call) |
| `PUT /me/list/{movieId}` / `DELETE ...` | `{added:bool}` / `{removed:bool}`, idempotent, server updates the `lists` counter; private/unknown title -> 404 on PUT |
| `GET /me/progress` | `{items:[{movieId,season?,ep?,t,d,at}]}` newest first |
| `PUT /me/progress/{movieId}` | `{season?,ep?,t,d}` (t,d required; floats are rounded; t capped to d) -> 204. `DELETE` -> 204 (client uses it for the >95% "finished" rule) |
| `POST /presence` / `DELETE /presence` | in-memory presence (`{name?}` optional, falls back to token name); `users.last_seen` saved at most every 10 min; DELETE = tab closed (old `pagehide`) |
| `POST /broken-links` | `{movieId,epInfo,serverType,url}` -> `{ok,counted}`; per user 10/min, same user+title once per hour |
| `POST /errors` | token required (as in the old rules); any small JSON object, <= 4000 bytes incl. server-added `ts`/`panel` (client cannot set those); 20/min per user; stored write-once with panel `user` |
| `POST /auth/password-reset` | public; see below |

Password reset: validates the email format (400 `field:email`), per-IP limit (10 per 10 min), per-email limit = `settings.pwdResetLimit` (default 3) per 24 h counted by `repo.ResetKey` (hash). The attempt is counted BEFORE calling Firebase (`accounts:sendOobCode`, `requestType=PASSWORD_RESET`, key = `FIREBASE_API_KEY`), so real and made-up addresses behave identically. Firebase refusals (EMAIL_NOT_FOUND, USER_DISABLED, its own throttling...) are swallowed and logged: the caller always gets the same 200 `{ok:true,message}`. Only transport errors / 5xx give 503 `unavailable`. Over the limit: 429 `rate_limited` with the old message ("Limit reached. You can only request a password reset N times in 24 hours.") + `Retry-After`. The API key is part of the Firebase URL: errors are logged without the URL. `Deps.FirebaseURL` / `Deps.HTTPClient` make it testable.

Also: `Server.MaintainOnce` + hourly `maintenanceLoop` (started by `Run` on main): keeps the newest 5000 error logs, deletes reset attempts older than 48 h, prunes presence. P15 adds `changes` pruning here; P13 adds the backup scheduler separately.
`repo.IsPublicMovie` added (private titles are invisible for list add, progress and broken-link reports: 404).

Decisions (P7+, P17-P19 MUST follow these):
- Private routes do not exist on workers (404). **P16: workers must proxy every `/api/v1/*` request they do not serve locally to main** (me, list, progress, presence, broken-links, errors, auth/password-reset, view, search/*, and `sort=hot|relevance` movie lists), forwarding `Authorization` and a correct `X-Forwarded-For`.
- Private user data responses are `Cache-Control: no-store`.
- Client rewrite notes (P18): old `users/<uid>/status` realtime listener -> react to 403 `account_blocked` (`status` frozen|deleted) from any call and to `GET /me`; old `ensureUserRecord` -> `GET /me`; old My List map `{movieId: ts}` -> array of `{movieId, addedAt}`; old progress map `{movieId:{t,d,season,ep,at}}` -> array; stats/`lists` counters are server-side now (client must not write them); old `online_users` set/remove -> `POST/DELETE /presence`; old direct RTDB reset flow -> one `POST /auth/password-reset` and show the returned `message` (the 30 s UI cooldown stays client-side).
- The old client logged errors with a token only; there is no anonymous error endpoint.

## P7: Admin infrastructure (DONE)
Package `internal/admin` (mounted by `server.New` on **ROLE=main only**; workers answer 404; `Server.Admin()` exposes it). Migration `004_admin_sessions.sql` + `repo/adminsessions.go`.

What exists:
- **Login**: `GET/POST /admin/login`. The server calls Firebase `accounts:signInWithPassword` (key = `FIREBASE_API_KEY`), verifies the returned idToken with `auth.Verifier`, requires `claims.UID == localId` and `uid in ADMIN_UIDS`. EVERY failure (wrong password, unknown account, non-admin, mismatch, empty fields) shows the same message and page => no account/admin enumeration. Firebase down => 503 page. Throttle: 8 attempts / 10 min per IP AND per email (429 + Retry-After; a throttled attempt never reaches Firebase). The login form has a stateless signed CSRF token (valid 2 h). Passwords and the API key are never logged.
- **Sessions are server-side**: cookie `mx_admin` = `<64-hex random id>.<HMAC-SHA256(SESSION_SECRET)>`, `HttpOnly; Secure (when COOKIE_SECURE); SameSite=Strict; Path=/admin; 12 h`. Only SHA-256(id) is stored (`admin_sessions`), max 10 per admin, expired rows pruned hourly by `MaintainOnce`. Logout deletes the row (a copied cookie dies too). `ADMIN_UIDS` is re-checked on every request, so removing a uid revokes its sessions at once. A bad signature never reaches the database.
- **CSRF**: every POST/PUT/PATCH/DELETE needs (a) `Sec-Fetch-Site` same-origin (when sent) and `Origin` host == request Host, and (b) the per-session token (HMAC of the session id) in header `X-CSRF-Token` (htmx; `admin.js` adds it to every request from `<meta name="csrf-token">`) or form field `csrf`. A token in the query string is NOT accepted. DELETE with a form body is not parsed by Go: use the header (htmx).
- **Headers** on every admin response: `Cache-Control: no-store`, CSP `default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: https:; font-src 'self'; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'; object-src 'none'`, `X-Frame-Options: DENY`, nosniff, `Referrer-Policy: no-referrer`, COOP, Permissions-Policy, HSTS when COOKIE_SECURE. No CORS on admin routes.
- **Redirects**: anonymous GET => 303 `/admin/login?next=<path>` (next must be `/admin` or `/admin/...`, no `//`, no `..`, not login/logout); anonymous htmx => 401 + `HX-Redirect`; anonymous POST => 303 login; unknown admin paths do not reveal anything to anonymous users.
- **Layout**: `templates/base.html` (+ `_nav.html`: sidebar with Categories / Broken Links (+open-report badge) / Server, header, 6-item bottom nav Home/Upload/Content/Banner/Users/Settings, logout form, flash area), `bare.html` (login), `login.html`, `dashboard.html` (PLACEHOLDER with 3 counts), `error.html`. Legacy black/red theme ported to `static/css/admin-theme.css`; Font Awesome 6.5.1 and Poppins are self-hosted (`static/webfonts`, `static/fonts`), htmx 2.0.4 pinned (`static/js/htmx.min.js`), no CDN anywhere. `htmx-config` meta disables eval, script tags and indicator styles (CSP-safe).
- **Static assets**: `/admin/static/*` (no login needed, login page uses it), versioned URLs via the template func `{{static "css/x.css"}}` => `?v=<hash>` and `immutable` caching; no directory listings.

API for later parts (P8-P14):
- `a := s.Admin()`; `a.Handle("GET /admin/movies", func(c *admin.Ctx){...})` (needs a signed-in admin; unsafe methods get the CSRF/origin checks before `fn` runs).
- `c.Render("page", admin.Page{Title, Active, Data})` renders `templates/page.html` inside `base.html` (page files `{{define "content"}}...{{end}}`; put shared partials in `templates/_*.html`); `Page.Layout="bare"` for chrome-less pages; `Page.Status` sets the HTTP status. `c.RenderBlock("page","blockname",data)` renders one named block (htmx fragments). `c.Flash("success"|"error", text)` + `c.Redirect("/admin/...")` (redirect is local-only; htmx gets `HX-Redirect`). `c.Error(status,msg)`. `c.Audit(action,target,detail)`, `c.UID()`, `c.IsHTMX()`, `c.R`, `c.W`. Template funcs: `static`, `ms` (unix-ms -> UTC "2006-01-02 15:04"), `add`. View fields: `.Title .Active .Data .CSRF .Flash .AppName .Brand .BrokenBadge`. Put `<input type="hidden" name="csrf" value="{{.CSRF}}">` in every form.
- `Active` keys used by the nav: `dashboard upload movies banners users settings categories broken-links server`.
- Nav links already point at: `/admin/movies/new`, `/admin/movies`, `/admin/banners`, `/admin/users`, `/admin/settings`, `/admin/categories`, `/admin/broken-links`, `/admin/server` (they 404 until their part lands).

**MANDATORY after editing any `internal/admin/templates/*.html` or `static/js/admin.js`: run `make css` (or `internal/admin/build-css.sh`; needs Node/npm).** It rebuilds `static/css/tailwind.css` (Tailwind 3.4.17) and `tailwind.css.sources`; `TestTailwindCSSMatchesTheTemplates` fails when you forget. Class names must appear literally in templates (never build them from strings in Go). The legacy panel used the Tailwind CDN, Chart.js, FA and Google Fonts CDNs plus inline `onclick`/`style=`; none of that is allowed by the CSP, `TestTemplatesRespectTheCSP` forbids inline `<script>`, `on*=` handlers, `style=`/`<style>`, `javascript:` and third-party URLs in templates. Use `data-action` / `data-confirm` attributes handled by `admin.js` (`toggle-sidebar`, `data-confirm="Sure?"` on forms/buttons/links) or add new handlers to `admin.js`.
- No Chart.js: the growth chart must be a server-rendered SVG (P8).
- `safeNext` fixed during tests: `/administrator` is no longer accepted as a `next` target.
- Binary size is ~27 MB with the sandbox SQLite driver (similar with modernc).

## P8: Dashboard + broken links (DONE*)

Done:
- Replaced the dashboard placeholder with audience/security and content cards: live users, active users (24h), total users, password-reset totals (all-time/24h), title count, total views, open broken reports, and client errors (24h).
- Added top-search terms, recent client errors (message/panel/time/UID), recent open broken reports, and recent admin audit activity.
- Added a server-rendered SVG user-growth chart with 7/30/90-day range links. No Chart.js, inline scripts, or inline styles. SVG geometry is built only from integer counts and fixed dimensions.
- Added `/admin/broken-links`: search by title/episode, movie/series filter, open/resolved filter, 25-row pagination, edit-link shortcut, Resolve and Delete actions. Actions require the existing CSRF/origin middleware and write audit entries.
- Added `CountActiveUsersSince` repository query and tests; added SVG geometry unit tests and integration coverage for broken-link filters/actions/CSRF/audit.
- Registered the new routes and updated the dashboard/broken-link templates. `admin-theme.css` contains local utility fallbacks for newly introduced classes; updated `tailwind.css.sources` fingerprint.
- Made `internal/admin/build-css.sh` executable so `make css` can invoke it on a network-enabled machine.

Verification:
- `go test -tags nodriver ./...` PASS.
- `go vet -tags nodriver ./...` PASS.
- `go test -modfile=/tmp/movex.mod -run '^$' ./...` PASS using a temporary compile-only SQLite stub outside the repository; this confirms the normal (non-`nodriver`) integration-test files compile, but does not execute DB tests.
- Full `go test ./...` could not run because this checkout has no `modernc.org/sqlite` dependency in `go.mod`/`go.sum`, and the sandbox cannot fetch it. Database-backed integration tests are added but remain unverified here.
- Tailwind rebuild was attempted, but `npx tailwindcss@3.4.17` could not complete because npm package download timed out. The checked-in stylesheet has local fallback rules and the source fingerprint is current; run `cd backend && make css` in a network-enabled environment to regenerate the canonical Tailwind output.

Decisions/issues:
- The growth selector is 7/30/90 days, as specified for the Go migration, rather than the legacy Chart.js selector's longer ranges.
- `Active (24h)` counts non-deleted users by the later of `last_seen` and `last_login_at`.
- `DONE*` means implementation is complete, but full SQLite-backed tests and canonical Tailwind regeneration need a network-enabled Go/npm environment.

## Test recipes
- Anywhere: `cd backend && go test -race ./...` (needs modernc.org/sqlite downloadable).
- Restricted sandbox (no modernc.org / golang.org): `go test -tags nodriver ./...` runs everything except real-SQLite tests.
  Real-SQLite check in such a sandbox (used in P1, worked): copy backend/ to a scratch dir, delete `internal/db/driver_modernc.go`, set `DriverName="sqlite3"`, add `_ "github.com/ncruces/go-sqlite3/driver"` + `_ ".../embed"`, `go mod edit -replace golang.org/x/{sys,sync,text}=github.com/golang/{sys,sync,text}@<ver>`, `GOPROXY=direct GOSUMDB=off`. Scratch only; never commit it.
- Go toolchain in a sandbox without go: `pip download go-bin` then `pip install` the wheel (PyPI is reachable). `gofmt` is at `$(go env GOROOT)/bin/gofmt`.

## Environment notes
- Sandbox network: github.com reachable; proxy.golang.org, gitlab.com, modernc.org, golang.org blocked.

## P9: Movie form (DONE*)

Done:
- Added authenticated SSR routes: `GET/POST /admin/movies/new`, `GET/POST /admin/movies/{id}/edit`, and `POST /admin/movies/{id}/delete`.
- Added the Movie/Series form with title, content type, content type label, industry, release year, rating, poster URL, cover/backdrop URL, cast, description, dubbed, private, upcoming and local release date/time.
- Added genre checkboxes from the database-managed categories and language checkboxes from the fixed taxonomy; type and industry use the canonical taxonomy lists.
- Added poster and cover image previews, plus safe text-only link detection for YouTube, Vimeo, Videasy, embed/Drive, Dropbox, HLS, direct video and custom URLs. No inline script, inline styles, inline event handlers or unsafe HTML.
- Added server-side parsing and validation, body size limit, CSRF-protected writes, audit events, flash messages and confirmation before deletion.
- Existing season/episode trees are preserved when editing series in this part; their dedicated manager is P10. Movie-level primary stream link is retained in the legacy `server1` equivalent.
- Added parser unit tests and local CSS fallback utilities for the new form. Updated `tailwind.css.sources` fingerprint.

Verification:
- `go test -race -tags nodriver ./...` PASS.
- `go vet -tags nodriver ./...` PASS.
- Admin template CSP checks and Tailwind source fingerprint test PASS.
- Default `go test ./...` cannot run in this sandbox because `modernc.org/sqlite` is not declared/downloaded in this checkout; database-backed tests therefore remain unverified here.
- `make css` was attempted but the npm/Tailwind package download timed out. The existing stylesheet was retained and local fallback utilities were added; run `cd backend && make css` in a network-enabled environment for the canonical Tailwind rebuild.

Decisions/issues:
- P9 keeps the old single primary stream field as one `stream` link. Multiple stream/download links and episode-level link editing belong to P10.
- Editing an existing series does not replace its season tree in P9. Switching a series to a movie follows the repository rule and removes its seasons.
- `release_ts` is calculated in the browser from the admin's local `datetime-local` value and submitted as Unix milliseconds, avoiding an unintended UTC reinterpretation.

## Next: P10 (seasons, episodes, links + All Movies)

Read `legacy/assets/js/admin/pages/seasons-episodes.js`, `legacy/admin/pages/all-movies.html`, the episode/link sections of `legacy/admin/pages/movies.html`, and the bulk-action parts of `legacy/assets/js/admin/pages/movie-actions.js`. Add season/episode CRUD and ordering, per-movie/per-episode stream and download link management, and the All Movies list with search, filters, sorting, pagination and bulk actions. Preserve the P9 form and do not silently discard metadata or existing series content.


## P10: Season / episode / link management + All Movies (DONE*)

Done:
- Added a JSON-backed season/episode editor to the series form. The editor supports multiple seasons, multiple episodes per season, episode names, multiple stream/download links per episode, quality/label metadata, and ordering by array order. `[]` explicitly clears the season tree; an empty field preserves existing seasons.
- Added movie-level multiple stream/download link editing with kind, quality, label, URL, and stable display order. The legacy Primary URL remains available and compatible.
- The existing repository transaction layer (`ReplaceSeasons` / `SetLinks` and cascade foreign keys) is reused; season replacement is atomic, and invalid URLs/link kinds are rejected by the repository normalizer.
- Added `/admin/movies` with title search, content type, public/private, upcoming/released, genre, industry, language, year and sort filters; 25-row pagination; poster/title/type/status/genre/year/rating table; edit shortcuts.
- Added CSRF-protected bulk deletion (maximum 100 selected records), confirmation prompt, and per-record audit entries. Added page-level select-all behavior.
- Added JSON parsing tests for season/episode trees, multiple links and malformed JSON. Updated local CSS fallback utilities and Tailwind source fingerprint.

Verification:
- `go test -race -tags nodriver ./...` PASS.
- `go vet -tags nodriver ./...` PASS.
- Template/CSP/source-fingerprint tests PASS.
- Full SQLite-backed `go test ./...` remains unverified in this network-restricted sandbox because the prior checkout does not contain/download `modernc.org/sqlite`. Tailwind canonical rebuild was attempted but npm package download timed out; local fallback CSS is included.

Decisions/issues:
- To avoid adding a large client-side dynamic form framework or inline script under the strict CSP, the season/episode and multi-link editor uses a validated JSON array in a clearly labelled textarea. Array order controls season, episode and link order. This is fully functional but less visual than a drag-and-drop editor.
- Bulk action currently provides safe bulk delete only; other bulk actions were not defined by the legacy page's behavior and are not invented.
- Run `cd backend && go test -race ./...` and `make css` in a network-enabled environment before production deployment.

## P11: Categories, banners and user administration (DONE*)

Done:
- Added `/admin/categories`: create, edit, delete, popularity flag, sort value, movie usage count and bulk ordering by comma-separated category IDs. Category rename uses the existing repository transaction to refresh movie search text; deletion removes category assignments safely.
- Added `/admin/banners`: create, edit, delete, active/inactive toggle, optional linked movie ID, optional custom URL, sort value and ordering by comma-separated banner IDs. Repository validation checks HTTPS image URLs and linked movie existence.
- Added `/admin/users`: search by name/email/UID, status filter, joined/last-seen timestamps, profile details, and status controls (active/frozen/deleted). Prevents the current admin from blocking or deleting their own account. User deletion is a soft access-status change, not Firebase identity deletion or permanent row erasure.
- All mutations use the existing admin CSRF/origin guard, flash feedback and audit logging. Existing nav links now resolve for categories, banners and users.
- Added responsive template styling fallbacks and refreshed the Tailwind source fingerprint.

Verification:
- `go test -race -tags nodriver ./...` PASS.
- `go vet -tags nodriver ./...` PASS.
- Template CSP and Tailwind source-fingerprint checks PASS.
- Full SQLite-backed tests remain unverified because `modernc.org/sqlite` is not available in this restricted checkout. Canonical Tailwind rebuild still requires npm access; P11 local utility fallbacks are included.

Not included yet:
- Settings management, social-link management and notification management are deferred to P12 so they can be implemented and tested together rather than adding unverified partial forms.

## P12: Banners + settings (DONE*)

Done:
- Kept the P11 banner manager and completed the settings area at `/admin/settings`.
- Added app name and logo URL settings, static/auto theme mode, theme color, auto interval (5..86400 seconds), and password-reset request limit (1..50 per 24 hours). Values are validated and saved atomically through the existing repository API.
- Added social-link create, edit and delete with name, URL and Font Awesome icon. Existing repository URL and icon validation is reused.
- Added notification send, edit and delete. The page lists the latest 100 notifications newest first; edits refresh `sent_at`, matching the legacy behavior.
- Added audit events for settings, social-link and notification mutations. All POST routes use the existing session, CSRF and same-origin protections.
- Confirmed public settings omit `pwdResetLimit`; it remains server-side only. The old Server Settings UI is intentionally excluded and remains planned for P14 `/admin/server`.
- Updated sidebar navigation and local CSS fallback utilities; refreshed the Tailwind source fingerprint.
- Added an integration test covering settings persistence, public-setting filtering, social CRUD and notification CRUD.

Verification:
- `go test -race -tags nodriver ./...` PASS.
- `go vet -tags nodriver ./...` PASS.
- Admin template/CSP and Tailwind source-fingerprint checks PASS (included in the tagged test run).
- The full SQLite-backed integration test could not be executed in this restricted environment: the default build requires `modernc.org/sqlite`, and the repository's GitHub-based sandbox-driver fallback could not resolve its Go module mirrors. The new integration test is included for execution in a network-enabled environment with `modernc.org/sqlite` available.
- Canonical Tailwind rebuild still needs npm network access. P12-specific CSS fallback utilities are included.

## Next: P13 (Telegram backup)
- Implement the Telegram client interface and gotd/td integration, SQLite `VACUUM INTO` snapshot, gzip, upload, pin, retention, retry/FloodWait, mutex and scheduler. Add mock-client tests before enabling live Telegram operations.


## P13: Telegram backup (PARTIAL*)
Done:
- Added `backend/internal/backup`: SQLite `VACUUM INTO` snapshot, table row counts, raw DB SHA-256, gzip output, atomic final file, metadata caption, local retention.
- Added injectable `TelegramClient` interface with upload, pin, unpin and delete operations; manifest-based Telegram retention (`TG_KEEP`).
- Added bounded exponential retry and explicit FloodWait-aware delay.
- Added mutex to prevent concurrent backups and a context-cancellable interval scheduler.
- Added mock SQL driver + mock Telegram tests for snapshot/gzip, upload retry, pinning, retention, local cleanup and overlap prevention.
- `go test -race -tags nodriver ./...` PASS.
- `go vet -tags nodriver ./...` PASS.

Not done / blocker:
- Real `github.com/gotd/td` MTProto user-session adapter is not implemented. Attempted `go get github.com/gotd/td@v0.120.0`, but this environment cannot resolve github.com (DNS/network unavailable). Bot API was not substituted because the specification requires MTProto and large file support.
- Scheduler is intentionally not started from `main` until a real authenticated adapter/session lifecycle is present; otherwise startup could falsely imply backups are running.
- Full real-SQLite integration tests still depend on downloading the configured SQLite driver. The new orchestration tests use a deterministic fake SQL driver.

Decisions:
- `TelegramClient` is a narrow transport boundary so the backup engine remains testable without Telegram credentials.
- No Telegram session file is included in any snapshot or backup archive.
- P13 must be completed with the gotd adapter before moving to P14 restore/login. See `docs/P13_BACKUP_NOTES.md`.

⏭️ Next: finish P13 gotd/td adapter + session connection before P14.
⚠️ Network limitation: Go module download from github.com failed with DNS resolution error.

### P13 follow-up: Telegram session storage hardening

Done in this continuation:
- Added `backup.SessionFileStore` for a future gotd session-storage adapter. It stores opaque MTProto session bytes outside SQLite and outside generated backup archives.
- Session writes use a same-directory temporary file, mode `0600`, fsync, and atomic rename; parent directory is created as `0700`.
- Session reads reject symlinks/non-regular files, reject empty or oversized state, and tighten existing file permissions to `0600`.
- Added explicit clear operation and tests for round-trip, permissions, empty/oversized data, symlink rejection and removal.
- `go test -race -tags nodriver ./...` PASS; `go vet -tags nodriver ./...` PASS.

Still blocked:
- The real gotd/td MTProto client adapter, phone-code/2FA login lifecycle, chat resolution, document upload and message pin/unpin/delete calls remain unimplemented. A fresh attempt to resolve `github.com/gotd/td@v0.120.0` still fails because this execution environment cannot resolve `proxy.golang.org` (DNS/network unavailable).
- The session file store is only a secure persistence primitive. It is not a Telegram client and does not make Telegram backups operational. The scheduler remains deliberately disabled until the real adapter and authenticated session lifecycle are available.

## P14: Restore + Telegram login + `/admin/server` (PARTIAL*)

Done in this continuation:
- Added `/admin/server` as a read-only server overview using `Config.Summary()`; values are sorted and secret values are never rendered.
- Shows whether Telegram API settings are configured and whether the protected session file exists, without exposing session bytes or credentials.
- Lists up to 20 latest local `.db.gz` snapshots from `DATA_DIR/backups` with file sizes.
- Clearly marks Telegram login, live connection test and backup actions as disabled until the real MTProto adapter is available; no fake success controls were added.
- Added a security-focused admin test proving the page renders and does not leak Firebase API key or session secret.
- Updated the Tailwind source fingerprint for the new server template.

Verification:
- `go test -race -tags nodriver ./...` PASS.
- `go vet -tags nodriver ./...` PASS.

Still not done:
- P13 real gotd/td adapter and authenticated session lifecycle remain blocked. A fresh `go get github.com/gotd/td@v0.120.0` attempt still fails because `proxy.golang.org` DNS/network is unavailable in this environment.
- P14 auto-restore, manual restore confirmation + integrity validation + atomic database replacement, Telegram phone/code/2FA login, Test Telegram and Backup Now are not implemented yet. These require a proper restore lifecycle and the actual MTProto dependency.

Decision:
- `/admin/server` is intentionally read-only until environment mutation/restart semantics and Telegram transport are wired. Deployment environment variables must be changed in the hosting provider, not edited in-process.
- P13 remains PARTIAL; P14 is PARTIAL. Do not claim Telegram backup/restore is operational.

## P15: Replication main side (PARTIAL*)

Done in this continuation:
- Added `Repo.EnsureEpoch` / `ResetEpoch` using a random 128-bit epoch stored in `meta.epoch`; main initializes the epoch before opening the HTTP listener.
- Added `Repo.ReplicationBatch`: incremental batches from the `changes` log, resolves each row by current existence (upsert with current full aggregate, or delete if the row no longer exists), and requests a full snapshot when the epoch differs or the worker cursor predates the retained log.
- Full snapshot includes movies with genres/languages/movie links/seasons/episodes/episode links, categories, banners (including inactive), social links, notifications and public settings only. Private settings such as `pwdResetLimit` are excluded. User/private tables and stats are not included.
- Added rate-limited authenticated `GET /internal/sync?epoch=&since=&limit=` on main, using `X-Worker-Key`; invalid keys receive 401, invalid cursors 400. Worker key hashes are checked in constant time after indexed lookup. Worker last-seen/last-seq are updated to the last actually delivered sequence (or snapshot max sequence).
- Added `/admin/workers`: register worker name+URL, show its generated `WORKER_KEY` exactly once, list workers and remove workers. Only the key hash is stored; audit events are recorded.
- Added change-log pruning during hourly maintenance. It removes entries only when they are outside both retention windows (older than the newest 100,000 AND older than 30 days).
- Added `replication_test.go` coverage for key authentication, aggregate payload, full snapshot on epoch mismatch, and cursor validation (real-SQLite test build; see verification note below).
- Updated Tailwind source fingerprint for the new admin template and script.

Verification:
- `go test -race -tags nodriver ./...` PASS.
- `go vet -tags nodriver ./...` PASS.
- The new real-SQLite replication HTTP tests are tagged `!nodriver` and could not be executed in this sandbox because the configured `modernc.org/sqlite` dependency is not downloadable here. The sandbox driver script also could not complete its dependency resolution. Run `go test -race ./...` in a network-enabled Go environment to execute the full suite.

Not done / deferred:
- Worker-side puller, local apply/full rebuild, `/internal/notify` receiver and main-side debounced push notifications belong to P16 / require worker-side sync protocol. No claim is made that worker replicas are already syncing end-to-end.
- Main-to-worker push notification authentication still needs its explicit worker-side secret lifecycle in P16; the main stores only the one-time worker key hash and does not persist raw worker secrets.

Decisions:
- `max_seq` is the current main sequence, while `last_seq` is advanced only to the last change actually included in the returned batch. This prevents a worker from skipping changes when a batch is limited.
- Full sync is selected on epoch mismatch or when `since < MIN(changes.seq)-1`; an empty change log does not force a full snapshot when epoch matches.

⏭️ Next: P16 worker-side puller, incremental/full apply, local catalog DB, main proxy routing and notify endpoint.

## P16: Worker-side replication (PARTIAL)

Implemented in this continuation:
- Added worker API proxy fallback for API paths that are not served locally. Requests are sent to the configured `MAIN_URL` with the original method, path, query and request body.
- Preserves `Authorization` and normal end-to-end request headers while stripping hop-by-hop headers, `Host`, and `X-Worker-Key`.
- Rebuilds `X-Forwarded-For` from the validated client address and immediate peer rather than blindly forwarding a caller-controlled chain.
- Proxies `sort=hot|relevance`, movie view counting, and search suggest/hit requests to main because their ranking/counters are intentionally main-only.
- If main cannot be reached, the worker returns HTTP 503 with `Retry-After`; it does not report a successful write.
- Added tests for auth/path/query forwarding, worker-key non-disclosure, client IP forwarding, and upstream outage behavior.

Verification:
- `go test -race -tags nodriver ./...` PASS.
- `go vet -tags nodriver ./...` PASS.

Not done; P16 remains PARTIAL:
- Worker puller / scheduled sync from `/internal/sync` is not implemented.
- Incremental and full snapshot application into the worker's SQLite catalog is not implemented. Therefore local catalog endpoints on a worker do not yet have a populated replica and this is NOT end-to-end replication.
- `/internal/notify` receiver and main-side push notifications are not implemented.
- Epoch mismatch recovery / full rebuild tests are not implemented.
- The real-SQLite tests remain unavailable in this sandbox because the module dependency is not present/downloadable here.

Safety note: worker proxy routing provides main-authoritative handling for private/write routes and main-only stats/search paths, but do not put a worker into production service until the puller and atomic local catalog apply are completed and verified.

⏭️ Next: finish P16 puller, incremental/full snapshot application, local catalog DB replacement, notify protocol, and `httptest` main+worker end-to-end coverage.

### P13 retry (2026-11-07)

- Re-ran `go test -race -tags nodriver ./...`: PASS (all packages).
- Re-ran `go vet -tags nodriver ./...`: PASS.
- Retried `GOPROXY=https://proxy.golang.org go get github.com/gotd/td@v0.120.0`: BLOCKED by DNS/network (`proxy.golang.org` lookup refused).
- No mock/fake production adapter or Bot API workaround was introduced. P13 remains PARTIAL; scheduler remains disabled until a real MTProto user-session adapter is implemented and verified.

Next action: fetch gotd/td v0.120.0 in a network-enabled Go environment, implement the real Telegram adapter and session login lifecycle, then continue P14 restore.


## P14: Restore safety core (PARTIAL)

Implemented in this continuation:
- Added a reusable gzip SQLite restore pipeline with bounded decompression (2 GiB), temp-file extraction, and validation before touching the live DB.
- Validation checks SQLite `integrity_check`, migration schema version (rejects backups newer than the running server), and caller-specified required tables.
- Automatic restore requires an explicit database-empty check and refuses non-empty databases.
- Manual restore requires the exact confirmation string `RESTORE`.
- Before replacement, the workflow invokes a caller-provided stop/close hook, keeps a local `.bak` copy, atomically renames the validated snapshot, then invokes a reopen hook. If reopen fails, it attempts rollback from `.bak`.
- Added tests for manual confirmation, auto-restore non-empty refusal, validation-before-replacement, safety copy, and invalid gzip.

Verification:
- `go test -race -tags nodriver ./internal/backup ./internal/admin ./internal/server` PASS.
- `go vet -tags nodriver ./...` PASS.

Not done; P14 remains PARTIAL:
- Restore pipeline is not yet wired into the live server lifecycle or `/admin/server` actions.
- Telegram pinned-backup listing/download API is not implemented because the real MTProto adapter remains blocked on fetching `github.com/gotd/td` (DNS/network unavailable in this environment).
- Telegram phone/code/2FA login flow, logout/reset, Test Telegram, Backup Now UI actions, and startup auto-restore orchestration remain outstanding.
- Real SQLite restore integration tests require the `modernc.org/sqlite` module, which is not present in this offline sandbox.

Next: continue P14 UI and orchestration only after the Telegram transport API is available; do not enable auto-restore or claim Telegram connectivity until real MTProto tests pass.

### P14 follow-up: restore lifecycle recovery hardening

Done:
- Required `BeforeReplace` and `AfterReplace` lifecycle hooks to be supplied as a pair, preventing a caller from closing the live database without arranging a reopen.
- On failures after the live DB has been stopped (safety-copy, chmod, or publish failure), the restore pipeline now attempts to reopen the unchanged live DB before returning.
- Added directory fsync after publishing/reverting the database file so the rename is durably recorded where supported.
- If reopening the restored snapshot fails, the pipeline restores the original `.bak` (or removes the new file when no original existed), fsyncs the directory, and makes a second reopen attempt against the recovered database.
- Added tests for reopen-failure rollback, recovery after safety-copy failure, and lifecycle-hook pairing.

Verification:
- `go test -race -tags nodriver ./...` PASS.
- `go vet -tags nodriver ./...` PASS.

Still outstanding: this remains a tested restore core only. It is not connected to the running server's DB lifecycle, no Telegram backup can be fetched until the real MTProto client exists, and no auto-restore or admin restore button is enabled. Keep P14 PARTIAL.

### P14 follow-up: Telegram login orchestration core (transport-independent)

Done in this continuation:
- Added `backup.LoginFlow` with a narrow `LoginTransport` interface for phone-code request, code sign-in, optional cloud-password (2FA), authorized session export and transport close.
- Enforces international phone format (`+` prefix), one pending login at a time, non-empty challenge ID, bounded pending-login TTL and explicit cancellation.
- Keeps the challenge and transport in process memory only; an unfinished login expires safely on timeout or process restart.
- Persists session bytes through the hardened `SessionFileStore` only after the transport confirms authorization. Unauthorized or incomplete logins never create a session file.
- Added tests for phone/code success, 2FA success, expired challenge, invalid phone/empty challenge, file permissions and refusal to persist an unauthorized session.

Verification:
- `go test -race -tags nodriver ./...` PASS.
- `go vet -tags nodriver ./...` PASS.

Still not done / blockers:
- This is an orchestration contract, not a Telegram client. The real gotd/td `LoginTransport` adapter is still blocked by unavailable Go module network access.
- LoginFlow is not yet wired to `/admin/server`; no phone/code/password controls are exposed until the real transport and admin request protections are connected.
- Telegram connection test, Telegram backup upload/download and the admin restore lifecycle remain incomplete. Restore validation/replacement core exists, but is not connected to the running server's DB/repo lifecycle.

Security decisions:
- Never log or persist phone codes, 2FA passwords or challenge IDs. Only the authorized opaque MTProto session is persisted.
- Pending auth state is deliberately not recoverable across process restart; the operator can safely start a new login.


### P14 follow-up: manual local snapshot action

Done in this continuation:
- Initialized the existing SQLite snapshot manager on `ROLE=main` and made it available to the admin panel. It writes compressed snapshots under `DATA_DIR/backups` and retains the newest 30 local files.
- Added CSRF-protected `POST /admin/server/backup` and a **Create local snapshot now** action on `/admin/server`. The action is serialized by the backup manager, uses a 45-second request context, and reports a generic failure to the browser while keeping details in server-side logs where available.
- The server page now shows whether the local backup manager is available/running and the last successful snapshot filename.
- The UI explicitly distinguishes local snapshots from Telegram uploads. No Telegram login, MTProto connection, or Telegram upload is claimed or enabled.
- Added a test that rejects the backup action without CSRF and verifies the safe unavailable-state redirect.

Verification:
- `go test -count=1 -race -tags nodriver ./...` PASS.
- `go vet -tags nodriver ./...` PASS.
- `TestTailwindCSSMatchesTheTemplates` PASS after updating the source fingerprint. Canonical Tailwind regeneration was attempted but the npm download timed out; the button uses existing utility classes.

Still not done:
- This is a local-only snapshot action. P13 real gotd/td MTProto transport, Telegram upload/retention, Telegram LoginTransport, and the scheduled Telegram backup lifecycle remain blocked by unavailable Go module network access.
- RestoreGzip and LoginFlow remain core libraries and are not connected to the live DB lifecycle or Telegram admin controls. Do not treat restore/login as production-operational.

### P14 follow-up: authenticated local snapshot download

Done in this continuation:
- Added `GET /admin/server/backups/{name}` to the authenticated admin panel. The Server page now offers a Download link beside each listed local snapshot.
- The handler accepts only the exact generated `movex-YYYYMMDD-HHMMSS.db.gz` basename, rejects traversal/other names, refuses symlink backup directories/files, verifies the opened file still matches the inspected regular file, and serves it as an attachment with gzip content type.
- Downloads are recorded in the admin audit log. The route inherits the existing admin-session authorization, security headers and no-store policy.
- Added admin tests for authenticated download, anonymous denial, unsafe names and symlink rejection. These tests require the real SQLite driver and are present but could not be executed in this offline sandbox.
- Updated the Tailwind source fingerprint; no new CSS utility classes were introduced.

Verification:
- `go test -race -tags nodriver ./...` PASS (production packages compile and all nodriver-enabled tests pass).
- `go vet -tags nodriver ./...` PASS.
- Full admin integration tests remain blocked because `modernc.org/sqlite` is not installed and network access is unavailable.

Still not done:
- Live DB restore lifecycle integration is deliberately not enabled. The current process keeps DB and repository handles in long-lived dependencies; replacing the DB file from a request without a coordinated shutdown/reopen owner would leave stale handles and risk data corruption.
- Telegram MTProto transport, Telegram login/upload/download, scheduled remote backups and auto-restore remain incomplete.

### P13 follow-up: retention failure safety

Done in this continuation:
- Telegram retention now writes the manifest with the newly pinned backup before deleting older messages. If Telegram deletion fails, the manifest keeps both the old and new message IDs so cleanup can be retried on a later run.
- If the first manifest write fails, the manager makes a best-effort unpin/delete rollback of the newly uploaded message rather than intentionally leaving an untracked pinned backup.
- Local retention now always preserves the current snapshot and removes enough other old files to enforce the configured exact retention count, including when the system clock moves backwards and the current filename sorts first.
- Added regression tests for Telegram delete failure preserving retry state and local retention under clock rollback.

Verification in this continuation:
- `go test -race -tags nodriver ./internal/backup` PASS.
- `go vet -tags nodriver ./internal/backup` PASS.

Still not done:
- The production gotd/td MTProto transport and its session-backed long-lived client are not implemented yet. Current TelegramClient remains an injectable interface with test doubles only.
- Telegram login UI/API, Telegram upload/download against a real channel, scheduled remote backup wiring, and remote restore remain incomplete. Do not claim Telegram backup is operational.


## Session audit: 2026-11-07 (continuation from P16/P13 retention hardening)

Reviewed the supplied `movex-after-part16-p13-retention-hardening.zip` and `MOVEX_GO_MIGRATION_PROMPT.md`. The archive is the working source; no old features or security gates were removed.

Verification performed in this environment:
- `go test -count=1 -race -tags nodriver ./...` PASS (all packages).
- `go vet -tags nodriver ./...` PASS.
- `gofmt -l` across all backend Go files: PASS (no files need formatting).
- `node --check` across all frontend JavaScript files: PASS.
- `go test -count=1 ./...` BLOCKED: `go.mod` intentionally has no downloaded modules in this offline environment and the real SQLite driver import `modernc.org/sqlite` is unavailable.

Remaining work confirmed from source and the migration handoff:
1. **P13:** implement and test the real gotd/td MTProto client (session restore, phone/code/2FA, channel lookup, upload, pin/unpin/delete, download) and wire the Telegram scheduler. The current Telegram interfaces/test doubles and retention core do not make Telegram backup operational.
2. **P14:** wire the existing restore/login cores into the live process lifecycle and admin UI only after P13 transport is real; implement safe startup restore and remote manual restore. Current local snapshot create/download actions are local-only.
3. **P16:** implement worker pull loop, incremental/full snapshot apply into the worker DB, epoch recovery, notify receiver + debounced main push, and main/worker end-to-end tests. Current worker proxy exists, but local worker catalog replication is not end-to-end.
4. **P17-P19:** complete frontend API migration and GitHub Pages deployment validation against the new API.
5. **P20:** deployment/docs/security review, full real-SQLite race suite, browser smoke tests, and remove legacy/reference files only with user consent.

Hard blocker: the actual Go module dependencies are absent (`go.mod` contains no `require` entries and there is no `go.sum`). This execution environment cannot resolve/download `modernc.org/sqlite` or `github.com/gotd/td`; the standard full test/build therefore cannot run. No fake Telegram adapter, Bot API substitution, or unverified restore path was added. Keep P13/P14/P16 marked PARTIAL and do not deploy a worker as a production replica yet.

Next safe action in a network-enabled Go environment:
`cd backend && go get modernc.org/sqlite@<approved-version> github.com/gotd/td@<approved-version> && go mod tidy`
Then implement P13's real adapter first, run `go test -race ./...`, and continue P14.

## P16 follow-up: worker-side catalog sync and atomic apply (IMPLEMENTED; integration verification pending)

Implemented in this continuation:
- Added `internal/replica.Syncer`, which authenticates to the main `/internal/sync` endpoint with the worker key, requests bounded 200-change batches, enforces a 64 MiB response limit, validates epoch/sequence shape, and applies the response through the repository layer.
- Added persistent worker cursor keys (`replica_epoch`, `replica_seq`) in the existing `meta` table. The cursor is advanced in the same SQLite transaction as catalog application, so interrupted writes cannot skip changes.
- Added atomic full-snapshot replacement of the public catalog and incremental upsert/delete application for movies (including genres, languages, seasons, episodes and links), categories, banners, social links, notifications and public settings. Full snapshots now include every notification (not only the 100-item user-facing page limit). Private tables are not copied or cleared.
- Worker startup now attempts an initial full/incremental sync before listening. A new worker refuses to serve an empty catalog if the main cannot be reached; a previously initialized worker can continue serving its last committed catalog and retries in the background.
- Added an authenticated worker `POST /internal/notify` receiver (`X-Notify` must match the worker's configured key) that wakes the puller immediately. Normal polling remains enabled as recovery if a notification is missed.
- Added unit tests for sync URL/query/key handling, successful batch application, unauthorized main responses, store failures and syncer configuration.
- Added real-SQLite integration tests (build tag `!nodriver`) for full snapshot, incremental create/delete and rollback on invalid snapshot. These tests are included but cannot run in this offline environment because `modernc.org/sqlite` is unavailable.

Verification:
- `go test -race -tags nodriver ./...` PASS.
- `go vet -tags nodriver ./...` PASS.
- Added `modernc.org/sqlite v1.34.5` as the pinned pure-Go SQLite driver dependency in `go.mod`. Downloading it and generating `go.sum` are blocked by DNS/network restrictions in this environment.
- Real-SQLite integration tests remain unexecuted here; run `go mod download && go test -race ./...` in a network-enabled Go environment.

Still outstanding in P16:
- Main-side debounced push notifications are not wired yet. The main currently stores only the SHA-256 worker-key hash, so it cannot authenticate outbound `X-Notify` calls without a deliberate secure key-delivery/storage design. Worker polling is operational and remains the fallback; do not claim push notification delivery is complete.
- Full main+worker HTTP/SQLite end-to-end tests require the real SQLite dependency and must be run after dependencies are available.

Next: finish the secure main-to-worker notification credential design and end-to-end replication tests, then continue the frontend migration. P13/P14 Telegram MTProto and remote restore remain outstanding and must be implemented with the real gotd/td transport, not mocked production behavior.

## P13 continuation: real gotd MTProto adapter and admin login (2026-11-07)

Implemented in source, pending a network-enabled dependency build:
- Added `internal/telegrammtproto` backed by pinned `github.com/gotd/td v0.120.0` (MTProto user session, not Bot API).
- Added phone-code login, optional 2FA password, authorization checks, protected gotd session file storage, document upload with the backup JSON caption, message ID extraction, pin/unpin, and retention deletion.
- Numeric `-100...` channel IDs are resolved against the logged-in account's joined dialogs so the channel access hash is obtained from Telegram; public `@username` targets use gotd peer resolution.
- Telegram FLOOD_WAIT errors are converted to `backup.FloodWaitError` so the existing bounded retry policy can honor Telegram's wait duration.
- Wired Telegram login into the protected `/admin/server` page (phone, code, 2FA, cancel forms) and connected the backup manager to the MTProto transport when all TG settings are configured.
- Started the configured scheduled backup interval from the main server only when Telegram settings are present. Scheduler now retries on the next configured interval rather than retrying every minute after a failure.
- Added unit tests for adapter configuration, Telegram response message-ID extraction, and FLOOD_WAIT conversion.

Validation:
- `go test -race ./internal/backup` PASS.
- `go test -race -tags nodriver ./internal/server ./internal/config ./internal/replica` PASS.
- `go test -race -tags nodriver ./internal/admin` currently FAILS only the template/CSS source fingerprint guard because `build-css.sh` could not complete (Tailwind CLI requires npm/network in this sandbox). CSS was not falsely marked as rebuilt.
- The new `internal/telegrammtproto` package and full executable cannot be compiled here: the sandbox cannot fetch gotd v0.120.0 and its transitive modules; Go reports missing go.sum entries. `go.mod` pins the direct dependency, but no fabricated go.sum was added. This part is source-implemented but NOT compile-verified or Telegram-live-tested.
- P13 remains PARTIAL until a network-enabled `go mod tidy`, full `go test -race ./...`, `go vet ./...`, and live admin login/upload/pin/retention test are completed. P14 remote restore remains separate and incomplete.

### P14 follow-up: deferred manual restore wired to startup (2026-11-07)

Implemented:
- Added `POST /admin/server/restore` to upload a `.db.gz` archive with the exact confirmation word `RESTORE`. The endpoint is admin-session protected, CSRF protected (including multipart form parsing), and capped at 512 MiB request size.
- Uploaded archives are staged atomically as `DATA_DIR/restore.pending.db.gz` with mode `0600`; staging validates gzip readability and bounded decompression but never modifies the live database. A second pending archive is refused until the first is cancelled.
- Added a CSRF-protected cancel action and pending-state indicator on `/admin/server`.
- The next `ROLE=main` startup checks for the staged archive before opening SQLite. It then validates SQLite integrity, schema version and required tables through the existing restore core; only after validation does it preserve the old DB as `.bak` and atomically publish the snapshot.
- If validation/replacement fails, startup fails closed and the pending archive remains for operator recovery. The live database is not changed by validation failures. After successful restore the pending archive is removed; the `.bak` safety copy remains.
- Added tests for staged restore, live DB remaining unchanged during staging, successful pre-open application, invalid gzip rejection, existing pending archive preservation and snapshot validation failure preserving the live DB.

Verification:
- `go test -race -tags nodriver ./internal/auth ./internal/admin ./internal/backup ./internal/config ./internal/db ./internal/httpx ./internal/presence ./internal/replica ./internal/repo ./internal/server ./internal/taxonomy` PASS.
- `go vet -tags nodriver` for the same package set PASS.
- `TestTailwindCSSMatchesTheTemplates` PASS after refreshing `static/css/tailwind.css.sources`. Canonical Tailwind regeneration was attempted but npm download timed out; no new CSS utility class family was introduced.
- `cmd/movex` and `internal/telegrammtproto` could not be compiled in this sandbox because `github.com/gotd/td` has no go.sum entries and `proxy.golang.org` is unreachable. The main startup hook is formatted but still needs a network-enabled full build.

Still not done; P14 remains PARTIAL:
- This continuation adds manual archive upload and deferred startup restore, not Telegram-side backup listing/download or automatic restore from Telegram.
- The admin UI upload limit is 512 MiB. The restore library itself permits up to 2 GiB decompressed data.
- A failed pending restore intentionally prevents startup; remove `DATA_DIR/restore.pending.db.gz` using the hosting file manager/console to boot the untouched original DB after investigating the failure.
- Full real-SQLite restore integration and end-to-end host restart testing remain outstanding because the SQLite module/network is unavailable here.
- Startup restore additionally opens the old database only after the new snapshot passes validation, runs `PRAGMA wal_checkpoint(TRUNCATE)`, and closes it before replacement. This avoids carrying crash-left WAL pages into the restored database. A busy/failed checkpoint aborts before the live file is replaced.

## P20 continuation: frontend runtime/API foundation and GitHub Pages pipeline (2026-11-07)

Implemented in this continuation:
- Added `frontend/assets/js/shared/deploy-config.js` and `config.js`. Runtime configuration is centralized at `window.MX`; `apiBase` can be injected at deployment time and remote HTTP API URLs are rejected (HTTPS required; localhost HTTP is allowed for development).
- Added a typed `MX_API` client for the existing Go `/api/v1` routes: health, public settings, movies/details/views, categories, banners, social links, notifications, search suggestions/hits, `/me`, My List, playback progress, presence, broken-link reports, client errors and password reset.
- The API client attaches Firebase ID tokens, refreshes once after a 401, sends writes to the main server, uses worker servers only for catalog GETs, and falls back to main when a worker is unavailable. Private requests use `cache: no-store`.
- Corrected worker discovery in both API clients to use the actual backend routes `/api/v1/servers` and `/api/v1/health`, and the actual response field `servers` (not `workers`).
- Removed the Firebase Analytics SDK and initialization. Firebase is now initialized for Authentication only; the app's data store remains the MoveX Go API.
- Added `.nojekyll` and a GitHub Pages Actions workflow. The workflow injects the public `MOVEX_API_BASE` repository variable into the built artifact and rejects non-HTTPS remote URLs. Configure the repository Pages source as GitHub Actions and set `MOVEX_API_BASE` to the main Go server's HTTPS origin.
- Added Node tests for runtime config validation, v1 URL/query construction, Firebase bearer token use, one-time 401 refresh, and main-server writes.

Verification:
- `node --check` across all frontend JavaScript and CommonJS test files: PASS.
- `node --test frontend/tests/api-client.test.cjs`: 4/4 PASS.
- `git diff --check`: PASS (where git metadata is available).

Important remaining limitation (do not deploy the frontend as fully migrated yet):
- The legacy `db-client.js` compatibility facade and its existing callers still issue `/api/db/...` requests. The Go backend currently exposes `/api/v1/...`, not `/api/db/...`. This continuation adds the typed API client foundation but does NOT yet migrate all existing user-page/core call sites to `MX_API`.
- Existing user call sites that depend on legacy Firebase-shaped paths (user status listeners, app settings/theme, movies list, search stats, analytics/errors, broken links, My List, progress and presence) still need to be migrated to their corresponding typed API functions. The UI behavior must be preserved while doing so.
- Frontend browser end-to-end testing against a running Go server and GitHub Pages deployment were not possible in this sandbox. The `MOVEX_API_BASE` variable and backend `ALLOWED_ORIGINS` must match the deployed domains.
- P20 is PARTIAL. Do not remove `legacy/` or claim the migration complete. Next: migrate core/auth/user-record/user-data/presence/error logger and user-lists call sites; then migrate page-level movie/search/category/notification/player call sites, add browser smoke tests, and finish deployment/security validation.


## Part 21 - Core frontend API migration (2026-11-07)

Implemented real Go API integration for the user-facing core flows:
- Added paginated `MX_API.allMovies()` and corrected search-hit payload to `{term}`.
- Migrated catalog, banners, social links, notifications, and full movie/episode loading from Realtime Database adapter calls to `/api/v1` endpoints.
- Adapted Go season/episode arrays into the existing player’s season-keyed object contract.
- Migrated account bootstrap/profile status to `GET /api/v1/me`; server now owns user record creation and last-login updates.
- Migrated presence heartbeat/leave to `/api/v1/presence`.
- Migrated My List and Continue Watching to `/api/v1/me/list` and `/api/v1/me/progress`; removed client-side stats writes.
- Migrated popular search retrieval and search counting to `/api/v1/search/suggest` and `/api/v1/search/hit`.
- Migrated public app branding reads to `/api/v1/settings/public`.

Validation performed:
- JavaScript syntax checks and targeted Node tests (see frontend/tests).
- At the initial checkpoint, remaining legacy `db.ref()` call sites were identified for auth status, theme settings, error logging, broken-link reporting, and password reset. These were subsequently migrated in this same part; the active user frontend now has no Realtime Database call sites.
- Real browser/Firebase/Go end-to-end testing remains pending because it requires the deployed backend and Firebase credentials.

- Follow-up in this part also migrated theme/branding reads, client error reports, broken-link reports, and password-reset dispatch to their dedicated Go API endpoints.
- Removed the Realtime Database compatibility adapter from the user app bootstrap; Firebase is now loaded for Authentication only. Legacy adapter file remains in the repository for audit/history but is not loaded by the user frontend.
- Account status checks now poll the authoritative `/api/v1/me` endpoint once per minute while the page is visible; this is polling, not instant realtime enforcement.

Part 21 validation results:
- `node --test frontend/tests/api-client.test.cjs`: PASS, 7/7 tests.
- `node --check` across every `frontend/assets/js/**/*.js`: PASS.
- Static scan confirms the active user frontend no longer calls `db.ref()`, `firebase.database()`, or loads `db-client.js` from bootstrap.
- `go test -race -tags nodriver ./...` could not start because `go.sum` entries for gotd/td are missing and network access to fetch modules is unavailable. This is an environment/dependency-resolution blocker, not a passing Go test.
- Browser end-to-end verification against the real Go API and Firebase project is still pending.

## Part 22 - Server-backed search results (2026-11-07)

Implemented:
- Added `MX_API.queryMovies(params)` as a paginated catalog query helper. It preserves caller filters and follows the backend's `hasMore` / `offset` contract using 100-item pages.
- `MX_API.allMovies()` now reuses the same pagination helper, avoiding two independent pagination implementations.
- Committed Search queries now use `GET /api/v1/movies?q=...&sort=relevance` rather than relying exclusively on the browser's full local catalog copy.
- Search results are normalized to the existing `[id, movie]` UI shape, merged into the shared catalog cache, and still use the existing result tabs, cards, and related-title rendering.
- Added a monotonically increasing request ID so slower older searches cannot overwrite a newer query or a reset search page.
- Added a loading state and a graceful local-cache fallback when the API request fails.
- Preserved legacy special search terms (`new`, `latest`, `trending`, `popular`, `hot`) as local semantic searches because the Go catalog query treats `q` as literal searchable text and does not implement those pseudo-filters.
- Search hit counting remains a write to the main server only and occurs after successful result resolution.

Validation:
- `node --test frontend/tests/api-client.test.cjs`: PASS, 8/8.
- `node --check` across all frontend JavaScript files: PASS.
- Backend API contract reviewed: `GET /api/v1/movies` accepts `q`, `sort=relevance`, `limit` up to 100, `offset`, and returns `items`, `hasMore`, and pagination metadata.
- Not run: browser E2E against a deployed Go backend/Firebase project; not available in this environment.

Still outstanding:
- Search autocomplete remains client-side over the catalog cache plus server-provided popular terms. A dedicated title-suggestion endpoint is not part of the current Go API.
- Category browse/filter pages still filter the cached catalog client-side; a later part should move those filters to `category`, `industry`, `language`, `type`, `year`, `contentType`, `upcoming`, and `sort` query parameters without changing the existing UI behavior.
- Full browser/API integration and real deployment checks remain pending.


## Part 23 - Server-side category browse and filters (2026-11-07)

Implemented:
- Replaced category browse's full-catalog client-side filtering with `MX_API.queryMovies()` calls to `GET /api/v1/movies`.
- Mapped the existing browse UI filters to backend query parameters: `category`, `industry`, `language`, `type`, `year`, `contentType`, `upcoming`, and `sort`.
- Preserved existing browse tabs: Movies maps to `contentType=movie`, Series maps to `contentType=series`, and Anime maps to `type=Anime`.
- Preserved the filter-tab intersection behavior: Anime tab combined with a non-Anime Type filter returns an empty result instead of silently broadening the query.
- Mapped UI sort modes to backend-supported sort keys: Suggested -> `hot`, Newest -> `new`, Top Rated -> `rating`, A-Z -> `title`.
- Added a Release filter with Released (default), Upcoming, and All options. Released uses `upcoming=no`, matching the previous `mxList()` behavior, including titles whose release time has already passed.
- Kept the existing poster cards, grid chunking, recommendation strips, filter sheet, labels, and browse navigation.
- Added request sequencing so a slow response from an earlier filter/tab selection cannot overwrite the newest browse result.
- Added explicit loading and request-failure states rather than silently showing stale cached results.
- Filter option choices are still derived from the already-loaded catalog metadata; actual result filtering and ordering are performed by the Go API.

Validation:
- `node --test frontend/tests/api-client.test.cjs`: PASS, 10/10.
- `node --check frontend/assets/js/user/pages/category-movies.js`: PASS.
- `node --check` across all frontend JavaScript files: see Part 23 final validation.
- Confirmed backend contract supports the exact parameters and sort values used here in `backend/internal/server/api_catalog.go` and `backend/internal/repo/movies.go`.

Not verified in this environment:
- Browser end-to-end rendering against a running Go backend and real Firebase authentication.
- Real worker-to-main failover under network interruption.
- The backend's full Go test suite remains blocked by unavailable external Go module downloads documented in earlier parts; this part changes frontend files only.

Remaining frontend work:
- Search autocomplete is still based on local catalog cache plus popular terms; there is no dedicated title-suggestion endpoint.
- Explore-page category/industry/language/type counts are still calculated from the catalog already loaded into the frontend. Selecting an item opens the server-filtered browse page.
- Browser smoke tests and deployment validation against the configured GitHub Pages origin and `ALLOWED_ORIGINS` remain outstanding.

## Part 24 - Explore facets and server title autocomplete (2026-11-07)

Implemented:
- Added `GET /api/v1/catalog/facets`, an authenticated catalog endpoint returning category, industry, language, type, and year values with counts.
- Added `Repo.ListPublicCatalogFacets`, using aggregate SQL over the public catalog only. Private movies are excluded from every count; empty industry/language/type values are omitted; categories and languages count distinct movie IDs.
- The endpoint works against the main database and a worker's replicated public catalog; it does not expose user/private tables.
- Migrated Explore tab counts from browser-side `mxList()` scans to the server-provided facet response. Counts are loaded once per page session and reused when switching tabs; the existing Explore UI and `openBrowse` behavior remain intact.
- Added `MX_API.movieSuggestions(q, limit)` for a small relevance-ranked `/api/v1/movies` response.
- Search autocomplete now requests up to five title suggestions from the Go catalog API while retaining popular-search phrase suggestions. It keeps a local-catalog fallback only when the API request fails.
- Added suggestion request sequencing and input-value checks so a stale response cannot replace suggestions for a newer input value.
- Added API client and static frontend tests for facet retrieval, server-side title suggestions, removal of local Explore count scans, and stale autocomplete protection.

Validation:
- `node --test frontend/tests/api-client.test.cjs`: PASS, 14/14.
- `node --check` across all frontend JavaScript and test files: PASS.
- `go test -tags nodriver ./internal/repo ./internal/server`: PASS.
- `gofmt` applied to changed Go files.
- `git diff --check`: PASS where git metadata is available.

Not verified:
- `go test ./internal/server` cannot start because the sandbox has no `go.sum` entry for `modernc.org/sqlite` and dependency network access is unavailable. The new SQL-backed endpoint integration test is therefore written but not executed here.
- Real browser tests against the running main/worker backend and Firebase are still pending.
- Explore facet data is cached for the page lifetime. Reloading Explore fetches fresh counts; live count updates are not implemented.

Next:
- Continue the remaining frontend/API integration audit, test the actual browser routes against the Go backend when dependencies and Firebase credentials are available, and resolve any contract mismatches before calling the frontend migration complete.

## Part 25 - Player resources, download links, and notification safety (2026-11-07)

Implemented:
- Audited the existing movie-detail API contract: `GET /api/v1/movies/{id}` returns movie-level and episode-level typed `links` entries with `kind`, `quality`, `label`, and `url`; the player can consume these without adding a duplicate download endpoint.
- Updated the player link resolver to select only `kind=stream` entries when the legacy `server1` field is absent. Download-only URLs are never treated as playback URLs.
- Added an explicit Download action when a movie or one of its episodes has download resources. The action opens the selected movie/episode's typed download links in a source chooser, with quality/label metadata and safe external-link attributes.
- Added HTTP/HTTPS URL validation before rendering or opening a download source. The UI explains that actual save behavior depends on the external host and browser; this is not represented as a native/offline download manager.
- Hardened social-link rendering: only HTTP/HTTPS URLs are accepted, icon class input is restricted, visible names are HTML-escaped, and external links use `rel=noopener noreferrer`.
- Hardened notification rendering by escaping title/message content, sorting with numeric timestamps, and displaying a safe fallback when a timestamp is missing or invalid. The backend's `timestamp` is Unix milliseconds, which is passed to `Date` as expected.
- Added styling for the download-source chooser.
- Added three contract/security regression tests covering typed player resources, safe URL and stream/download separation, and notification/social-link output safety.

Validation:
- `node --test frontend/tests/*.test.cjs`: PASS, 17/17.
- `node --check` across all frontend JavaScript and CommonJS tests: PASS.
- ZIP integrity check: PASS.

Not verified:
- Real browser playback/download-source behavior against deployed movie records and third-party hosts remains untested.
- The download action opens the configured external source. It does not implement background downloading, pause/resume, local file management, or a native app download queue; those require a separate Android/native download implementation or a dedicated backend streaming/download contract.
- Browser E2E with Firebase authentication and a live Go backend remains pending.

Next:
- Continue the frontend/API contract audit, particularly movie details edge cases, player source fallback behavior, and notification/account flows. Do not mark the overall frontend migration complete until real backend/browser checks are run.


## Part 26 - Player media URL validation follow-up (2026-11-07)

Implemented:
- Closed a validation gap in the player compatibility path: the legacy `server1` field is now validated before it can be passed to the playback engine. An unsafe legacy value no longer bypasses typed-link validation.
- Tightened media URL validation to require an absolute HTTP(S) URL with a hostname. Relative URLs, whitespace/control-containing values, and URLs containing embedded username/password credentials are rejected. This matches the backend's full-URL requirement and avoids resolving untrusted media paths against the frontend origin.
- Kept typed stream-link fallback behavior intact: if the legacy `server1` value is invalid, the player checks the typed `kind=stream` entries; download-only entries remain excluded from playback.
- Strengthened the regression test to assert legacy-field validation and absolute URL parsing.

Validation:
- `node --test frontend/tests/*.test.cjs`: PASS, 17/17.
- JavaScript syntax checks across all frontend JS and CommonJS test files: PASS.
- Go package tests that do not require the unavailable gotd/td dependency: see the validation note in the session handoff.
- Full `go test ./...` remains BLOCKED because this environment cannot download `github.com/gotd/td` and the ZIP has no corresponding `go.sum` entries. No fake dependency checks or production stubs were added.

Not verified:
- Browser playback against real movie records and third-party media hosts.
- Full Go executable build and Telegram MTProto package compilation/live login.

Next:
- Continue the frontend/API integration audit, then complete the remaining operational work: verify/build the gotd adapter in a network-enabled Go environment, finish Telegram remote restore, finish main-to-worker notification credentials and end-to-end worker sync tests, and run full browser/deployment smoke tests.

## Part 27 - Player detail request ordering and payload hardening (2026-11-07)

Implemented:
- Added a monotonically increasing movie-detail request ID. If a user opens another title while an earlier detail request is still pending, the older response is ignored and cannot overwrite the currently selected title's details.
- Stale request failures are also ignored, so a late error from an older title does not show an irrelevant error modal over the newer page.
- Re-checks that the requested title still exists in the local catalog before applying the response.
- Normalizes the movie detail payload defensively: `seasons`, episode arrays, movie links, and episode links must be arrays; malformed optional resource fields become empty arrays rather than throwing during rendering. Episode names and legacy `server1` values are accepted only as strings.
- Added a regression contract test for stale-response guards and defensive detail-resource normalization.

Validation:
- `node --test frontend/tests/*.test.cjs`: PASS, 18/18.
- `node --check` across all frontend and backend JavaScript/CommonJS files: PASS.
- Go test attempt: BLOCKED before tests start because `go.sum` does not contain the required `github.com/gotd/td` module checksums and dependency downloads are unavailable in this environment. No fake module or stub was added.
- Browser E2E against live Firebase/API and real third-party playback hosts was not available in this environment.

Decision:
- Latest user selection wins. A response for an older title is intentionally discarded rather than rendered in the background; this prevents incorrect detail/player state when users tap titles quickly on mobile.

Next:
- Continue the frontend integration audit around account/session transitions and detail/player lifecycle, then address the remaining operational work: real gotd build/live Telegram verification, remote restore verification, main-worker end-to-end replication, and browser/deployment smoke tests.


## Part 28 - Account/session transition and stale catalog isolation (2026-11-07)

Implemented:
- Added an auth-transition generation guard to the Firebase `onAuthStateChanged` flow. If account A initialization is still pending when account B signs in (or the user logs out), account A's late result cannot update the UI, start presence, load data, or sign out account B.
- Account-status checks are now tied to the current UID and auth-transition generation. A delayed status response for an old account is ignored; the in-flight status-check lock is tracked by UID rather than globally blocking a newly signed-in account.
- Blocked/deleted-account handling re-checks that the same Firebase user is still current before deleting the Firebase identity or signing out.
- Logout now stops the user-data refresh interval, invalidates pending catalog requests, clears the in-memory public catalog view, and removes last-page/last-movie navigation state.
- Catalog loading is UID-scoped and request-sequenced. A late catalog response from a previous session cannot overwrite the current session's catalog state.
- My List and watch-progress refreshes now capture the requesting UID and discard responses if the Firebase account changes before both private API calls complete.
- Added regression contract tests for auth-transition guards, account-scoped catalog loading, and private My List/progress isolation.

Validation:
- `node --test frontend/tests/*.test.cjs`: PASS, 21/21.
- `node --check` across all frontend/backend JavaScript and CommonJS files: PASS, 107 files checked.
- No Go source changed in this part; Go tests were not rerun.
- Live Firebase account-switch and browser E2E testing remains unavailable without the configured Firebase project and browser test environment.

Not verified:
- Real rapid account switching (A -> logout -> B) with live Firebase credentials.
- Server-side presence cleanup behavior under an abrupt mobile network loss.

Next:
- Complete operational verification of the gotd/td Telegram adapter and a full production build in a network-enabled Go environment.
- Verify Telegram remote backup/restore (including empty-DB auto-restore and manual restore) against a real test channel.
- Complete main-worker notification, incremental/full replication, and write-proxy end-to-end checks.
- Run browser E2E and production deployment smoke tests against the configured Firebase project, API origin, GitHub Pages origin, and worker hosts.
- Finish final security review and deployment documentation. The remaining work is grouped into roughly 4-5 substantial parts, depending on issues discovered during real integration tests; this is an estimate, not a fixed part count.


## Part 29 - Telegram backup manifest, pin recovery, and retention hardening (2026-11-07)

Implemented:
- Changed Telegram retention ordering so the complete uploaded-message manifest is atomically persisted before pin/unpin/delete operations. A successful upload is no longer left untracked merely because the first pin operation fails.
- Reconciles pins for every message in the retention window on each successful backup rotation. A transient pin failure can therefore be repaired by a later backup run.
- Keeps every not-yet-deleted expired Telegram message ID in the manifest. If deletion fails, the next run retries it instead of silently dropping the ID.
- Writes the final retention manifest only after expired-message deletion succeeds. If the final manifest write fails after deletion, stale IDs remain discoverable; the Telegram delete adapter's already-deleted-message behavior still needs live MTProto verification.
- Validates new and existing manifest entries before Telegram operations: message IDs must be positive, filenames must be plain basenames, and duplicate message IDs are rejected.
- Makes manifest replacement durable by syncing the backup directory after the atomic rename.
- Added regression tests for failed pin recovery, retrying expired-message deletion, unsafe filenames, duplicate message IDs, and pin reconciliation.

Validation:
- `go test ./internal/backup`: PASS.
- `go test -race ./internal/backup`: PASS.
- `go vet ./internal/backup`: PASS.
- `go test ./internal/config`: PASS.
- `go test ./internal/auth`: PASS.
- `go test -tags nodriver ./internal/repo ./internal/server`: PASS.
- `gofmt` applied to modified Go files.

Blocked / not verified:
- `go test ./internal/telegrammtproto` and `go test ./cmd/movex` cannot start because the ZIP does not include `backend/go.sum` entries for `github.com/gotd/td` and `modernc.org/sqlite`.
- `go mod download` was attempted; the environment cannot resolve `proxy.golang.org` (DNS/network unavailable). No fake dependency, checksum, or replacement module was added.
- Real Telegram OTP/2FA login, upload, pin/unpin/delete, FloodWait behavior, and session persistence require a network-enabled Go build plus the configured Telegram API credentials and a test backup channel.
- The MTProto adapter itself was not modified in this part because its dependency-backed build could not be validated safely here.

Next:
- Part 30: finish and harden Telegram restore discovery/download, archive integrity/hash verification, and empty-database automatic restore behavior. Keep restore disabled on any missing/invalid backup and never overwrite a non-empty database automatically.


## Part 30 - Telegram restore discovery, archive integrity, and first-boot safety (2026-11-07)

Implemented:
- Added Telegram restore metadata parsing for the existing backup caption format (`at`, `schema_version`, `uncompressed_bytes`, `sha256`, `rows`). Invalid/missing timestamps, schema versions, byte counts, and SHA-256 values are rejected.
- Added a read-only Telegram restore interface and `RestoreLatestFromTelegram`: discovers the newest MOVEX `.db.gz` document, downloads it into a mode-0600 temporary file, enforces a compressed-size limit, and applies it only through the automatic empty-database restore path.
- Implemented the gotd MTProto adapter's backup discovery and download operations. It scans at most the newest 2,000 messages in the configured backup chat, accepts only MOVEX database documents with valid captions, and resolves the exact message/file again before downloading.
- Wired main-role startup to restore from Telegram only when no manual restore is pending and the database path is missing or zero bytes. A configured Telegram source that is unauthorized, has no backup, or returns invalid data stops startup rather than silently creating a fresh database.
- Strengthened empty-database detection: a missing or zero-byte DB is empty only when no non-empty SQLite WAL or rollback-journal sidecar exists. Any initialized/non-zero DB is protected, even if its application tables contain no rows.
- Added raw SHA-256, uncompressed byte-count, and schema-version checks against Telegram caption metadata. Gzip must be fully readable and pass its checksum; SQLite must pass `PRAGMA integrity_check`, schema-version validation, and required-table checks before replacement.
- Added compressed and decompressed archive size limits, private temporary files, and regression coverage for checksum/size mismatch, invalid metadata, newest-backup selection, no-backup fail-closed behavior, and refusal to overwrite an existing database.
- Extended the local Telegram manifest with `uncompressed_bytes` while retaining the existing `bytes` field for compressed document size.

Validation:
- `go test -race -tags nodriver ./internal/backup`: PASS.
- `go test -race -tags nodriver ./internal/config ./internal/auth ./internal/repo ./internal/server`: PASS.
- `go vet -tags nodriver ./internal/backup ./internal/config ./internal/auth ./internal/repo ./internal/server`: PASS.
- `gofmt` applied to all modified Go files.

Blocked / not verified:
- `go test -tags nodriver ./internal/telegrammtproto ./cmd/movex` cannot start because `backend/go.sum` lacks the gotd/td checksums. `go mod download github.com/gotd/td@v0.120.0` was attempted but DNS/network access to `proxy.golang.org` is unavailable. No fake checksums or replacement modules were added.
- Consequently, the new gotd adapter and startup wiring have not had a local dependency-backed compile. The implementation follows the gotd v0.120 query iterator and downloader APIs, but must be compiled in a network-enabled Go environment before deployment.
- Real Telegram channel history, message download, session authorization, FloodWait, gzip transfer, and first-boot restore have not been live-tested with credentials.
- SQLite-backed restore tests with `modernc.org/sqlite` could not run in this environment; restore safety and workflow tests use the package's injectable snapshot opener.

Next:
- Part 31: finish the remaining replication/operational integration work, verify the gotd adapter and main binary in a network-enabled Go environment, and test Telegram restore against a dedicated test backup channel before production rollout.


## Part 31 - Replication cursor recovery and worker batch validation (2026-11-07)

Implemented:
- Hardened the worker sync client to reject unsafe MAIN_URL values containing URL credentials, query strings, or fragments; worker keys are trimmed before use.
- Centralized the requested incremental batch size and validate every response before it reaches the local store.
- Rejects malformed replication responses: missing epoch, negative maximum sequence, missing full snapshot, mixed full/incremental payloads, oversized change arrays, unsupported tables/operations, empty row IDs, missing upsert data, unexpected delete data, unordered/out-of-range sequences, and incomplete short batches.
- Prevents an empty incremental response from being applied when the main advertises a sequence beyond the worker cursor; a full snapshot is required instead.
- Changed the main replication query to cap returned rows at the exact advertised maximum sequence, preventing a concurrent later write from leaking into a batch whose `max_seq` is older.
- Updated the main sequence high-water mark to include SQLite's `sqlite_sequence`, so deleting/pruning the tail of the change log does not make the main appear to have sequence zero.
- Detects missing change-log tails and cursors ahead of the main, and requests a full snapshot instead of silently leaving a worker permanently behind.
- Rejects negative repository replication cursors.
- Added regression tests for malformed worker responses, unsafe main URLs, missing change-log history, and negative cursors.

Validation:
- `go test -count=1 -race -tags nodriver ./internal/replica ./internal/repo ./internal/server ./internal/config ./internal/auth ./internal/backup`: PASS.
- `go vet -tags nodriver ./internal/replica ./internal/repo ./internal/server ./internal/config ./internal/auth ./internal/backup`: PASS.
- `gofmt` applied to all modified Go files.

Blocked / not verified:
- The SQLite-backed repo regression tests are present but cannot be executed without the normal SQLite build: `go test -race ./internal/repo` is blocked because `go.sum` lacks the `modernc.org/sqlite` checksum, and dependency downloads are unavailable in this environment.
- `go test -race -tags nodriver ./...` is blocked when it reaches `internal/telegrammtproto`, because the gotd/td checksums are missing. The selected packages listed above compile and pass with `nodriver`.
- No live main-worker deployment was available, so polling behavior, large full snapshots, real database pruning/recovery, and worker discovery under network failures still require integration testing.
- Main-to-worker immediate notification is still not wired end-to-end; workers remain protected by periodic polling. Worker status currently reflects the main's sync response handling rather than a separate post-apply acknowledgement. These are retained for the next operational integration part rather than storing plaintext worker secrets.

Next:
- Part 32: complete replication operational status/acknowledgement and notification design without persisting plaintext worker keys; then continue network-enabled gotd build and real Telegram restore verification.

## Part 32 - Worker post-commit acknowledgements and authenticated push wake-ups (2026-11-07)

Implemented:
- Added migration `005_worker_ack.sql` with a `last_epoch` field for each registered worker. Existing worker records migrate with an empty epoch and are not treated as current until they acknowledge the active main epoch.
- Removed the incorrect progress update from `GET /internal/sync`. Sending a batch no longer counts as a successful worker sync.
- Added authenticated `POST /internal/ack`. A worker reports its epoch and committed sequence only after `ApplyReplicationBatch` completes successfully. The main validates the current epoch, non-negative sequence, and main high-water mark before updating worker status.
- Updated the worker sync client to read its committed cursor after applying the batch and send the acknowledgement. If acknowledgement fails, the local transaction remains committed; the next polling attempt safely retries the acknowledgement using the now-current cursor.
- Worker status is now tied to `last_epoch`; `/api/v1/servers` excludes workers whose last acknowledged epoch is stale after a main reset/restore.
- Added optional `SYNC_NOTIFY_KEY`, a separate shared random secret (minimum 32 characters) configured on main and workers. The main sends debounced wake-up requests when its replication epoch or sequence changes. The worker validates this secret with constant-time comparison and schedules an immediate pull.
- Kept periodic worker polling as the fallback when `SYNC_NOTIFY_KEY` is unset or a push request fails. Per-worker `WORKER_KEY` remains stored only as a hash and is never used to send notifications.
- Added tests for post-commit acknowledgement, acknowledgement failure after local commit, stale epoch rejection, worker discovery filtering, notification authentication, and main-to-worker notification request construction.
- Updated `.env.example`, README, deployment documentation, and this progress log.

Validation:
- `go test -count=1 -race -tags nodriver ./internal/replica ./internal/repo ./internal/server ./internal/config ./internal/auth ./internal/backup`: PASS.
- `go vet -tags nodriver ./internal/replica ./internal/repo ./internal/server ./internal/config ./internal/auth ./internal/backup`: PASS.
- `go test -count=1 -tags nodriver ./internal/db`: PASS (migration filename/order and embedded migration checks).
- `go vet -tags nodriver ./internal/db`: PASS.
- `gofmt` applied to all modified Go files.

Blocked / not verified:
- `go test -tags nodriver ./...` remains blocked before completing because the archive does not contain required `go.sum` entries for `github.com/gotd/td`; this environment cannot resolve the Go module proxy. No fake checksum or dependency stub was added.
- The selected package tests use the repository's `nodriver` test path. Actual SQLite-driver-backed migration/application tests and a complete executable build still require the normal dependency downloads.
- No live main/worker deployment was available. Real HTTPS notification delivery, shared-secret deployment, full snapshot apply/ack, worker discovery after a real restore, and recovery under worker network outages remain to be verified in deployment.
- Real gotd build, Telegram login, remote restore, and Telegram backup lifecycle remain blocked on the same dependency/network/credential requirements.

Next:
- Part 33: final integration and security pass. Revisit worker sync/notification behavior under concurrent updates and epoch changes, finish remaining Telegram adapter build/restore checks where dependencies permit, review deployment/env documentation, and run the broadest available regression suite. Keep live Telegram, browser, and production deployment checks explicitly marked as external verification until actually performed.


## Part 33 - Final integration review and notification retry hardening (2026-11-07)

Implemented:
- Completed a final code-path review of worker synchronization, post-commit acknowledgements, epoch handling, notification authentication, startup behavior, and deployment configuration.
- Fixed a notification delivery gap: previously, the main server advanced its observed epoch/sequence before sending worker wake-ups. If a worker notification failed, that worker was not retried until another catalog change occurred. Failed deliveries are now retained in an in-memory pending queue and retried with bounded exponential backoff (1s, 2s, 4s, ... up to 1 minute).
- Refreshes pending worker URLs when the catalog epoch/sequence changes and removes pending deliveries for workers that have been deleted.
- Successful notification delivery removes the pending item. Failed delivery remains non-fatal because the worker's configured periodic polling is still the final recovery path.
- Added a unit test for the notification retry backoff bounds.
- Updated deployment documentation to explain retry behavior and the polling fallback.

Validation:
- `go test -count=1 -race -tags nodriver ./internal/replica ./internal/repo ./internal/server ./internal/config ./internal/auth ./internal/backup ./internal/db ./internal/admin ./internal/httpx ./internal/presence ./internal/taxonomy`: PASS.
- `go vet -tags nodriver` for the same packages: PASS.
- `node --test frontend/tests/*.test.cjs`: PASS, 21/21.
- `gofmt` applied to changed Go files.
- ZIP integrity check: PASS.

Blocked / not verified:
- `go test -count=1 -race -tags nodriver ./...` is blocked at `internal/telegrammtproto` because `go.sum` lacks required `github.com/gotd/td` entries. The environment cannot download modules from the Go proxy; no fake checksums or dependency stubs were introduced.
- The `modernc.org/sqlite`-backed test path and full production executable build still require normal dependency downloads in a network-enabled Go environment.
- Real Telegram OTP/2FA, backup upload/pin/retention, remote restore, and Telegram session persistence were not live-tested because Telegram credentials/channel access and a network-enabled build are unavailable here.
- No live main/worker deployment was available. HTTPS push delivery, retry recovery, full snapshot commit/ack, worker discovery after restore, and failure recovery must still be verified against deployed instances.
- Browser E2E, Firebase account flows, GitHub Pages deployment, and backend-host deployment remain external checks. The frontend GitHub Pages workflow does not deploy the Go backend.

Final handoff:
- Code-side work covered by the available offline test paths is complete through Part 33.
- Production readiness is not certified until the blocked dependency build and external Telegram, main/worker, browser, and deployment checks above pass.
- Do not treat an unverified live integration as complete; record and fix any issues found during deployment testing.
