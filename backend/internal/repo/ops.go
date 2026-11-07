package repo

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func sha256hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ---------- movie stats ----------

type MovieStat struct{ Plays, Lists int }

// AllMovieStats returns the counters of every title that has any (the user app uses them for the "hot" ranking).
func (r *Repo) AllMovieStats(ctx context.Context) (map[string]MovieStat, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT movie_id, plays, lists FROM movie_stats`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]MovieStat{}
	for rows.Next() {
		var id string
		var s MovieStat
		if err := rows.Scan(&id, &s.Plays, &s.Lists); err != nil {
			return nil, err
		}
		out[id] = s
	}
	return out, rows.Err()
}

// IncPlays counts one view of a title (ErrNotFound when the title does not exist).
func (r *Repo) IncPlays(ctx context.Context, movieID string) error {
	return r.inTx(ctx, func(tx *sql.Tx) error {
		if err := r.mustExist(ctx, tx, movieID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO movie_stats(movie_id, plays, lists) VALUES (?, 1, 0)
			ON CONFLICT(movie_id) DO UPDATE SET plays = plays + 1`, movieID)
		return err
	})
}

func (r *Repo) TotalPlays(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(plays), 0) FROM movie_stats`).Scan(&n)
	return n, err
}

// ---------- search stats ----------

type SearchStat struct {
	Term   string
	Count  int
	LastAt int64
}

// NormalizeSearchTerm lower-cases, trims and collapses spaces; terms must be 2-40 characters.
func NormalizeSearchTerm(t string) (string, error) {
	t = strings.Join(strings.Fields(strings.ToLower(t)), " ")
	if n := len([]rune(t)); n < 2 || n > 40 {
		return "", invalid("term", "must be 2-40 characters")
	}
	return t, nil
}

// HitSearch counts one search for the term.
func (r *Repo) HitSearch(ctx context.Context, term string) error {
	term, err := NormalizeSearchTerm(term)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO search_stats(term, count, last_at) VALUES (?, 1, ?)
		ON CONFLICT(term) DO UPDATE SET count = count + 1, last_at = excluded.last_at`, term, r.nowMs())
	return err
}

