package repo

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// User statuses. "frozen" and "deleted" users get 403 on every authenticated request (see Blocked).
const (
	StatusActive  = "active"
	StatusFrozen  = "frozen"
	StatusDeleted = "deleted"
)

// Blocked reports whether a user with this status must be refused by the API.
func Blocked(status string) bool { return status == StatusFrozen || status == StatusDeleted }

type User struct {
	UID         string
	Email       string
	Name        string
	PhotoURL    string
	Status      string
	CreatedAt   int64
	LastLoginAt int64
	LastSeen    int64
	DeletedAt   int64
}

// UserProfile is what the verified Firebase token tells us about a user.
type UserProfile struct {
	UID      string
	Email    string
	Name     string
	PhotoURL string
}

const userCols = `uid, email, name, photo_url, status, created_at, last_login_at, last_seen, COALESCE(deleted_at, 0)`

func scanUser(s rowScanner) (*User, error) {
	var u User
	err := s.Scan(&u.UID, &u.Email, &u.Name, &u.PhotoURL, &u.Status, &u.CreatedAt, &u.LastLoginAt, &u.LastSeen, &u.DeletedAt)
	return &u, err
}

func (r *Repo) GetUser(ctx context.Context, uid string) (*User, error) {
	u, err := scanUser(r.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE uid = ?`, uid))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// UserStatus returns the status of uid ("" and false when the user has no record yet).
func (r *Repo) UserStatus(ctx context.Context, uid string) (string, bool, error) {
	var s string
	err := r.db.QueryRowContext(ctx, `SELECT status FROM users WHERE uid = ?`, uid).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return s, err == nil, err
}

func cleanName(name string) string {
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 80 {
		name = string(r[:80])
	}
	if name == "" {
		name = "User"
	}
	return name
}

func cleanPhoto(u string) string {
	u = strings.TrimSpace(u)
	if len(u) > 2000 || !isHTTPURL(u) || strings.ContainsAny(u, " \t\r\n") {
		return ""
	}
	return u
}

// EnsureUser is called after a successful login. A first login creates an active record (same as the old
// ensureUserRecord); later logins only refresh last_login_at, and only for users who are not frozen/deleted.
func (r *Repo) EnsureUser(ctx context.Context, p UserProfile) (u *User, created bool, err error) {
	if p.UID == "" || len(p.UID) > 128 {
		return nil, false, invalid("uid", "is required")
	}
	now := r.nowMs()
	err = r.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO users(uid, email, name, photo_url, status, created_at, last_login_at, last_seen, updated_at)
			VALUES (?,?,?,?, 'active', ?, ?, ?, ?) ON CONFLICT(uid) DO NOTHING`,
			p.UID, strings.TrimSpace(p.Email), cleanName(p.Name), cleanPhoto(p.PhotoURL), now, now, now, now)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		created = n == 1
		if !created {
			_, err = tx.ExecContext(ctx, `UPDATE users SET last_login_at = ?, updated_at = ? WHERE uid = ? AND status = 'active'`, now, now, p.UID)
		}
		return err
	})
	if err != nil {
		return nil, false, err
	}
	u, err = r.GetUser(ctx, p.UID)
	return u, created, err
}

// UpdateProfile changes the display name and photo of a user (users cannot change email or status themselves).
func (r *Repo) UpdateProfile(ctx context.Context, uid, name, photoURL string) (*User, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 80 {
		return nil, invalid("name", "must be 1-80 characters")
	}
	photoURL = strings.TrimSpace(photoURL)
	if photoURL != "" {
		var err error
		if photoURL, err = cleanURL("photoURL", photoURL, false); err != nil {
			return nil, err
		}
	}
	res, err := r.db.ExecContext(ctx, `UPDATE users SET name = ?, photo_url = ?, updated_at = ? WHERE uid = ?`, name, photoURL, r.nowMs(), uid)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return r.GetUser(ctx, uid)
}

// TouchLastSeen persists the last activity time, but writes at most once per `every` per user.
func (r *Repo) TouchLastSeen(ctx context.Context, uid string, every time.Duration) error {
	now := r.nowMs()
	_, err := r.db.ExecContext(ctx, `UPDATE users SET last_seen = ? WHERE uid = ? AND last_seen < ?`, now, uid, now-every.Milliseconds())
	return err
}

