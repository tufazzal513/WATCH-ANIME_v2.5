package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"movex/internal/httpx"
	"movex/internal/repo"
)

// Limits for the catalog endpoints.
const (
	browsePageSize  = 24 // default page size (Browse); the search screen asks for 20
	maxPageSize     = 100
	maxOffset       = 100000
	maxIDsParam     = 100
	maxQueryLen     = 100
	maxFilterLen    = 60
	suggestMinCount = 2 // a term must have been searched at least this often to be shown to other users
	workerAliveFor  = 10 * time.Minute
)

// ---------- JSON shapes ----------

type movieLite struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	ContentType string   `json:"contentType"`
	Type        string   `json:"type"`
	Industry    string   `json:"industry"`
	Year        *int     `json:"year"`
	Rating      *float64 `json:"rating"`
	PosterURL   string   `json:"posterUrl"`
	CoverURL    string   `json:"coverUrl"`
	Dubbed      bool     `json:"dubbed"`
	IsUpcoming  bool     `json:"isUpcoming"`
	ReleaseDate string   `json:"releaseDate"`
	ReleaseTS   *int64   `json:"releaseTs"`
	Genres      []string `json:"genres"`
	Category    string   `json:"category"` // the primary (first) genre
	Languages   []string `json:"languages"`
	CreatedAt   int64    `json:"createdAt"`
}

type linkDTO struct {
	Kind    string `json:"kind"`
	Quality string `json:"quality,omitempty"`
	Label   string `json:"label,omitempty"`
	URL     string `json:"url"`
}

type episodeDTO struct {
	Name    string    `json:"name"`
	Server1 string    `json:"server1"` // first stream link (same field the old player used)
	Links   []linkDTO `json:"links"`
}

type seasonDTO struct {
	Name     string       `json:"name"`
	Episodes []episodeDTO `json:"episodes"`
}

type movieFull struct {
	movieLite
	Description string      `json:"description"`
	Cast        string      `json:"cast"`
	Server1     string      `json:"server1"` // first stream link of a movie
	Links       []linkDTO   `json:"links"`
	Seasons     []seasonDTO `json:"seasons"`
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func toLite(m *repo.Movie) movieLite {
	l := movieLite{
		ID: m.ID, Title: m.Title, ContentType: m.ContentType, Type: m.Type, Industry: m.Industry, Year: m.Year, Rating: m.Rating,
		PosterURL: m.PosterURL, CoverURL: m.CoverURL, Dubbed: m.Dubbed, IsUpcoming: m.IsUpcoming, ReleaseDate: m.ReleaseDate,
		ReleaseTS: m.ReleaseTS, Genres: nonNil(m.Genres), Languages: nonNil(m.Languages), CreatedAt: m.CreatedAt,
	}
	if len(m.Genres) > 0 {
		l.Category = m.Genres[0]
	}
	return l
}

func toLinks(in []repo.Link) []linkDTO {
	out := make([]linkDTO, 0, len(in))
	for _, l := range in {
		out = append(out, linkDTO{Kind: l.Kind, Quality: l.Quality, Label: l.Label, URL: l.URL})
	}
	return out
}

func toFull(m *repo.Movie) movieFull {
	f := movieFull{movieLite: toLite(m), Description: m.Description, Cast: m.Cast, Server1: repo.FirstStream(m.Links),
		Links: toLinks(m.Links), Seasons: make([]seasonDTO, 0, len(m.Seasons))}
	for _, s := range m.Seasons {
		sd := seasonDTO{Name: s.Name, Episodes: make([]episodeDTO, 0, len(s.Episodes))}
		for _, e := range s.Episodes {
			sd.Episodes = append(sd.Episodes, episodeDTO{Name: e.Name, Server1: repo.FirstStream(e.Links), Links: toLinks(e.Links)})
		}
		f.Seasons = append(f.Seasons, sd)
	}
	return f
}

// ---------- query parsing ----------

func qInt(r *http.Request, name string, def, min, max int) (int, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		return 0, &repo.ValidationError{Field: name, Msg: "must be a whole number between " + strconv.Itoa(min) + " and " + strconv.Itoa(max)}
	}
	return n, nil
}