// TopSearches returns the most searched terms (limit 1-100).
func (r *Repo) TopSearches(ctx context.Context, limit int) ([]SearchStat, error) {
	if limit < 1 || limit > 100 {
		limit = 12
	}
	rows, err := r.db.QueryContext(ctx, `SELECT term, count, last_at FROM search_stats ORDER BY count DESC, last_at DESC, term LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchStat
	for rows.Next() {
		var s SearchStat
		if err := rows.Scan(&s.Term, &s.Count, &s.LastAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SuggestSearches returns popular terms for the search box: those starting with prefix (any term when prefix is
// empty) that at least minCount people searched, most searched first. A minimum above 1 keeps one-off queries
// (which may contain personal names) out of what other users see.
func (r *Repo) SuggestSearches(ctx context.Context, prefix string, minCount, limit int) ([]SearchStat, error) {
	if limit < 1 || limit > 50 {
		limit = 12
	}
	prefix = strings.Join(strings.Fields(strings.ToLower(prefix)), " ")
	rows, err := r.db.QueryContext(ctx, `SELECT term, count, last_at FROM search_stats WHERE count >= ? AND term LIKE ? ESCAPE '\'
		ORDER BY count DESC, last_at DESC, term LIMIT ?`, minCount, likeEscape(prefix)+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchStat
	for rows.Next() {
		var s SearchStat
		if err := rows.Scan(&s.Term, &s.Count, &s.LastAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ---------- broken links ----------

type BrokenLink struct {
	ID          string
	MovieID     string
	Title       string // from movies
	PosterURL   string
	ContentType string
	EpInfo      string
	ServerType  string
	URL         string
	ReporterUID string
	Reports     int
	Status      string // "open" or "resolved"
	ReportedAt  int64
}

type BrokenLinkInput struct {
	MovieID     string
	EpInfo      string // e.g. "Season 1 · Episode 3" or "Movie"
	ServerType  string
	URL         string
	ReporterUID string
}

// ReportBrokenLink records a report. There is one row per title: a new report refreshes it, bumps the
// counter and re-opens it (same as the old `broken_links/<movieId>` node, which was simply overwritten).
func (r *Repo) ReportBrokenLink(ctx context.Context, in BrokenLinkInput) error {
	var err error
	if in.EpInfo, err = trimLen("epInfo", in.EpInfo, 120); err != nil {
		return err
	}
	if in.ServerType, err = trimLen("serverType", in.ServerType, 40); err != nil {
		return err
	}
	in.URL = strings.TrimSpace(in.URL)
	if len(in.URL) > 2000 {
		return invalid("url", "is too long")
	}
	return r.inTx(ctx, func(tx *sql.Tx) error {
		if err := r.mustExist(ctx, tx, in.MovieID); err != nil {
			return err
		}
		now := r.nowMs()
		_, err := tx.ExecContext(ctx, `INSERT INTO broken_links(id, movie_id, ep_info, server_type, url, reporter_uid, reports, status, reported_at, updated_at)
			VALUES (?,?,?,?,?,?,1,'open',?,?)
			ON CONFLICT(movie_id) DO UPDATE SET ep_info = excluded.ep_info, server_type = excluded.server_type, url = excluded.url,
				reporter_uid = excluded.reporter_uid, reports = reports + 1, status = 'open', reported_at = excluded.reported_at, updated_at = excluded.updated_at`,
			r.newID(), in.MovieID, in.EpInfo, in.ServerType, in.URL, in.ReporterUID, now, now)
		return err
	})
}

type BrokenListOpts struct {
	Query       string // title or episode info
	ContentType string // "movie" or "series" or ""
	Status      string // "open", "resolved" or "" (all)
	Offset      int
	Limit       int
}

func (r *Repo) ListBrokenLinks(ctx context.Context, o BrokenListOpts) ([]BrokenLink, int, error) {
	var where []string
	var args []any
	if q := strings.ToLower(strings.TrimSpace(o.Query)); q != "" {
		like := "%" + likeEscape(q) + "%"
		where = append(where, `(lower(m.title) LIKE ? ESCAPE '\' OR lower(b.ep_info) LIKE ? ESCAPE '\')`)
		args = append(args, like, like)
	}
	if o.ContentType != "" {
		where, args = append(where, `m.content_type = ?`), append(args, o.ContentType)
	}
	if o.Status != "" {
		where, args = append(where, `b.status = ?`), append(args, o.Status)
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}
	from := ` FROM broken_links b JOIN movies m ON m.id = b.movie_id` + clause
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*)`+from, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := o.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `SELECT b.id, b.movie_id, m.title, m.poster_url, m.content_type, b.ep_info, b.server_type, b.url,
		b.reporter_uid, b.reports, b.status, b.reported_at`+from+` ORDER BY b.reported_at DESC, b.id LIMIT ? OFFSET ?`,
		append(args, limit, max(o.Offset, 0))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []BrokenLink
	for rows.Next() {
		var b BrokenLink
		if err := rows.Scan(&b.ID, &b.MovieID, &b.Title, &b.PosterURL, &b.ContentType, &b.EpInfo, &b.ServerType, &b.URL,
			&b.ReporterUID, &b.Reports, &b.Status, &b.ReportedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, b)
	}
	return out, total, rows.Err()
}

func (r *Repo) CountOpenBrokenLinks(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM broken_links WHERE status = 'open'`).Scan(&n)
	return n, err
}

