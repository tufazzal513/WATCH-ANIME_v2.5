-- Private / operational data. Main only: NOT replicated, so there are deliberately NO change-log triggers here.
-- Timestamps are unix milliseconds.

CREATE TABLE users (
    uid           TEXT PRIMARY KEY,                       -- Firebase uid
    email         TEXT NOT NULL DEFAULT '',
    name          TEXT NOT NULL DEFAULT 'User',
    photo_url     TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','frozen','deleted')),
    created_at    INTEGER NOT NULL,
    last_login_at INTEGER NOT NULL DEFAULT 0,
    last_seen     INTEGER NOT NULL DEFAULT 0,             -- persisted at most every few minutes; live presence is in memory
    deleted_at    INTEGER,
    updated_at    INTEGER NOT NULL
);
CREATE INDEX users_created ON users(created_at);
CREATE INDEX users_status ON users(status);

CREATE TABLE my_list (
    uid      TEXT NOT NULL REFERENCES users(uid) ON DELETE CASCADE,
    movie_id TEXT NOT NULL REFERENCES movies(id) ON DELETE CASCADE,
    added_at INTEGER NOT NULL,
    PRIMARY KEY (uid, movie_id)
) WITHOUT ROWID;
CREATE INDEX my_list_added ON my_list(uid, added_at);

-- Resume point. season = the season NAME as the player knows it, ep = episode index (both NULL for a movie).
CREATE TABLE watch_progress (
    uid        TEXT NOT NULL REFERENCES users(uid) ON DELETE CASCADE,
    movie_id   TEXT NOT NULL REFERENCES movies(id) ON DELETE CASCADE,
    season     TEXT,
    ep         INTEGER,
    t          INTEGER NOT NULL CHECK (t >= 0),
    d          INTEGER NOT NULL CHECK (d > 0),
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (uid, movie_id)
) WITHOUT ROWID;
CREATE INDEX watch_progress_recent ON watch_progress(uid, updated_at);

-- One report per title (a new report on the same title refreshes it and bumps `reports`).
CREATE TABLE broken_links (
    id           TEXT PRIMARY KEY,
    movie_id     TEXT NOT NULL UNIQUE REFERENCES movies(id) ON DELETE CASCADE,
    ep_info      TEXT NOT NULL DEFAULT '',
    server_type  TEXT NOT NULL DEFAULT '',
    url          TEXT NOT NULL DEFAULT '',
    reporter_uid TEXT NOT NULL DEFAULT '',
    reports      INTEGER NOT NULL DEFAULT 1,
    status       TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','resolved')),
    reported_at  INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);
CREATE INDEX broken_links_status ON broken_links(status, reported_at);

-- Client error reports. Rows are only ever inserted (write-once); `data` is the whole JSON, at most 4000 bytes.
CREATE TABLE error_logs (
    id         TEXT PRIMARY KEY,
    uid        TEXT NOT NULL DEFAULT '',
    panel      TEXT NOT NULL DEFAULT 'user',
    message    TEXT NOT NULL DEFAULT '',
    data       TEXT NOT NULL CHECK (length(CAST(data AS BLOB)) <= 4000),
    created_at INTEGER NOT NULL
);
CREATE INDEX error_logs_created ON error_logs(created_at);

CREATE TABLE search_stats (
    term    TEXT PRIMARY KEY,
    count   INTEGER NOT NULL DEFAULT 1,
    last_at INTEGER NOT NULL
) WITHOUT ROWID;
CREATE INDEX search_stats_count ON search_stats(count);

-- plays: +1 per counted view; lists: how many users have the title in My List.
CREATE TABLE movie_stats (
    movie_id TEXT PRIMARY KEY REFERENCES movies(id) ON DELETE CASCADE,
    plays    INTEGER NOT NULL DEFAULT 0 CHECK (plays >= 0),
    lists    INTEGER NOT NULL DEFAULT 0 CHECK (lists >= 0)
) WITHOUT ROWID;

-- Password-reset requests for rate limiting. email_key = sha256(lower-cased email), never the address itself.
CREATE TABLE pwd_reset_attempts (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    email_key TEXT NOT NULL,
    ts        INTEGER NOT NULL
);
CREATE INDEX pwd_reset_email ON pwd_reset_attempts(email_key, ts);
CREATE INDEX pwd_reset_ts ON pwd_reset_attempts(ts);

CREATE TABLE audit_log (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    ts        INTEGER NOT NULL,
    actor_uid TEXT NOT NULL,
    action    TEXT NOT NULL,
    target    TEXT NOT NULL DEFAULT '',
    detail    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX audit_ts ON audit_log(ts);

-- Read replicas. Only the SHA-256 of the key is stored; the key itself is shown once when the worker is added.
CREATE TABLE workers (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    key_hash     TEXT NOT NULL UNIQUE,
    url          TEXT NOT NULL,
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL DEFAULT 0,
    last_seq     INTEGER NOT NULL DEFAULT 0
);
