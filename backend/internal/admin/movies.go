package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"movex/internal/repo"
	"movex/internal/taxonomy"
)

type movieFormData struct {
	Movie                        *repo.Movie
	Categories                   []repo.Category
	Types, Industries, Languages []string
	IsNew                        bool
	Error                        string
}

func (a *Admin) movieForm(c *Ctx, m *repo.Movie, isNew bool, message string, status int) {
	cats, err := a.deps.Repo.ListCategories(c.R.Context())
	if err != nil {
		c.Error(http.StatusInternalServerError, "Could not load genres")
		return
	}
	if m == nil {
		m = &repo.Movie{ContentType: "movie", Type: "Movie"}
	}
	title := "Edit Content"
	if isNew {
		title = "Add Content"
	}
	c.Render("movie-form", Page{Title: title, Active: "movies", Status: status, Data: movieFormData{Movie: m, Categories: cats, Types: taxonomy.Types, Industries: taxonomy.Industries, Languages: taxonomy.Languages, IsNew: isNew, Error: message}})
}
func (a *Admin) newMoviePage(c *Ctx) { a.movieForm(c, nil, true, "", http.StatusOK) }
func (a *Admin) editMoviePage(c *Ctx) {
	m, err := a.deps.Repo.GetMovie(c.R.Context(), c.R.PathValue("id"))
	if err != nil {
		if err == repo.ErrNotFound {
			c.Error(http.StatusNotFound, "Content not found")
		} else {
			c.Error(http.StatusInternalServerError, "Could not load content")
		}
		return
	}
	a.movieForm(c, m, false, "", http.StatusOK)
}
func (a *Admin) saveMovie(c *Ctx) {
	c.R.Body = http.MaxBytesReader(c.W, c.R.Body, maxFormBytes)
	if err := c.R.ParseForm(); err != nil {
		c.Error(http.StatusBadRequest, "Invalid or oversized form")
		return
	}
	id := c.R.PathValue("id")
	isNew := id == ""
	in, m, err := parseMovieForm(c.R.Form)
	if !isNew && m != nil {
		if existing, loadErr := a.deps.Repo.GetMovie(c.R.Context(), id); loadErr == nil {
			m.Seasons = existing.Seasons
		}
	}
	if err != nil {
		a.movieForm(c, m, isNew, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	var saved *repo.Movie
	if isNew {
		saved, err = a.deps.Repo.CreateMovie(c.R.Context(), in)
	} else {
		saved, err = a.deps.Repo.UpdateMovie(c.R.Context(), id, in)
	}
	if err != nil {
		if err == repo.ErrNotFound {
			c.Error(http.StatusNotFound, "Content not found")
			return
		}
		a.movieForm(c, m, isNew, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	m = saved
	action := "movie.create"
	if !isNew {
		action = "movie.update"
	}
	c.Audit(action, m.ID, "Saved content: "+m.Title)
	c.Flash("success", "Content saved successfully.")
	c.Redirect("/admin/movies/" + url.PathEscape(m.ID) + "/edit")
}
func parseMovieForm(v url.Values) (repo.MovieInput, *repo.Movie, error) {
	m := &repo.Movie{Title: strings.TrimSpace(v.Get("title")), ContentType: v.Get("content_type"), Type: v.Get("type"), Industry: v.Get("industry"), PosterURL: strings.TrimSpace(v.Get("poster_url")), CoverURL: strings.TrimSpace(v.Get("cover_url")), Cast: strings.TrimSpace(v.Get("cast")), Description: strings.TrimSpace(v.Get("description")), Dubbed: v.Get("dubbed") == "on", IsPrivate: v.Get("is_private") == "on", IsUpcoming: v.Get("is_upcoming") == "on", ReleaseDate: strings.TrimSpace(v.Get("release_date")), Genres: append([]string(nil), v["genres"]...), Languages: append([]string(nil), v["languages"]...)}
	if m.ContentType == "" {
		m.ContentType = "movie"
	}
	if m.Type == "" {
		if m.ContentType == "series" {
			m.Type = "Series"
		} else {
			m.Type = "Movie"
		}
	}
	if raw := strings.TrimSpace(v.Get("year")); raw != "" {
		y, e := strconv.Atoi(raw)
		if e != nil {
			return repo.MovieInput{}, m, fmt.Errorf("Release year must be a number")
		}
		m.Year = &y
	}
	if raw := strings.TrimSpace(v.Get("rating")); raw != "" {
		f, e := strconv.ParseFloat(raw, 64)
		if e != nil {
			return repo.MovieInput{}, m, fmt.Errorf("Rating must be a number between 0 and 10")
		}
		m.Rating = &f
	}
	ts, _ := strconv.ParseInt(strings.TrimSpace(v.Get("release_ts")), 10, 64)
	links := []repo.LinkInput{}
	if raw := strings.TrimSpace(v.Get("movie_links_json")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &links); err != nil {
			return repo.MovieInput{}, m, fmt.Errorf("Movie links must be valid JSON")
		}
	} else if raw := strings.TrimSpace(v.Get("video_url")); raw != "" {
		links = append(links, repo.LinkInput{Kind: "stream", Label: "Primary", URL: raw})
	}
	for _, l := range links {
		m.Links = append(m.Links, repo.Link{Kind: l.Kind, Quality: l.Quality, Label: l.Label, URL: l.URL})
	}
	var seasons []repo.SeasonInput
	if raw := strings.TrimSpace(v.Get("seasons_json")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &seasons); err != nil {
			return repo.MovieInput{}, m, fmt.Errorf("Seasons and episodes must be valid JSON")
		}
		m.Seasons = make([]repo.Season, 0, len(seasons))
		for si, season := range seasons {
			sm := repo.Season{Number: si + 1, Name: season.Name}
			for ei, episode := range season.Episodes {
				em := repo.Episode{Number: ei + 1, Name: episode.Name}
				for _, link := range episode.Links {
					em.Links = append(em.Links, repo.Link{Kind: link.Kind, Quality: link.Quality, Label: link.Label, URL: link.URL})
				}
				sm.Episodes = append(sm.Episodes, em)
			}
			m.Seasons = append(m.Seasons, sm)
		}
	}
	in := repo.MovieInput{Title: m.Title, ContentType: m.ContentType, Type: m.Type, Industry: m.Industry, Year: m.Year, Rating: m.Rating, PosterURL: m.PosterURL, CoverURL: m.CoverURL, Cast: m.Cast, Description: m.Description, Dubbed: m.Dubbed, IsPrivate: m.IsPrivate, IsUpcoming: m.IsUpcoming, ReleaseDate: m.ReleaseDate, ReleaseTS: ts, Genres: m.Genres, Languages: m.Languages, Links: links, Seasons: seasons}
	if in.Title == "" {
		return in, m, fmt.Errorf("Content title is required")
	}
	if in.PosterURL == "" {
		return in, m, fmt.Errorf("Poster image URL is required")
	}
	if in.IsUpcoming && in.ReleaseDate == "" {
		return in, m, fmt.Errorf("Release date and time are required for upcoming content")
	}
	return in, m, nil
}
func (a *Admin) deleteMovie(c *Ctx) {
	id := c.R.PathValue("id")
	m, err := a.deps.Repo.GetMovie(c.R.Context(), id)
	if err != nil {
		if err == repo.ErrNotFound {
			c.Error(http.StatusNotFound, "Content not found")
		} else {
			c.Error(http.StatusInternalServerError, "Could not load content")
		}
		return
	}
	if err := a.deps.Repo.DeleteMovie(c.R.Context(), id); err != nil {
		c.Error(http.StatusInternalServerError, "Could not delete content")
		return
	}
	c.Audit("movie.delete", id, "Deleted content: "+m.Title)
	c.Flash("success", "Content deleted successfully.")
	c.Redirect("/admin/movies/new")
}

type movieListData struct {
	Movies                                                                      []*repo.Movie
	Total, Page, Pages                                                          int
	Query, ContentType, Status, Upcoming, Sort, Genre, Industry, Language, Year string
	Categories                                                                  []repo.Category
	Years                                                                       []int
	Industries, Languages                                                       []string
}

func (a *Admin) allMoviesPage(c *Ctx) {
	q := c.R.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	const pageSize = 25
	query := strings.TrimSpace(q.Get("q"))
	contentType := q.Get("type")
	if contentType == "all" {
		contentType = ""
	}
	status := q.Get("status")
	var private *bool
	if status == "private" {
		v := true
		private = &v
	} else if status == "public" {
		v := false
		private = &v
	}
	upcoming := q.Get("upcoming")
	if upcoming == "all" {
		upcoming = ""
	}
	sortBy := q.Get("sort")
	if sortBy == "" {
		sortBy = "new"
	}
	year, _ := strconv.Atoi(q.Get("year"))
	opts := repo.ListOpts{Query: query, ContentType: contentType, Private: private, Upcoming: upcoming, Sort: sortBy, Genre: q.Get("genre"), Industry: q.Get("industry"), Language: q.Get("language"), Year: year, Limit: pageSize, Offset: (page - 1) * pageSize}
	movies, total, err := a.deps.Repo.ListMovies(c.R.Context(), opts)
	if err != nil {
		c.Error(http.StatusInternalServerError, "Could not load content list")
		return
	}
	cats, err := a.deps.Repo.ListCategories(c.R.Context())
	if err != nil {
		c.Error(http.StatusInternalServerError, "Could not load genres")
		return
	}
	years, err := a.deps.Repo.MovieYears(c.R.Context())
	if err != nil {
		c.Error(http.StatusInternalServerError, "Could not load years")
		return
	}
	pages := (total + pageSize - 1) / pageSize
	if pages < 1 {
		pages = 1
	}
	data := movieListData{Movies: movies, Total: total, Page: page, Pages: pages, Query: query, ContentType: q.Get("type"), Status: status, Upcoming: q.Get("upcoming"), Sort: sortBy, Genre: q.Get("genre"), Industry: q.Get("industry"), Language: q.Get("language"), Year: q.Get("year"), Categories: cats, Years: years, Industries: taxonomy.Industries, Languages: taxonomy.Languages}
	c.Render("all-movies", Page{Title: "All Movies", Active: "movies", Data: data})
}

func (a *Admin) bulkMovies(c *Ctx) {
	c.R.Body = http.MaxBytesReader(c.W, c.R.Body, 1<<20)
	if err := c.R.ParseForm(); err != nil {
		c.Error(http.StatusBadRequest, "Invalid bulk request")
		return
	}
	ids := c.R.Form["ids"]
	if len(ids) == 0 || len(ids) > 100 {
		c.Error(http.StatusBadRequest, "Select between 1 and 100 titles")
		return
	}
	action := c.R.Form.Get("action")
	if action != "delete" {
		c.Error(http.StatusBadRequest, "Unsupported bulk action")
		return
	}
	deleted := 0
	for _, id := range ids {
		m, err := a.deps.Repo.GetMovie(c.R.Context(), id)
		if err != nil {
			continue
		}
		if err = a.deps.Repo.DeleteMovie(c.R.Context(), id); err == nil {
			deleted++
			c.Audit("movie.bulk_delete", id, "Bulk deleted: "+m.Title)
		}
	}
	c.Flash("success", fmt.Sprintf("%d title(s) deleted.", deleted))
	c.Redirect("/admin/movies")
}
