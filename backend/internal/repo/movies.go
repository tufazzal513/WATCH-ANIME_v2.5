package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"movex/internal/taxonomy"
)

type Link struct {
	ID      string
	Kind    string // "stream" or "download"
	Quality string
	Label   string
	URL     string
	Sort    int
}

type Episode struct {
	ID     string
	Number int
	Name   string
	Links  []Link
}

type Season struct {
	ID       string
	Number   int
	Name     string
	Episodes []Episode
}

type Movie struct {
	ID          string
	Title       string
	ContentType string // "movie" or "series"
	Type        string // Movie, Series, Anime, Web Series, TV Show
	Industry    string
	Year        *int
	Rating      *float64
	PosterURL   string
	CoverURL    string
	Cast        string
	Description string
	Dubbed      bool
	IsPrivate   bool
	IsUpcoming  bool
	ReleaseDate string // as typed in the admin form (datetime-local), "" when not upcoming
	ReleaseTS   *int64 // unix milliseconds
	Genres      []string
	Languages   []string
	Links       []Link   // movie-level links (full load only)
	Seasons     []Season // full load only
	CreatedAt   int64
	UpdatedAt   int64
}

// FirstStream returns the URL of the first stream link (the old "server1" field), or "".
func FirstStream(links []Link) string {
	for _, l := range links {
		if l.Kind == "stream" {
			return l.URL
		}
	}
	return ""
}

type LinkInput struct {
	Kind    string // default "stream"
	Quality string
	Label   string
	URL     string
}

type EpisodeInput struct {
	Name  string // default "Episode N"
	Links []LinkInput
}

type SeasonInput struct {
	Name     string // default "Season N"
	Episodes []EpisodeInput
}

// MovieInput is used for create and update. On update, Links and Seasons use nil to mean "keep what is
// stored" and a non-nil (even empty) slice to mean "replace". Switching ContentType to "movie" removes seasons.
type MovieInput struct {
	Title       string
	ContentType string
	Type        string
	Industry    string
	Year        *int
	Rating      *float64
	PosterURL   string
	CoverURL    string
	Cast        string
	Description string
	Dubbed      bool
	IsPrivate   bool
	IsUpcoming  bool
	ReleaseDate string
	ReleaseTS   int64 // unix ms; 0 = derive from ReleaseDate (read as UTC; pass the admin's local value when known)
	Genres      []string
	Languages   []string
	Links       []LinkInput
	Seasons     []SeasonInput
}

const releaseLayout = "2006-01-02T15:04"

func uniqueFold(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		k := strings.ToLower(s)
		if s == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, s)
	}
	return out
}