func qStr(r *http.Request, name string, max int) (string, error) {
	v := strings.TrimSpace(r.URL.Query().Get(name))
	if len([]rune(v)) > max {
		return "", &repo.ValidationError{Field: name, Msg: "is too long"}
	}
	return v, nil
}

func qEnum(r *http.Request, name string, allowed ...string) (string, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return "", nil
	}
	for _, a := range allowed {
		if v == a {
			return v, nil
		}
	}
	return "", &repo.ValidationError{Field: name, Msg: "must be one of " + strings.Join(allowed, ", ")}
}

// ---------- registration ----------

func (s *Server) catalogRoutes() {
	s.Public("GET /api/v1/settings/public", s.settingsPublic)
	if s.deps.Cfg.IsMain() {
		s.Public("GET /api/v1/servers", s.servers)
	}
	s.Private("GET /api/v1/movies", s.listMovies)
	s.Private("GET /api/v1/movies/{id}", s.getMovie)
	s.Private("POST /api/v1/movies/{id}/view", s.viewMovie)
	s.Private("GET /api/v1/categories", s.categories)
	s.Private("GET /api/v1/catalog/facets", s.catalogFacets)
	s.Private("GET /api/v1/banners", s.banners)
	s.Private("GET /api/v1/social-links", s.socialLinks)
	s.Private("GET /api/v1/notifications", s.notifications)
	s.Private("GET /api/v1/search/suggest", s.searchSuggest)
	s.Private("POST /api/v1/search/hit", s.searchHit)
}

// ---------- movies ----------

// GET /movies?offset&limit&category&type&contentType&industry&language&year&upcoming&q&sort&ids
// Always the "lite" card shape. Private titles never appear. sort defaults to "new", or to "relevance" when q is set.
func (s *Server) listMovies(w http.ResponseWriter, r *http.Request, _ *Principal) {
	if s.deps.Cfg.IsWorker() && (r.URL.Query().Get("sort") == "hot" || r.URL.Query().Get("sort") == "relevance") {
		s.proxyWorkerAPI(w, r)
		return
	}
	var err error
	o := repo.ListOpts{Private: boolPtr(false)}
	fail := func(e error) { s.fail(w, r, e) }

	if o.Limit, err = qInt(r, "limit", browsePageSize, 1, maxPageSize); err != nil {
		fail(err)
		return
	}
	if o.Offset, err = qInt(r, "offset", 0, 0, maxOffset); err != nil {
		fail(err)
		return
	}
	if o.Year, err = qInt(r, "year", 0, 1850, 2200); err != nil {
		fail(err)
		return
	}
	for name, dst := range map[string]*string{"q": &o.Query, "category": &o.Genre, "type": &o.Type, "industry": &o.Industry, "language": &o.Language} {
		max := maxFilterLen
		if name == "q" {
			max = maxQueryLen
		}
		if *dst, err = qStr(r, name, max); err != nil {
			fail(err)
			return
		}
	}
	if o.ContentType, err = qEnum(r, "contentType", "movie", "series"); err != nil {
		fail(err)
		return
	}
	if o.Upcoming, err = qEnum(r, "upcoming", "yes", "no"); err != nil {
		fail(err)
		return
	}
	if o.Sort, err = qEnum(r, "sort", "new", "old", "title", "year", "rating", "hot", "relevance"); err != nil {
		fail(err)
		return
	}
	if o.Sort == "" {
		o.Sort = "new"
		if o.Query != "" {
			o.Sort = "relevance"
		}
	}
	if o.Sort == "relevance" && o.Query == "" {
		o.Sort = "hot"
	}

	var order []string
	if raw := r.URL.Query().Get("ids"); raw != "" { // a specific set of titles (My List, continue watching), in the order asked
		for _, id := range strings.Split(raw, ",") {
			if id = strings.TrimSpace(id); id != "" {
				order = append(order, id)
			}
		}
		if len(order) == 0 || len(order) > maxIDsParam {
			fail(&repo.ValidationError{Field: "ids", Msg: "send 1-" + strconv.Itoa(maxIDsParam) + " comma-separated ids"})
			return
		}
		for _, id := range order {
			if len(id) > 40 {
				fail(&repo.ValidationError{Field: "ids", Msg: "bad id"})
				return
			}
		}
		o.IDs, o.Limit, o.Offset = order, len(order), 0
	}

	items, total, err := s.deps.Repo.ListMovies(r.Context(), o)
	if err != nil {
		fail(err)
		return
	}
	out := make([]movieLite, 0, len(items))
	if order != nil {
		byID := make(map[string]*repo.Movie, len(items))
		for _, m := range items {
			byID[m.ID] = m
		}
		seen := map[string]bool{}
		for _, id := range order {
			if m := byID[id]; m != nil && !seen[id] {
				seen[id] = true
				out = append(out, toLite(m))
			}
		}
	} else {
		for _, m := range items {
			out = append(out, toLite(m))
		}
	}
	httpx.WriteJSONETag(w, r, map[string]any{
		"items": out, "total": total, "offset": o.Offset, "limit": o.Limit, "hasMore": o.Offset+len(items) < total && order == nil,
	})
}

