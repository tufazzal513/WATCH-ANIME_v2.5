-- Server-side admin sessions (main only, never replicated). The browser holds a random id; only its SHA-256 is
-- stored here, so a copy of the database cannot be turned into a logged-in browser. Logout deletes the row.
CREATE TABLE admin_sessions (
    id_hash    TEXT PRIMARY KEY,
    uid        TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    ip         TEXT NOT NULL DEFAULT '',
    ua         TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;
CREATE INDEX admin_sessions_uid ON admin_sessions(uid, created_at);
CREATE INDEX admin_sessions_exp ON admin_sessions(expires_at);