// SetUserStatus is the admin action (freeze / unfreeze / delete). Deleting keeps the row so the user's own
// client sees "deleted" and removes the Firebase account, like the old panel.
func (r *Repo) SetUserStatus(ctx context.Context, uid, status string) error {
	if status != StatusActive && status != StatusFrozen && status != StatusDeleted {
		return invalid("status", "must be active, frozen or deleted")
	}
	now := r.nowMs()
	var del any
	if status == StatusDeleted {
		del = now
	}
	res, err := r.db.ExecContext(ctx, `UPDATE users SET status = ?, deleted_at = ?, updated_at = ? WHERE uid = ?`, status, del, now, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PurgeUser removes a user row and everything attached to it (My List, progress).
func (r *Repo) PurgeUser(ctx context.Context, uid string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM users WHERE uid = ?`, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

type UserListOpts struct {
	Offset, Limit  int
	Query          string   // matches name, email or uid (case-insensitive substring)
	Status         string   // "" = every user except deleted; "active", "frozen" or "deleted"
	JoinedSince    int64    // unix ms; 0 = no filter (used for the "new users" filter)
	OnlyUIDs       []string // restrict to these uids (used for the "live" filter); nil = no restriction
	RestrictToUIDs bool     // set true together with OnlyUIDs so an empty list means "nobody"
}

func (r *Repo) ListUsers(ctx context.Context, o UserListOpts) ([]*User, int, error) {
	var where []string
	var args []any
	switch o.Status {
	case "":
		where = append(where, `status <> 'deleted'`)
	case StatusActive, StatusFrozen, StatusDeleted:
		where, args = append(where, `status = ?`), append(args, o.Status)
	default:
		return nil, 0, invalid("status", "unknown status %q", o.Status)
	}
	if q := strings.ToLower(strings.TrimSpace(o.Query)); q != "" {
		like := "%" + likeEscape(q) + "%"
		where = append(where, `(lower(name) LIKE ? ESCAPE '\' OR lower(email) LIKE ? ESCAPE '\' OR lower(uid) LIKE ? ESCAPE '\')`)
		args = append(args, like, like, like)
	}
	if o.JoinedSince > 0 {
		where, args = append(where, `created_at >= ?`), append(args, o.JoinedSince)
	}
	if o.RestrictToUIDs {
		if len(o.OnlyUIDs) == 0 {
			return nil, 0, nil
		}
		where = append(where, `uid IN (`+placeholders(len(o.OnlyUIDs))+`)`)
		args = append(args, idsToArgs(o.OnlyUIDs)...)
	}
	clause := ` WHERE ` + strings.Join(where, " AND ")
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := o.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+userCols+` FROM users`+clause+` ORDER BY created_at DESC, uid LIMIT ? OFFSET ?`,
		append(args, limit, max(o.Offset, 0))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, u)
	}
	return out, total, rows.Err()
}

type UserCounts struct{ Total, Active, Frozen, Deleted int }

