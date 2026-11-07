//go:build !nodriver

package repo

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAdminSessions(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	adv := fakeClock(r, time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC))

	id, err := r.CreateAdminSession(ctx, "admin1", "203.0.113.5", strings.Repeat("u", 500), 12*time.Hour)
	if err != nil || len(id) != 64 {
		t.Fatalf("%v %q", err, id)
	}
	var stored, ua string
	_ = r.db.QueryRow(`SELECT id_hash, ua FROM admin_sessions`).Scan(&stored, &ua)
	if stored == id || len(stored) != 64 || len(ua) != 200 {
		t.Fatal("only a hash of the id is stored, and the user agent is clipped")
	}
	s, err := r.GetAdminSession(ctx, id)
	if err != nil || s.UID != "admin1" || s.ExpiresAt-s.CreatedAt != (12*time.Hour).Milliseconds() {
		t.Fatalf("%v %+v", err, s)
	}
	for _, bad := range []string{"", "nope", id + "x", stored} { // the stored hash itself must not work as an id
		if _, err := r.GetAdminSession(ctx, bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q must not authenticate", bad)
		}
	}
	adv(11 * time.Hour)
	if _, err := r.GetAdminSession(ctx, id); err != nil {
		t.Fatal("still valid after 11 h")
	}
	adv(2 * time.Hour)
	if _, err := r.GetAdminSession(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired after 12 h")
	}
	if n, _ := r.PruneAdminSessions(ctx); n != 1 {
		t.Fatalf("pruned %d", n)
	}

	// logout / sign out everywhere
	a, _ := r.CreateAdminSession(ctx, "admin1", "", "", time.Hour)
	b, _ := r.CreateAdminSession(ctx, "admin1", "", "", time.Hour)
	o, _ := r.CreateAdminSession(ctx, "admin2", "", "", time.Hour)
	_ = r.DeleteAdminSession(ctx, a)
	if _, err := r.GetAdminSession(ctx, a); !errors.Is(err, ErrNotFound) {
		t.Fatal("logout must invalidate the session")
	}
	if _, err := r.GetAdminSession(ctx, b); err != nil {
		t.Fatal("other sessions stay")
	}
	if n, _ := r.DeleteAdminSessionsOf(ctx, "admin1"); n != 1 {
		t.Fatalf("n=%d", n)
	}
	if _, err := r.GetAdminSession(ctx, o); err != nil {
		t.Fatal("another admin is unaffected")
	}

	// bounded per admin: the oldest session goes first
	var ids []string
	for i := 0; i < maxSessionsPerAdmin+2; i++ {
		adv(time.Second)
		x, _ := r.CreateAdminSession(ctx, "admin3", "", "", time.Hour)
		ids = append(ids, x)
	}
	var n int
	_ = r.db.QueryRow(`SELECT COUNT(*) FROM admin_sessions WHERE uid = 'admin3'`).Scan(&n)
	if n != maxSessionsPerAdmin {
		t.Fatalf("kept %d sessions", n)
	}
	if _, err := r.GetAdminSession(ctx, ids[0]); !errors.Is(err, ErrNotFound) {
		t.Fatal("the oldest session must be dropped")
	}
	if _, err := r.GetAdminSession(ctx, ids[len(ids)-1]); err != nil {
		t.Fatal("the newest must stay")
	}
	// sessions are not replicated
	if ch := changesAfter(t, r, 69); len(ch) != 0 {
		t.Fatalf("admin sessions must never reach the change log: %v", ch)
	}
}
