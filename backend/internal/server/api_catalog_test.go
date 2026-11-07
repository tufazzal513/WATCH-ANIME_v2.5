//go:build !nodriver

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"movex/internal/config"
	"movex/internal/httpx"
	"movex/internal/repo"
)

type fx struct {
	t                          *testing.T
	s                          *Server
	r                          *repo.Repo
	film, series, hidden, soon *repo.Movie
}

func newFx(t *testing.T) *fx {
	t.Helper()
	ctx := context.Background()
	s, r := dbServer(t, config.RoleMain)
	if _, err := r.SeedCategories(ctx); err != nil {
		t.Fatal(err)
	}
	f := &fx{t: t, s: s, r: r}
	mk := func(in repo.MovieInput) *repo.Movie {
		m, err := r.CreateMovie(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // distinct created_at
		return m
	}
	y := func(v int) *int { return &v }
	rt := func(v float64) *float64 { return &v }
	f.film = mk(repo.MovieInput{Title: "Great Film", Year: y(2020), Rating: rt(8), Genres: []string{"Action", "Drama"}, Languages: []string{"English"},
		PosterURL: "https://img.example.com/p.jpg", Description: "Plot", Cast: "Ann, Bob", Links: []repo.LinkInput{{URL: "https://cdn.example.com/film.m3u8"}, {Kind: "download", Quality: "720p", URL: "https://cdn.example.com/film.mp4"}}})
	f.series = mk(repo.MovieInput{Title: "Long Series", ContentType: "series", Type: "Anime", Year: y(2018), Rating: rt(7), Genres: []string{"Action"}, Languages: []string{"Japanese"},
		Seasons: []repo.SeasonInput{{Name: "Season 1", Episodes: []repo.EpisodeInput{{Name: "Pilot", Links: []repo.LinkInput{{URL: "https://cdn.example.com/s1e1"}}}, {}}}, {Episodes: []repo.EpisodeInput{{}}}}})
	f.hidden = mk(repo.MovieInput{Title: "Secret Film", IsPrivate: true, Genres: []string{"Action"}, Links: []repo.LinkInput{{URL: "https://cdn.example.com/secret"}}})
	f.soon = mk(repo.MovieInput{Title: "Coming Soon", IsUpcoming: true, ReleaseDate: "2099-01-01T00:00", Genres: []string{"Drama"}})
	return f
}

func (f *fx) call(method, path, uid string, body string, hdr ...string) *httptest.ResponseRecorder {
	f.t.Helper()
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	var req *http.Request
	if rd != nil {
		req = httptest.NewRequest(method, path, rd)
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.RemoteAddr = "192.0.2.10:5555"
	if uid != "" {
		req.Header.Set("Authorization", "Bearer dev:"+uid)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rr := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(rr, req)
	return rr
}

func (f *fx) get(path, uid string) *httptest.ResponseRecorder { return f.call("GET", path, uid, "") }

type listResp struct {
	Items   []map[string]any `json:"items"`
	Total   int              `json:"total"`
	Offset  int              `json:"offset"`
	Limit   int              `json:"limit"`
	HasMore bool             `json:"hasMore"`
}

func (f *fx) list(path string) listResp {
	f.t.Helper()
	rr := f.get(path, "u1")
	if rr.Code != 200 {
		f.t.Fatalf("GET %s: %d %s", path, rr.Code, rr.Body.String())
	}
	var l listResp
	if err := json.Unmarshal(rr.Body.Bytes(), &l); err != nil {
		f.t.Fatal(err)
	}
	return l
}

func titlesOf(l listResp) string {
	var o []string
	for _, it := range l.Items {
		o = append(o, it["title"].(string))
	}
	return strings.Join(o, "|")
}

func TestCatalogNeedsLoginExceptPublicRoutes(t *testing.T) {
	f := newFx(t)
	for _, p := range []string{"/api/v1/movies", "/api/v1/movies/" + f.film.ID, "/api/v1/categories", "/api/v1/catalog/facets", "/api/v1/banners", "/api/v1/social-links",
		"/api/v1/notifications", "/api/v1/search/suggest"} {
		if rr := f.get(p, ""); rr.Code != 401 {
			t.Errorf("GET %s without a token: %d", p, rr.Code)
		}
	}
	for _, p := range []string{"/api/v1/movies/" + f.film.ID + "/view", "/api/v1/search/hit"} {
		if rr := f.call("POST", p, "", `{"term":"abc"}`); rr.Code != 401 {
			t.Errorf("POST %s without a token: %d", p, rr.Code)
		}
	}
	for _, p := range []string{"/api/v1/health", "/api/v1/settings/public", "/api/v1/servers"} {
		if rr := f.get(p, ""); rr.Code != 200 {
			t.Errorf("GET %s is public: %d", p, rr.Code)
		}
	}
}

func TestListMoviesShapeAndPrivacy(t *testing.T) {
	f := newFx(t)
	l := f.list("/api/v1/movies")
	if l.Total != 3 || titlesOf(l) != "Coming Soon|Long Series|Great Film" || l.Limit != 24 || l.Offset != 0 || l.HasMore {
		t.Fatalf("private titles must never be listed; newest first: %+v %s", l, titlesOf(l))
	}
	it := l.Items[2]
	for _, k := range []string{"id", "title", "contentType", "type", "industry", "year", "rating", "posterUrl", "coverUrl", "dubbed", "isUpcoming",
		"releaseDate", "releaseTs", "genres", "category", "languages", "createdAt"} {
		if _, ok := it[k]; !ok {
			t.Errorf("lite card is missing %q", k)
		}
	}
	for _, k := range []string{"description", "cast", "server1", "links", "seasons"} {
		if _, ok := it[k]; ok {
			t.Errorf("lite card must not carry %q (that is the detail shape)", k)
		}
	}
	if it["category"] != "Action" || it["year"].(float64) != 2020 || it["rating"].(float64) != 8 || it["type"] != "Movie" {
		t.Fatalf("%v", it)
	}
	if g := it["genres"].([]any); len(g) != 2 || g[0] != "Action" || g[1] != "Drama" {
		t.Fatalf("genre order is kept: %v", g)
	}
	soon := l.Items[0]
	if soon["isUpcoming"] != true || soon["releaseTs"] == nil || soon["releaseDate"] != "2099-01-01T00:00" {
		t.Fatalf("upcoming titles are listed (the app shows a countdown): %v", soon)
	}
}

func TestListMoviesFiltersPagingAndErrors(t *testing.T) {
	f := newFx(t)
	if got := titlesOf(f.list("/api/v1/movies?category=Drama")); got != "Coming Soon|Great Film" {
		t.Fatal(got)
	}
	if got := titlesOf(f.list("/api/v1/movies?contentType=series")); got != "Long Series" {
		t.Fatal(got)
	}
	if got := titlesOf(f.list("/api/v1/movies?type=anime")); got != "Long Series" {
		t.Fatal(got)
	}
	if got := titlesOf(f.list("/api/v1/movies?language=Japanese&year=2018")); got != "Long Series" {
		t.Fatal(got)
	}
	if got := titlesOf(f.list("/api/v1/movies?upcoming=yes")); got != "Coming Soon" {
		t.Fatal(got)
	}
	if got := titlesOf(f.list("/api/v1/movies?upcoming=no&sort=title")); got != "Great Film|Long Series" {
		t.Fatal(got)
	}
	if got := titlesOf(f.list("/api/v1/movies?q=ann+action")); got != "Great Film" { // cast + genre, any order of words
		t.Fatal(got)
	}
	if got := titlesOf(f.list("/api/v1/movies?q=secret")); got != "" {
		t.Fatalf("search must not find private titles: %q", got)
	}
	if got := titlesOf(f.list("/api/v1/movies?sort=rating")); got != "Great Film|Long Series|Coming Soon" {
		t.Fatal(got)
	}
	if got := titlesOf(f.list("/api/v1/movies?sort=hot")); got != "Great Film|Long Series|Coming Soon" {
		t.Fatal(got)
	}
	if got := titlesOf(f.list("/api/v1/movies?q=film&sort=relevance")); !strings.HasPrefix(got, "Great Film") {
		t.Fatal(got)
	}
	// q without sort => relevance; a missing q with sort=relevance => hot (no error)
	f.list("/api/v1/movies?q=great")
	f.list("/api/v1/movies?sort=relevance")

	p1 := f.list("/api/v1/movies?limit=2")
	p2 := f.list("/api/v1/movies?limit=2&offset=2")
	if p1.Total != 3 || len(p1.Items) != 2 || !p1.HasMore || len(p2.Items) != 1 || p2.HasMore || p2.Offset != 2 {
		t.Fatalf("%+v %+v", p1, p2)
	}

	for _, bad := range []string{"limit=0", "limit=101", "limit=abc", "offset=-1", "offset=100001", "year=1500", "sort=random", "contentType=show",
		"upcoming=maybe", "q=" + strings.Repeat("x", 101), "category=" + strings.Repeat("x", 61), "ids=" + strings.Repeat("a,", 101) + "a", "ids=" + strings.Repeat("a", 41)} {
		rr := f.get("/api/v1/movies?"+bad, "u1")
		if rr.Code != 400 || !strings.Contains(rr.Body.String(), `"validation"`) {
			t.Errorf("%s: %d %s", bad, rr.Code, rr.Body.String())
		}
	}
}

func TestListMoviesByIDsKeepsTheRequestedOrder(t *testing.T) {
	f := newFx(t)
	l := f.list("/api/v1/movies?ids=" + f.series.ID + "," + f.film.ID + "," + f.hidden.ID + ",nope," + f.series.ID)
	if titlesOf(l) != "Long Series|Great Film" || l.Total != 2 || l.HasMore {
		t.Fatalf("requested order, private/unknown/duplicate ids dropped: %s %+v", titlesOf(l), l)
	}
}

func TestMovieDetail(t *testing.T) {
	f := newFx(t)
	rr := f.get("/api/v1/movies/"+f.film.ID, "u1")
	var d map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &d)
	if rr.Code != 200 || d["title"] != "Great Film" || d["description"] != "Plot" || d["cast"] != "Ann, Bob" || d["server1"] != "https://cdn.example.com/film.m3u8" {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	links := d["links"].([]any)
	if len(links) != 2 || links[1].(map[string]any)["kind"] != "download" || links[1].(map[string]any)["quality"] != "720p" {
		t.Fatalf("%v", links)
	}
	if s, ok := d["seasons"].([]any); !ok || len(s) != 0 {
		t.Fatalf("a movie has an empty seasons array, not null: %v", d["seasons"])
	}

	rr = f.get("/api/v1/movies/"+f.series.ID, "u1")
	_ = json.Unmarshal(rr.Body.Bytes(), &d)
	seasons := d["seasons"].([]any)
	s1 := seasons[0].(map[string]any)
	eps := s1["episodes"].([]any)
	if len(seasons) != 2 || s1["name"] != "Season 1" || len(eps) != 2 || eps[0].(map[string]any)["name"] != "Pilot" ||
		eps[0].(map[string]any)["server1"] != "https://cdn.example.com/s1e1" || eps[1].(map[string]any)["server1"] != "" || eps[1].(map[string]any)["name"] != "Episode 2" {
		t.Fatalf("%s", rr.Body.String())
	}

	for _, id := range []string{f.hidden.ID, "nope", strings.Repeat("x", 41)} {
		if rr := f.get("/api/v1/movies/"+id, "u1"); rr.Code != 404 {
			t.Errorf("%s: %d (private and unknown titles are both 404)", id, rr.Code)
		}
	}
}

func TestETagRevalidation(t *testing.T) {
	f := newFx(t)
	for _, path := range []string{"/api/v1/movies", "/api/v1/movies/" + f.film.ID, "/api/v1/categories", "/api/v1/notifications", "/api/v1/settings/public"} {
		uid := "u1"
		if strings.Contains(path, "settings") {
			uid = ""
		}
		first := f.get(path, uid)
		etag := first.Header().Get("ETag")
		if first.Code != 200 || etag == "" || first.Header().Get("Cache-Control") != "private, no-cache" {
			t.Fatalf("%s: %d %v", path, first.Code, first.Header())
		}
		again := f.call("GET", path, uid, "", "If-None-Match", etag)
		if again.Code != 304 || again.Body.Len() != 0 {
			t.Errorf("%s: unchanged data must answer 304, got %d", path, again.Code)
		}
	}
	// when the data changes the old ETag stops matching
	path := "/api/v1/movies"
	etag := f.get(path, "u1").Header().Get("ETag")
	if _, err := f.r.CreateMovie(context.Background(), repo.MovieInput{Title: "Brand New", Genres: []string{"Action"}}); err != nil {
		t.Fatal(err)
	}
	if rr := f.call("GET", path, "u1", "", "If-None-Match", etag); rr.Code != 200 {
		t.Fatalf("changed list must not be 304: %d", rr.Code)
	}
	// the counters must NOT leak into card responses, otherwise every view would invalidate every cache
	etag = f.get(path, "u1").Header().Get("ETag")
	_ = f.r.IncPlays(context.Background(), f.film.ID)
	if rr := f.call("GET", path, "u1", "", "If-None-Match", etag); rr.Code != 304 {
		t.Fatalf("a play count change must not change the list ETag: %d", rr.Code)
	}
}

func TestCatalogFacetsCountOnlyPublicTitles(t *testing.T) {
	f := newFx(t)
	rr := f.get("/api/v1/catalog/facets", "u1")
	if rr.Code != http.StatusOK {
		t.Fatalf("facets status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got repo.CatalogFacets
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	find := func(items []repo.FacetValue, name string) int {
		for _, item := range items {
			if item.Name == name {
				return item.Count
			}
		}
		return 0
	}
	if find(got.Categories, "Action") != 2 {
		t.Fatalf("Action count includes private or misses public titles: %+v", got.Categories)
	}
	if find(got.Industries, "") != 0 {
		t.Fatal("empty industry must not be returned")
	}
	if find(got.Languages, "English") != 1 || find(got.Languages, "Japanese") != 1 {
		t.Fatalf("unexpected language counts: %+v", got.Languages)
	}
	if find(got.Types, "Anime") != 1 {
		t.Fatalf("unexpected type counts: %+v", got.Types)
	}
	if find(got.Years, "2020") != 1 || find(got.Years, "2018") != 1 {
		t.Fatalf("unexpected year counts: %+v", got.Years)
	}
}

func TestCategoriesBannersSocialNotificationsSettings(t *testing.T) {
	f := newFx(t)
	ctx := context.Background()

	var cats struct {
		Items []struct {
			ID, Name string
			Popular  bool
			Sort     int
			Count    int
		}
	}
	rr := f.get("/api/v1/categories", "u1")
	_ = json.Unmarshal(rr.Body.Bytes(), &cats)
	var action int = -1
	for _, c := range cats.Items {
		if c.ID == "action" {
			action = c.Count
		}
	}
	if len(cats.Items) != 69 || action != 2 { // Great Film + Long Series; the private title is not counted
		t.Fatalf("%d categories, action=%d", len(cats.Items), action)
	}

	_, _ = f.r.CreateBanner(ctx, repo.BannerInput{ImageURL: "https://i.example.com/a.jpg", MovieID: f.film.ID, Active: true})
	_, _ = f.r.CreateBanner(ctx, repo.BannerInput{ImageURL: "https://i.example.com/b.jpg", MovieID: f.hidden.ID, Active: true})
	_, _ = f.r.CreateBanner(ctx, repo.BannerInput{ImageURL: "https://i.example.com/c.jpg", Active: false})
	_, _ = f.r.CreateBanner(ctx, repo.BannerInput{ImageURL: "https://i.example.com/d.jpg", LinkURL: "https://promo.example.com", Active: true})
	var ban struct{ Items []map[string]any }
	_ = json.Unmarshal(f.get("/api/v1/banners", "u1").Body.Bytes(), &ban)
	if len(ban.Items) != 2 || ban.Items[0]["movieTitle"] != "Great Film" || ban.Items[0]["movieId"] != f.film.ID || ban.Items[1]["linkUrl"] != "https://promo.example.com" {
		t.Fatalf("inactive banners and banners of private titles are hidden: %v", ban.Items)
	}

	_, _ = f.r.CreateSocialLink(ctx, repo.SocialInput{Name: "Telegram", URL: "https://t.me/x", Icon: "fab fa-telegram"})
	var soc struct{ Items []map[string]any }
	_ = json.Unmarshal(f.get("/api/v1/social-links", "u1").Body.Bytes(), &soc)
	if len(soc.Items) != 1 || soc.Items[0]["icon"] != "fab fa-telegram" {
		t.Fatalf("%v", soc.Items)
	}

	for i := 0; i < 3; i++ {
		_, _ = f.r.CreateNotification(ctx, fmt.Sprintf("N%d", i), "msg")
		time.Sleep(2 * time.Millisecond)
	}
	var nt struct{ Items []map[string]any }
	_ = json.Unmarshal(f.get("/api/v1/notifications?limit=2", "u1").Body.Bytes(), &nt)
	if len(nt.Items) != 2 || nt.Items[0]["title"] != "N2" || nt.Items[0]["timestamp"].(float64) == 0 {
		t.Fatalf("%v", nt.Items)
	}
	if rr := f.get("/api/v1/notifications?limit=0", "u1"); rr.Code != 400 {
		t.Fatal("limit validation")
	}

	_ = f.r.SetSettings(ctx, map[string]string{"appName": "MoveX", "themeMode": "auto", "themeColor": "#e50914", "pwdResetLimit": "3"})
	var st map[string]string
	_ = json.Unmarshal(f.get("/api/v1/settings/public", "").Body.Bytes(), &st)
	if st["appName"] != "MoveX" || st["themeMode"] != "auto" {
		t.Fatalf("%v", st)
	}
	if _, leaked := st["pwdResetLimit"]; leaked {
		t.Fatal("server-side settings must not be public")
	}
}

func TestViewCounting(t *testing.T) {
	f := newFx(t)
	ctx := context.Background()
	plays := func() int { s, _ := f.r.AllMovieStats(ctx); return s[f.film.ID].Plays }
	path := "/api/v1/movies/" + f.film.ID + "/view"

	if rr := f.call("POST", path, "u1", ""); rr.Code != 200 || !strings.Contains(rr.Body.String(), `"counted":true`) || plays() != 1 {
		t.Fatalf("first view: %d %s plays=%d", rr.Code, rr.Body.String(), plays())
	}
	if rr := f.call("POST", path, "u1", ""); !strings.Contains(rr.Body.String(), `"counted":false`) || plays() != 1 {
		t.Fatalf("the same user is counted once per 6 hours: %s plays=%d", rr.Body.String(), plays())
	}
	if rr := f.call("POST", path, "u2", ""); !strings.Contains(rr.Body.String(), `"counted":true`) || plays() != 2 {
		t.Fatalf("another user counts: %s plays=%d", rr.Body.String(), plays())
	}
	if rr := f.call("POST", "/api/v1/movies/"+f.series.ID+"/view", "u1", ""); !strings.Contains(rr.Body.String(), `"counted":true`) {
		t.Fatal("a different title counts again")
	}
	if rr := f.call("POST", "/api/v1/movies/"+f.hidden.ID+"/view", "u1", ""); rr.Code != 404 {
		t.Fatalf("private: %d", rr.Code)
	}
	if rr := f.call("POST", "/api/v1/movies/nope/view", "u1", ""); rr.Code != 404 {
		t.Fatalf("unknown: %d", rr.Code)
	}
}

func TestSearchHitAndSuggest(t *testing.T) {
	f := newFx(t)
	hit := func(uid, body string) int { return f.call("POST", "/api/v1/search/hit", uid, body).Code }

	if hit("u1", `{"term":"Avengers"}`) != 204 || hit("u2", `{"term":" avengers "}`) != 204 || hit("u1", `{"term":"one-off person"}`) != 204 {
		t.Fatal("hits")
	}
	for body, want := range map[string]int{`{"term":"a"}`: 400, `{"term":""}`: 400, `{"term":"` + strings.Repeat("x", 41) + `"}`: 400, `{oops`: 400, `{"term":"ab"} {"x":1}`: 400} {
		if got := hit("u1", body); got != want {
			t.Errorf("%s: %d, want %d", body, got, want)
		}
	}
	if rr := f.call("POST", "/api/v1/search/hit", "u1", `{"term":"`+strings.Repeat("x", 3000)+`"}`); rr.Code != 413 {
		t.Fatalf("oversized body: %d", rr.Code)
	}

	var sg struct {
		Items []struct {
			Term  string
			Count int
		}
	}
	_ = json.Unmarshal(f.get("/api/v1/search/suggest", "u3").Body.Bytes(), &sg)
	if len(sg.Items) != 1 || sg.Items[0].Term != "avengers" || sg.Items[0].Count != 2 {
		t.Fatalf("only terms searched by 2+ people are shown (and normalised): %+v", sg.Items)
	}
	_ = json.Unmarshal(f.get("/api/v1/search/suggest?q=AVE", "u3").Body.Bytes(), &sg)
	if len(sg.Items) != 1 {
		t.Fatal("prefix")
	}
	_ = json.Unmarshal(f.get("/api/v1/search/suggest?q=zzz", "u3").Body.Bytes(), &sg)
	if len(sg.Items) != 0 {
		t.Fatal("no match")
	}

	// per-user limit: 30 per minute
	f.s.hitLimit = httpx.NewLimiter(2, time.Minute)
	codes := []int{hit("rl", `{"term":"one"}`), hit("rl", `{"term":"two"}`), hit("rl", `{"term":"three"}`)}
	if codes[0] != 204 || codes[1] != 204 || codes[2] != 429 {
		t.Fatalf("%v", codes)
	}
}

func TestServersListsOnlyRecentlySeenWorkers(t *testing.T) {
	f := newFx(t)
	ctx := context.Background()
	w1, _, _ := f.r.CreateWorker(ctx, "Singapore", "https://sg.example.com")
	_, _, _ = f.r.CreateWorker(ctx, "Never synced", "https://x.example.com")
	epoch, _ := f.r.EnsureEpoch(ctx)
	_ = f.r.AcknowledgeWorker(ctx, w1.ID, epoch, 0)
	rr := f.get("/api/v1/servers", "")
	var out struct{ Servers []map[string]string }
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if len(out.Servers) != 1 || out.Servers[0]["name"] != "Singapore" || out.Servers[0]["url"] != "https://sg.example.com" {
		t.Fatalf("%s", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "mxw_") || strings.Contains(rr.Body.String(), "hash") {
		t.Fatal("never expose keys")
	}
}

func TestCatalogRoutesOnWorkerRole(t *testing.T) {
	w, wr := dbServer(t, config.RoleWorker)
	_, _ = wr.SeedCategories(context.Background())
	req := func(path string) int {
		rr := httptest.NewRecorder()
		w.Handler().ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
		return rr.Code
	}
	if req("/api/v1/servers") != 404 {
		t.Fatal("/servers is a main-only endpoint")
	}
	if req("/api/v1/settings/public") != 200 {
		t.Fatal("workers serve the public catalog from their replica")
	}
}

func TestBlockedUserCannotBrowse(t *testing.T) {
	f := newFx(t)
	ctx := context.Background()
	_, _, _ = f.r.EnsureUser(ctx, repo.UserProfile{UID: "bad"})
	_ = f.r.SetUserStatus(ctx, "bad", repo.StatusFrozen)
	for _, p := range []string{"/api/v1/movies", "/api/v1/movies/" + f.film.ID, "/api/v1/categories", "/api/v1/search/suggest"} {
		if rr := f.get(p, "bad"); rr.Code != 403 || !strings.Contains(rr.Body.String(), "account_blocked") {
			t.Errorf("%s: %d", p, rr.Code)
		}
	}
	if rr := f.call("POST", "/api/v1/movies/"+f.film.ID+"/view", "bad", ""); rr.Code != 403 {
		t.Fatalf("view: %d", rr.Code)
	}
}
