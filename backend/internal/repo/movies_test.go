//go:build !nodriver

package repo

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCreateAndGetMovie(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	m, err := r.CreateMovie(ctx, basicMovie())
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "The Test Movie" || m.ContentType != "movie" || m.Type != "Movie" || *m.Year != 2023 || *m.Rating != 7.5 {
		t.Fatalf("%+v", m)
	}
	if got := strings.Join(m.Genres, ","); got != "Action,Sci-Fi" { // canonical names, admin order kept
		t.Fatalf("genres %q", got)
	}
	if got := strings.Join(m.Languages, ","); got != "English,Hindi" {
		t.Fatalf("languages %q", got)
	}
	if FirstStream(m.Links) != "https://cdn.example.com/m.m3u8" {
		t.Fatalf("links %+v", m.Links)
	}
	if len(m.Seasons) != 0 || m.CreatedAt == 0 || m.UpdatedAt != m.CreatedAt {
		t.Fatalf("%+v", m)
	}
	if _, err := r.GetMovie(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestSeriesTreeRoundTripAndSwitchToMovie(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	in := basicMovie()
	in.ContentType = "series"
	in.Links = nil
	in.Seasons = []SeasonInput{
		{Name: "Season 1", Episodes: []EpisodeInput{
			{Name: "Pilot", Links: []LinkInput{{URL: "https://a.example.com/1"}, {Kind: "download", Quality: "720p", URL: "https://a.example.com/1.mp4"}}},
			{Links: []LinkInput{{URL: "https://a.example.com/2"}}},
		}},
		{Episodes: []EpisodeInput{{Name: "Finale", Links: []LinkInput{{URL: "https://a.example.com/3"}}}}},
	}
	m, err := r.CreateMovie(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if m.Type != "Series" || len(m.Seasons) != 2 || m.Seasons[1].Name != "Season 2" {
		t.Fatalf("%+v", m.Seasons)
	}
	s1 := m.Seasons[0]
	if len(s1.Episodes) != 2 || s1.Episodes[0].Name != "Pilot" || s1.Episodes[1].Name != "Episode 2" || s1.Episodes[1].Number != 2 {
		t.Fatalf("%+v", s1.Episodes)
	}
	if len(s1.Episodes[0].Links) != 2 || FirstStream(s1.Episodes[0].Links) != "https://a.example.com/1" || s1.Episodes[0].Links[1].Kind != "download" {
		t.Fatalf("%+v", s1.Episodes[0].Links)
	}

	// nil Seasons on update keeps the tree
	up := in
	up.Title = "Renamed"
	up.Seasons = nil
	m2, err := r.UpdateMovie(ctx, m.ID, up)
	if err != nil || m2.Title != "Renamed" || len(m2.Seasons) != 2 {
		t.Fatalf("keep tree: %v %+v", err, m2)
	}
	// non-nil replaces
	up.Seasons = []SeasonInput{{Name: "Only", Episodes: []EpisodeInput{{Name: "E1"}}}}
	if m3, err := r.UpdateMovie(ctx, m.ID, up); err != nil || len(m3.Seasons) != 1 || m3.Seasons[0].Name != "Only" {
		t.Fatalf("replace tree: %v %+v", err, m3)
	}
	// switching to movie drops seasons and their links
	up.ContentType, up.Seasons = "movie", nil
	if m4, err := r.UpdateMovie(ctx, m.ID, up); err != nil || len(m4.Seasons) != 0 {
		t.Fatalf("to movie: %v %+v", err, m4)
	}
	var n int
	_ = r.db.QueryRow(`SELECT COUNT(*) FROM episodes`).Scan(&n)
	if n != 0 {
		t.Fatalf("episodes must be gone, have %d", n)
	}
	_ = r.db.QueryRow(`SELECT COUNT(*) FROM links WHERE episode_id IS NOT NULL`).Scan(&n)
	if n != 0 {
		t.Fatalf("episode links must be gone, have %d", n)
	}
}

func TestMovieValidation(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	mut := func(f func(*MovieInput)) MovieInput { in := basicMovie(); f(&in); return in }
	cases := map[string]MovieInput{
		"empty title":     mut(func(m *MovieInput) { m.Title = "   " }),
		"long title":      mut(func(m *MovieInput) { m.Title = strings.Repeat("x", 301) }),
		"bad content":     mut(func(m *MovieInput) { m.ContentType = "show" }),
		"bad type":        mut(func(m *MovieInput) { m.Type = "Podcast" }),
		"bad industry":    mut(func(m *MovieInput) { m.Industry = "Mars" }),
		"bad year":        mut(func(m *MovieInput) { m.Year = intp(1700) }),
		"bad rating":      mut(func(m *MovieInput) { m.Rating = floatp(11) }),
		"js poster":       mut(func(m *MovieInput) { m.PosterURL = "javascript:alert(1)" }),
		"unknown genre":   mut(func(m *MovieInput) { m.Genres = []string{"Nonexistent"} }),
		"unknown lang":    mut(func(m *MovieInput) { m.Languages = []string{"Klingon"} }),
		"js stream link":  mut(func(m *MovieInput) { m.Links = []LinkInput{{URL: "javascript:alert(1)"}} }),
		"bad link kind":   mut(func(m *MovieInput) { m.Links = []LinkInput{{Kind: "torrent", URL: "https://a.com/x"}} }),
		"upcoming no day": mut(func(m *MovieInput) { m.IsUpcoming = true }),
		"upcoming bad":    mut(func(m *MovieInput) { m.IsUpcoming, m.ReleaseDate = true, "tomorrow" }),
		"dup season": mut(func(m *MovieInput) {
			m.ContentType = "series"
			m.Seasons = []SeasonInput{{Name: "S"}, {Name: "s"}}
		}),
	}
	for name, in := range cases {
		_, err := r.CreateMovie(ctx, in)
		if !IsValidation(err) {
			t.Errorf("%s: want validation error, got %v", name, err)
		}
	}
	if n, _ := r.CountMovies(ctx); n != 0 {
		t.Fatalf("failed creates must leave nothing behind, have %d movies", n)
	}
}

func TestUpcomingDerivesTimestamp(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	in := basicMovie()
	in.IsUpcoming, in.ReleaseDate = true, "2030-05-01T20:30"
	m, err := r.CreateMovie(ctx, in)
	if err != nil || m.ReleaseTS == nil || *m.ReleaseTS != time.Date(2030, 5, 1, 20, 30, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("%v %+v", err, m)
	}
	in.ReleaseTS = 12345 // explicit timestamp from the browser wins
	if m, _ = r.UpdateMovie(ctx, m.ID, in); *m.ReleaseTS != 12345 {
		t.Fatalf("explicit ts lost: %v", *m.ReleaseTS)
	}
	in.IsUpcoming = false
	if m, _ = r.UpdateMovie(ctx, m.ID, in); m.ReleaseDate != "" || m.ReleaseTS != nil || m.IsUpcoming {
		t.Fatalf("not upcoming must clear date: %+v", m)
	}
}

func TestListMoviesFiltersSearchSortPaging(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	clock := time.Now() // strictly increasing fake clock: every call moves 1 ms forward
	r.now = func() time.Time { clock = clock.Add(time.Millisecond); return clock }
	mk := func(f func(*MovieInput)) *Movie {
		in := basicMovie()
		f(&in)
		m, err := r.CreateMovie(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	avengers := mk(func(m *MovieInput) {
		m.Title, m.Cast, m.Genres, m.Year, m.Rating, m.Languages = "Avengers Endgame", "Robert Downey", []string{"Action", "Superhero"}, intp(2019), floatp(8.4), []string{"English"}
	})
	mk(func(m *MovieInput) {
		m.Title, m.Genres, m.Year, m.Rating, m.Industry, m.Languages, m.IsPrivate = "Dangal", []string{"Drama"}, intp(2016), floatp(8.3), "Bollywood", []string{"Hindi"}, true
	})
	mk(func(m *MovieInput) {
		m.Title, m.ContentType, m.Type, m.Genres, m.Year, m.Rating, m.Industry, m.Links = "Naruto", "series", "Anime", []string{"Action", "Shounen"}, intp(2002), nil, "Japanese", nil
		m.Languages = []string{"Japanese"}
	})
	future := mk(func(m *MovieInput) {
		m.Title, m.IsUpcoming, m.ReleaseDate, m.Year = "Future Film", true, "2099-01-01T00:00", intp(2099)
	})
	released := mk(func(m *MovieInput) {
		m.Title, m.IsUpcoming, m.ReleaseDate, m.Year = "Past Release", true, "2001-01-01T00:00", intp(2001)
	})

	count := func(o ListOpts) int {
		t.Helper()
		_, total, err := r.ListMovies(ctx, o)
		if err != nil {
			t.Fatal(err)
		}
		return total
	}
	if count(ListOpts{}) != 5 {
		t.Fatal("total")
	}
	// old client search: every word is a substring of title/cast/genres/industry/languages/type/year, case-insensitive
	if count(ListOpts{Query: "AVENG"}) != 1 || count(ListOpts{Query: "downey action"}) != 1 || count(ListOpts{Query: "downey drama"}) != 0 {
		t.Fatal("word search")
	}
	if count(ListOpts{Query: "bollywood 2016 hindi"}) != 1 || count(ListOpts{Query: "anime japanese"}) != 1 || count(ListOpts{Query: "2099"}) != 1 {
		t.Fatal("search over metadata")
	}
	if count(ListOpts{Query: "100%"}) != 0 || count(ListOpts{Query: "_"}) != 0 {
		t.Fatal("LIKE wildcards must be escaped")
	}
	if count(ListOpts{Genre: "Action"}) != 4 || count(ListOpts{Language: "hindi"}) != 3 || count(ListOpts{Year: 2019}) != 1 {
		t.Fatal("genre/language/year filters")
	}
	if count(ListOpts{ContentType: "series"}) != 1 || count(ListOpts{Type: "anime"}) != 1 || count(ListOpts{Industry: "Bollywood"}) != 1 {
		t.Fatal("type filters")
	}
	if count(ListOpts{Private: boolp(true)}) != 1 || count(ListOpts{Private: boolp(false)}) != 4 {
		t.Fatal("private filter")
	}
	if count(ListOpts{Upcoming: "yes"}) != 1 || count(ListOpts{Upcoming: "no"}) != 4 {
		t.Fatal("upcoming filter: a title whose release time has passed is no longer upcoming")
	}

	// sort + paging
	page, total, err := r.ListMovies(ctx, ListOpts{Sort: "title", Limit: 2, Offset: 1})
	if err != nil || total != 5 || len(page) != 2 || page[0].Title != "Dangal" || page[1].Title != "Future Film" {
		t.Fatalf("title page: %v %d %+v", err, total, titles(page))
	}
	if top, _, _ := r.ListMovies(ctx, ListOpts{Sort: "rating", Limit: 1}); top[0].ID != avengers.ID {
		t.Fatalf("rating sort: %v", titles(top))
	}
	newest, _, _ := r.ListMovies(ctx, ListOpts{Limit: 5})
	if newest[0].ID != released.ID || newest[1].ID != future.ID {
		t.Fatalf("default sort is newest first: %v", titles(newest))
	}
	if len(newest[len(newest)-1].Genres) == 0 {
		t.Fatal("list must include tags")
	}
	if _, _, err := r.ListMovies(ctx, ListOpts{Sort: "bogus"}); !IsValidation(err) {
		t.Fatal("unknown sort must be a validation error")
	}
	if ys, _ := r.MovieYears(ctx); len(ys) != 5 || ys[0] != 2099 {
		t.Fatalf("years %v", ys)
	}
}

func titles(ms []*Movie) []string {
	var o []string
	for _, m := range ms {
		o = append(o, m.Title)
	}
	return o
}

func TestDeleteMovieCascades(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	in := basicMovie()
	in.ContentType, in.Links = "series", []LinkInput{{URL: "https://a.com/x"}}
	in.Seasons = []SeasonInput{{Episodes: []EpisodeInput{{Links: []LinkInput{{URL: "https://a.com/e1"}}}}}}
	m, _ := r.CreateMovie(ctx, in)
	b, err := r.CreateBanner(ctx, BannerInput{ImageURL: "https://img.example.com/b.jpg", MovieID: m.ID, Active: true})
	if err != nil || b.MovieTitle != "The Test Movie" {
		t.Fatalf("%v %+v", err, b)
	}
	if err := r.DeleteMovie(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"movies", "seasons", "episodes", "links", "movie_categories", "movie_languages"} {
		var n int
		_ = r.db.QueryRow(`SELECT COUNT(*) FROM ` + tbl).Scan(&n)
		if n != 0 {
			t.Errorf("%s has %d rows left", tbl, n)
		}
	}
	if b2, err := r.GetBanner(ctx, b.ID); err != nil || b2.MovieID != "" || b2.MovieTitle != "" {
		t.Fatalf("banner must survive unlinked: %v %+v", err, b2)
	}
	if err := r.DeleteMovie(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestSetLinksAndReplaceSeasons(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	in := basicMovie()
	in.ContentType, in.Links = "series", nil
	in.Seasons = []SeasonInput{{Episodes: []EpisodeInput{{Links: []LinkInput{{URL: "https://a.com/1"}}}}}}
	m, _ := r.CreateMovie(ctx, in)
	ep := m.Seasons[0].Episodes[0]

	if err := r.SetLinks(ctx, m.ID, ep.ID, []LinkInput{{URL: "https://b.com/1"}, {Kind: "download", URL: "https://b.com/1.mp4"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.SetLinks(ctx, m.ID, "no-such-episode", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign episode: %v", err)
	}
	if err := r.SetLinks(ctx, "no-such-movie", "", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing movie: %v", err)
	}
	if err := r.SetLinks(ctx, m.ID, ep.ID, []LinkInput{{URL: "not a url"}}); !IsValidation(err) {
		t.Fatalf("bad url: %v", err)
	}
	got, _ := r.GetMovie(ctx, m.ID)
	if l := got.Seasons[0].Episodes[0].Links; len(l) != 2 || l[0].URL != "https://b.com/1" {
		t.Fatalf("%+v", l)
	}
	if err := r.ReplaceSeasons(ctx, m.ID, []SeasonInput{{Name: "A"}, {Name: "B"}}); err != nil {
		t.Fatal(err)
	}
	got, _ = r.GetMovie(ctx, m.ID)
	if len(got.Seasons) != 2 || len(got.Seasons[0].Episodes) != 0 || got.Seasons[1].Number != 2 {
		t.Fatalf("%+v", got.Seasons)
	}
}
