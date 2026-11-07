//go:build !nodriver

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"movex/internal/config"
	"movex/internal/httpx"
	"movex/internal/presence"
	"movex/internal/repo"
)

func (f *fx) as(uid string) { // make sure the account exists and is active
	f.t.Helper()
	if _, _, err := f.r.EnsureUser(context.Background(), repo.UserProfile{UID: uid, Name: "Name " + uid}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fx) status(uid, status string) {
	f.t.Helper()
	f.as(uid)
	if err := f.r.SetUserStatus(context.Background(), uid, status); err != nil {
		f.t.Fatal(err)
	}
}

func decode(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("not JSON (%d): %q", rr.Code, rr.Body.String())
	}
	return m
}

// ---------- authorization matrix ----------

func TestPrivateRoutesAuthorizationMatrix(t *testing.T) {
	f := newFx(t)
	f.status("frozen", repo.StatusFrozen)
	f.status("gone", repo.StatusDeleted)
	f.as("active")

	id := f.film.ID
	routes := []struct{ method, path, body string }{
		{"GET", "/api/v1/me", ""},
		{"PUT", "/api/v1/me", `{"name":"X"}`},
		{"GET", "/api/v1/me/list", ""},
		{"PUT", "/api/v1/me/list/" + id, ""},
		{"DELETE", "/api/v1/me/list/" + id, ""},
		{"GET", "/api/v1/me/progress", ""},
		{"PUT", "/api/v1/me/progress/" + id, `{"t":1,"d":10}`},
		{"DELETE", "/api/v1/me/progress/" + id, ""},
		{"POST", "/api/v1/presence", ""},
		{"DELETE", "/api/v1/presence", ""},
		{"POST", "/api/v1/broken-links", `{"movieId":"` + id + `"}`},
		{"POST", "/api/v1/errors", `{"message":"x"}`},
	}
	for _, rt := range routes {
		name := rt.method + " " + rt.path
		if rr := f.call(rt.method, rt.path, "", rt.body); rr.Code != 401 {
			t.Errorf("%s without a token: %d", name, rr.Code)
		}
		if rr := f.call(rt.method, rt.path, "bad.token.here", rt.body); rr.Code != 401 {
			t.Errorf("%s with a bad token: %d", name, rr.Code)
		}
		for uid, st := range map[string]string{"frozen": "frozen", "gone": "deleted"} {
			rr := f.call(rt.method, rt.path, uid, rt.body)
			if rr.Code != 403 || decode(t, rr)["error"] != "account_blocked" || decode(t, rr)["status"] != st {
				t.Errorf("%s as %s account: %d %s", name, st, rr.Code, rr.Body.String())
			}
		}
		if rr := f.call(rt.method, rt.path, "active", rt.body); rr.Code >= 400 {
			t.Errorf("%s as an active user: %d %s", name, rr.Code, rr.Body.String())
		}
	}
	// no private response may be cached
	for _, p := range []string{"/api/v1/me", "/api/v1/me/list", "/api/v1/me/progress"} {
		if cc := f.get(p, "active").Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%s Cache-Control = %q", p, cc)
		}
	}
	// wrong method
	if rr := f.call("POST", "/api/v1/me", "active", ""); rr.Code != 405 {
		t.Fatalf("POST /me: %d", rr.Code)
	}
}

func TestPrivateRoutesAbsentOnWorkers(t *testing.T) {
	w, _ := dbServer(t, config.RoleWorker)
	for _, p := range []string{"/api/v1/me", "/api/v1/me/list", "/api/v1/presence", "/api/v1/auth/password-reset"} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("GET", p, nil)
		req.Header.Set("Authorization", "Bearer dev:x")
		w.Handler().ServeHTTP(rr, req)
		if rr.Code != 404 && rr.Code != 405 {
			t.Errorf("%s on a worker: %d (private data lives on main only)", p, rr.Code)
		}
	}
}

// ---------- /me ----------