// CountUsers counts users by status; Total excludes deleted users.
func (r *Repo) CountUsers(ctx context.Context) (UserCounts, error) {
	var c UserCounts
	err := r.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(status = 'active'), 0), COALESCE(SUM(status = 'frozen'), 0), COALESCE(SUM(status = 'deleted'), 0) FROM users`).
		Scan(&c.Active, &c.Frozen, &c.Deleted)
	c.Total = c.Active + c.Frozen
	return c, err
}

type DayCount struct {
	Day   string // YYYY-MM-DD (UTC)
	Count int
}

// UserGrowth returns new users per day for the last `days` days (UTC), oldest first, zero days included.
func (r *Repo) UserGrowth(ctx context.Context, days int) ([]DayCount, error) {
	if days < 1 || days > 366 {
		return nil, invalid("days", "must be 1-366")
	}
	today := r.now().UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -(days - 1))
	rows, err := r.db.QueryContext(ctx, `SELECT date(created_at / 1000, 'unixepoch') AS d, COUNT(*) FROM users WHERE created_at >= ? GROUP BY d`, start.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	have := map[string]int{}
	for rows.Next() {
		var d string
		var n int
		if err := rows.Scan(&d, &n); err != nil {
			return nil, err
		}
		have[d] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]DayCount, 0, days)
	for i := 0; i < days; i++ {
		d := start.AddDate(0, 0, i).Format("2006-01-02")
		out = append(out, DayCount{Day: d, Count: have[d]})
	}
	return out, nil
}

// ---------- My List ----------

type ListItem struct {
	MovieID string
	AddedAt int64
}

// GetList returns the user's list, most recently added first.
func (r *Repo) GetList(ctx context.Context, uid string) ([]ListItem, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT movie_id, added_at FROM my_list WHERE uid = ? ORDER BY added_at DESC, movie_id`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ListItem
	for rows.Next() {
		var it ListItem
		if err := rows.Scan(&it.MovieID, &it.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

const maxListItems = 2000

// AddToList adds a title (idempotent). The title's `lists` counter goes up only when it was really added.
func (r *Repo) AddToList(ctx context.Context, uid, movieID string) (added bool, err error) {
	err = r.inTx(ctx, func(tx *sql.Tx) error {
		if err := r.mustExist(ctx, tx, movieID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM my_list WHERE uid = ?`, uid).Scan(&n); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO my_list(uid, movie_id, added_at) VALUES (?,?,?) ON CONFLICT DO NOTHING`, uid, movieID, r.nowMs())
		if err != nil {
			return err
		}
		c, _ := res.RowsAffected()
		if c == 0 {
			return nil
		}
		if n >= maxListItems {
			return invalid("list", "My List is full (max %d titles)", maxListItems)
		}
		added = true
		_, err = tx.ExecContext(ctx, `INSERT INTO movie_stats(movie_id, plays, lists) VALUES (?, 0, 1)
			ON CONFLICT(movie_id) DO UPDATE SET lists = lists + 1`, movieID)
		return err
	})
	return added && err == nil, err
}

// RemoveFromList removes a title (idempotent). The `lists` counter never goes below 0.
func (r *Repo) RemoveFromList(ctx context.Context, uid, movieID string) (removed bool, err error) {
	err = r.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM my_list WHERE uid = ? AND movie_id = ?`, uid, movieID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		removed = true
		_, err = tx.ExecContext(ctx, `UPDATE movie_stats SET lists = MAX(lists - 1, 0) WHERE movie_id = ?`, movieID)
		return err
	})
	return removed && err == nil, err
}

// ---------- watch progress ----------

type Progress struct {
	MovieID string
	Season  string // season name, "" for a movie
	Ep      *int   // episode index, nil for a movie
	T       int    // seconds watched
	D       int    // duration in seconds
	At      int64
}

const maxProgressRows = 300

func (r *Repo) GetProgress(ctx context.Context, uid string) ([]Progress, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT movie_id, COALESCE(season, ''), ep, t, d, updated_at FROM watch_progress WHERE uid = ? ORDER BY updated_at DESC`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Progress
	for rows.Next() {
		var p Progress
		var ep sql.NullInt64
		if err := rows.Scan(&p.MovieID, &p.Season, &ep, &p.T, &p.D, &p.At); err != nil {
			return nil, err
		}
		if ep.Valid {
			v := int(ep.Int64)
			p.Ep = &v
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetProgress stores the resume point. t and d are required (like the old rules): d > 0, 0 <= t; t is capped at d.
func (r *Repo) SetProgress(ctx context.Context, uid, movieID, season string, ep *int, t, d int) error {
	if d <= 0 || d > 10_000_000 {
		return invalid("d", "duration must be a positive number of seconds")
	}
	if t < 0 {
		return invalid("t", "must be 0 or more")
	}
	if t > d {
		t = d
	}
	season = strings.TrimSpace(season)
	if len([]rune(season)) > 100 {
		return invalid("season", "is too long")
	}
	if ep != nil && (*ep < 0 || *ep > 100000) {
		return invalid("ep", "out of range")
	}
	return r.inTx(ctx, func(tx *sql.Tx) error {
		if err := r.mustExist(ctx, tx, movieID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO watch_progress(uid, movie_id, season, ep, t, d, updated_at) VALUES (?,?,?,?,?,?,?)
			ON CONFLICT(uid, movie_id) DO UPDATE SET season = excluded.season, ep = excluded.ep, t = excluded.t, d = excluded.d, updated_at = excluded.updated_at`,
			uid, movieID, nullStr(season), ep, t, d, r.nowMs()); err != nil {
			return err
		}
		// keep the table bounded: only the most recent titles stay
		_, err := tx.ExecContext(ctx, `DELETE FROM watch_progress WHERE uid = ? AND movie_id NOT IN
			(SELECT movie_id FROM watch_progress WHERE uid = ? ORDER BY updated_at DESC LIMIT ?)`, uid, uid, maxProgressRows)
		return err
	})
}

func (r *Repo) DeleteProgress(ctx context.Context, uid, movieID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM watch_progress WHERE uid = ? AND movie_id = ?`, uid, movieID)
	return err
}