func (r *Repo) ResolveBrokenLink(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `UPDATE broken_links SET status = 'resolved', updated_at = ? WHERE id = ?`, r.nowMs(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repo) DeleteBrokenLink(ctx context.Context, id string) error {
	return r.deleteByID(ctx, "broken_links", id)
}

// ClearBrokenLinks removes the report of a title (the old panel did this whenever a title was saved after editing).
func (r *Repo) ClearBrokenLinks(ctx context.Context, movieID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM broken_links WHERE movie_id = ?`, movieID)
	return err
}

// ---------- error logs (write-once) ----------

type ErrorLog struct {
	ID      string
	UID     string
	Panel   string
	Message string
	Data    string // the stored JSON
	At      int64
}

const maxErrorBytes = 4000

// LogError stores a client error report. The row can never be changed afterwards. The JSON of the whole
// report (payload + ts + panel) must be at most 4000 bytes, like the old rule.
func (r *Repo) LogError(ctx context.Context, uid, panel string, payload map[string]any) (string, error) {
	if panel != "user" && panel != "admin" {
		return "", invalid("panel", "must be user or admin")
	}
	doc := make(map[string]any, len(payload)+2)
	for k, v := range payload {
		doc[k] = v
	}
	now := r.nowMs()
	doc["ts"], doc["panel"] = now, panel
	b, err := json.Marshal(doc)
	if err != nil {
		return "", invalid("payload", "is not valid JSON")
	}
	if len(b) > maxErrorBytes {
		return "", invalid("payload", "is too large (max %d bytes)", maxErrorBytes)
	}
	msg, _ := payload["message"].(string)
	if rs := []rune(msg); len(rs) > 300 {
		msg = string(rs[:300])
	}
	id := r.newID()
	_, err = r.db.ExecContext(ctx, `INSERT INTO error_logs(id, uid, panel, message, data, created_at) VALUES (?,?,?,?,?,?)`, id, uid, panel, msg, string(b), now)
	return id, err
}

func (r *Repo) RecentErrors(ctx context.Context, limit int) ([]ErrorLog, error) {
	if limit < 1 || limit > 500 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, uid, panel, message, data, created_at FROM error_logs ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ErrorLog
	for rows.Next() {
		var e ErrorLog
		if err := rows.Scan(&e.ID, &e.UID, &e.Panel, &e.Message, &e.Data, &e.At); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *Repo) CountErrorsSince(ctx context.Context, since time.Time) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM error_logs WHERE created_at >= ?`, since.UnixMilli()).Scan(&n)
	return n, err
}

// PruneErrors keeps only the newest `keep` rows and returns how many were removed.
func (r *Repo) PruneErrors(ctx context.Context, keep int) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM error_logs WHERE id NOT IN (SELECT id FROM error_logs ORDER BY created_at DESC, id DESC LIMIT ?)`, keep)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountActiveUsersSince counts non-deleted users whose last-seen or last-login timestamp is within the window.
func (r *Repo) CountActiveUsersSince(ctx context.Context, since time.Time) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users
		WHERE status <> 'deleted' AND MAX(last_seen, last_login_at) >= ?`, since.UnixMilli()).Scan(&n)
	return n, err
}

// ---------- password reset rate limit ----------

// ResetKey turns an email address into the key stored for rate limiting (the address itself is never stored).
func ResetKey(email string) string { return sha256hex(strings.ToLower(strings.TrimSpace(email))) }

// CountResetAttempts counts requests for this key within the window.
func (r *Repo) CountResetAttempts(ctx context.Context, key string, window time.Duration) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pwd_reset_attempts WHERE email_key = ? AND ts > ?`, key, r.nowMs()-window.Milliseconds()).Scan(&n)
	return n, err
}

func (r *Repo) AddResetAttempt(ctx context.Context, key string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO pwd_reset_attempts(email_key, ts) VALUES (?, ?)`, key, r.nowMs())
	return err
}

// ResetStats returns the total number of reset requests and those in the last 24 hours (dashboard cards).
func (r *Repo) ResetStats(ctx context.Context) (total, last24h int, err error) {
	err = r.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(ts > ?), 0) FROM pwd_reset_attempts`, r.nowMs()-24*time.Hour.Milliseconds()).Scan(&total, &last24h)
	return
}

// PruneResetAttempts deletes attempts older than the given age.
func (r *Repo) PruneResetAttempts(ctx context.Context, olderThan time.Duration) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM pwd_reset_attempts WHERE ts < ?`, r.nowMs()-olderThan.Milliseconds())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---------- audit log ----------

type AuditEntry struct {
	ID       int64
	TS       int64
	ActorUID string
	Action   string
	Target   string
	Detail   string
}

