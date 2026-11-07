//go:build !nodriver

package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"movex/internal/auth"
	"movex/internal/config"
	"movex/internal/db"
	"movex/internal/httpx"
	"movex/internal/repo"
)

func dbServer(t *testing.T, role config.Role) (*Server, *repo.Repo) {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(ctx, d); err != nil {
		t.Fatal(err)
	}
	r := repo.New(d)
	cfg := testCfg()
	cfg.Role = role
	s := newTestServer(t, Deps{Cfg: cfg, DB: d, Repo: r, Seq: r.SeqFunc(),
		Verifier: auth.NewVerifier(auth.Options{ProjectID: "p", DevInsecure: true})})
	return s, r
}

func TestBlockedAccountsGet403Everywhere(t *testing.T) {
	ctx := context.Background()
	s, r := dbServer(t, config.RoleMain)
	for _, uid := range []string{"ok", "frozen", "gone"} {
		if _, _, err := r.EnsureUser(ctx, repo.UserProfile{UID: uid}); err != nil {
			t.Fatal(err)
		}
	}
	_ = r.SetUserStatus(ctx, "frozen", repo.StatusFrozen)
	_ = r.SetUserStatus(ctx, "gone", repo.StatusDeleted)

	if rr := do(s, "GET", "/api/v1/test/private", asUser("ok")); rr.Code != 200 {
		t.Fatalf("active user: %d", rr.Code)
	}
	for uid, status := range map[string]string{"frozen": "frozen", "gone": "deleted"} {
		rr := do(s, "GET", "/api/v1/test/private", asUser(uid))
		var e httpx.APIError
		_ = json.Unmarshal(rr.Body.Bytes(), &e)
		if rr.Code != 403 || e.Code != "account_blocked" || e.Status != status {
			t.Errorf("%s: %d %s", uid, rr.Code, rr.Body.String())
		}
	}
	// a blocked admin is blocked too
	_, _, _ = r.EnsureUser(ctx, repo.UserProfile{UID: "admin1"})
	_ = r.SetUserStatus(ctx, "admin1", repo.StatusFrozen)
	if rr := do(s, "GET", "/api/v1/test/admin", asUser("admin1")); rr.Code != 403 {
		t.Fatalf("frozen admin: %d", rr.Code)
	}
	// unfreezing takes effect immediately
	_ = r.SetUserStatus(ctx, "frozen", repo.StatusActive)
	if rr := do(s, "GET", "/api/v1/test/private", asUser("frozen")); rr.Code != 200 {
		t.Fatalf("unfrozen: %d", rr.Code)
	}
}

func TestUserWithoutRecordIsAllowedAndWorkersSkipTheCheck(t *testing.T) {
	s, _ := dbServer(t, config.RoleMain)
	rr := do(s, "GET", "/api/v1/test/private", asUser("brand-new"))
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if rr.Code != 200 || out["status"] != "" {
		t.Fatalf("first login has no record yet: %d %s", rr.Code, rr.Body.String())
	}

	// on a worker the users table is empty and not authoritative: private routes are proxied to main there
	w, r := dbServer(t, config.RoleWorker)
	_, _, _ = r.EnsureUser(context.Background(), repo.UserProfile{UID: "x"})
	_ = r.SetUserStatus(context.Background(), "x", repo.StatusFrozen)
	if rr := do(w, "GET", "/api/v1/test/private", asUser("x")); rr.Code != 200 {
		t.Fatalf("worker must not consult the local users table: %d", rr.Code)
	}
}

func TestFailMapsErrors(t *testing.T) {
	s := testServer(t, nil)
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{&repo.ValidationError{Field: "title", Msg: "is required"}, 400, "validation"},
		{errors.Join(errors.New("ctx"), repo.ErrNotFound), 404, "not_found"},
		{errors.New("sql: disk I/O error /secret/path"), 500, "internal"},
	}
	for _, c := range cases {
		rr := httptest.NewRecorder()
		s.fail(rr, httptest.NewRequest("GET", "/x", nil), c.err)
		var e httpx.APIError
		_ = json.Unmarshal(rr.Body.Bytes(), &e)
		if rr.Code != c.status || e.Code != c.code {
			t.Errorf("%v: %d %s", c.err, rr.Code, rr.Body.String())
		}
		if c.status == 500 && (e.Message == "" || len(rr.Body.String()) > 200 || containsAny(rr.Body.String(), "secret", "disk")) {
			t.Errorf("internal errors must not leak details: %s", rr.Body.String())
		}
	}
	rr := httptest.NewRecorder()
	s.fail(rr, httptest.NewRequest("GET", "/x", nil), &repo.ValidationError{Field: "title", Msg: "is required"})
	var e httpx.APIError
	_ = json.Unmarshal(rr.Body.Bytes(), &e)
	if e.Field != "title" || e.Message != "is required" {
		t.Fatalf("%+v", e)
	}
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		for i := 0; i+len(x) <= len(s); i++ {
			if s[i:i+len(x)] == x {
				return true
			}
		}
	}
	return false
}

func TestAdminPanelExistsOnMainOnly(t *testing.T) {
	main, _ := dbServer(t, config.RoleMain)
	if main.Admin() == nil {
		t.Fatal("main must serve the admin panel")
	}
	rr := httptest.NewRecorder()
	main.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/admin/login", nil))
	if rr.Code != 200 || rr.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("main /admin/login: %d", rr.Code)
	}
	// the admin panel is not an API: no CORS headers even for a listed origin
	req := httptest.NewRequest("GET", "/admin/login", nil)
	req.Header.Set("Origin", origin)
	rr = httptest.NewRecorder()
	main.Handler().ServeHTTP(rr, req)
	if rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("admin routes must not send CORS headers")
	}

	worker, _ := dbServer(t, config.RoleWorker)
	if worker.Admin() != nil {
		t.Fatal("workers have no admin panel")
	}
	for _, p := range []string{"/admin/login", "/admin/", "/admin/static/js/admin.js", "/admin/movies"} {
		rr := httptest.NewRecorder()
		worker.Handler().ServeHTTP(rr, httptest.NewRequest("GET", p, nil))
		if rr.Code != 404 {
			t.Errorf("worker %s: %d, want 404", p, rr.Code)
		}
	}
}