func boolPtr(b bool) *bool { return &b }

// GET /movies/{id}: the whole title. Private titles answer 404, like the old app that filtered them out.
func (s *Server) getMovie(w http.ResponseWriter, r *http.Request, _ *Principal) {
	m, err := s.visibleMovie(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSONETag(w, r, toFull(m))
}

func (s *Server) visibleMovie(r *http.Request) (*repo.Movie, error) {
	id := r.PathValue("id")
	if id == "" || len(id) > 40 {
		return nil, repo.ErrNotFound
	}
	m, err := s.deps.Repo.GetMovie(r.Context(), id)
	if err != nil {
		return nil, err
	}
	if m.IsPrivate {
		return nil, repo.ErrNotFound
	}
	return m, nil
}

// POST /movies/{id}/view counts one play. The same user is counted at most once per 6 hours per title.
func (s *Server) viewMovie(w http.ResponseWriter, r *http.Request, p *Principal) {
	if s.deps.Cfg.IsWorker() {
		s.proxyWorkerAPI(w, r)
		return
	}
	m, err := s.visibleMovie(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	counted := false
	if ok, _ := s.viewLimit.Allow(p.UID + "|" + m.ID); ok {
		if err := s.deps.Repo.IncPlays(r.Context(), m.ID); err != nil {
			s.fail(w, r, err)
			return
		}
		counted = true
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"counted": counted})
}

// ---------- small catalogs ----------

func (s *Server) catalogFacets(w http.ResponseWriter, r *http.Request, _ *Principal) {
	facets, err := s.deps.Repo.ListPublicCatalogFacets(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSONETag(w, r, facets)
}

func (s *Server) categories(w http.ResponseWriter, r *http.Request, _ *Principal) {
	cats, err := s.deps.Repo.ListPublicCategories(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	type dto struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Popular bool   `json:"popular"`
		Sort    int    `json:"sort"`
		Count   int    `json:"count"`
	}
	out := make([]dto, 0, len(cats))
	for _, c := range cats {
		out = append(out, dto{c.ID, c.Name, c.Popular, c.Sort, c.MovieCount})
	}
	httpx.WriteJSONETag(w, r, map[string]any{"items": out})
}

func (s *Server) banners(w http.ResponseWriter, r *http.Request, _ *Principal) {
	list, err := s.deps.Repo.ListBanners(r.Context(), true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	type dto struct {
		ID         string `json:"id"`
		ImageURL   string `json:"imageUrl"`
		MovieID    string `json:"movieId"`
		MovieTitle string `json:"movieTitle"`
		LinkURL    string `json:"linkUrl"`
	}
	out := make([]dto, 0, len(list))
	for _, b := range list {
		if b.MoviePrivate { // never advertise a private title
			continue
		}
		out = append(out, dto{b.ID, b.ImageURL, b.MovieID, b.MovieTitle, b.LinkURL})
	}
	httpx.WriteJSONETag(w, r, map[string]any{"items": out})
}

func (s *Server) socialLinks(w http.ResponseWriter, r *http.Request, _ *Principal) {
	list, err := s.deps.Repo.ListSocialLinks(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	type dto struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		URL  string `json:"url"`
		Icon string `json:"icon"`
	}
	out := make([]dto, 0, len(list))
	for _, l := range list {
		out = append(out, dto{l.ID, l.Name, l.URL, l.Icon})
	}
	httpx.WriteJSONETag(w, r, map[string]any{"items": out})
}

func (s *Server) notifications(w http.ResponseWriter, r *http.Request, _ *Principal) {
	limit, err := qInt(r, "limit", 50, 1, 200)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := s.deps.Repo.ListNotifications(r.Context(), limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	type dto struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Message   string `json:"message"`
		Timestamp int64  `json:"timestamp"`
	}
	out := make([]dto, 0, len(list))
	for _, n := range list {
		out = append(out, dto{n.ID, n.Title, n.Message, n.SentAt})
	}
	httpx.WriteJSONETag(w, r, map[string]any{"items": out})
}

// GET /settings/public needs no login (the login page already uses the app name, logo and theme).
func (s *Server) settingsPublic(w http.ResponseWriter, r *http.Request) {
	kv, err := s.deps.Repo.PublicSettings(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSONETag(w, r, kv)
}

// GET /servers lists the workers that have recently synced, so the app can pick the nearest/fastest one.
func (s *Server) servers(w http.ResponseWriter, r *http.Request) {
	ws, err := s.deps.Repo.ListWorkers(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	currentEpoch, err := s.deps.Repo.EnsureEpoch(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	type dto struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	out := make([]dto, 0, len(ws))
	cutoff := time.Now().Add(-workerAliveFor).UnixMilli()
	for _, x := range ws {
		if x.LastEpoch == currentEpoch && x.LastSeenAt >= cutoff {
			out = append(out, dto{x.Name, x.URL})
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"servers": out})
}

// ---------- search ----------

// GET /search/suggest?q=: popular searches (starting with q when given). Only terms searched by at least
// two people are shown, so one person's unusual query never reaches others.
func (s *Server) searchSuggest(w http.ResponseWriter, r *http.Request, _ *Principal) {
	if s.deps.Cfg.IsWorker() {
		s.proxyWorkerAPI(w, r)
		return
	}
	q, err := qStr(r, "q", maxQueryLen)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	limit, err := qInt(r, "limit", 12, 1, 30)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	stats, err := s.deps.Repo.SuggestSearches(r.Context(), q, suggestMinCount, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	type dto struct {
		Term  string `json:"term"`
		Count int    `json:"count"`
	}
	out := make([]dto, 0, len(stats))
	for _, st := range stats {
		out = append(out, dto{st.Term, st.Count})
	}
	httpx.WriteJSONETag(w, r, map[string]any{"items": out})
}

// POST /search/hit {"term": "..."} counts one search. At most 30 per minute per user.
func (s *Server) searchHit(w http.ResponseWriter, r *http.Request, p *Principal) {
	if s.deps.Cfg.IsWorker() {
		s.proxyWorkerAPI(w, r)
		return
	}
	var body struct {
		Term string `json:"term"`
	}
	if !httpx.DecodeJSON(w, r, &body, 2<<10) {
		return
	}
	if ok, wait := s.hitLimit.Allow(p.UID); !ok {
		httpx.TooMany(w, wait)
		return
	}
	if err := s.deps.Repo.HitSearch(r.Context(), body.Term); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
