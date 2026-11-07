//go:build !nodriver

package repo

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSeedCategoriesIsIdempotent(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t) // already seeded once
	if n, err := r.SeedCategories(ctx); err != nil || n != 0 {
		t.Fatalf("second seed must do nothing: %d %v", n, err)
	}
	cats, _ := r.ListCategories(ctx)
	if len(cats) != 69 || cats[0].Name != "Action" || !cats[0].Popular {
		t.Fatalf("%d %+v", len(cats), cats[0])
	}
	var scifi *Category
	for i := range cats {
		if cats[i].Name == "Sci-Fi" {
			scifi = &cats[i]
		}
	}
	if scifi == nil || scifi.ID != "sci-fi" || !scifi.Popular {
		t.Fatalf("%+v", scifi)
	}
}

func TestCategoryCRUDAndSearchRefresh(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	in := basicMovie() // genres Action, Sci-Fi
	m, _ := r.CreateMovie(ctx, in)

	c, err := r.CreateCategory(ctx, "  K-Drama ", true, 0)
	if err != nil || c.ID != "k-drama" || c.Sort <= 690 || !c.Popular {
		t.Fatalf("%v %+v", err, c)
	}
	if _, err := r.CreateCategory(ctx, "k-drama", false, 0); !IsValidation(err) {
		t.Fatalf("duplicate name (case-insensitive) must be a validation error: %v", err)
	}
	if _, err := r.CreateCategory(ctx, "  ", false, 0); !IsValidation(err) {
		t.Fatal("empty name")
	}
	// same slug, different name -> unique id
	c2, err := r.CreateCategory(ctx, "K Drama", false, 0)
	if err != nil || c2.ID == c.ID {
		t.Fatalf("slug collision: %v %+v", err, c2)
	}

	// rename flows into movie genres and search
	if _, err := r.UpdateCategory(ctx, "sci-fi", "Science Fiction", true, 440); err != nil {
		t.Fatal(err)
	}
	got, _ := r.GetMovie(ctx, m.ID)
	if strings.Join(got.Genres, ",") != "Action,Science Fiction" {
		t.Fatalf("%v", got.Genres)
	}
	if _, total, _ := r.ListMovies(ctx, ListOpts{Query: "science fiction"}); total != 1 {
		t.Fatal("renamed genre must be searchable")
	}
	if _, total, _ := r.ListMovies(ctx, ListOpts{Query: "sci-fi"}); total != 0 {
		t.Fatal("old genre name must no longer match")
	}
	if _, err := r.UpdateCategory(ctx, "action", "science fiction", true, 10); !IsValidation(err) {
		t.Fatalf("rename onto existing name: %v", err)
	}
	if _, err := r.UpdateCategory(ctx, "nope", "x", false, 1); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing category")
	}

	// delete removes the genre from movies
	if err := r.DeleteCategory(ctx, "action"); err != nil {
		t.Fatal(err)
	}
	got, _ = r.GetMovie(ctx, m.ID)
	if strings.Join(got.Genres, ",") != "Science Fiction" {
		t.Fatalf("%v", got.Genres)
	}
	if _, total, _ := r.ListMovies(ctx, ListOpts{Query: "action"}); total != 0 {
		t.Fatal("deleted genre must leave the search text")
	}
	if err := r.DeleteCategory(ctx, "action"); !errors.Is(err, ErrNotFound) {
		t.Fatal("second delete")
	}

	// reorder
	if err := r.ReorderCategories(ctx, []string{"k-drama", "drama"}); err != nil {
		t.Fatal(err)
	}
	cats, _ := r.ListCategories(ctx)
	if cats[0].ID != "k-drama" || cats[1].ID != "drama" || cats[2].ID != "adventure" {
		t.Fatalf("listed ids first, others keep their order: %v %v %v", cats[0].ID, cats[1].ID, cats[2].ID)
	}
	seen := map[int]bool{}
	for _, c := range cats {
		if seen[c.Sort] {
			t.Fatalf("sort values must stay unique, %d repeated", c.Sort)
		}
		seen[c.Sort] = true
	}
	if one, _ := r.GetCategory(ctx, "science-fiction"); one != nil {
		t.Fatal("ids do not change on rename; science-fiction is not an id")
	}
	if one, err := r.GetCategory(ctx, "sci-fi"); err != nil || one.MovieCount != 1 || one.Name != "Science Fiction" {
		t.Fatalf("%v %+v", err, one)
	}
}

