//go:build !nodriver

package repo

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStatsAndSearch(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	m := mustMovie(t, r, "Hot")
	for i := 0; i < 3; i++ {
		if err := r.IncPlays(ctx, m.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.IncPlays(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown movie")
	}
	if s, _ := r.AllMovieStats(ctx); s[m.ID].Plays != 3 || s[m.ID].Lists != 0 {
		t.Fatalf("%+v", s)
	}
	if n, _ := r.TotalPlays(ctx); n != 3 {
		t.Fatalf("total plays %d", n)
	}

	for _, bad := range []string{"", "a", " ", strings.Repeat("x", 41)} {
		if err := r.HitSearch(ctx, bad); !IsValidation(err) {
			t.Errorf("%q must be rejected", bad)
		}
	}
	adv := fakeClock(r, time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC))
	_ = r.HitSearch(ctx, "  Avengers   Endgame ")
	adv(time.Second)
	_ = r.HitSearch(ctx, "avengers endgame")
	adv(time.Second)
	_ = r.HitSearch(ctx, "naruto")
	adv(time.Second)
	_ = r.HitSearch(ctx, "dangal")
	top, _ := r.TopSearches(ctx, 2)
	if len(top) != 2 || top[0].Term != "avengers endgame" || top[0].Count != 2 || top[1].Term != "dangal" {
		t.Fatalf("%+v", top)
	}
}

func TestBrokenLinks(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	adv := fakeClock(r, time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC))
	film := mustMovie(t, r, "Some Film")
	in := basicMovie()
	in.Title, in.ContentType, in.Links = "Some Series", "series", nil
	in.Seasons = []SeasonInput{{Episodes: []EpisodeInput{{}}}}
	series, _ := r.CreateMovie(ctx, in)

	if err := r.ReportBrokenLink(ctx, BrokenLinkInput{MovieID: "nope"}); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown movie")
	}
	if err := r.ReportBrokenLink(ctx, BrokenLinkInput{MovieID: film.ID, EpInfo: strings.Repeat("x", 121)}); !IsValidation(err) {
		t.Fatal("long epInfo")
	}
	_ = r.ReportBrokenLink(ctx, BrokenLinkInput{MovieID: film.ID, EpInfo: "Movie", ServerType: "server1", URL: "https://a.com/x", ReporterUID: "u1"})
	adv(time.Minute)
	_ = r.ReportBrokenLink(ctx, BrokenLinkInput{MovieID: series.ID, EpInfo: "Season 1 · Episode 1", ServerType: "server1", ReporterUID: "u2"})
	adv(time.Minute)
	_ = r.ReportBrokenLink(ctx, BrokenLinkInput{MovieID: film.ID, EpInfo: "Movie", ServerType: "server1", ReporterUID: "u3"})

	list, total, err := r.ListBrokenLinks(ctx, BrokenListOpts{})
	if err != nil || total != 2 || list[0].MovieID != film.ID || list[0].Reports != 2 || list[0].ReporterUID != "u3" || list[0].Title != "Some Film" {
		t.Fatalf("one row per title, refreshed and counted: %v %d %+v", err, total, list)
	}
	if _, n, _ := r.ListBrokenLinks(ctx, BrokenListOpts{ContentType: "series"}); n != 1 {
		t.Fatal("type filter")
	}
	if _, n, _ := r.ListBrokenLinks(ctx, BrokenListOpts{Query: "episode 1"}); n != 1 {
		t.Fatal("query matches episode info")
	}
	if _, n, _ := r.ListBrokenLinks(ctx, BrokenListOpts{Query: "SOME"}); n != 2 {
		t.Fatal("query matches title")
	}

	if err := r.ResolveBrokenLink(ctx, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := r.CountOpenBrokenLinks(ctx); n != 1 {
		t.Fatalf("open=%d", n)
	}
	_ = r.ReportBrokenLink(ctx, BrokenLinkInput{MovieID: film.ID, EpInfo: "Movie"}) // a new report re-opens it
	if n, _ := r.CountOpenBrokenLinks(ctx); n != 2 {
		t.Fatalf("re-open: open=%d", n)
	}
	_ = r.ClearBrokenLinks(ctx, film.ID)
	if _, n, _ := r.ListBrokenLinks(ctx, BrokenListOpts{}); n != 1 {
		t.Fatal("clear after edit")
	}
	if err := r.ResolveBrokenLink(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatal("resolve unknown")
	}
	_ = r.DeleteMovie(ctx, series.ID)
	if _, n, _ := r.ListBrokenLinks(ctx, BrokenListOpts{}); n != 0 {
		t.Fatal("deleting the title removes its report")
	}
}

