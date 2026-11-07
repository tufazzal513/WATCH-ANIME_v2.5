//go:build !nodriver

package repo

import (
	"context"
	"strings"
	"testing"
	"time"
)

func order(ms []*Movie) string {
	var t []string
	for _, m := range ms {
		t = append(t, m.Title)
	}
	return strings.Join(t, " | ")
}

func TestHotSortFollowsTheOldFormula(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	clock := time.Now()
	r.now = func() time.Time { clock = clock.Add(time.Second); return clock }
	mk := func(title string, rating float64) *Movie {
		in := basicMovie()
		in.Title, in.Rating = title, floatp(rating)
		m, err := r.CreateMovie(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	a := mk("A watched a lot", 5)  // 0.25 + 0.3 (max plays)         = 0.55
	b := mk("B great rating", 9.5) // 0.475                          = 0.475
	c := mk("C often listed", 5)   // 0.25 + 0.2 (max lists)         = 0.45
	d := mk("D unrated newer", 0)  // 0
	e := mk("E unrated newest", 0) // 0, newer than D -> comes first on ties
	for i := 0; i < 10; i++ {
		_ = r.IncPlays(ctx, a.ID)
	}
	mustUser(t, r, "u1")
	_, _ = r.AddToList(ctx, "u1", c.ID)
	_ = d

	got, _, err := r.ListMovies(ctx, ListOpts{Sort: "hot"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "A watched a lot | B great rating | C often listed | E unrated newest | D unrated newer"; order(got) != want {
		t.Fatalf("hot order:\n got %s\nwant %s", order(got), want)
	}
	if got[0].ID != a.ID || got[1].ID != b.ID || got[2].ID != c.ID || got[3].ID != e.ID {
		t.Fatal("ids")
	}
	// hot ordering works together with filters and paging, and the total stays correct
	page, total, _ := r.ListMovies(ctx, ListOpts{Sort: "hot", Limit: 2, Offset: 1, Query: "unrated", Genre: "Action"})
	if total != 2 || len(page) != 1 || page[0].Title != "D unrated newer" {
		t.Fatalf("%d %s", total, order(page))
	}
	// no stats at all: plain rating order, no division by zero
	r2 := newTestRepo(t)
	in := basicMovie()
	in.Title, in.Rating = "Only", floatp(8)
	_, _ = r2.CreateMovie(ctx, in)
	if got, _, err = r2.ListMovies(ctx, ListOpts{Sort: "hot"}); err != nil || len(got) != 1 {
		t.Fatalf("%v %d", err, len(got))
	}
}

func TestRelevanceSortRanksTitleMatchesFirst(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	clock := time.Now()
	r.now = func() time.Time { clock = clock.Add(time.Second); return clock }
	mk := func(title, cast string, rating float64) *Movie {
		in := basicMovie()
		in.Title, in.Cast, in.Rating = title, cast, floatp(rating)
		m, _ := r.CreateMovie(ctx, in)
		return m
	}
	mk("Cast mention only", "Avengers star", 9.9) // matches via cast, best rating, but not in the title
	mk("Mega Avengers", "", 6)                    // contains
	mk("Avengers: Endgame", "", 5)                // starts with
	mk("The Avengers Saga", "", 8)                // contains, better rating than Mega
	mk("Avengers", "", 4)                         // exact

	got, total, err := r.ListMovies(ctx, ListOpts{Query: "  AVENGERS ", Sort: "relevance"})
	if err != nil || total != 5 {
		t.Fatalf("%v %d", err, total)
	}
	if want := "Avengers | Avengers: Endgame | The Avengers Saga | Mega Avengers | Cast mention only"; order(got) != want {
		t.Fatalf("relevance order:\n got %s\nwant %s", order(got), want)
	}
	// wildcard characters in the query are literal
	if got, _, _ = r.ListMovies(ctx, ListOpts{Query: "av%", Sort: "relevance"}); len(got) != 0 {
		t.Fatal("% must not act as a wildcard")
	}
}

func TestIDsFilter(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	a, b, c := mustMovie(t, r, "A"), mustMovie(t, r, "B"), mustMovie(t, r, "C")
	got, total, _ := r.ListMovies(ctx, ListOpts{IDs: []string{c.ID, a.ID, "nope"}})
	if total != 2 || len(got) != 2 {
		t.Fatalf("%d %s", total, order(got))
	}
	if got, total, _ = r.ListMovies(ctx, ListOpts{IDs: []string{}}); total != 0 || len(got) != 0 {
		t.Fatal("an empty id list matches nothing (not everything)")
	}
	if _, total, _ = r.ListMovies(ctx, ListOpts{}); total != 3 {
		t.Fatal("nil IDs means no restriction")
	}
	_ = b
}

func TestPublicCategoriesAndBannerPrivacy(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	pub := mustMovie(t, r, "Public")
	in := basicMovie()
	in.Title, in.IsPrivate = "Hidden", true
	hidden, _ := r.CreateMovie(ctx, in)

	all, _ := r.ListCategories(ctx)
	pubc, _ := r.ListPublicCategories(ctx)
	count := func(cs []Category, id string) int {
		for _, c := range cs {
			if c.ID == id {
				return c.MovieCount
			}
		}
		return -1
	}
	if count(all, "action") != 2 || count(pubc, "action") != 1 || len(pubc) != len(all) {
		t.Fatalf("admin sees %d, users see %d", count(all, "action"), count(pubc, "action"))
	}

	bp, _ := r.CreateBanner(ctx, BannerInput{ImageURL: "https://i.example.com/1.jpg", MovieID: pub.ID, Active: true})
	bh, _ := r.CreateBanner(ctx, BannerInput{ImageURL: "https://i.example.com/2.jpg", MovieID: hidden.ID, Active: true})
	bn, _ := r.CreateBanner(ctx, BannerInput{ImageURL: "https://i.example.com/3.jpg", Active: true})
	if bp.MoviePrivate || !bh.MoviePrivate || bn.MoviePrivate {
		t.Fatalf("%v %v %v", bp.MoviePrivate, bh.MoviePrivate, bn.MoviePrivate)
	}
}

func TestSuggestSearches(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	for _, term := range []string{"avengers", "avengers", "avengers", "avatar", "avatar", "naruto", "naruto", "one-off name", "100%"} {
		if err := r.HitSearch(ctx, term); err != nil {
			t.Fatal(err)
		}
	}
	names := func(s []SearchStat) string {
		var o []string
		for _, x := range s {
			o = append(o, x.Term)
		}
		return strings.Join(o, ",")
	}
	got, _ := r.SuggestSearches(ctx, "", 2, 10)
	if names(got) != "avengers,naruto,avatar" && names(got) != "avengers,avatar,naruto" {
		t.Fatalf("one-off terms stay private: %s", names(got))
	}
	if got, _ = r.SuggestSearches(ctx, " AV", 2, 10); names(got) != "avengers,avatar" {
		t.Fatalf("prefix: %s", names(got))
	}
	if got, _ = r.SuggestSearches(ctx, "%", 1, 10); names(got) != "" {
		t.Fatalf("a literal %% prefix must not match everything: %s", names(got))
	}
	if got, _ = r.SuggestSearches(ctx, "100", 1, 10); names(got) != "100%" {
		t.Fatalf("%s", names(got))
	}
	if got, _ = r.SuggestSearches(ctx, "av", 2, 1); len(got) != 1 {
		t.Fatal("limit")
	}
}
