package repo

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

type ReplicationChange struct {
	Seq   int64  `json:"seq"`
	Table string `json:"table"`
	ID    string `json:"id"`
	Op    string `json:"op"`
	Data  any    `json:"data,omitempty"`
}

type ReplicationSnapshot struct {
	Movies        []*Movie          `json:"movies"`
	Categories    []Category        `json:"categories"`
	Banners       []Banner          `json:"banners"`
	SocialLinks   []SocialLink      `json:"socialLinks"`
	Notifications []Notification    `json:"notifications"`
	Settings      map[string]string `json:"settings"`
}

type ReplicationBatch struct {
	Epoch    string               `json:"epoch"`
	MaxSeq   int64                `json:"max_seq"`
	Full     bool                 `json:"full"`
	Changes  []ReplicationChange  `json:"changes,omitempty"`
	Snapshot *ReplicationSnapshot `json:"snapshot,omitempty"`
}

func (r *Repo) EnsureEpoch(ctx context.Context) (string, error) {
	v, ok, err := r.GetMeta(ctx, "epoch")
	if err != nil {
		return "", err
	}
	if ok && v != "" {
		return v, nil
	}
	var b [16]byte
	if _, err = rand.Read(b[:]); err != nil {
		return "", err
	}
	v = hex.EncodeToString(b[:])
	if err = r.SetMeta(ctx, "epoch", v); err != nil {
		return "", err
	}
	return v, nil
}

func (r *Repo) ResetEpoch(ctx context.Context) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	v := hex.EncodeToString(b[:])
	return v, r.SetMeta(ctx, "epoch", v)
}

func (r *Repo) ReplicationBatch(ctx context.Context, epoch string, since int64, limit int) (*ReplicationBatch, error) {
	if since < 0 {
		return nil, fmt.Errorf("replication cursor must be non-negative")
	}
	if limit < 1 || limit > 500 {
		limit = 200
	}
	currentEpoch, err := r.EnsureEpoch(ctx)
	if err != nil {
		return nil, err
	}
	maxSeq, err := r.MaxSeq(ctx)
	if err != nil {
		return nil, err
	}
	var minSeq, actualMaxSeq sql.NullInt64
	if err = r.db.QueryRowContext(ctx, `SELECT MIN(seq), MAX(seq) FROM changes`).Scan(&minSeq, &actualMaxSeq); err != nil {
		return nil, err
	}
	// sqlite_sequence preserves the high-water mark even if rows are removed.
	// If the retained rows no longer reach that mark, history has a tail gap and
	// incremental replication cannot safely advance across it.
	missingTail := maxSeq > 0 && (!actualMaxSeq.Valid || actualMaxSeq.Int64 < maxSeq)
	// A cursor ahead of the main, a pruned prefix, or a missing tail all require
	// a full snapshot instead of an empty/incomplete incremental response.
	full := epoch != currentEpoch || since > maxSeq || missingTail ||
		(minSeq.Valid && since < minSeq.Int64-1) || (!minSeq.Valid && since < maxSeq)
	out := &ReplicationBatch{Epoch: currentEpoch, MaxSeq: maxSeq, Full: full}
	if full {
		snap, e := r.replicationSnapshot(ctx)
		if e != nil {
			return nil, e
		}
		out.Snapshot = snap
		return out, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT seq,tbl,row_id,op FROM changes WHERE seq>? AND seq<=? ORDER BY seq LIMIT ?`, since, maxSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c ReplicationChange
		if err := rows.Scan(&c.Seq, &c.Table, &c.ID, &c.Op); err != nil {
			return nil, err
		}
		if c.Op == "upsert" {
			c.Data, err = r.replicationRow(ctx, c.Table, c.ID)
			if errors.Is(err, ErrNotFound) {
				c.Op = "delete"
				c.Data = nil
			} else if err != nil {
				return nil, err
			}
		}
		out.Changes = append(out.Changes, c)
	}
	return out, rows.Err()
}

func (r *Repo) replicationSnapshot(ctx context.Context) (*ReplicationSnapshot, error) {
	s := &ReplicationSnapshot{Settings: map[string]string{}}
	ids, err := queryStrings(ctx, r.db, `SELECT id FROM movies ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		m, e := r.GetMovie(ctx, id)
		if e != nil {
			return nil, e
		}
		s.Movies = append(s.Movies, m)
	}
	if s.Categories, err = r.ListCategories(ctx); err != nil {
		return nil, err
	}
	if s.Banners, err = r.ListBanners(ctx, false); err != nil {
		return nil, err
	}
	if s.SocialLinks, err = r.ListSocialLinks(ctx); err != nil {
		return nil, err
	}
	if s.Notifications, err = r.listAllNotifications(ctx); err != nil {
		return nil, err
	}
	if s.Settings, err = r.PublicSettings(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (r *Repo) listAllNotifications(ctx context.Context) ([]Notification, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,title,message,sent_at FROM notifications ORDER BY sent_at DESC,id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.Title, &n.Message, &n.SentAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func queryStrings(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, query string) ([]string, error) {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *Repo) replicationRow(ctx context.Context, table, id string) (any, error) {
	switch table {
	case "movies":
		return r.GetMovie(ctx, id)
	case "categories":
		return r.GetCategory(ctx, id)
	case "banners":
		return r.GetBanner(ctx, id)
	case "social_links":
		return r.GetSocialLink(ctx, id)
	case "notifications":
		return r.GetNotification(ctx, id)
	case "settings":
		var v string
		var updated int64
		err := r.db.QueryRowContext(ctx, `SELECT v,updated_at FROM settings WHERE k=? AND k!='pwdResetLimit'`, id).Scan(&v, &updated)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"key": id, "value": v, "updatedAt": updated}, nil
	default:
		return nil, fmt.Errorf("unsupported replicated table %q", table)
	}
}

func (r *Repo) PruneChanges(ctx context.Context, keep int, maxAge time.Duration) (int64, error) {
	if keep < 1000 {
		keep = 100000
	}
	if maxAge <= 0 {
		maxAge = 30 * 24 * time.Hour
	}
	cutoff := time.Now().Add(-maxAge).Unix()
	// Retain at least the newest 100k entries OR all entries from the last 30 days.
	// A row is pruned only when it is outside both retention windows.
	res, err := r.db.ExecContext(ctx, `DELETE FROM changes WHERE seq <= COALESCE((SELECT MAX(seq)-? FROM changes),0) AND ts < ?`, keep, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