// normalize validates and cleans the input in place.
func (in *MovieInput) normalize() error {
	var err error
	if in.Title, err = trimLen("title", in.Title, 300); err != nil {
		return err
	}
	if in.Title == "" {
		return invalid("title", "is required")
	}
	switch in.ContentType {
	case "":
		in.ContentType = "movie"
	case "movie", "series":
	default:
		return invalid("content_type", "must be movie or series")
	}
	if in.Type == "" {
		in.Type = "Movie"
		if in.ContentType == "series" {
			in.Type = "Series"
		}
	}
	if !taxonomy.IsType(in.Type) {
		return invalid("type", "unknown type %q", in.Type)
	}
	if in.Industry != "" && !taxonomy.IsIndustry(in.Industry) {
		return invalid("industry", "unknown industry %q", in.Industry)
	}
	if in.Year != nil && (*in.Year < 1850 || *in.Year > 2200) {
		return invalid("year", "must be between 1850 and 2200")
	}
	if in.Rating != nil && (*in.Rating < 0 || *in.Rating > 10) {
		return invalid("rating", "must be between 0 and 10")
	}
	if in.PosterURL, err = cleanURL("poster_url", in.PosterURL, false); err != nil {
		return err
	}
	if in.CoverURL, err = cleanURL("cover_url", in.CoverURL, false); err != nil {
		return err
	}
	if in.Cast, err = trimLen("cast", in.Cast, 2000); err != nil {
		return err
	}
	if in.Description, err = trimLen("description", in.Description, 10000); err != nil {
		return err
	}
	in.ReleaseDate = strings.TrimSpace(in.ReleaseDate)
	if in.IsUpcoming {
		if in.ReleaseDate == "" {
			return invalid("release_date", "upcoming content must have a release date/time")
		}
		if in.ReleaseTS == 0 {
			t, perr := time.Parse(releaseLayout, in.ReleaseDate)
			if perr != nil {
				return invalid("release_date", "must look like 2026-12-31T20:00")
			}
			in.ReleaseTS = t.UnixMilli()
		}
	} else {
		in.ReleaseDate, in.ReleaseTS = "", 0
	}
	in.Genres = uniqueFold(in.Genres)
	if len(in.Genres) > 30 {
		return invalid("genres", "at most 30 genres")
	}
	in.Languages = uniqueFold(in.Languages)
	for i, l := range in.Languages {
		var ok bool
		for _, known := range taxonomy.Languages {
			if strings.EqualFold(known, l) {
				in.Languages[i], ok = known, true
				break
			}
		}
		if !ok {
			return invalid("languages", "unknown language %q", l)
		}
	}
	if in.Links != nil {
		if len(in.Links) > 20 {
			return invalid("links", "at most 20 links")
		}
		if in.Links, err = normalizeLinks(in.Links); err != nil {
			return err
		}
	}
	if len(in.Seasons) > 100 {
		return invalid("seasons", "at most 100 seasons")
	}
	return nil
}