func TestMeLifecycle(t *testing.T) {
	f := newFx(t)
	rr := f.get("/api/v1/me", "newbie")
	me := decode(t, rr)
	if rr.Code != 200 || me["uid"] != "newbie" || me["status"] != "active" || me["email"] != "newbie@dev.local" || me["name"] != "newbie" || me["createdAt"].(float64) == 0 {
		t.Fatalf("first login creates an active record from the token: %d %s", rr.Code, rr.Body.String())
	}
	if _, ok := me["isAdmin"]; ok {
		t.Fatal("the user API does not expose admin status")
	}
	first := me["lastLoginAt"].(float64)
	time.Sleep(3 * time.Millisecond)
	if again := decode(t, f.get("/api/v1/me", "newbie")); again["lastLoginAt"].(float64) <= first || again["createdAt"] != me["createdAt"] {
		t.Fatalf("a later login only refreshes lastLoginAt: %v", again)
	}

	rr = f.call("PUT", "/api/v1/me", "newbie", `{"name":" New Name ","photoURL":"https://img.example.com/a.png"}`)
	if me = decode(t, rr); rr.Code != 200 || me["name"] != "New Name" || me["photoURL"] != "https://img.example.com/a.png" {
		t.Fatalf("%d %v", rr.Code, me)
	}
	// users can never change status or email through the body
	rr = f.call("PUT", "/api/v1/me", "newbie", `{"name":"Still Me","status":"deleted","email":"evil@example.com","uid":"admin1","createdAt":1}`)
	u, _ := f.r.GetUser(context.Background(), "newbie")
	if rr.Code != 200 || u.Status != "active" || u.Email != "newbie@dev.local" || u.Name != "Still Me" || u.CreatedAt <= 1 {
		t.Fatalf("only name and photo can change: %d %+v", rr.Code, u)
	}
	// PUT before any GET works too (the record is created)
	if rr = f.call("PUT", "/api/v1/me", "direct", `{"name":"Direct"}`); rr.Code != 200 {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	for body, want := range map[string]int{`{"name":""}`: 400, `{"name":"x","photoURL":"javascript:alert(1)"}`: 400, `{oops`: 400, `{"name":"` + strings.Repeat("x", 81) + `"}`: 400} {
		if rr = f.call("PUT", "/api/v1/me", "newbie", body); rr.Code != want {
			t.Errorf("%s: %d want %d", body, rr.Code, want)
		}
	}
	req := httptest.NewRequest("PUT", "/api/v1/me", strings.NewReader(`name=x`))
	req.Header.Set("Authorization", "Bearer dev:newbie")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(rec, req)
	if rec.Code != 415 {
		t.Fatalf("content type: %d", rec.Code)
	}
}

func TestDeleteMeOnlyForDeletedAccounts(t *testing.T) {
	f := newFx(t)
	ctx := context.Background()
	f.as("active")
	f.status("frozen", repo.StatusFrozen)
	f.status("gone", repo.StatusDeleted)
	if rr := f.call("DELETE", "/api/v1/me", "", ""); rr.Code != 401 {
		t.Fatal("token required")
	}
	if rr := f.call("DELETE", "/api/v1/me", "active", ""); rr.Code != 403 || decode(t, rr)["error"] != "forbidden" {
		t.Fatalf("an active user cannot delete their record: %d %s", rr.Code, rr.Body.String())
	}
	if rr := f.call("DELETE", "/api/v1/me", "frozen", ""); rr.Code != 403 {
		t.Fatalf("frozen: %d", rr.Code)
	}
	if _, err := f.r.GetUser(ctx, "active"); err != nil {
		t.Fatal("record must still exist")
	}
	_, _ = f.r.AddToList(ctx, "gone", f.film.ID)
	if rr := f.call("DELETE", "/api/v1/me", "gone", ""); rr.Code != 204 {
		t.Fatalf("deleted account may remove its own record: %d %s", rr.Code, rr.Body.String())
	}
	if _, err := f.r.GetUser(ctx, "gone"); err == nil {
		t.Fatal("record must be gone")
	}
	if l, _ := f.r.GetList(ctx, "gone"); len(l) != 0 {
		t.Fatal("their list goes with it")
	}
	if rr := f.call("DELETE", "/api/v1/me", "gone", ""); rr.Code != 403 {
		t.Fatalf("second call: %d", rr.Code)
	}
}

// ---------- My List ----------

func TestMyList(t *testing.T) {
	f := newFx(t)
	ctx := context.Background()
	lists := func() int { s, _ := f.r.AllMovieStats(ctx); return s[f.film.ID].Lists }

	rr := f.call("PUT", "/api/v1/me/list/"+f.film.ID, "a", "")
	if rr.Code != 200 || decode(t, rr)["added"] != true || lists() != 1 {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	if rr = f.call("PUT", "/api/v1/me/list/"+f.film.ID, "a", ""); decode(t, rr)["added"] != false || lists() != 1 {
		t.Fatal("adding twice is idempotent and counts once")
	}
	time.Sleep(3 * time.Millisecond)
	f.call("PUT", "/api/v1/me/list/"+f.series.ID, "a", "")
	f.call("PUT", "/api/v1/me/list/"+f.film.ID, "b", "")
	if lists() != 2 {
		t.Fatalf("lists=%d", lists())
	}
	if _, err := f.r.GetUser(ctx, "a"); err != nil {
		t.Fatal("PUT creates the account record if it is missing")
	}

	items := decode(t, f.get("/api/v1/me/list", "a"))["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["movieId"] != f.series.ID || items[1].(map[string]any)["addedAt"].(float64) == 0 {
		t.Fatalf("newest first: %v", items)
	}
	if b := decode(t, f.get("/api/v1/me/list", "b"))["items"].([]any); len(b) != 1 {
		t.Fatalf("users only see their own list: %v", b)
	}
	if c := decode(t, f.get("/api/v1/me/list", "c"))["items"].([]any); len(c) != 0 {
		t.Fatalf("a new user has an empty list ([] not null): %v", c)
	}

	if rr = f.call("DELETE", "/api/v1/me/list/"+f.film.ID, "a", ""); decode(t, rr)["removed"] != true || lists() != 1 {
		t.Fatalf("%s lists=%d", rr.Body.String(), lists())
	}
	if rr = f.call("DELETE", "/api/v1/me/list/"+f.film.ID, "a", ""); decode(t, rr)["removed"] != false || lists() != 1 {
		t.Fatal("removing twice does not change the counter")
	}
	for path, want := range map[string]int{f.hidden.ID: 404, "nope": 404, strings.Repeat("x", 41): 400} {
		if rr = f.call("PUT", "/api/v1/me/list/"+path, "a", ""); rr.Code != want {
			t.Errorf("PUT %s: %d want %d", path, rr.Code, want)
		}
	}
	// the title disappears from lists when it is deleted
	_ = f.r.DeleteMovie(ctx, f.series.ID)
	if items = decode(t, f.get("/api/v1/me/list", "a"))["items"].([]any); len(items) != 0 {
		t.Fatalf("%v", items)
	}
}

// ---------- progress ----------

func TestProgress(t *testing.T) {
	f := newFx(t)
	put := func(uid, id, body string) *httptest.ResponseRecorder {
		return f.call("PUT", "/api/v1/me/progress/"+id, uid, body)
	}
	if rr := put("a", f.series.ID, `{"season":"Season 1","ep":1,"t":123.6,"d":2999.9}`); rr.Code != 204 {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	items := decode(t, f.get("/api/v1/me/progress", "a"))["items"].([]any)
	it := items[0].(map[string]any)
	if len(items) != 1 || it["movieId"] != f.series.ID || it["season"] != "Season 1" || it["ep"].(float64) != 1 || it["t"].(float64) != 124 || it["d"].(float64) != 3000 || it["at"].(float64) == 0 {
		t.Fatalf("floats from the video element are rounded: %v", it)
	}
	if rr := put("a", f.film.ID, `{"t":5,"d":100}`); rr.Code != 204 {
		t.Fatal("a movie has no season/ep")
	}
	items = decode(t, f.get("/api/v1/me/progress", "a"))["items"].([]any)
	mv := items[0].(map[string]any)
	if mv["movieId"] != f.film.ID || mv["season"] != nil || mv["ep"] != nil {
		t.Fatalf("%v", mv)
	}
	if rr := put("a", f.film.ID, `{"t":99999,"d":100}`); rr.Code != 204 {
		t.Fatal("t above d is capped")
	}
	if got := decode(t, f.get("/api/v1/me/progress", "a"))["items"].([]any)[0].(map[string]any)["t"].(float64); got != 100 {
		t.Fatalf("t capped to d: %v", got)
	}

	for name, body := range map[string]string{
		"missing t": `{"d":100}`, "missing d": `{"t":5}`, "d zero": `{"t":0,"d":0}`, "negative t": `{"t":-1,"d":10}`,
		"huge": `{"t":1,"d":1e300}`, "bad ep": `{"t":1,"d":10,"ep":-3}`, "not json": `{oops`, "long season": `{"t":1,"d":10,"season":"` + strings.Repeat("s", 101) + `"}`,
	} {
		if rr := put("a", f.film.ID, body); rr.Code != 400 {
			t.Errorf("%s: %d %s", name, rr.Code, rr.Body.String())
		}
	}
	for _, id := range []string{f.hidden.ID, "nope"} {
		if rr := put("a", id, `{"t":1,"d":10}`); rr.Code != 404 {
			t.Errorf("%s: %d", id, rr.Code)
		}
	}
	if b := decode(t, f.get("/api/v1/me/progress", "b"))["items"].([]any); len(b) != 0 {
		t.Fatal("progress is private to its owner")
	}
	if rr := f.call("DELETE", "/api/v1/me/progress/"+f.film.ID, "a", ""); rr.Code != 204 {
		t.Fatal("delete")
	}
	if items = decode(t, f.get("/api/v1/me/progress", "a"))["items"].([]any); len(items) != 1 {
		t.Fatalf("%v", items)
	}
}

// ---------- presence ----------

func TestPresence(t *testing.T) {
	f := newFx(t)
	f.s.deps.Presence = presence.New(0)
	f.as("a")
	if rr := f.call("POST", "/api/v1/presence", "a", `{"name":"Alice"}`); rr.Code != 204 {
		t.Fatalf("%d", rr.Code)
	}
	f.call("POST", "/api/v1/presence", "b", "") // no body: falls back to the token's name
	live := f.s.deps.Presence.Live(presence.LiveWindow)
	names := map[string]string{}
	for _, e := range live {
		names[e.UID] = e.Name
	}
	if len(live) != 2 || names["a"] != "Alice" || names["b"] != "b" {
		t.Fatalf("%v", names)
	}
	f.call("DELETE", "/api/v1/presence", "a", "")
	if f.s.deps.Presence.IsLive("a") || !f.s.deps.Presence.IsLive("b") {
		t.Fatal("DELETE removes only the caller")
	}
	u, _ := f.r.GetUser(context.Background(), "a")
	if u.LastSeen == 0 {
		t.Fatal("last seen is persisted")
	}
	if rr := f.call("POST", "/api/v1/presence", "a", `{"name":`); rr.Code != 400 {
		t.Fatalf("bad body: %d", rr.Code)
	}
}

// ---------- broken links ----------

func TestBrokenLinkReports(t *testing.T) {
	f := newFx(t)
	ctx := context.Background()
	body := `{"movieId":"` + f.series.ID + `","epInfo":"Season 1 · Episode 2","serverType":"server1","url":"https://cdn.example.com/s1e2"}`
	rr := f.call("POST", "/api/v1/broken-links", "a", body)
	if rr.Code != 200 || decode(t, rr)["counted"] != true {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	if rr = f.call("POST", "/api/v1/broken-links", "a", body); decode(t, rr)["counted"] != false {
		t.Fatal("the same user cannot re-report the same title within an hour")
	}
	f.call("POST", "/api/v1/broken-links", "b", body)
	list, total, _ := f.r.ListBrokenLinks(ctx, repo.BrokenListOpts{})
	if total != 1 || list[0].Reports != 2 || list[0].ReporterUID != "b" || list[0].EpInfo != "Season 1 · Episode 2" || list[0].Status != "open" {
		t.Fatalf("%+v", list)
	}
	for name, b := range map[string]string{"no id": `{}`, "long epInfo": `{"movieId":"` + f.film.ID + `","epInfo":"` + strings.Repeat("x", 121) + `"}`, "bad json": `{`} {
		if rr = f.call("POST", "/api/v1/broken-links", "c", b); rr.Code != 400 {
			t.Errorf("%s: %d", name, rr.Code)
		}
	}
	for _, id := range []string{f.hidden.ID, "nope"} {
		if rr = f.call("POST", "/api/v1/broken-links", "c", `{"movieId":"`+id+`"}`); rr.Code != 404 {
			t.Errorf("%s: %d", id, rr.Code)
		}
	}
	f.s.brokenLimit = httpx.NewLimiter(2, time.Minute)
	codes := []int{}
	for _, id := range []string{f.film.ID, f.soon.ID, f.series.ID} {
		codes = append(codes, f.call("POST", "/api/v1/broken-links", "spam", `{"movieId":"`+id+`"}`).Code)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 {
		t.Fatalf("per-user limit: %v", codes)
	}
}

// ---------- client errors ----------

func TestClientErrorReports(t *testing.T) {
	f := newFx(t)
	ctx := context.Background()
	if rr := f.call("POST", "/api/v1/errors", "", `{"message":"x"}`); rr.Code != 401 {
		t.Fatal("token required")
	}
	rr := f.call("POST", "/api/v1/errors", "a", `{"message":"boom","source":"app.js","line":12,"stack":"Error: boom","ts":1,"panel":"admin"}`)
	if rr.Code != 204 {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	logs, _ := f.r.RecentErrors(ctx, 5)
	if len(logs) != 1 || logs[0].UID != "a" || logs[0].Panel != "user" || logs[0].Message != "boom" {
		t.Fatalf("%+v", logs)
	}
	var stored map[string]any
	_ = json.Unmarshal([]byte(logs[0].Data), &stored)
	if stored["ts"].(float64) == 1 || stored["panel"] != "user" || stored["source"] != "app.js" {
		t.Fatalf("the client cannot set ts or panel: %v", stored)
	}

	big := `{"message":"` + strings.Repeat("x", 3990) + `"}` // passes the body limit but not the 4000-byte report limit
	if rr = f.call("POST", "/api/v1/errors", "a", big); rr.Code != 400 {
		t.Fatalf("over 4000 bytes in total: %d", rr.Code)
	}
	if rr = f.call("POST", "/api/v1/errors", "a", `{"message":"`+strings.Repeat("x", 5000)+`"}`); rr.Code != 413 {
		t.Fatalf("far too large: %d", rr.Code)
	}
	for _, b := range []string{`[1,2]`, `"text"`, `null`, `{oops`} {
		if rr = f.call("POST", "/api/v1/errors", "a", b); rr.Code != 400 {
			t.Errorf("%s: %d", b, rr.Code)
		}
	}
	f.s.errLimit = httpx.NewLimiter(2, time.Minute)
	codes := []int{}
	for i := 0; i < 3; i++ {
		codes = append(codes, f.call("POST", "/api/v1/errors", "flood", `{"message":"x"}`).Code)
	}
	if codes[0] != 204 || codes[1] != 204 || codes[2] != 429 {
		t.Fatalf("%v", codes)
	}
}

// ---------- password reset ----------

type fakeFirebase struct {
	srv    *httptest.Server
	mu     sync.Mutex
	calls  []map[string]string
	keys   []string
	status int
	body   string
}

func newFakeFirebase(t *testing.T) *fakeFirebase {
	ff := &fakeFirebase{status: 200, body: `{"email":"x"}`}
	ff.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]string
		_ = json.Unmarshal(b, &m)
		m["path"] = r.URL.Path
		ff.mu.Lock()
		ff.calls, ff.keys = append(ff.calls, m), append(ff.keys, r.URL.Query().Get("key"))
		st, body := ff.status, ff.body
		ff.mu.Unlock()
		w.WriteHeader(st)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(ff.srv.Close)
	return ff
}

func (ff *fakeFirebase) set(status int, body string) {
	ff.mu.Lock()
	ff.status, ff.body = status, body
	ff.mu.Unlock()
}
func (ff *fakeFirebase) count() int { ff.mu.Lock(); defer ff.mu.Unlock(); return len(ff.calls) }

func resetFx(t *testing.T) (*fx, *fakeFirebase, *bytes.Buffer) {
	f := newFx(t)
	ff := newFakeFirebase(t)
	var logs bytes.Buffer
	f.s.deps.FirebaseURL = ff.srv.URL
	f.s.deps.Cfg.FirebaseAPIKey = "SECRET-API-KEY"
	f.s.deps.Log = slog.New(slog.NewJSONHandler(&logs, nil))
	return f, ff, &logs
}

func reset(f *fx, email string) *httptest.ResponseRecorder {
	return f.call("POST", "/api/v1/auth/password-reset", "", `{"email":"`+email+`"}`)
}

func TestPasswordResetCallsFirebaseAndNeedsNoLogin(t *testing.T) {
	f, ff, _ := resetFx(t)
	rr := reset(f, "  Alice@Example.com ")
	if rr.Code != 200 || decode(t, rr)["ok"] != true {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	if ff.count() != 1 || ff.calls[0]["requestType"] != "PASSWORD_RESET" || ff.calls[0]["email"] != "alice@example.com" || ff.calls[0]["path"] != "/v1/accounts:sendOobCode" || ff.keys[0] != "SECRET-API-KEY" {
		t.Fatalf("%v %v", ff.calls, ff.keys)
	}
	if strings.Contains(rr.Body.String(), "SECRET-API-KEY") {
		t.Fatal("never leak the key")
	}
}

func TestPasswordResetAnswerNeverRevealsWhetherTheAccountExists(t *testing.T) {
	f, ff, _ := resetFx(t)
	ok := reset(f, "real@example.com").Body.String()
	for _, firebaseSays := range []string{
		`{"error":{"code":400,"message":"EMAIL_NOT_FOUND"}}`,
		`{"error":{"code":400,"message":"USER_DISABLED"}}`,
		`{"error":{"code":400,"message":"TOO_MANY_ATTEMPTS_TRY_LATER"}}`,
		`{"error":{"code":400,"message":"OPERATION_NOT_ALLOWED"}}`,
	} {
		ff.set(400, firebaseSays)
		rr := reset(f, fmt.Sprintf("ghost%d@example.com", len(firebaseSays)))
		if rr.Code != 200 || rr.Body.String() != ok {
			t.Errorf("%s must look exactly like success: %d %s vs %s", firebaseSays, rr.Code, rr.Body.String(), ok)
		}
	}
}

func TestPasswordResetLimits(t *testing.T) {
	f, ff, _ := resetFx(t)
	ff.set(400, `{"error":{"code":400,"message":"EMAIL_NOT_FOUND"}}`) // unknown address: must be limited like a real one
	for i := 0; i < 3; i++ {
		addr := []string{"a@example.com", "A@EXAMPLE.COM", " a@example.com"}[i] // same address, different spelling
		if rr := reset(f, addr); rr.Code != 200 {
			t.Fatalf("request %d: %d", i, rr.Code)
		}
	}
	rr := reset(f, "a@example.com")
	if rr.Code != 429 || decode(t, rr)["error"] != "rate_limited" || !strings.Contains(rr.Body.String(), "3 times in 24 hours") || rr.Header().Get("Retry-After") == "" {
		t.Fatalf("4th request in 24 h: %d %s", rr.Code, rr.Body.String())
	}
	if ff.count() != 3 {
		t.Fatalf("a limited request must not reach Firebase: %d calls", ff.count())
	}
	if rr = reset(f, "other@example.com"); rr.Code != 200 {
		t.Fatal("the limit is per address")
	}

	// the admin can change the limit
	_ = f.r.SetSettings(context.Background(), map[string]string{"pwdResetLimit": "1"})
	reset(f, "b@example.com")
	if rr = reset(f, "b@example.com"); rr.Code != 429 || !strings.Contains(rr.Body.String(), "1 times") {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}

	// per-IP limit across different addresses
	f.s.resetLimit = httpx.NewLimiter(2, time.Minute)
	codes := []int{}
	for i := 0; i < 3; i++ {
		codes = append(codes, reset(f, fmt.Sprintf("n%d@example.com", i)).Code)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 {
		t.Fatalf("%v", codes)
	}
}

func TestPasswordResetValidationAndOutage(t *testing.T) {
	f, ff, logs := resetFx(t)
	for _, bad := range []string{"", "nope", "a@b", "@example.com", "a b@example.com", "a@@example.com", "a@example.", strings.Repeat("x", 250) + "@example.com"} {
		if rr := reset(f, bad); rr.Code != 400 || decode(t, rr)["field"] != "email" {
			t.Errorf("%q: %d %s", bad, rr.Code, rr.Body.String())
		}
	}
	if rr := f.call("POST", "/api/v1/auth/password-reset", "", `{oops`); rr.Code != 400 {
		t.Fatal("bad json")
	}
	if ff.count() != 0 {
		t.Fatal("invalid input must not reach Firebase")
	}

	ff.set(503, `unavailable`)
	rr := reset(f, "down@example.com")
	if rr.Code != 503 || decode(t, rr)["error"] != "unavailable" {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	f.s.deps.FirebaseURL = "http://127.0.0.1:1" // unreachable
	if rr = reset(f, "down2@example.com"); rr.Code != 503 {
		t.Fatalf("%d", rr.Code)
	}
	if strings.Contains(logs.String(), "SECRET-API-KEY") || strings.Contains(rr.Body.String(), "SECRET-API-KEY") {
		t.Fatalf("the API key is part of the Firebase URL and must never be logged or returned: %s", logs.String())
	}
}

// ---------- maintenance ----------

func TestMaintenanceTrimsLogsAndAttempts(t *testing.T) {
	f := newFx(t)
	ctx := context.Background()
	db := f.r.DB()
	tx, _ := db.Begin()
	for i := 0; i < keepErrorLogs+50; i++ {
		_, _ = tx.Exec(`INSERT INTO error_logs(id, data, created_at) VALUES (?, '{}', ?)`, fmt.Sprintf("e%06d", i), int64(i))
	}
	old := time.Now().Add(-72 * time.Hour).UnixMilli()
	_, _ = tx.Exec(`INSERT INTO pwd_reset_attempts(email_key, ts) VALUES ('k', ?), ('k', ?)`, old, time.Now().UnixMilli())
	_ = tx.Commit()
	f.s.deps.Presence = presence.New(time.Nanosecond)
	f.s.deps.Presence.Touch("x", "X")

	f.s.MaintainOnce(ctx)
	var errs, resets int
	_ = db.QueryRow(`SELECT COUNT(*) FROM error_logs`).Scan(&errs)
	_ = db.QueryRow(`SELECT COUNT(*) FROM pwd_reset_attempts`).Scan(&resets)
	if errs != keepErrorLogs || resets != 1 {
		t.Fatalf("errors=%d resets=%d", errs, resets)
	}
	var newest string
	_ = db.QueryRow(`SELECT id FROM error_logs ORDER BY created_at DESC LIMIT 1`).Scan(&newest)
	if newest != fmt.Sprintf("e%06d", keepErrorLogs+49) {
		t.Fatalf("the newest reports are kept: %s", newest)
	}
}
