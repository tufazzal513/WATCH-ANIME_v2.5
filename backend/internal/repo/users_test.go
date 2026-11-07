//go:build !nodriver

package repo

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// fakeClock makes the repo clock controllable; advance with the returned func.
func fakeClock(r *Repo, start time.Time) func(d time.Duration) {
	cur := start
	r.now = func() time.Time { return cur }
	return func(d time.Duration) { cur = cur.Add(d) }
}

func mustUser(t *testing.T, r *Repo, uid string) *User {
	t.Helper()
	u, _, err := r.EnsureUser(context.Background(), UserProfile{UID: uid, Email: uid + "@example.com", Name: "Name " + uid})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func mustMovie(t *testing.T, r *Repo, title string) *Movie {
	t.Helper()
	in := basicMovie()
	in.Title = title
	m, err := r.CreateMovie(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestEnsureUserLifecycle(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	adv := fakeClock(r, time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC))

	u, created, err := r.EnsureUser(ctx, UserProfile{UID: "u1", Email: " a@b.com ", Name: "  ", PhotoURL: "javascript:x"})
	if err != nil || !created || u.Status != StatusActive || u.Name != "User" || u.Email != "a@b.com" || u.PhotoURL != "" {
		t.Fatalf("%v %v %+v", err, created, u)
	}
	first := u.LastLoginAt
	adv(time.Hour)
	u, created, _ = r.EnsureUser(ctx, UserProfile{UID: "u1", Name: "Changed On Login"})
	if created || u.LastLoginAt <= first || u.Name != "User" {
		t.Fatalf("later login only refreshes last_login_at (profile is not overwritten): %v %+v", created, u)
	}

	// frozen users keep their last_login_at and status
	if err := r.SetUserStatus(ctx, "u1", StatusFrozen); err != nil {
		t.Fatal(err)
	}
	frozenLogin := u.LastLoginAt
	adv(time.Hour)
	u, _, _ = r.EnsureUser(ctx, UserProfile{UID: "u1"})
	if u.Status != StatusFrozen || u.LastLoginAt != frozenLogin || !Blocked(u.Status) {
		t.Fatalf("%+v", u)
	}
	// deleted keeps the row and a deletion time; a login can never revive it
	_ = r.SetUserStatus(ctx, "u1", StatusDeleted)
	u, _, _ = r.EnsureUser(ctx, UserProfile{UID: "u1"})
	if u.Status != StatusDeleted || u.DeletedAt == 0 || !Blocked(u.Status) {
		t.Fatalf("%+v", u)
	}
	_ = r.SetUserStatus(ctx, "u1", StatusActive)
	if u, _ = r.GetUser(ctx, "u1"); u.Status != StatusActive || u.DeletedAt != 0 {
		t.Fatalf("admin can reactivate: %+v", u)
	}

	if err := r.SetUserStatus(ctx, "u1", "banned"); !IsValidation(err) {
		t.Fatal("unknown status")
	}
	if err := r.SetUserStatus(ctx, "ghost", StatusFrozen); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown user")
	}
	if _, _, err := r.EnsureUser(ctx, UserProfile{}); !IsValidation(err) {
		t.Fatal("empty uid")
	}
	if s, ok, _ := r.UserStatus(ctx, "u1"); !ok || s != StatusActive {
		t.Fatal("UserStatus")
	}
	if _, ok, _ := r.UserStatus(ctx, "ghost"); ok {
		t.Fatal("UserStatus of unknown user")
	}
}

func TestUpdateProfileAndLastSeen(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	adv := fakeClock(r, time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC))
	mustUser(t, r, "u1")
	if _, err := r.UpdateProfile(ctx, "u1", " ", ""); !IsValidation(err) {
		t.Fatal("empty name")
	}
	if _, err := r.UpdateProfile(ctx, "u1", "Ok", "javascript:alert(1)"); !IsValidation(err) {
		t.Fatal("bad photo")
	}
	if _, err := r.UpdateProfile(ctx, "ghost", "Ok", ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown user")
	}
	u, err := r.UpdateProfile(ctx, "u1", " New Name ", "https://img.example.com/a.png")
	if err != nil || u.Name != "New Name" || u.PhotoURL != "https://img.example.com/a.png" || u.Email != "u1@example.com" {
		t.Fatalf("%v %+v", err, u)
	}

	base, _ := r.GetUser(ctx, "u1")
	adv(time.Minute)
	_ = r.TouchLastSeen(ctx, "u1", 5*time.Minute) // too soon after creation (last_seen = creation time)
	if u, _ = r.GetUser(ctx, "u1"); u.LastSeen != base.LastSeen {
		t.Fatal("throttled write must not happen")
	}
	adv(10 * time.Minute)
	_ = r.TouchLastSeen(ctx, "u1", 5*time.Minute)
	if u, _ = r.GetUser(ctx, "u1"); u.LastSeen <= base.LastSeen {
		t.Fatal("last_seen must advance after the throttle window")
	}
}

