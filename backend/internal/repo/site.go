package repo

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ---------- banners ----------

type Banner struct {
	ID           string
	ImageURL     string
	MovieID      string // "" when not linked to a title
	MovieTitle   string // joined from movies ("" when unlinked)
	MoviePrivate bool   // the linked title is private (hide the banner from the user app)
	LinkURL      string // optional custom link
	Sort         int
	Active       bool
	CreatedAt    int64
}

type BannerInput struct {
	ImageURL string
	MovieID  string
	LinkURL  string
	Sort     int // 0 = place last
	Active   bool
}

const bannerSelect = `SELECT b.id, b.image_url, COALESCE(b.movie_id, ''), COALESCE(m.title, ''), COALESCE(m.is_private, 0), b.link_url, b.sort, b.active, b.created_at
	FROM banners b LEFT JOIN movies m ON m.id = b.movie_id`

func scanBanner(s rowScanner) (Banner, error) {
	var b Banner
	var act, priv int
	err := s.Scan(&b.ID, &b.ImageURL, &b.MovieID, &b.MovieTitle, &priv, &b.LinkURL, &b.Sort, &act, &b.CreatedAt)
	b.Active, b.MoviePrivate = act == 1, priv == 1
	return b, err
}

// ListBanners returns banners in display order.
func (r *Repo) ListBanners(ctx context.Context, activeOnly bool) ([]Banner, error) {
	q := bannerSelect
	if activeOnly {
		q += ` WHERE b.active = 1`
	}
	rows, err := r.db.QueryContext(ctx, q+` ORDER BY b.sort, b.created_at, b.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Banner
	for rows.Next() {
		b, err := scanBanner(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *Repo) GetBanner(ctx context.Context, id string) (*Banner, error) {
	b, err := scanBanner(r.db.QueryRowContext(ctx, bannerSelect+` WHERE b.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &b, err
}

func (in *BannerInput) normalize(ctx context.Context, q queryer) error {
	var err error
	if in.ImageURL, err = cleanURL("image_url", in.ImageURL, true); err != nil {
		return err
	}
	if in.LinkURL, err = cleanURL("link_url", in.LinkURL, false); err != nil {
		return err
	}
	in.MovieID = strings.TrimSpace(in.MovieID)
	if in.MovieID != "" {
		var n int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM movies WHERE id = ?`, in.MovieID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return invalid("movie_id", "no such title")
		}
	}
	return nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (r *Repo) CreateBanner(ctx context.Context, in BannerInput) (*Banner, error) {
	if err := in.normalize(ctx, r.db); err != nil {
		return nil, err
	}
	id := r.newID()
	err := r.inTx(ctx, func(tx *sql.Tx) error {
		if in.Sort == 0 {
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort), 0) + 10 FROM banners`).Scan(&in.Sort); err != nil {
				return err
			}
		}
		now := r.nowMs()
		_, err := tx.ExecContext(ctx, `INSERT INTO banners(id, image_url, movie_id, link_url, sort, active, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?)`,
			id, in.ImageURL, nullStr(in.MovieID), in.LinkURL, in.Sort, b2i(in.Active), now, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return r.GetBanner(ctx, id)
}

func (r *Repo) UpdateBanner(ctx context.Context, id string, in BannerInput) (*Banner, error) {
	if err := in.normalize(ctx, r.db); err != nil {
		return nil, err
	}
	res, err := r.db.ExecContext(ctx, `UPDATE banners SET image_url=?, movie_id=?, link_url=?, sort=?, active=?, updated_at=? WHERE id=?`,
		in.ImageURL, nullStr(in.MovieID), in.LinkURL, in.Sort, b2i(in.Active), r.nowMs(), id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return r.GetBanner(ctx, id)
}

func (r *Repo) DeleteBanner(ctx context.Context, id string) error {
	return r.deleteByID(ctx, "banners", id)
}

func (r *Repo) ReorderBanners(ctx context.Context, ids []string) error {
	return r.reorder(ctx, "banners", ids)
}

func (r *Repo) deleteByID(ctx context.Context, table, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM `+table+` WHERE id = ?`, id) // table is a compile-time constant at every call site
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// reorder gives the listed ids the first positions (in the given order) and keeps every other row after them in its
// current order, then renumbers sort = 10, 20, 30... Unknown and duplicate ids are ignored.
func (r *Repo) reorder(ctx context.Context, table string, ids []string) error { // table is a constant at every call site
	return r.inTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM `+table+` ORDER BY sort, id`)
		if err != nil {
			return err
		}
		var current []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			current = append(current, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		exists := make(map[string]bool, len(current))
		for _, id := range current {
			exists[id] = true
		}
		placed := map[string]bool{}
		order := make([]string, 0, len(current))
		for _, id := range ids {
			if exists[id] && !placed[id] {
				placed[id] = true
				order = append(order, id)
			}
		}
		for _, id := range current {
			if !placed[id] {
				order = append(order, id)
			}
		}
		for i, id := range order {
			if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET sort = ?, updated_at = ? WHERE id = ? AND sort <> ?`,
				(i+1)*10, r.nowMs(), id, (i+1)*10); err != nil {
				return err
			}
		}
		return nil
	})
}

// ---------- social links ----------

type SocialLink struct {
	ID   string
	Name string
	URL  string
	Icon string
	Sort int
}

type SocialInput struct {
	Name string
	URL  string
	Icon string // Font Awesome classes, default "fas fa-link"
}

var iconRe = regexp.MustCompile(`^[a-z0-9][a-z0-9 -]{0,79}$`)

func (in *SocialInput) normalize() error {
	var err error
	if in.Name, err = trimLen("name", in.Name, 60); err != nil {
		return err
	}
	if in.Name == "" {
		return invalid("name", "is required")
	}
	in.URL = strings.TrimSpace(in.URL)
	if in.URL == "" {
		return invalid("url", "is required")
	}
	l := strings.ToLower(in.URL)
	if !(isHTTPURL(in.URL) || strings.HasPrefix(l, "mailto:") || strings.HasPrefix(l, "tel:")) || len(in.URL) > 2000 || strings.ContainsAny(in.URL, " \t\r\n") {
		return invalid("url", "must be an http(s), mailto: or tel: link")
	}
	in.Icon = strings.ToLower(strings.TrimSpace(in.Icon))
	if in.Icon == "" {
		in.Icon = "fas fa-link"
	}
	if !iconRe.MatchString(in.Icon) {
		return invalid("icon", "must be Font Awesome class names, e.g. fab fa-telegram")
	}
	return nil
}

func (r *Repo) ListSocialLinks(ctx context.Context) ([]SocialLink, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, url, icon, sort FROM social_links ORDER BY sort, created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SocialLink
	for rows.Next() {
		var s SocialLink
		if err := rows.Scan(&s.ID, &s.Name, &s.URL, &s.Icon, &s.Sort); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repo) GetSocialLink(ctx context.Context, id string) (*SocialLink, error) {
	var s SocialLink
	err := r.db.QueryRowContext(ctx, `SELECT id, name, url, icon, sort FROM social_links WHERE id = ?`, id).Scan(&s.ID, &s.Name, &s.URL, &s.Icon, &s.Sort)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &s, err
}

func (r *Repo) CreateSocialLink(ctx context.Context, in SocialInput) (*SocialLink, error) {
	if err := in.normalize(); err != nil {
		return nil, err
	}
	id := r.newID()
	now := r.nowMs()
	_, err := r.db.ExecContext(ctx, `INSERT INTO social_links(id, name, url, icon, sort, created_at, updated_at)
		VALUES (?,?,?,?, (SELECT COALESCE(MAX(sort), 0) + 10 FROM social_links), ?, ?)`, id, in.Name, in.URL, in.Icon, now, now)
	if err != nil {
		return nil, err
	}
	return r.GetSocialLink(ctx, id)
}

func (r *Repo) UpdateSocialLink(ctx context.Context, id string, in SocialInput) (*SocialLink, error) {
	if err := in.normalize(); err != nil {
		return nil, err
	}
	res, err := r.db.ExecContext(ctx, `UPDATE social_links SET name=?, url=?, icon=?, updated_at=? WHERE id=?`, in.Name, in.URL, in.Icon, r.nowMs(), id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return r.GetSocialLink(ctx, id)
}

func (r *Repo) DeleteSocialLink(ctx context.Context, id string) error {
	return r.deleteByID(ctx, "social_links", id)
}

// ---------- notifications ----------

type Notification struct {
	ID      string
	Title   string
	Message string
	SentAt  int64 // unix ms; refreshed when the notification is edited (same as the old admin panel)
}

func cleanNotification(title, message string) (string, string, error) {
	title, err := trimLen("title", title, 120)
	if err != nil {
		return "", "", err
	}
	message, err = trimLen("message", message, 1000)
	if err != nil {
		return "", "", err
	}
	if title == "" || message == "" {
		return "", "", invalid("title", "title and message are required")
	}
	return title, message, nil
}

// ListNotifications returns the newest first. limit <= 0 means 100.
func (r *Repo) ListNotifications(ctx context.Context, limit int) ([]Notification, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, title, message, sent_at FROM notifications ORDER BY sent_at DESC, id DESC LIMIT ?`, limit)
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

func (r *Repo) GetNotification(ctx context.Context, id string) (*Notification, error) {
	var n Notification
	err := r.db.QueryRowContext(ctx, `SELECT id, title, message, sent_at FROM notifications WHERE id = ?`, id).Scan(&n.ID, &n.Title, &n.Message, &n.SentAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &n, err
}

func (r *Repo) CreateNotification(ctx context.Context, title, message string) (*Notification, error) {
	title, message, err := cleanNotification(title, message)
	if err != nil {
		return nil, err
	}
	id, now := r.newID(), r.nowMs()
	if _, err := r.db.ExecContext(ctx, `INSERT INTO notifications(id, title, message, sent_at, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
		id, title, message, now, now, now); err != nil {
		return nil, err
	}
	return r.GetNotification(ctx, id)
}

func (r *Repo) UpdateNotification(ctx context.Context, id, title, message string) (*Notification, error) {
	title, message, err := cleanNotification(title, message)
	if err != nil {
		return nil, err
	}
	now := r.nowMs()
	res, err := r.db.ExecContext(ctx, `UPDATE notifications SET title=?, message=?, sent_at=?, updated_at=? WHERE id=?`, title, message, now, now, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return r.GetNotification(ctx, id)
}

func (r *Repo) DeleteNotification(ctx context.Context, id string) error {
	return r.deleteByID(ctx, "notifications", id)
}

// ---------- public settings ----------

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func intRule(min, max int) func(string) (string, error) {
	return func(v string) (string, error) {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < min || n > max {
			return "", errors.New("must be a whole number between " + strconv.Itoa(min) + " and " + strconv.Itoa(max))
		}
		return strconv.Itoa(n), nil
	}
}

// settingRules lists every allowed key with its validator/normalizer. Unknown keys are rejected.
var settingRules = map[string]func(string) (string, error){
	"appName": func(v string) (string, error) {
		v = strings.TrimSpace(v)
		if v == "" || len([]rune(v)) > 60 {
			return "", errors.New("must be 1-60 characters")
		}
		return v, nil
	},
	"appLogo": func(v string) (string, error) { return cleanURLPlain(v) },
	"themeMode": func(v string) (string, error) {
		if v != "static" && v != "auto" {
			return "", errors.New("must be static or auto")
		}
		return v, nil
	},
	"themeColor": func(v string) (string, error) {
		if !colorRe.MatchString(v) {
			return "", errors.New("must look like #e50914")
		}
		return strings.ToLower(v), nil
	},
	"themeInterval": intRule(5, 86400),
	"pwdResetLimit": intRule(1, 50),
}

func cleanURLPlain(v string) (string, error) {
	u, err := cleanURL("appLogo", v, false)
	if err != nil {
		return "", errors.New("must be a full http(s) URL or empty")
	}
	return u, nil
}

// PublicSettingKeys are the settings the user app may read without logging in. (pwdResetLimit is server-side only.)
var PublicSettingKeys = []string{"appName", "appLogo", "themeMode", "themeColor", "themeInterval"}

// GetSettings returns every stored setting.
func (r *Repo) GetSettings(ctx context.Context) (map[string]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT k, v FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// PublicSettings returns only the keys in PublicSettingKeys.
func (r *Repo) PublicSettings(ctx context.Context) (map[string]string, error) {
	all, err := r.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, k := range PublicSettingKeys {
		if v, ok := all[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

// SetSettings validates and stores the given keys in one transaction (nothing is stored if any value is bad).
func (r *Repo) SetSettings(ctx context.Context, kv map[string]string) error {
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	clean := map[string]string{}
	for _, k := range keys {
		rule, ok := settingRules[k]
		if !ok {
			return invalid(k, "unknown setting")
		}
		v, err := rule(kv[k])
		if err != nil {
			return invalid(k, "%s", err.Error())
		}
		clean[k] = v
	}
	return r.inTx(ctx, func(tx *sql.Tx) error {
		for _, k := range keys {
			if _, err := tx.ExecContext(ctx, `INSERT INTO settings(k, v, updated_at) VALUES (?,?,?)
				ON CONFLICT(k) DO UPDATE SET v = excluded.v, updated_at = excluded.updated_at WHERE v <> excluded.v`,
				k, clean[k], r.nowMs()); err != nil {
				return err
			}
		}
		return nil
	})
}