// Audit records an admin action ("movie.delete", "user.freeze", ...). Long details are cut, never rejected.
func (r *Repo) Audit(ctx context.Context, actorUID, action, target, detail string) error {
	cut := func(s string, n int) string {
		if rs := []rune(s); len(rs) > n {
			return string(rs[:n])
		}
		return s
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO audit_log(ts, actor_uid, action, target, detail) VALUES (?,?,?,?,?)`,
		r.nowMs(), cut(actorUID, 128), cut(action, 60), cut(target, 200), cut(detail, 1000))
	return err
}

func (r *Repo) RecentAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit < 1 || limit > 500 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, ts, actor_uid, action, target, detail FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var a AuditEntry
		if err := rows.Scan(&a.ID, &a.TS, &a.ActorUID, &a.Action, &a.Target, &a.Detail); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---------- workers ----------

type Worker struct {
	ID         string
	Name       string
	URL        string
	CreatedAt  int64
	LastSeenAt int64
	LastSeq    int64
	LastEpoch  string
}

const workerCols = `id, name, url, created_at, last_seen_at, last_seq, last_epoch`

func scanWorker(s rowScanner) (*Worker, error) {
	var w Worker
	err := s.Scan(&w.ID, &w.Name, &w.URL, &w.CreatedAt, &w.LastSeenAt, &w.LastSeq, &w.LastEpoch)
	return &w, err
}

// CreateWorker registers a worker and returns its secret key. The key is returned ONCE; only its hash is stored.
func (r *Repo) CreateWorker(ctx context.Context, name, url string) (*Worker, string, error) {
	name, err := trimLen("name", name, 60)
	if err != nil {
		return nil, "", err
	}
	if name == "" {
		return nil, "", invalid("name", "is required")
	}
	if url, err = cleanURL("url", strings.TrimRight(url, "/"), true); err != nil {
		return nil, "", err
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, "", err
	}
	key := "mxw_" + hex.EncodeToString(raw[:])
	id := r.newID()
	if _, err := r.db.ExecContext(ctx, `INSERT INTO workers(id, name, key_hash, url, created_at) VALUES (?,?,?,?,?)`, id, name, sha256hex(key), url, r.nowMs()); err != nil {
		return nil, "", err
	}
	w, err := r.getWorker(ctx, id)
	return w, key, err
}

func (r *Repo) getWorker(ctx context.Context, id string) (*Worker, error) {
	w, err := scanWorker(r.db.QueryRowContext(ctx, `SELECT `+workerCols+` FROM workers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return w, err
}

// WorkerByKey finds the worker that owns a key (ErrNotFound for an unknown key).
func (r *Repo) WorkerByKey(ctx context.Context, key string) (*Worker, error) {
	if key == "" {
		return nil, ErrNotFound
	}
	got := sha256hex(key)
	var stored string
	err := r.db.QueryRowContext(ctx, `SELECT key_hash FROM workers WHERE key_hash = ?`, got).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(stored)) != 1 {
		return nil, ErrNotFound
	}
	return scanWorker(r.db.QueryRowContext(ctx, `SELECT `+workerCols+` FROM workers WHERE key_hash = ?`, stored))
}

func (r *Repo) ListWorkers(ctx context.Context) ([]Worker, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+workerCols+` FROM workers ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Worker
	for rows.Next() {
		w, err := scanWorker(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

// AcknowledgeWorker records progress only after the worker has committed its local batch.
// Epoch changes reset the sequence because a full snapshot may restart the main's history.
func (r *Repo) AcknowledgeWorker(ctx context.Context, id, epoch string, seq int64) error {
	if strings.TrimSpace(epoch) == "" || seq < 0 {
		return invalid("ack", "invalid replication epoch or sequence")
	}
	currentEpoch, err := r.EnsureEpoch(ctx)
	if err != nil {
		return err
	}
	if epoch != currentEpoch {
		return invalid("epoch", "acknowledgement belongs to a stale replication epoch")
	}
	maxSeq, err := r.MaxSeq(ctx)
	if err != nil {
		return err
	}
	if seq > maxSeq {
		return invalid("seq", "acknowledgement is ahead of the main server")
	}
	res, err := r.db.ExecContext(ctx, `UPDATE workers SET last_seen_at = ?, last_seq = CASE WHEN last_epoch = ? THEN MAX(last_seq, ?) ELSE ? END, last_epoch = ? WHERE id = ?`, r.nowMs(), epoch, seq, seq, epoch, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repo) DeleteWorker(ctx context.Context, id string) error {
	return r.deleteByID(ctx, "workers", id)
}

// ---------- meta (small internal key/value) ----------

func (r *Repo) GetMeta(ctx context.Context, k string) (string, bool, error) {
	var v string
	err := r.db.QueryRowContext(ctx, `SELECT v FROM meta WHERE k = ?`, k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

func (r *Repo) SetMeta(ctx context.Context, k, v string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO meta(k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, k, v)
	return err
}