func normalizeLinks(in []LinkInput) ([]LinkInput, error) {
	out := make([]LinkInput, 0, len(in))
	for i, l := range in {
		f := "links[" + strconv.Itoa(i) + "]"
		switch l.Kind {
		case "":
			l.Kind = "stream"
		case "stream", "download":
		default:
			return nil, invalid(f+".kind", "must be stream or download")
		}
		var err error
		if l.URL, err = cleanURL(f+".url", l.URL, true); err != nil {
			return nil, err
		}
		if l.Label, err = trimLen(f+".label", l.Label, 60); err != nil {
			return nil, err
		}
		if l.Quality, err = trimLen(f+".quality", l.Quality, 20); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}

// ---------- reads ----------

const movieCols = `id, title, content_type, type, industry, year, rating, poster_url, cover_url, cast_text, description,
	dubbed, is_private, is_upcoming, release_date, release_ts, created_at, updated_at`

// movieColsQ is movieCols with the table name, for queries that join other tables.
var movieColsQ = "movies." + strings.ReplaceAll(strings.Join(strings.Fields(movieCols), " "), ", ", ", movies.")

type rowScanner interface{ Scan(dest ...any) error }

func scanMovie(s rowScanner) (*Movie, error) {
	var m Movie
	var year sql.NullInt64
	var rating sql.NullFloat64
	var rts sql.NullInt64
	var dubbed, priv, upc int
	if err := s.Scan(&m.ID, &m.Title, &m.ContentType, &m.Type, &m.Industry, &year, &rating, &m.PosterURL, &m.CoverURL,
		&m.Cast, &m.Description, &dubbed, &priv, &upc, &m.ReleaseDate, &rts, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return nil, err
	}
	if year.Valid {
		y := int(year.Int64)
		m.Year = &y
	}
	if rating.Valid {
		f := rating.Float64
		m.Rating = &f
	}
	if rts.Valid {
		v := rts.Int64
		m.ReleaseTS = &v
	}
	m.Dubbed, m.IsPrivate, m.IsUpcoming = dubbed == 1, priv == 1, upc == 1
	return &m, nil
}

// attachTags loads genres and languages for the given movies (2 queries, whatever the count).
func attachTags(ctx context.Context, q queryer, ms []*Movie) error {
	if len(ms) == 0 {
		return nil
	}
	idx := make(map[string]*Movie, len(ms))
	ids := make([]string, len(ms))
	for i, m := range ms {
		idx[m.ID], ids[i] = m, m.ID
	}
	args := idsToArgs(ids)
	in := placeholders(len(ids))

	rows, err := q.QueryContext(ctx, `SELECT mc.movie_id, c.name FROM movie_categories mc JOIN categories c ON c.id = mc.category_id
		WHERE mc.movie_id IN (`+in+`) ORDER BY mc.movie_id, mc.pos, c.name`, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return err
		}
		idx[id].Genres = append(idx[id].Genres, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	rows, err = q.QueryContext(ctx, `SELECT movie_id, language FROM movie_languages WHERE movie_id IN (`+in+`) ORDER BY movie_id, pos`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, l string
		if err := rows.Scan(&id, &l); err != nil {
			return err
		}
		idx[id].Languages = append(idx[id].Languages, l)
	}
	return rows.Err()
}

func loadLinks(ctx context.Context, q queryer, movieID string) (movieLinks []Link, byEpisode map[string][]Link, err error) {
	rows, err := q.QueryContext(ctx, `SELECT id, COALESCE(episode_id, ''), kind, quality, label, url, sort FROM links
		WHERE movie_id = ? ORDER BY sort, created_at, id`, movieID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	byEpisode = map[string][]Link{}
	for rows.Next() {
		var l Link
		var ep string
		if err := rows.Scan(&l.ID, &ep, &l.Kind, &l.Quality, &l.Label, &l.URL, &l.Sort); err != nil {
			return nil, nil, err
		}
		if ep == "" {
			movieLinks = append(movieLinks, l)
		} else {
			byEpisode[ep] = append(byEpisode[ep], l)
		}
	}
	return movieLinks, byEpisode, rows.Err()
}

// GetMovie returns the whole movie: tags, movie-level links, seasons, episodes and episode links.
func (r *Repo) GetMovie(ctx context.Context, id string) (*Movie, error) {
	m, err := scanMovie(r.db.QueryRowContext(ctx, `SELECT `+movieCols+` FROM movies WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := attachTags(ctx, r.db, []*Movie{m}); err != nil {
		return nil, err
	}
	var byEp map[string][]Link
	if m.Links, byEp, err = loadLinks(ctx, r.db, id); err != nil {
		return nil, err
	}
	srows, err := r.db.QueryContext(ctx, `SELECT id, number, name FROM seasons WHERE movie_id = ? ORDER BY number, id`, id)
	if err != nil {
		return nil, err
	}
	seasonIdx := map[string]int{}
	for srows.Next() {
		var s Season
		if err := srows.Scan(&s.ID, &s.Number, &s.Name); err != nil {
			srows.Close()
			return nil, err
		}
		seasonIdx[s.ID] = len(m.Seasons)
		m.Seasons = append(m.Seasons, s)
	}
	if err := srows.Err(); err != nil {
		srows.Close()
		return nil, err
	}
	srows.Close()
	erows, err := r.db.QueryContext(ctx, `SELECT id, season_id, number, name FROM episodes WHERE movie_id = ? ORDER BY number, id`, id)
	if err != nil {
		return nil, err
	}
	defer erows.Close()
	for erows.Next() {
		var e Episode
		var sid string
		if err := erows.Scan(&e.ID, &sid, &e.Number, &e.Name); err != nil {
			return nil, err
		}
		e.Links = byEp[e.ID]
		if i, ok := seasonIdx[sid]; ok {
			m.Seasons[i].Episodes = append(m.Seasons[i].Episodes, e)
		}
	}
	return m, erows.Err()
}

// ListOpts filters the movie list. Zero values mean "no filter".
type ListOpts struct {
	Offset, Limit int    // Limit default 50, max 500
	Query         string // every word must appear in title/cast/genres/industry/languages/type/year (case-insensitive)
	ContentType   string
	Type          string
	Industry      string
	Genre         string
	Language      string
	Year          int
	Private       *bool    // nil = both
	Upcoming      string   // "yes" = still upcoming, "no" = not upcoming (or already released), "" = both
	IDs           []string // restrict to these ids (the caller re-orders the result if it needs a specific order)
	// Sort: new (default), old, title, year, rating,
	//   hot       = 50% rating + 30% plays + 20% My List adds (each normalised by the maximum), newer first on ties
	//   relevance = how well the title matches Query (exact > starts with > contains), then hot
	Sort string
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// ListMovies returns one page of "lite" movies (tags included, no seasons/links) and the total match count.
func (r *Repo) ListMovies(ctx context.Context, o ListOpts) ([]*Movie, int, error) {
	var where []string
	var args []any
	for _, w := range strings.Fields(strings.ToLower(o.Query)) {
		where = append(where, `search_text LIKE ? ESCAPE '\'`)
		args = append(args, "%"+likeEscape(w)+"%")
	}
	if o.ContentType != "" {
		where, args = append(where, `content_type = ?`), append(args, o.ContentType)
	}
	if o.Type != "" {
		where, args = append(where, `type = ? COLLATE NOCASE`), append(args, o.Type)
	}
	if o.Industry != "" {
		where, args = append(where, `industry = ?`), append(args, o.Industry)
	}
	if o.Year != 0 {
		where, args = append(where, `year = ?`), append(args, o.Year)
	}
	if o.Genre != "" {
		where = append(where, `EXISTS (SELECT 1 FROM movie_categories mc JOIN categories c ON c.id = mc.category_id WHERE mc.movie_id = movies.id AND c.name = ?)`)
		args = append(args, o.Genre)
	}
	if o.Language != "" {
		where = append(where, `EXISTS (SELECT 1 FROM movie_languages ml WHERE ml.movie_id = movies.id AND ml.language = ? COLLATE NOCASE)`)
		args = append(args, o.Language)
	}
	if o.Private != nil {
		where, args = append(where, `is_private = ?`), append(args, b2i(*o.Private))
	}
	if o.IDs != nil {
		if len(o.IDs) == 0 {
			return nil, 0, nil
		}
		where = append(where, `movies.id IN (`+placeholders(len(o.IDs))+`)`)
		args = append(args, idsToArgs(o.IDs)...)
	}
	switch o.Upcoming {
	case "yes":
		where, args = append(where, `(is_upcoming = 1 AND COALESCE(release_ts, 0) > ?)`), append(args, r.nowMs())
	case "no":
		where, args = append(where, `(is_upcoming = 0 OR COALESCE(release_ts, 0) <= ?)`), append(args, r.nowMs())
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM movies`+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	const hot = `(COALESCE(rating, 0) / 10.0) * 0.5
		+ (COALESCE(s.plays, 0) * 1.0 / MAX(COALESCE((SELECT MAX(plays) FROM movie_stats), 0), 1)) * 0.3
		+ (COALESCE(s.lists, 0) * 1.0 / MAX(COALESCE((SELECT MAX(lists) FROM movie_stats), 0), 1)) * 0.2`
	const when = `COALESCE(release_ts, created_at)`
	join := ""
	var orderArgs []any
	order, known := map[string]string{
		"":       "created_at DESC, id DESC",
		"new":    "created_at DESC, id DESC",
		"old":    "created_at ASC, id ASC",
		"title":  "title COLLATE NOCASE ASC, id ASC",
		"year":   "year IS NULL, year DESC, id DESC",
		"rating": "rating IS NULL, rating DESC, id DESC",
		"hot":    hot + " DESC, " + when + " DESC, id DESC",
	}[o.Sort]
	switch {
	case o.Sort == "relevance":
		known = true
		q := strings.Join(strings.Fields(strings.ToLower(o.Query)), " ")
		order = `CASE WHEN lower(title) = ? THEN 3 WHEN lower(title) LIKE ? ESCAPE '\' THEN 2 WHEN lower(title) LIKE ? ESCAPE '\' THEN 1 ELSE 0 END DESC, ` +
			hot + " DESC, " + when + " DESC, id DESC"
		orderArgs = []any{q, likeEscape(q) + "%", "%" + likeEscape(q) + "%"}
		join = ` LEFT JOIN movie_stats s ON s.movie_id = movies.id`
	case o.Sort == "hot":
		join = ` LEFT JOIN movie_stats s ON s.movie_id = movies.id`
	}
	if !known {
		return nil, 0, invalid("sort", "unknown sort %q", o.Sort)
	}
	limit := o.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	offset := o.Offset
	if offset < 0 {
		offset = 0
	}
	args = append(append(args, orderArgs...), limit, offset)
	rows, err := r.db.QueryContext(ctx, `SELECT `+movieColsQ+` FROM movies`+join+clause+` ORDER BY `+order+` LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	var out []*Movie
	for rows.Next() {
		m, err := scanMovie(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, 0, err
	}
	rows.Close()
	if err := attachTags(ctx, r.db, out); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// MovieYears lists the distinct years in use, newest first (for the admin year filter).
func (r *Repo) MovieYears(ctx context.Context) ([]int, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT year FROM movies WHERE year IS NOT NULL ORDER BY year DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var y int
		if err := rows.Scan(&y); err != nil {
			return nil, err
		}
		out = append(out, y)
	}
	return out, rows.Err()
}

// IsPublicMovie reports whether the title exists and is not private (what the user app is allowed to touch).
func (r *Repo) IsPublicMovie(ctx context.Context, id string) (bool, error) {
	var priv int
	err := r.db.QueryRowContext(ctx, `SELECT is_private FROM movies WHERE id = ?`, id).Scan(&priv)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && priv == 0, err
}

func (r *Repo) CountMovies(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM movies`).Scan(&n)
	return n, err
}

// ---------- writes ----------

// setTags replaces genres and languages of a movie and refreshes search_text. Genres must already exist.
func (r *Repo) setTags(ctx context.Context, tx *sql.Tx, movieID string, genres, languages []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM movie_categories WHERE movie_id = ?`, movieID); err != nil {
		return err
	}
	for i, g := range genres {
		var cid, canon string
		err := tx.QueryRowContext(ctx, `SELECT id, name FROM categories WHERE name = ?`, g).Scan(&cid, &canon)
		if errors.Is(err, sql.ErrNoRows) {
			return invalid("genres", "unknown genre %q (add it under Categories first)", g)
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO movie_categories(movie_id, category_id, pos) VALUES (?, ?, ?)`, movieID, cid, i); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM movie_languages WHERE movie_id = ?`, movieID); err != nil {
		return err
	}
	for i, l := range languages {
		if _, err := tx.ExecContext(ctx, `INSERT INTO movie_languages(movie_id, language, pos) VALUES (?, ?, ?)`, movieID, l, i); err != nil {
			return err
		}
	}
	return refreshSearch(ctx, tx, movieID)
}

// refreshSearch rebuilds movies.search_text: lower-cased title, cast, genres, industry, languages, type, year.
// This reproduces the old client-side search ("every word is a substring of the combined text").
func refreshSearch(ctx context.Context, q queryer, movieID string) error {
	var title, cast, industry, typ string
	var year sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT title, cast_text, industry, type, year FROM movies WHERE id = ?`, movieID).
		Scan(&title, &cast, &industry, &typ, &year)
	if err != nil {
		return err
	}
	parts := []string{title, cast}
	for _, query := range []string{
		`SELECT c.name FROM movie_categories mc JOIN categories c ON c.id = mc.category_id WHERE mc.movie_id = ? ORDER BY mc.pos`,
		`SELECT language FROM movie_languages WHERE movie_id = ? ORDER BY pos`,
	} {
		rows, err := q.QueryContext(ctx, query, movieID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				rows.Close()
				return err
			}
			parts = append(parts, s)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if strings.Contains(query, "movie_categories") {
			parts = append(parts, industry)
		}
	}
	parts = append(parts, typ)
	if year.Valid {
		parts = append(parts, strconv.FormatInt(year.Int64, 10))
	}
	text := strings.ToLower(strings.Join(parts, " "))
	_, err = q.ExecContext(ctx, `UPDATE movies SET search_text = ? WHERE id = ? AND search_text <> ?`, text, movieID, text)
	return err
}

func (r *Repo) replaceSeasons(ctx context.Context, tx *sql.Tx, movieID string, seasons []SeasonInput) error {
	// episodes (and their links) go with the seasons via ON DELETE CASCADE
	if _, err := tx.ExecContext(ctx, `DELETE FROM seasons WHERE movie_id = ?`, movieID); err != nil {
		return err
	}
	now := r.nowMs()
	seen := map[string]bool{}
	for si, s := range seasons {
		name, err := trimLen(fmt.Sprintf("seasons[%d].name", si), s.Name, 100)
		if err != nil {
			return err
		}
		if name == "" {
			name = fmt.Sprintf("Season %d", si+1)
		}
		if seen[strings.ToLower(name)] {
			return invalid("seasons", "duplicate season name %q", name)
		}
		seen[strings.ToLower(name)] = true
		if len(s.Episodes) > 2000 {
			return invalid("seasons", "at most 2000 episodes per season")
		}
		sid := r.newID()
		if _, err := tx.ExecContext(ctx, `INSERT INTO seasons(id, movie_id, number, name, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
			sid, movieID, si+1, name, now, now); err != nil {
			return err
		}
		for ei, e := range s.Episodes {
			ename, err := trimLen(fmt.Sprintf("seasons[%d].episodes[%d].name", si, ei), e.Name, 200)
			if err != nil {
				return err
			}
			if ename == "" {
				ename = fmt.Sprintf("Episode %d", ei+1)
			}
			links, err := normalizeLinks(e.Links)
			if err != nil {
				return err
			}
			eid := r.newID()
			if _, err := tx.ExecContext(ctx, `INSERT INTO episodes(id, season_id, movie_id, number, name, created_at, updated_at) VALUES (?,?,?,?,?,?,?)`,
				eid, sid, movieID, ei+1, ename, now, now); err != nil {
				return err
			}
			if err := r.insertLinks(ctx, tx, movieID, eid, links); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Repo) insertLinks(ctx context.Context, tx *sql.Tx, movieID, episodeID string, links []LinkInput) error {
	now := r.nowMs()
	var ep any
	if episodeID != "" {
		ep = episodeID
	}
	for i, l := range links {
		if _, err := tx.ExecContext(ctx, `INSERT INTO links(id, movie_id, episode_id, kind, quality, label, url, sort, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?)`, r.newID(), movieID, ep, l.Kind, l.Quality, l.Label, l.URL, i, now, now); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repo) replaceMovieLinks(ctx context.Context, tx *sql.Tx, movieID string, links []LinkInput) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM links WHERE movie_id = ? AND episode_id IS NULL`, movieID); err != nil {
		return err
	}
	return r.insertLinks(ctx, tx, movieID, "", links)
}

// CreateMovie stores a movie with its genres, languages, links and (for series) seasons in one transaction.
func (r *Repo) CreateMovie(ctx context.Context, in MovieInput) (*Movie, error) {
	if err := in.normalize(); err != nil {
		return nil, err
	}
	id := r.newID()
	now := r.nowMs()
	err := r.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO movies(id, title, content_type, type, industry, year, rating, poster_url, cover_url,
			cast_text, description, dubbed, is_private, is_upcoming, release_date, release_ts, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, in.Title, in.ContentType, in.Type, in.Industry, in.Year, in.Rating, in.PosterURL, in.CoverURL,
			in.Cast, in.Description, b2i(in.Dubbed), b2i(in.IsPrivate), b2i(in.IsUpcoming), in.ReleaseDate, nullInt(in.ReleaseTS), now, now); err != nil {
			return err
		}
		if err := r.setTags(ctx, tx, id, in.Genres, in.Languages); err != nil {
			return err
		}
		if in.ContentType == "movie" {
			return r.replaceMovieLinks(ctx, tx, id, in.Links)
		}
		if len(in.Links) > 0 {
			if err := r.replaceMovieLinks(ctx, tx, id, in.Links); err != nil {
				return err
			}
		}
		return r.replaceSeasons(ctx, tx, id, in.Seasons)
	})
	if err != nil {
		return nil, err
	}
	return r.GetMovie(ctx, id)
}

func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// UpdateMovie updates a movie. See MovieInput for the nil-means-keep rule of Links and Seasons.
func (r *Repo) UpdateMovie(ctx context.Context, id string, in MovieInput) (*Movie, error) {
	if err := in.normalize(); err != nil {
		return nil, err
	}
	err := r.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE movies SET title=?, content_type=?, type=?, industry=?, year=?, rating=?, poster_url=?, cover_url=?,
			cast_text=?, description=?, dubbed=?, is_private=?, is_upcoming=?, release_date=?, release_ts=?, updated_at=? WHERE id=?`,
			in.Title, in.ContentType, in.Type, in.Industry, in.Year, in.Rating, in.PosterURL, in.CoverURL,
			in.Cast, in.Description, b2i(in.Dubbed), b2i(in.IsPrivate), b2i(in.IsUpcoming), in.ReleaseDate, nullInt(in.ReleaseTS), r.nowMs(), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		if err := r.setTags(ctx, tx, id, in.Genres, in.Languages); err != nil {
			return err
		}
		if in.Links != nil {
			if err := r.replaceMovieLinks(ctx, tx, id, in.Links); err != nil {
				return err
			}
		}
		switch {
		case in.ContentType == "movie":
			_, err = tx.ExecContext(ctx, `DELETE FROM seasons WHERE movie_id = ?`, id)
			return err
		case in.Seasons != nil:
			return r.replaceSeasons(ctx, tx, id, in.Seasons)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r.GetMovie(ctx, id)
}

// ReplaceSeasons replaces the whole season/episode tree of a series.
func (r *Repo) ReplaceSeasons(ctx context.Context, movieID string, seasons []SeasonInput) error {
	return r.inTx(ctx, func(tx *sql.Tx) error {
		if err := r.mustExist(ctx, tx, movieID); err != nil {
			return err
		}
		if err := r.replaceSeasons(ctx, tx, movieID, seasons); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE movies SET updated_at = ? WHERE id = ?`, r.nowMs(), movieID)
		return err
	})
}

// SetLinks replaces the links of a movie (episodeID == "") or of one of its episodes.
func (r *Repo) SetLinks(ctx context.Context, movieID, episodeID string, links []LinkInput) error {
	links, err := normalizeLinks(links)
	if err != nil {
		return err
	}
	return r.inTx(ctx, func(tx *sql.Tx) error {
		if err := r.mustExist(ctx, tx, movieID); err != nil {
			return err
		}
		if episodeID == "" {
			if err := r.replaceMovieLinks(ctx, tx, movieID, links); err != nil {
				return err
			}
		} else {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM episodes WHERE id = ? AND movie_id = ?`, episodeID, movieID).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return ErrNotFound
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM links WHERE episode_id = ?`, episodeID); err != nil {
				return err
			}
			if err := r.insertLinks(ctx, tx, movieID, episodeID, links); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE movies SET updated_at = ? WHERE id = ?`, r.nowMs(), movieID)
		return err
	})
}

func (r *Repo) mustExist(ctx context.Context, q queryer, movieID string) error {
	var n int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM movies WHERE id = ?`, movieID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteMovie removes a movie; seasons, episodes, links and tags go with it, banners lose their movie link.
func (r *Repo) DeleteMovie(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM movies WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