func TestErrorLogsAreWriteOnceAndBounded(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	adv := fakeClock(r, time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC))

	if _, err := r.LogError(ctx, "u1", "hacker", map[string]any{"message": "x"}); !IsValidation(err) {
		t.Fatal("panel")
	}
	if _, err := r.LogError(ctx, "u1", "user", map[string]any{"message": strings.Repeat("x", 4000)}); !IsValidation(err) {
		t.Fatal("over 4000 bytes must be rejected")
	}
	if _, err := r.LogError(ctx, "u1", "user", map[string]any{"message": "boom", "bad": make(chan int)}); !IsValidation(err) {
		t.Fatal("non-JSON payload")
	}
	// the multi-byte characters count as bytes, like the old JSON length check
	if _, err := r.LogError(ctx, "u1", "user", map[string]any{"message": strings.Repeat("বাংলা", 300)}); !IsValidation(err) {
		t.Fatal("size is measured in bytes")
	}
	id1, err := r.LogError(ctx, "u1", "user", map[string]any{"message": strings.Repeat("é", 400), "source": "app.js", "line": 12})
	if err != nil {
		t.Fatal(err)
	}
	adv(time.Second)
	id2, _ := r.LogError(ctx, "", "admin", map[string]any{"message": "second"})
	list, _ := r.RecentErrors(ctx, 10)
	if len(list) != 2 || list[0].ID != id2 || list[1].ID != id1 || len([]rune(list[1].Message)) != 300 || !strings.Contains(list[1].Data, `"source":"app.js"`) || !strings.Contains(list[1].Data, `"panel":"user"`) {
		t.Fatalf("%+v", list)
	}
	// the database itself refuses an oversized row, even if the repo were bypassed
	if _, err := r.db.Exec(`INSERT INTO error_logs(id, data, created_at) VALUES ('x', ?, 1)`, strings.Repeat("y", 4001)); err == nil {
		t.Fatal("CHECK constraint must refuse > 4000 bytes")
	}
	if n, _ := r.CountErrorsSince(ctx, time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC).Add(500*time.Millisecond)); n != 1 {
		t.Fatalf("since: %d", n)
	}
	if n, _ := r.PruneErrors(ctx, 1); n != 1 {
		t.Fatalf("prune removed %d", n)
	}
	if list, _ = r.RecentErrors(ctx, 10); len(list) != 1 || list[0].ID != id2 {
		t.Fatal("prune keeps the newest")
	}
}

func TestPasswordResetLimiter(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	adv := fakeClock(r, time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC))
	k := ResetKey("  Alice@Example.com ")
	if k != ResetKey("alice@example.com") || len(k) != 64 || strings.Contains(k, "alice") {
		t.Fatalf("key must be a case-insensitive hash, never the address: %q", k)
	}
	for i := 0; i < 3; i++ {
		_ = r.AddResetAttempt(ctx, k)
		adv(time.Hour)
	}
	_ = r.AddResetAttempt(ctx, ResetKey("bob@example.com"))
	if n, _ := r.CountResetAttempts(ctx, k, 24*time.Hour); n != 3 {
		t.Fatalf("n=%d", n)
	}
	adv(21*time.Hour + 30*time.Minute) // now = first attempt + 24.5h: only the first attempt is outside the 24h window
	if n, _ := r.CountResetAttempts(ctx, k, 24*time.Hour); n != 2 {
		t.Fatalf("old attempts leave the 24h window: n=%d", n)
	}
	if total, d, _ := r.ResetStats(ctx); total != 4 || d != 3 {
		t.Fatalf("stats total=%d 24h=%d", total, d)
	}
	if n, _ := r.PruneResetAttempts(ctx, 24*time.Hour); n != 1 {
		t.Fatalf("pruned %d", n)
	}
}