func TestBanners(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	m, _ := r.CreateMovie(ctx, basicMovie())
	if _, err := r.CreateBanner(ctx, BannerInput{ImageURL: "javascript:x"}); !IsValidation(err) {
		t.Fatal("bad image url")
	}
	if _, err := r.CreateBanner(ctx, BannerInput{ImageURL: "https://i.example.com/1.jpg", MovieID: "missing"}); !IsValidation(err) {
		t.Fatal("unknown movie")
	}
	b1, err := r.CreateBanner(ctx, BannerInput{ImageURL: "https://i.example.com/1.jpg", MovieID: m.ID, Active: true})
	if err != nil || b1.MovieTitle != "The Test Movie" || b1.Sort != 10 {
		t.Fatalf("%v %+v", err, b1)
	}
	b2, _ := r.CreateBanner(ctx, BannerInput{ImageURL: "https://i.example.com/2.jpg", LinkURL: "https://promo.example.com", Active: false})
	if b2.Sort != 20 || b2.MovieID != "" {
		t.Fatalf("%+v", b2)
	}
	if act, _ := r.ListBanners(ctx, true); len(act) != 1 || act[0].ID != b1.ID {
		t.Fatalf("active only: %+v", act)
	}
	if err := r.ReorderBanners(ctx, []string{b2.ID, b1.ID}); err != nil {
		t.Fatal(err)
	}
	if all, _ := r.ListBanners(ctx, false); all[0].ID != b2.ID {
		t.Fatal("reorder")
	}
	up, err := r.UpdateBanner(ctx, b2.ID, BannerInput{ImageURL: "https://i.example.com/3.jpg", Sort: 5, Active: true})
	if err != nil || !up.Active || up.ImageURL != "https://i.example.com/3.jpg" || up.LinkURL != "" {
		t.Fatalf("%v %+v", err, up)
	}
	if err := r.DeleteBanner(ctx, b1.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteBanner(ctx, b1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("second delete")
	}
}

func TestSocialLinksAndNotifications(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	clock := time.Now()
	r.now = func() time.Time { clock = clock.Add(time.Second); return clock }

	for _, bad := range []SocialInput{
		{Name: "", URL: "https://t.me/x"}, {Name: "TG", URL: ""}, {Name: "TG", URL: "javascript:alert(1)"},
		{Name: "TG", URL: "https://t.me/x", Icon: `x" onclick="alert(1)`},
	} {
		if _, err := r.CreateSocialLink(ctx, bad); !IsValidation(err) {
			t.Errorf("%+v must be rejected", bad)
		}
	}
	s, err := r.CreateSocialLink(ctx, SocialInput{Name: " Telegram ", URL: "https://t.me/x", Icon: "FAB fa-telegram"})
	if err != nil || s.Name != "Telegram" || s.Icon != "fab fa-telegram" {
		t.Fatalf("%v %+v", err, s)
	}
	s2, _ := r.CreateSocialLink(ctx, SocialInput{Name: "Mail", URL: "mailto:a@b.com"})
	if s2.Icon != "fas fa-link" || s2.Sort <= s.Sort {
		t.Fatalf("%+v", s2)
	}
	if up, err := r.UpdateSocialLink(ctx, s.ID, SocialInput{Name: "TG", URL: "https://t.me/y"}); err != nil || up.URL != "https://t.me/y" {
		t.Fatalf("%v %+v", err, up)
	}
	if l, _ := r.ListSocialLinks(ctx); len(l) != 2 || l[0].ID != s.ID {
		t.Fatalf("%+v", l)
	}
	if err := r.DeleteSocialLink(ctx, s2.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := r.CreateNotification(ctx, "", "msg"); !IsValidation(err) {
		t.Fatal("empty title")
	}
	if _, err := r.CreateNotification(ctx, "t", strings.Repeat("x", 1001)); !IsValidation(err) {
		t.Fatal("long message")
	}
	n1, _ := r.CreateNotification(ctx, "First", "one")
	n2, _ := r.CreateNotification(ctx, "Second", "two")
	if l, _ := r.ListNotifications(ctx, 0); l[0].ID != n2.ID {
		t.Fatal("newest first")
	}
	up, err := r.UpdateNotification(ctx, n1.ID, "First (edited)", "one!")
	if err != nil || up.SentAt <= n2.SentAt {
		t.Fatalf("editing refreshes sent_at like the old panel: %v %+v", err, up)
	}
	if l, _ := r.ListNotifications(ctx, 0); l[0].ID != n1.ID {
		t.Fatal("edited notification moves to the top")
	}
	if err := r.DeleteNotification(ctx, n2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.UpdateNotification(ctx, n2.ID, "a", "b"); !errors.Is(err, ErrNotFound) {
		t.Fatal("update deleted")
	}
}

func TestSettings(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	bads := []map[string]string{
		{"adminUIDs": "x"}, {"themeMode": "dark"}, {"themeColor": "red"}, {"themeInterval": "1"}, {"pwdResetLimit": "0"},
		{"appName": ""}, {"appLogo": "javascript:alert(1)"},
	}
	for _, b := range bads {
		if err := r.SetSettings(ctx, b); !IsValidation(err) {
			t.Errorf("%v must be rejected: %v", b, err)
		}
	}
	// all-or-nothing
	if err := r.SetSettings(ctx, map[string]string{"appName": "MoveX", "themeMode": "nope"}); err == nil {
		t.Fatal("expected error")
	}
	if all, _ := r.GetSettings(ctx); len(all) != 0 {
		t.Fatalf("a rejected batch must store nothing: %v", all)
	}
	err := r.SetSettings(ctx, map[string]string{"appName": " MoveX ", "themeMode": "auto", "themeColor": "#E50914", "themeInterval": "60", "pwdResetLimit": "3", "appLogo": ""})
	if err != nil {
		t.Fatal(err)
	}
	all, _ := r.GetSettings(ctx)
	if all["appName"] != "MoveX" || all["themeColor"] != "#e50914" || all["pwdResetLimit"] != "3" {
		t.Fatalf("%v", all)
	}
	pub, _ := r.PublicSettings(ctx)
	if _, leaked := pub["pwdResetLimit"]; leaked || pub["themeMode"] != "auto" || len(pub) != 5 {
		t.Fatalf("public settings: %v", pub)
	}
}

// ---------- the replication log ----------

func TestChangesLogCoversEveryReplicatedTable(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	base := mustSeq(t, r)
	if base != 69 {
		t.Fatalf("seeding 69 genres must be logged, got seq %d", base)
	}
	has := func(entries []string, want string) bool {
		for _, e := range entries {
			if e == want {
				return true
			}
		}
		return false
	}

	// movie with children: only "movies" rows are logged, never child tables
	in := basicMovie()
	in.ContentType, in.Links = "series", []LinkInput{{URL: "https://a.com/x"}}
	in.Seasons = []SeasonInput{{Episodes: []EpisodeInput{{Links: []LinkInput{{URL: "https://a.com/e"}}}}}}
	m, _ := r.CreateMovie(ctx, in)
	ch := changesAfter(t, r, base)
	if len(ch) == 0 {
		t.Fatal("create movie must log")
	}
	for _, e := range ch {
		if e != "movies:"+m.ID+":upsert" {
			t.Fatalf("unexpected log entry %q (children must collapse into the movie)", e)
		}
	}

	// every kind of child write logs the movie
	base = mustSeq(t, r)
	if err := r.SetLinks(ctx, m.ID, m.Seasons[0].Episodes[0].ID, []LinkInput{{URL: "https://a.com/new"}}); err != nil {
		t.Fatal(err)
	}
	if ch = changesAfter(t, r, base); !has(ch, "movies:"+m.ID+":upsert") {
		t.Fatalf("SetLinks: %v", ch)
	}
	base = mustSeq(t, r)
	up := in
	up.Genres, up.Languages = []string{"Drama"}, []string{"Hindi"}
	if _, err := r.UpdateMovie(ctx, m.ID, up); err != nil {
		t.Fatal(err)
	}
	if ch = changesAfter(t, r, base); !has(ch, "movies:"+m.ID+":upsert") {
		t.Fatalf("UpdateMovie: %v", ch)
	}

	// the other replicated tables
	base = mustSeq(t, r)
	c, _ := r.CreateCategory(ctx, "Noir Test", false, 0)
	b, _ := r.CreateBanner(ctx, BannerInput{ImageURL: "https://i.example.com/1.jpg", Active: true})
	s, _ := r.CreateSocialLink(ctx, SocialInput{Name: "TG", URL: "https://t.me/x"})
	n, _ := r.CreateNotification(ctx, "t", "m")
	_ = r.SetSettings(ctx, map[string]string{"appName": "X"})
	ch = changesAfter(t, r, base)
	for _, want := range []string{"categories:" + c.ID + ":upsert", "banners:" + b.ID + ":upsert", "social_links:" + s.ID + ":upsert", "notifications:" + n.ID + ":upsert", "settings:appName:upsert"} {
		if !has(ch, want) {
			t.Errorf("missing %q in %v", want, ch)
		}
	}
	// updating a setting to the same value is not a change
	base = mustSeq(t, r)
	_ = r.SetSettings(ctx, map[string]string{"appName": "X"})
	if ch = changesAfter(t, r, base); len(ch) != 0 {
		t.Fatalf("no-op setting write must not be logged: %v", ch)
	}

	// deletes
	base = mustSeq(t, r)
	_ = r.DeleteCategory(ctx, c.ID)
	_ = r.DeleteBanner(ctx, b.ID)
	_ = r.DeleteSocialLink(ctx, s.ID)
	_ = r.DeleteNotification(ctx, n.ID)
	ch = changesAfter(t, r, base)
	for _, want := range []string{"categories:" + c.ID + ":delete", "banners:" + b.ID + ":delete", "social_links:" + s.ID + ":delete", "notifications:" + n.ID + ":delete"} {
		if !has(ch, want) {
			t.Errorf("missing %q in %v", want, ch)
		}
	}

	// deleting a whole movie (cascade over seasons/episodes/links/tags) logs exactly one delete
	base = mustSeq(t, r)
	if err := r.DeleteMovie(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if ch = changesAfter(t, r, base); len(ch) != 1 || ch[0] != "movies:"+m.ID+":delete" {
		t.Fatalf("cascade delete should log one entry, got %v", ch)
	}
}

func TestFailedWriteLeavesNoLogEntries(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	base := mustSeq(t, r)
	in := basicMovie()
	in.Genres = []string{"Action", "NotAGenre"} // fails midway, after the movie row was inserted
	if _, err := r.CreateMovie(ctx, in); !IsValidation(err) {
		t.Fatalf("%v", err)
	}
	if ch := changesAfter(t, r, base); len(ch) != 0 {
		t.Fatalf("rolled-back transaction must not leave log entries: %v", ch)
	}
	if n, _ := r.CountMovies(ctx); n != 0 {
		t.Fatal("movie row must be rolled back")
	}
	if seq := r.SeqFunc()(); seq != base {
		t.Fatalf("SeqFunc %d != %d", seq, base)
	}
}