func TestListUsersCountsAndGrowth(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	adv := fakeClock(r, time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC))
	for i, uid := range []string{"alice", "bob", "carol", "dave"} {
		if i > 0 {
			adv(24 * time.Hour)
		}
		_, _, _ = r.EnsureUser(ctx, UserProfile{UID: uid, Email: uid + "@Mail.com", Name: "N " + uid})
	}
	_ = r.SetUserStatus(ctx, "bob", StatusFrozen)
	_ = r.SetUserStatus(ctx, "carol", StatusDeleted)

	list := func(o UserListOpts) ([]*User, int) {
		t.Helper()
		us, n, err := r.ListUsers(ctx, o)
		if err != nil {
			t.Fatal(err)
		}
		return us, n
	}
	if us, n := list(UserListOpts{}); n != 3 || us[0].UID != "dave" {
		t.Fatalf("default hides deleted, newest first: %d %v", n, us[0].UID)
	}
	if _, n := list(UserListOpts{Status: StatusDeleted}); n != 1 {
		t.Fatal("deleted filter")
	}
	if _, n := list(UserListOpts{Status: StatusFrozen}); n != 1 {
		t.Fatal("frozen filter")
	}
	if _, n := list(UserListOpts{Query: "MAIL.com"}); n != 3 {
		t.Fatal("query matches email case-insensitively")
	}
	if us, n := list(UserListOpts{Query: "n ali"}); n != 1 || us[0].UID != "alice" {
		t.Fatal("query matches name")
	}
	if _, n := list(UserListOpts{Query: "100%"}); n != 0 {
		t.Fatal("wildcards escaped")
	}
	if _, n := list(UserListOpts{JoinedSince: time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC).UnixMilli()}); n != 1 {
		t.Fatal("joined since (carol is deleted, so only dave)")
	}
	if us, n := list(UserListOpts{RestrictToUIDs: true, OnlyUIDs: []string{"alice", "dave"}}); n != 2 || len(us) != 2 {
		t.Fatal("restrict to uids")
	}
	if _, n := list(UserListOpts{RestrictToUIDs: true}); n != 0 {
		t.Fatal("empty restriction means nobody")
	}
	if us, n := list(UserListOpts{Limit: 1, Offset: 1}); n != 3 || len(us) != 1 || us[0].UID != "bob" {
		t.Fatal("paging")
	}
	if _, _, err := r.ListUsers(ctx, UserListOpts{Status: "x"}); !IsValidation(err) {
		t.Fatal("unknown status filter")
	}

	c, _ := r.CountUsers(ctx)
	if c.Total != 3 || c.Active != 2 || c.Frozen != 1 || c.Deleted != 1 {
		t.Fatalf("%+v", c)
	}
	g, err := r.UserGrowth(ctx, 5) // now = 4 Mar; window 28 Feb..4 Mar... clock ended at 4 Mar 12:00
	if err != nil || len(g) != 5 || g[4].Day != "2026-03-04" {
		t.Fatalf("%v %+v", err, g)
	}
	sum := 0
	for _, d := range g {
		sum += d.Count
	}
	if sum != 4 || g[0].Count != 0 || g[4].Count != 1 {
		t.Fatalf("zero days included, all 4 sign-ups counted: %+v", g)
	}
	if _, err := r.UserGrowth(ctx, 0); !IsValidation(err) {
		t.Fatal("days range")
	}
}

