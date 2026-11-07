package repo

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// maxSessionsPerAdmin bounds the table: signing in an 11th time drops the oldest session of that admin.
const maxSessionsPerAdmin = 10

type AdminSession struct {
	UID       string
	CreatedAt int64
	ExpiresAt int64
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// CreateAdminSession starts a session and returns its secret id (shown to the browser once, never stored).
func (r *Repo) CreateAdminSession(ctx context.Context, uid, ip, ua string, ttl time.Duration) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw[:])
	now := r.nowMs()
	err := r.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO admin_sessions(id_hash, uid, created_at, expires_at, ip, ua) VALUES (?,?,?,?,?,?)`,
			sha256hex(id), uid, now, now+ttl.Milliseconds(), clip(ip, 64), clip(ua, 200)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM admin_sessions WHERE uid = ? AND id_hash NOT IN
			(SELECT id_hash FROM admin_sessions WHERE uid = ? ORDER BY created_at DESC, id_hash LIMIT ?)`, uid, uid, maxSessionsPerAdmin)
		return err
	})
	return id, err
}

// GetAdminSession returns the live session for a secret id (ErrNotFound when unknown, logged out or expired).
func (r *Repo) GetAdminSession(ctx context.Context, id string) (*AdminSession, error) {
	var s AdminSession
	err := r.db.QueryRowContext(ctx, `SELECT uid, created_at, expires_at FROM admin_sessions WHERE id_hash = ? AND expires_at > ?`,
		sha256hex(id), r.nowMs()).Scan(&s.UID, &s.CreatedAt, &s.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &s, err
}

func (r *Repo) DeleteAdminSession(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE id_hash = ?`, sha256hex(id))
	return err
}

// DeleteAdminSessionsOf signs an admin out everywhere.
func (r *Repo) DeleteAdminSessionsOf(ctx context.Context, uid string) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE uid = ?`, uid)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneAdminSessions removes expired sessions.
func (r *Repo) PruneAdminSessions(ctx context.Context) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE expires_at <= ?`, r.nowMs())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