func TestAuditLog(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	_ = r.Audit(ctx, "admin1", "movie.delete", "m1", "Deleted 'X'")
	_ = r.Audit(ctx, "admin1", "user.freeze", "u9", strings.Repeat("d", 2000))
	l, _ := r.RecentAudit(ctx, 10)
	if len(l) != 2 || l[0].Action != "user.freeze" || len(l[0].Detail) != 1000 || l[1].Target != "m1" || l[1].TS == 0 {
		t.Fatalf("%+v", l)
	}
}

func TestWorkers(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	if _, _, err := r.CreateWorker(ctx, "", "https://w.example.com"); !IsValidation(err) {
		t.Fatal("name")
	}
	if _, _, err := r.CreateWorker(ctx, "w1", "w.example.com"); !IsValidation(err) {
		t.Fatal("url must be http(s)")
	}
	w, key, err := r.CreateWorker(ctx, " Singapore ", "https://w.example.com/")
	if err != nil || w.Name != "Singapore" || w.URL != "https://w.example.com" || !strings.HasPrefix(key, "mxw_") || len(key) != 68 {
		t.Fatalf("%v %+v %q", err, w, key)
	}
	var stored string
	_ = r.db.QueryRow(`SELECT key_hash FROM workers WHERE id = ?`, w.ID).Scan(&stored)
	if stored == key || len(stored) != 64 {
		t.Fatal("only a hash of the key may be stored")
	}
	if got, err := r.WorkerByKey(ctx, key); err != nil || got.ID != w.ID {
		t.Fatalf("%v %+v", err, got)
	}
	for _, bad := range []string{"", "mxw_wrong", key + "x"} {
		if _, err := r.WorkerByKey(ctx, bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("key %q must not authenticate", bad)
		}
	}
	_, key2, _ := r.CreateWorker(ctx, "Frankfurt", "https://w2.example.com")
	if key2 == key {
		t.Fatal("keys must be unique")
	}
	epoch, err := r.EnsureEpoch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.AcknowledgeWorker(ctx, w.ID, epoch, 0); err != nil {
		t.Fatal(err)
	}
	if err := r.AcknowledgeWorker(ctx, w.ID, epoch, 0); err != nil {
		t.Fatal(err)
	}
	ws, _ := r.ListWorkers(ctx)
	if len(ws) != 2 || ws[0].LastSeq != 0 || ws[0].LastEpoch != epoch || ws[0].LastSeenAt == 0 {
		t.Fatalf("%+v", ws)
	}
	if err := r.AcknowledgeWorker(ctx, w.ID, "stale-epoch", 0); !IsValidation(err) {
		t.Fatal("stale epoch acknowledgement must be rejected")
	}
	if err := r.DeleteWorker(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.WorkerByKey(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted worker's key stops working")
	}
}

func TestMeta(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	if _, ok, _ := r.GetMeta(ctx, "epoch"); ok {
		t.Fatal("unset")
	}
	_ = r.SetMeta(ctx, "epoch", "a")
	_ = r.SetMeta(ctx, "epoch", "b")
	if v, ok, _ := r.GetMeta(ctx, "epoch"); !ok || v != "b" {
		t.Fatal("upsert")
	}
}

// Private tables must never reach workers: none of these writes may add an entry to the replication log.
func TestPrivateWritesAreNotReplicated(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	m := mustMovie(t, r, "Replicated")
	base := mustSeq(t, r)

	mustUser(t, r, "u1")
	_, _ = r.AddToList(ctx, "u1", m.ID)
	_ = r.SetProgress(ctx, "u1", m.ID, "", nil, 1, 10)
	_ = r.IncPlays(ctx, m.ID)
	_ = r.HitSearch(ctx, "abc")
	_ = r.ReportBrokenLink(ctx, BrokenLinkInput{MovieID: m.ID})
	_, _ = r.LogError(ctx, "u1", "user", map[string]any{"message": "x"})
	_ = r.AddResetAttempt(ctx, "k")
	_ = r.Audit(ctx, "a", "x", "", "")
	_, _, _ = r.CreateWorker(ctx, "w", "https://w.example.com")
	_ = r.SetMeta(ctx, "k", "v")
	_ = r.SetUserStatus(ctx, "u1", StatusFrozen)
	_, _ = r.RemoveFromList(ctx, "u1", m.ID)
	_ = r.PurgeUser(ctx, "u1")

	if ch := changesAfter(t, r, base); len(ch) != 0 {
		t.Fatalf("private data leaked into the replication log: %v", ch)
	}
}