func TestMyListCountersAndCascades(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	mustUser(t, r, "a")
	mustUser(t, r, "b")
	m := mustMovie(t, r, "Listed")
	stat := func() MovieStat { s, _ := r.AllMovieStats(ctx); return s[m.ID] }

	if ok, err := r.AddToList(ctx, "a", m.ID); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if ok, _ := r.AddToList(ctx, "a", m.ID); ok {
		t.Fatal("adding twice is idempotent and must not count twice")
	}
	_, _ = r.AddToList(ctx, "b", m.ID)
	if stat().Lists != 2 {
		t.Fatalf("lists=%d", stat().Lists)
	}
	if _, err := r.AddToList(ctx, "a", "no-such-movie"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown movie: %v", err)
	}
	if l, _ := r.GetList(ctx, "a"); len(l) != 1 || l[0].MovieID != m.ID || l[0].AddedAt == 0 {
		t.Fatalf("%+v", l)
	}
	if ok, _ := r.RemoveFromList(ctx, "a", m.ID); !ok || stat().Lists != 1 {
		t.Fatalf("remove: lists=%d", stat().Lists)
	}
	if ok, _ := r.RemoveFromList(ctx, "a", m.ID); ok || stat().Lists != 1 {
		t.Fatal("removing something that is not there changes nothing")
	}
	// the counter can never go negative, even if it was out of sync
	if _, err := r.db.Exec(`UPDATE movie_stats SET lists = 0 WHERE movie_id = ?`, m.ID); err != nil {
		t.Fatal(err)
	}
	_, _ = r.RemoveFromList(ctx, "b", m.ID)
	if stat().Lists != 0 {
		t.Fatalf("lists must stay at 0, got %d", stat().Lists)
	}

	// purging a user and deleting a movie clean up everything attached
	_, _ = r.AddToList(ctx, "a", m.ID)
	_ = r.SetProgress(ctx, "a", m.ID, "", nil, 10, 100)
	if err := r.PurgeUser(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = r.db.QueryRow(`SELECT (SELECT COUNT(*) FROM my_list) + (SELECT COUNT(*) FROM watch_progress)`).Scan(&n)
	if n != 0 {
		t.Fatalf("purge must cascade, %d rows left", n)
	}
	mustUser(t, r, "c")
	_, _ = r.AddToList(ctx, "c", m.ID)
	_ = r.IncPlays(ctx, m.ID)
	_ = r.DeleteMovie(ctx, m.ID)
	_ = r.db.QueryRow(`SELECT (SELECT COUNT(*) FROM my_list) + (SELECT COUNT(*) FROM movie_stats)`).Scan(&n)
	if n != 0 {
		t.Fatalf("movie delete must cascade to list and stats, %d rows left", n)
	}
	if err := r.PurgeUser(ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatal("purge unknown")
	}
}

func TestProgressRules(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	adv := fakeClock(r, time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC))
	mustUser(t, r, "a")
	m := mustMovie(t, r, "Series")
	ep := 2

	bad := []struct {
		name string
		err  error
	}{
		{"d=0", r.SetProgress(ctx, "a", m.ID, "", nil, 5, 0)},
		{"t<0", r.SetProgress(ctx, "a", m.ID, "", nil, -1, 100)},
		{"ep<0", r.SetProgress(ctx, "a", m.ID, "S1", func() *int { v := -1; return &v }(), 5, 100)},
		{"long season", r.SetProgress(ctx, "a", m.ID, string(make([]byte, 0))+fmt.Sprintf("%0101d", 1), nil, 5, 100)},
	}
	for _, b := range bad {
		if !IsValidation(b.err) {
			t.Errorf("%s: want validation error, got %v", b.name, b.err)
		}
	}
	if err := r.SetProgress(ctx, "a", "no-such-movie", "", nil, 5, 100); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown movie: %v", err)
	}
	if err := r.SetProgress(ctx, "a", m.ID, "Season 1", &ep, 50, 3000); err != nil {
		t.Fatal(err)
	}
	adv(time.Minute)
	if err := r.SetProgress(ctx, "a", m.ID, "Season 1", &ep, 9999, 3000); err != nil { // t above d is capped
		t.Fatal(err)
	}
	p, _ := r.GetProgress(ctx, "a")
	if len(p) != 1 || p[0].Season != "Season 1" || *p[0].Ep != 2 || p[0].T != 3000 || p[0].D != 3000 {
		t.Fatalf("%+v", p)
	}
	// a movie has no season/episode
	m2 := mustMovie(t, r, "Film")
	_ = r.SetProgress(ctx, "a", m2.ID, "", nil, 5, 100)
	p, _ = r.GetProgress(ctx, "a")
	if len(p) != 2 || p[0].MovieID != m2.ID || p[0].Season != "" || p[0].Ep != nil {
		t.Fatalf("most recent first, movie without season/ep: %+v", p)
	}
	_ = r.DeleteProgress(ctx, "a", m.ID)
	if p, _ = r.GetProgress(ctx, "a"); len(p) != 1 {
		t.Fatal("delete progress")
	}
}

func TestProgressTableStaysBounded(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	adv := fakeClock(r, time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC))
	mustUser(t, r, "a")
	for i := 0; i < maxProgressRows+5; i++ {
		id := fmt.Sprintf("m%04d", i)
		if _, err := r.db.Exec(`INSERT INTO movies(id, title, created_at, updated_at) VALUES (?, ?, 1, 1)`, id, id); err != nil {
			t.Fatal(err)
		}
		adv(time.Second)
		if err := r.SetProgress(ctx, "a", id, "", nil, 1, 10); err != nil {
			t.Fatal(err)
		}
	}
	p, _ := r.GetProgress(ctx, "a")
	if len(p) != maxProgressRows || p[0].MovieID != fmt.Sprintf("m%04d", maxProgressRows+4) {
		t.Fatalf("only the newest %d stay: have %d, first %s", maxProgressRows, len(p), p[0].MovieID)
	}
}

func TestCountActiveUsersSince(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	fakeClock(r, now)
	mustUser(t, r, "recent")
	mustUser(t, r, "old")
	if _, err := r.db.ExecContext(ctx, `UPDATE users SET last_login_at = ? WHERE uid = 'old'`, now.Add(-48*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if got, err := r.CountActiveUsersSince(ctx, now.Add(-24*time.Hour)); err != nil || got != 1 {
		t.Fatalf("active users = %d, err = %v; want 1", got, err)
	}
	if err := r.SetUserStatus(ctx, "recent", StatusDeleted); err != nil {
		t.Fatal(err)
	}
	if got, err := r.CountActiveUsersSince(ctx, now.Add(-24*time.Hour)); err != nil || got != 0 {
		t.Fatalf("deleted users must be excluded: %d, %v", got, err)
	}
}
