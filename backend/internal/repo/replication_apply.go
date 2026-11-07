package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	replicaEpochKey = "replica_epoch"
	replicaSeqKey   = "replica_seq"
)

// ReplicaCursor returns the epoch and last fully applied sequence stored by a worker.
func (r *Repo) ReplicaCursor(ctx context.Context) (string, int64, error) {
	var epoch, seqText string
	err := r.db.QueryRowContext(ctx, `SELECT v FROM meta WHERE k=?`, replicaEpochKey).Scan(&epoch)
	if errors.Is(err, sql.ErrNoRows) {
		epoch = ""
	} else if err != nil {
		return "", 0, err
	}
	err = r.db.QueryRowContext(ctx, `SELECT v FROM meta WHERE k=?`, replicaSeqKey).Scan(&seqText)
	if errors.Is(err, sql.ErrNoRows) {
		return epoch, 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	seq, parseErr := strconv.ParseInt(seqText, 10, 64)
	if parseErr != nil || seq < 0 {
		return "", 0, fmt.Errorf("invalid stored replica sequence")
	}
	return epoch, seq, nil
}

// ApplyReplicationBatch applies a main-server batch atomically. Cursor advancement is
// committed in the same transaction as catalog changes, so a crash cannot skip data.
func (r *Repo) ApplyReplicationBatch(ctx context.Context, batch ReplicationBatch) error {
	if strings.TrimSpace(batch.Epoch) == "" || batch.MaxSeq < 0 {
		return errors.New("replication batch has an invalid epoch or sequence")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if batch.Full {
		if batch.Snapshot == nil {
			return errors.New("full replication batch has no snapshot")
		}
		if err := clearReplicaCatalog(ctx, tx); err != nil {
			return err
		}
		if err := applySnapshot(ctx, tx, batch.Snapshot); err != nil {
			return err
		}
	} else {
		currentEpoch, currentSeq, err := r.replicaCursorTx(ctx, tx)
		if err != nil {
			return err
		}
		if currentEpoch == "" || currentEpoch != batch.Epoch {
			return errors.New("incremental batch epoch does not match the local replica; full snapshot required")
		}
		if batch.MaxSeq < currentSeq {
			return errors.New("incremental batch sequence is older than the local replica")
		}
		for i, change := range batch.Changes {
			if change.Seq <= currentSeq {
				return errors.New("replication change does not advance the local cursor")
			}
			if change.Seq <= 0 || change.Seq > batch.MaxSeq || (i > 0 && change.Seq <= batch.Changes[i-1].Seq) {
				return errors.New("replication changes are not strictly ordered")
			}
			if err := applyChange(ctx, tx, change); err != nil {
				return fmt.Errorf("apply replication change %d (%s/%s): %w", change.Seq, change.Table, change.ID, err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM changes`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, replicaEpochKey, batch.Epoch); err != nil {
		return err
	}
	seq := batch.MaxSeq
	if !batch.Full && len(batch.Changes) > 0 {
		seq = batch.Changes[len(batch.Changes)-1].Seq
	}
	if !batch.Full && len(batch.Changes) == 0 {
		_, current, err := r.replicaCursorTx(ctx, tx)
		if err != nil {
			return err
		}
		seq = current
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, replicaSeqKey, fmt.Sprint(seq)); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repo) replicaCursorTx(ctx context.Context, tx *sql.Tx) (string, int64, error) {
	var epoch, raw string
	if err := tx.QueryRowContext(ctx, `SELECT v FROM meta WHERE k=?`, replicaEpochKey).Scan(&epoch); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", 0, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT v FROM meta WHERE k=?`, replicaSeqKey).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return epoch, 0, nil
		}
		return "", 0, err
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return "", 0, errors.New("invalid stored replica sequence")
	}
	return epoch, n, nil
}

func clearReplicaCatalog(ctx context.Context, tx *sql.Tx) error {
	for _, table := range []string{"notifications", "social_links", "banners", "movie_categories", "movie_languages", "links", "episodes", "seasons", "movies", "categories", "settings"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	return nil
}

func applySnapshot(ctx context.Context, tx *sql.Tx, s *ReplicationSnapshot) error {
	for _, c := range s.Categories {
		if err := upsertCategory(ctx, tx, c); err != nil {
			return err
		}
	}
	for _, m := range s.Movies {
		if m == nil {
			return errors.New("snapshot contains a nil movie")
		}
		if err := upsertMovie(ctx, tx, m); err != nil {
			return err
		}
	}
	for _, b := range s.Banners {
		if err := upsertBanner(ctx, tx, b); err != nil {
			return err
		}
	}
	for _, v := range s.SocialLinks {
		if err := upsertSocialLink(ctx, tx, v); err != nil {
			return err
		}
	}
	for _, v := range s.Notifications {
		if err := upsertNotification(ctx, tx, v); err != nil {
			return err
		}
	}
	keys := make([]string, 0, len(s.Settings))
	for k := range s.Settings {
		if k != "pwdResetLimit" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings(k,v,updated_at) VALUES(?,?,?)`, k, s.Settings[k], time.Now().UnixMilli()); err != nil {
			return err
		}
	}
	return nil
}

func applyChange(ctx context.Context, tx *sql.Tx, c ReplicationChange) error {
	if c.ID == "" {
		return errors.New("empty row id")
	}
	if c.Op == "delete" {
		table, key := "", "id"
		switch c.Table {
		case "movies", "categories", "banners", "social_links", "notifications":
			table = c.Table
		case "settings":
			table, key = "settings", "k"
		default:
			return fmt.Errorf("unsupported replicated table %q", c.Table)
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE `+key+`=?`, c.ID)
		return err
	}
	if c.Op != "upsert" {
		return fmt.Errorf("unsupported operation %q", c.Op)
	}
	b, err := json.Marshal(c.Data)
	if err != nil {
		return err
	}
	switch c.Table {
	case "movies":
		var v Movie
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		if v.ID != c.ID {
			return errors.New("movie id mismatch")
		}
		return upsertMovie(ctx, tx, &v)
	case "categories":
		var v Category
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		if v.ID != c.ID {
			return errors.New("category id mismatch")
		}
		return upsertCategory(ctx, tx, v)
	case "banners":
		var v Banner
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		if v.ID != c.ID {
			return errors.New("banner id mismatch")
		}
		return upsertBanner(ctx, tx, v)
	case "social_links":
		var v SocialLink
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		if v.ID != c.ID {
			return errors.New("social link id mismatch")
		}
		return upsertSocialLink(ctx, tx, v)
	case "notifications":
		var v Notification
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		if v.ID != c.ID {
			return errors.New("notification id mismatch")
		}
		return upsertNotification(ctx, tx, v)
	case "settings":
		var v struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		if v.Key != c.ID || v.Key == "pwdResetLimit" {
			return errors.New("invalid or private setting")
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(k,v,updated_at) VALUES(?,?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v,updated_at=excluded.updated_at`, v.Key, v.Value, time.Now().UnixMilli())
		return err
	default:
		return fmt.Errorf("unsupported replicated table %q", c.Table)
	}
}

func upsertCategory(ctx context.Context, tx *sql.Tx, c Category) error {
	if c.ID == "" || strings.TrimSpace(c.Name) == "" {
		return errors.New("invalid category")
	}
	now := time.Now().UnixMilli()
	_, err := tx.ExecContext(ctx, `INSERT INTO categories(id,name,popular,sort,created_at,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,popular=excluded.popular,sort=excluded.sort,updated_at=excluded.updated_at`, c.ID, c.Name, b2i(c.Popular), c.Sort, now, now)
	return err
}

func upsertMovie(ctx context.Context, tx *sql.Tx, m *Movie) error {
	if m.ID == "" || strings.TrimSpace(m.Title) == "" {
		return errors.New("invalid movie")
	}
	created, updated := m.CreatedAt, m.UpdatedAt
	if created <= 0 {
		created = time.Now().UnixMilli()
	}
	if updated <= 0 {
		updated = created
	}
	var release any
	if m.ReleaseTS != nil {
		release = *m.ReleaseTS
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO movies(id,title,content_type,type,industry,year,rating,poster_url,cover_url,cast_text,description,dubbed,is_private,is_upcoming,release_date,release_ts,created_at,updated_at)
	VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET title=excluded.title,content_type=excluded.content_type,type=excluded.type,industry=excluded.industry,year=excluded.year,rating=excluded.rating,poster_url=excluded.poster_url,cover_url=excluded.cover_url,cast_text=excluded.cast_text,description=excluded.description,dubbed=excluded.dubbed,is_private=excluded.is_private,is_upcoming=excluded.is_upcoming,release_date=excluded.release_date,release_ts=excluded.release_ts,updated_at=excluded.updated_at`,
		m.ID, m.Title, m.ContentType, m.Type, m.Industry, m.Year, m.Rating, m.PosterURL, m.CoverURL, m.Cast, m.Description, b2i(m.Dubbed), b2i(m.IsPrivate), b2i(m.IsUpcoming), m.ReleaseDate, release, created, updated)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM links WHERE movie_id=?`, m.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM seasons WHERE movie_id=?`, m.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM movie_categories WHERE movie_id=?`, m.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM movie_languages WHERE movie_id=?`, m.ID); err != nil {
		return err
	}
	for i, genre := range m.Genres {
		var categoryID string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM categories WHERE name=?`, genre).Scan(&categoryID); err != nil {
			return fmt.Errorf("movie genre %q missing on replica: %w", genre, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO movie_categories(movie_id,category_id,pos) VALUES(?,?,?)`, m.ID, categoryID, i); err != nil {
			return err
		}
	}
	for i, language := range m.Languages {
		if _, err := tx.ExecContext(ctx, `INSERT INTO movie_languages(movie_id,language,pos) VALUES(?,?,?)`, m.ID, language, i); err != nil {
			return err
		}
	}
	for _, link := range m.Links {
		if err := insertReplicaLink(ctx, tx, m.ID, "", link, link.Sort, created, updated); err != nil {
			return err
		}
	}
	for si, season := range m.Seasons {
		if season.ID == "" {
			return errors.New("replicated season has empty id")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO seasons(id,movie_id,number,name,created_at,updated_at) VALUES(?,?,?,?,?,?)`, season.ID, m.ID, season.Number, season.Name, created, updated); err != nil {
			return err
		}
		for _, episode := range season.Episodes {
			if episode.ID == "" {
				return errors.New("replicated episode has empty id")
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO episodes(id,season_id,movie_id,number,name,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, episode.ID, season.ID, m.ID, episode.Number, episode.Name, created, updated); err != nil {
				return err
			}
			for _, link := range episode.Links {
				if err := insertReplicaLink(ctx, tx, m.ID, episode.ID, link, link.Sort, created, updated); err != nil {
					return err
				}
			}
		}
		_ = si
	}
	return refreshSearch(ctx, tx, m.ID)
}

func insertReplicaLink(ctx context.Context, tx *sql.Tx, movieID, episodeID string, l Link, sort int, created, updated int64) error {
	if l.ID == "" || l.URL == "" {
		return errors.New("replicated link has empty id or URL")
	}
	var ep any
	if episodeID != "" {
		ep = episodeID
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO links(id,movie_id,episode_id,kind,quality,label,url,sort,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, l.ID, movieID, ep, l.Kind, l.Quality, l.Label, l.URL, sort, created, updated)
	return err
}

func upsertBanner(ctx context.Context, tx *sql.Tx, b Banner) error {
	if b.ID == "" || b.ImageURL == "" {
		return errors.New("invalid banner")
	}
	now := b.CreatedAt
	if now <= 0 {
		now = time.Now().UnixMilli()
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO banners(id,image_url,movie_id,link_url,sort,active,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET image_url=excluded.image_url,movie_id=excluded.movie_id,link_url=excluded.link_url,sort=excluded.sort,active=excluded.active,updated_at=excluded.updated_at`, b.ID, b.ImageURL, nullStr(b.MovieID), b.LinkURL, b.Sort, b2i(b.Active), now, now)
	return err
}
func upsertSocialLink(ctx context.Context, tx *sql.Tx, s SocialLink) error {
	if s.ID == "" || s.Name == "" || s.URL == "" {
		return errors.New("invalid social link")
	}
	now := time.Now().UnixMilli()
	_, err := tx.ExecContext(ctx, `INSERT INTO social_links(id,name,url,icon,sort,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,url=excluded.url,icon=excluded.icon,sort=excluded.sort,updated_at=excluded.updated_at`, s.ID, s.Name, s.URL, s.Icon, s.Sort, now, now)
	return err
}
func upsertNotification(ctx context.Context, tx *sql.Tx, n Notification) error {
	if n.ID == "" || n.Title == "" || n.Message == "" {
		return errors.New("invalid notification")
	}
	now := n.SentAt
	if now <= 0 {
		now = time.Now().UnixMilli()
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO notifications(id,title,message,sent_at,created_at,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET title=excluded.title,message=excluded.message,sent_at=excluded.sent_at,updated_at=excluded.updated_at`, n.ID, n.Title, n.Message, now, now, now)
	return err
}
