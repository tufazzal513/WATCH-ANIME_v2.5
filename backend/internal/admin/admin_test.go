//go:build !nodriver

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"movex/internal/auth"
	"movex/internal/config"
	"movex/internal/db"
	"movex/internal/httpx"
	"movex/internal/repo"
)

const testAPIKey = "TEST-API-KEY"

// fakeFirebase answers accounts:signInWithPassword for a fixed set of accounts.
type fakeFirebase struct {
	srv    *httptest.Server
	mu     sync.Mutex
	users  map[string][2]string // email -> {password, uid}
	status int                  // when non-zero, every call answers this status
	calls  []map[string]any
	keys   []string
	uidFor func(email string) string // optional: token uid different from localId (tests)
}

func newFakeFirebase(t *testing.T) *fakeFirebase {
	f := &fakeFirebase{users: map[string][2]string{
		"admin@example.com": {"correct horse", "admin1"},
		"user@example.com":  {"user pass", "user9"},
	}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		m["path"] = r.URL.Path
		f.mu.Lock()
		f.calls, f.keys = append(f.calls, m), append(f.keys, r.URL.Query().Get("key"))
		st := f.status
		u, ok := f.users[fmt.Sprint(m["email"])]
		f.mu.Unlock()
		switch {
		case st != 0:
			w.WriteHeader(st)
			_, _ = io.WriteString(w, `{"error":{"message":"boom"}}`)
		case !ok || u[0] != fmt.Sprint(m["password"]):
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"error":{"code":400,"message":"INVALID_LOGIN_CREDENTIALS"}}`)
		default:
			tokUID := u[1]
			if f.uidFor != nil {
				tokUID = f.uidFor(fmt.Sprint(m["email"]))
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"idToken": "dev:" + tokUID, "localId": u[1]})
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeFirebase) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

type env struct {
	t    *testing.T
	a    *Admin
	mux  *http.ServeMux
	r    *repo.Repo
	fb   *fakeFirebase
	cfg  *config.Config
	logs *bytes.Buffer
	hits map[string]int // how often each test route ran
}

func newEnv(t *testing.T) *env {
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
	e := &env{t: t, r: repo.New(d), fb: newFakeFirebase(t), logs: &bytes.Buffer{}, hits: map[string]int{}}
	e.cfg = &config.Config{
		Role: config.RoleMain, AdminUIDs: []string{"admin1", "admin2"}, FirebaseProjectID: "p", FirebaseAPIKey: testAPIKey,
		SessionSecret: []byte("0123456789abcdef0123456789abcdef"), CookieSecure: true, DevInsecureAuth: true,
	}
	e.a, err = New(Deps{Cfg: e.cfg, Repo: e.r, Verifier: auth.NewVerifier(auth.Options{ProjectID: "p", DevInsecure: true}),
		Log: slog.New(slog.NewJSONHandler(e.logs, nil)), FirebaseURL: e.fb.srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	e.mux = http.NewServeMux()
	e.a.Register(e.mux)
	for _, pat := range []string{"POST /admin/test/act", "DELETE /admin/test/del", "PUT /admin/test/put", "GET /admin/test/page"} {
		pat := pat
		e.a.Handle(pat, func(c *Ctx) { e.hits[pat]++; c.W.WriteHeader(204) })
	}
	e.a.Handle("GET /admin/test/flash", func(c *Ctx) { c.Flash("success", "Saved <b>ok</b>"); c.Redirect("/admin/") })
	e.a.Handle("GET /admin/test/htmx-redirect", func(c *Ctx) { c.Redirect("/admin/movies") })
	return e
}

type opt func(*http.Request)

func withCookie(c *http.Cookie) opt { return func(r *http.Request) { r.AddCookie(c) } }
func withHeader(k, v string) opt    { return func(r *http.Request) { r.Header.Set(k, v) } }
func withForm(v url.Values) opt {
	return func(r *http.Request) {
		r.Body = io.NopCloser(strings.NewReader(v.Encode()))
		r.ContentLength = int64(len(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
}

func (e *env) do(method, path string, opts ...opt) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, nil) // Host: example.com
	req.RemoteAddr = "192.0.2.10:5555"
	if method != "GET" {
		req.Header.Set("Origin", "http://example.com") // what a browser sends for a same-origin form post
	}
	for _, o := range opts {
		o(req)
	}
	rr := httptest.NewRecorder()
	e.mux.ServeHTTP(rr, req)
	return rr
}

var tokenRe = regexp.MustCompile(`name="csrf" value="([^"]+)"`)
var metaCSRFRe = regexp.MustCompile(`<meta name="csrf-token" content="([^"]+)"`)

func (e *env) loginToken() string {
	e.t.Helper()
	m := tokenRe.FindStringSubmatch(e.do("GET", "/admin/login").Body.String())
	if m == nil {
		e.t.Fatal("login page has no token")
	}
	return m[1]
}

func (e *env) postLogin(email, password string, extra ...url.Values) *httptest.ResponseRecorder {
	v := url.Values{"email": {email}, "password": {password}, "csrf": {e.loginToken()}}
	for _, x := range extra {
		for k, vals := range x {
			v[k] = vals
		}
	}
	return e.do("POST", "/admin/login", withForm(v))
}

func sessionFromResponse(rr *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rr.Result().Cookies() {
		if c.Name == sessionCookie && c.MaxAge >= 0 && c.Value != "" {
			return c
		}
	}
	return nil
}

// signIn performs the real login flow and returns the session cookie and its CSRF token.
func (e *env) signIn(email, password string) (*http.Cookie, string) {
	e.t.Helper()
	rr := e.postLogin(email, password)
	ck := sessionFromResponse(rr)
	if rr.Code != 303 || ck == nil {
		e.t.Fatalf("sign-in failed: %d %s", rr.Code, rr.Body.String())
	}
	page := e.do("GET", "/admin/", withCookie(ck))
	m := metaCSRFRe.FindStringSubmatch(page.Body.String())
	if page.Code != 200 || m == nil {
		e.t.Fatalf("dashboard after login: %d", page.Code)
	}
	return ck, m[1]
}

func (e *env) admin1() (*http.Cookie, string) { return e.signIn("admin@example.com", "correct horse") }

// ---------- headers ----------

func requireSecurityHeaders(t *testing.T, rr *httptest.ResponseRecorder, what string) {
	t.Helper()
	h := rr.Header()
	csp := h.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "script-src 'self'", "style-src 'self'", "frame-ancestors 'none'", "object-src 'none'", "base-uri 'none'", "form-action 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("%s: CSP lacks %q: %s", what, want, csp)
		}
	}
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
		t.Errorf("%s: CSP must not allow unsafe-inline/eval", what)
	}
	for k, v := range map[string]string{"Cache-Control": "no-store", "X-Frame-Options": "DENY", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer"} {
		if h.Get(k) != v {
			t.Errorf("%s: %s = %q, want %q", what, k, h.Get(k), v)
		}
	}
	if h.Get("Strict-Transport-Security") == "" {
		t.Errorf("%s: HSTS missing while COOKIE_SECURE=1", what)
	}
}

func TestLoginPageAndHeaders(t *testing.T) {
	e := newEnv(t)
	rr := e.do("GET", "/admin/login")
	body := rr.Body.String()
	if rr.Code != 200 || !strings.Contains(body, `action="/admin/login"`) || !strings.Contains(body, `type="password"`) || !strings.Contains(body, "Secure Admin Access") {
		t.Fatalf("%d %s", rr.Code, body)
	}
	if len(rr.Result().Cookies()) != 0 {
		t.Fatal("the login page must not set any cookie")
	}
	requireSecurityHeaders(t, rr, "login page")
	if !strings.Contains(body, `rel="stylesheet" href="/admin/static/css/tailwind.css?v=`) {
		t.Fatal("assets must be versioned for cache busting")
	}

	// every page, redirect and error carries the headers
	ck, _ := e.admin1()
	for name, rr := range map[string]*httptest.ResponseRecorder{
		"anonymous redirect": e.do("GET", "/admin/movies"),
		"dashboard":          e.do("GET", "/admin/", withCookie(ck)),
		"404 page":           e.do("GET", "/admin/nope", withCookie(ck)),
		"csrf failure":       e.do("POST", "/admin/test/act", withCookie(ck)),
	} {
		requireSecurityHeaders(t, rr, name)
	}

	// HSTS only makes sense over HTTPS
	e.cfg.CookieSecure = false
	if hv := e.do("GET", "/admin/login").Header().Get("Strict-Transport-Security"); hv != "" {
		t.Fatalf("no HSTS in local http mode: %q", hv)
	}
}

// ---------- anonymous access ----------

func TestAnonymousVisitorsAreSentToLogin(t *testing.T) {
	e := newEnv(t)
	loc := func(rr *httptest.ResponseRecorder) string { return rr.Header().Get("Location") }

	if rr := e.do("GET", "/admin/"); rr.Code != 303 || loc(rr) != "/admin/login" {
		t.Fatalf("%d %s", rr.Code, loc(rr))
	}
	if rr := e.do("GET", "/admin/movies/abc/edit?tab=2"); rr.Code != 303 || loc(rr) != "/admin/login?next="+url.QueryEscape("/admin/movies/abc/edit?tab=2") {
		t.Fatalf("a deep link is remembered: %d %s", rr.Code, loc(rr))
	}
	if rr := e.do("GET", "/admin/nope"); rr.Code != 303 {
		t.Fatal("unknown admin paths must not reveal whether they exist")
	}
	if rr := e.do("POST", "/admin/test/act"); rr.Code != 303 || loc(rr) != "/admin/login" {
		t.Fatalf("anonymous POST: %d %s", rr.Code, loc(rr))
	}
	rr := e.do("GET", "/admin/test/page", withHeader("HX-Request", "true"))
	if rr.Code != 401 || rr.Header().Get("HX-Redirect") != "/admin/login" {
		t.Fatalf("htmx must be redirected as a whole page: %d %v", rr.Code, rr.Header())
	}
	if e.hits["GET /admin/test/page"] != 0 {
		t.Fatal("handlers must not run for anonymous visitors")
	}
	if rr := e.do("GET", "/admin"); rr.Code != 301 || loc(rr) != "/admin/" {
		t.Fatalf("/admin: %d %s", rr.Code, loc(rr))
	}
}

// ---------- login ----------

func TestLoginSuccessCreatesASecureSession(t *testing.T) {
	e := newEnv(t)
	rr := e.postLogin("  Admin@Example.com ", "correct horse")
	ck := sessionFromResponse(rr)
	if rr.Code != 303 || rr.Header().Get("Location") != "/admin/" || ck == nil {
		t.Fatalf("%d %v", rr.Code, rr.Header())
	}
	if !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/admin" || ck.MaxAge != 12*3600 {
		t.Fatalf("cookie attributes: %+v", ck)
	}
	if strings.Count(ck.Value, ".") != 1 || len(ck.Value) < 100 {
		t.Fatalf("cookie must be <random id>.<signature>: %q", ck.Value)
	}
	if e.fb.count() != 1 || e.fb.calls[0]["email"] != "admin@example.com" || e.fb.calls[0]["password"] != "correct horse" ||
		e.fb.calls[0]["returnSecureToken"] != true || e.fb.calls[0]["path"] != "/v1/accounts:signInWithPassword" || e.fb.keys[0] != testAPIKey {
		t.Fatalf("%v %v", e.fb.calls, e.fb.keys)
	}
	if page := e.do("GET", "/admin/", withCookie(ck)); page.Code != 200 || !strings.Contains(page.Body.String(), "Dashboard") {
		t.Fatalf("%d", page.Code)
	}
	audit, _ := e.r.RecentAudit(context.Background(), 5)
	if len(audit) != 1 || audit[0].Action != "admin.login" || audit[0].ActorUID != "admin1" {
		t.Fatalf("%+v", audit)
	}
	if strings.Contains(e.logs.String(), "correct horse") || strings.Contains(e.logs.String(), testAPIKey) {
		t.Fatal("neither the password nor the API key may be logged")
	}
	// a signed-in admin who opens the login page is sent on
	if rr = e.do("GET", "/admin/login?next=%2Fadmin%2Fusers", withCookie(ck)); rr.Code != 303 || rr.Header().Get("Location") != "/admin/users" {
		t.Fatalf("%d %s", rr.Code, rr.Header().Get("Location"))
	}
}

func TestLoginRedirectsOnlyInsideAdmin(t *testing.T) {
	e := newEnv(t)
	for next, want := range map[string]string{
		"/admin/movies":       "/admin/movies",
		"/admin/users?page=3": "/admin/users?page=3",
		"":                    "/admin/",
		"//evil.com":          "/admin/",
		"https://evil.com":    "/admin/",
		"/administrator":      "/admin/",
		"/admin/login":        "/admin/",
		"/\\evil.com":         "/admin/",
		"/admin/../evil":      "/admin/",
		"javascript:alert(1)": "/admin/",
	} {
		e.a.loginIP, e.a.loginEmail = httpx.NewLimiter(1000, time.Minute), httpx.NewLimiter(1000, time.Minute)
		rr := e.postLogin("admin@example.com", "correct horse", url.Values{"next": {next}})
		if got := rr.Header().Get("Location"); rr.Code != 303 || got != want {
			t.Errorf("next=%q: redirected to %q (%d), want %q", next, got, rr.Code, want)
		}
	}
}

func TestEveryLoginFailureLooksTheSame(t *testing.T) {
	e := newEnv(t)
	e.fb.uidFor = func(email string) string {
		if email == "mismatch@example.com" {
			return "admin2" // a valid admin uid in the token, but a different localId
		}
		return e.fb.users[email][1]
	}
	e.fb.users["mismatch@example.com"] = [2]string{"pw", "someone-else"}

	normalise := func(rr *httptest.ResponseRecorder) string {
		s := rr.Body.String()
		s = tokenRe.ReplaceAllString(s, `name="csrf" value="T"`)
		return regexp.MustCompile(`(name="email" value=)"[^"]*"`).ReplaceAllString(s, `$1"E"`)
	}
	cases := map[string]*httptest.ResponseRecorder{
		"wrong password":     e.postLogin("admin@example.com", "wrong"),
		"unknown account":    e.postLogin("nobody@example.com", "whatever"),
		"valid but no admin": e.postLogin("user@example.com", "user pass"),
		"token/localId diff": e.postLogin("mismatch@example.com", "pw"),
		"empty password":     e.postLogin("admin@example.com", ""),
		"empty email":        e.postLogin("", "x"),
	}
	var ref string
	for name, rr := range cases {
		if rr.Code != 401 || sessionFromResponse(rr) != nil || len(rr.Result().Cookies()) != 0 {
			t.Errorf("%s: %d, cookies %v", name, rr.Code, rr.Result().Cookies())
		}
		if !strings.Contains(rr.Body.String(), msgBadLogin) {
			t.Errorf("%s: wrong message", name)
		}
		if ref == "" {
			ref = normalise(rr)
		} else if normalise(rr) != ref {
			t.Errorf("%s: the page differs from the other failures, which would leak information", name)
		}
	}
	if audit, _ := e.r.RecentAudit(context.Background(), 5); len(audit) != 0 {
		t.Fatalf("failed logins create no session and no audit row: %+v", audit)
	}
}

func TestLoginServiceOutage(t *testing.T) {
	e := newEnv(t)
	e.fb.status = 503
	rr := e.postLogin("admin@example.com", "correct horse")
	if rr.Code != 503 || !strings.Contains(rr.Body.String(), msgUnavail) || sessionFromResponse(rr) != nil {
		t.Fatalf("%d", rr.Code)
	}
	e.fb.status = 0
	e.a.deps.FirebaseURL = "http://127.0.0.1:1" // unreachable
	rr = e.postLogin("admin@example.com", "correct horse")
	if rr.Code != 503 {
		t.Fatalf("%d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), testAPIKey) || strings.Contains(e.logs.String(), testAPIKey) {
		t.Fatal("the API key is part of the URL and must never leak")
	}
}

func TestLoginIsRateLimitedPerEmailAndPerIP(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < loginAttempts; i++ {
		if rr := e.postLogin("admin@example.com", "wrong"); rr.Code != 401 {
			t.Fatalf("attempt %d: %d", i, rr.Code)
		}
	}
	rr := e.postLogin("admin@example.com", "correct horse") // even the right password is refused while throttled
	if rr.Code != 429 || rr.Header().Get("Retry-After") == "" || !strings.Contains(rr.Body.String(), msgThrottled) || sessionFromResponse(rr) != nil {
		t.Fatalf("%d %v", rr.Code, rr.Header())
	}
	calls := e.fb.count()
	e.postLogin("admin@example.com", "x")
	if e.fb.count() != calls {
		t.Fatal("a throttled attempt must not reach Firebase")
	}

	e = newEnv(t) // per IP across different addresses
	for i := 0; i < loginAttempts; i++ {
		e.postLogin(fmt.Sprintf("user%d@example.com", i), "x")
	}
	if rr := e.postLogin("brand-new@example.com", "x"); rr.Code != 429 {
		t.Fatalf("per-IP limit: %d", rr.Code)
	}
}

func TestLoginFormCSRFAndOrigin(t *testing.T) {
	e := newEnv(t)
	post := func(token string, opts ...opt) *httptest.ResponseRecorder {
		v := url.Values{"email": {"admin@example.com"}, "password": {"correct horse"}, "csrf": {token}}
		return e.do("POST", "/admin/login", append([]opt{withForm(v)}, opts...)...)
	}
	good := e.loginToken()
	parts := strings.SplitN(good, ".", 2)
	for name, tok := range map[string]string{"missing": "", "garbage": "abc", "tampered time": "1." + parts[1], "wrong signature": parts[0] + ".AAAA"} {
		rr := post(tok)
		if rr.Code != 400 || !strings.Contains(rr.Body.String(), msgExpired) || sessionFromResponse(rr) != nil {
			t.Errorf("%s: %d", name, rr.Code)
		}
	}
	if e.fb.count() != 0 {
		t.Fatal("a bad form token must not reach Firebase")
	}
	// the token expires after 2 hours
	e.a.now = func() time.Time { return time.Now().Add(3 * time.Hour) }
	if rr := post(good); rr.Code != 400 {
		t.Fatalf("expired token: %d", rr.Code)
	}
	e.a.now = time.Now
	if rr := post(good); rr.Code != 303 {
		t.Fatalf("fresh token: %d", rr.Code)
	}

	// cross-site posts are refused before anything else
	for name, o := range map[string]opt{
		"Sec-Fetch-Site cross-site": withHeader("Sec-Fetch-Site", "cross-site"),
		"Sec-Fetch-Site same-site":  withHeader("Sec-Fetch-Site", "same-site"),
		"foreign Origin":            withHeader("Origin", "https://evil.example"),
	} {
		if rr := post(e.loginToken(), o); rr.Code != 403 {
			t.Errorf("%s: %d", name, rr.Code)
		}
	}
	if rr := post(e.loginToken(), withHeader("Sec-Fetch-Site", "same-origin")); rr.Code != 303 {
		t.Fatalf("same-origin: %d", rr.Code)
	}
}

// ---------- sessions ----------

func TestSessionCookieTamperingAndExpiry(t *testing.T) {
	e := newEnv(t)
	ck, _ := e.admin1()
	id, sig, _ := strings.Cut(ck.Value, ".")
	ok := func(c *http.Cookie) bool { return e.do("GET", "/admin/", withCookie(c)).Code == 200 }
	if !ok(ck) {
		t.Fatal("baseline")
	}
	for name, v := range map[string]string{
		"bad signature":     id + "." + sig[:len(sig)-2] + "AA",
		"swapped id":        strings.Repeat("a", 64) + "." + sig,
		"no signature":      id,
		"empty":             "",
		"signature only":    "." + sig,
		"huge":              strings.Repeat("x", 300),
		"unsigned known id": id + ".",
	} {
		if ok(&http.Cookie{Name: sessionCookie, Value: v}) {
			t.Errorf("%s must not authenticate", name)
		}
	}
	// a correctly signed cookie for an id the server never issued
	forged := strings.Repeat("b", 64)
	if ok(&http.Cookie{Name: sessionCookie, Value: forged + "." + e.a.mac("session", forged)}) {
		t.Fatal("a valid signature is not enough: the session must exist on the server")
	}
	// expiry is enforced by the server, not by the cookie
	if _, err := e.r.DB().Exec(`UPDATE admin_sessions SET expires_at = 1`); err != nil {
		t.Fatal(err)
	}
	if ok(ck) {
		t.Fatal("an expired session must stop working")
	}
	// a rejected cookie is cleared
	rr := e.do("GET", "/admin/", withCookie(ck))
	cleared := false
	for _, c := range rr.Result().Cookies() {
		cleared = cleared || (c.Name == sessionCookie && c.MaxAge < 0)
	}
	if !cleared {
		t.Fatal("the browser should be told to drop the dead cookie")
	}
}

func TestRemovingAnAdminFromTheEnvRevokesTheSessionAtOnce(t *testing.T) {
	e := newEnv(t)
	ck, _ := e.admin1()
	if e.do("GET", "/admin/", withCookie(ck)).Code != 200 {
		t.Fatal("baseline")
	}
	delete(e.a.admins, "admin1") // what a restart with a changed ADMIN_UIDS does
	if rr := e.do("GET", "/admin/", withCookie(ck)); rr.Code != 303 {
		t.Fatalf("%d", rr.Code)
	}
	if rr := e.postLogin("admin@example.com", "correct horse"); rr.Code != 401 {
		t.Fatalf("and a new login is refused: %d", rr.Code)
	}
}

func TestLogoutDestroysTheSession(t *testing.T) {
	e := newEnv(t)
	ck, csrf := e.admin1()

	if rr := e.do("POST", "/admin/logout", withCookie(ck)); rr.Code != 403 { // no CSRF token
		t.Fatalf("%d", rr.Code)
	}
	if rr := e.do("POST", "/admin/logout", withCookie(ck), withForm(url.Values{"csrf": {csrf}}), withHeader("Sec-Fetch-Site", "cross-site")); rr.Code != 403 {
		t.Fatalf("cross-site logout: %d", rr.Code)
	}
	if e.do("GET", "/admin/", withCookie(ck)).Code != 200 {
		t.Fatal("failed logouts must not end the session")
	}

	rr := e.do("POST", "/admin/logout", withCookie(ck), withForm(url.Values{"csrf": {csrf}}))
	if rr.Code != 303 || rr.Header().Get("Location") != "/admin/login" {
		t.Fatalf("%d %v", rr.Code, rr.Header())
	}
	cleared := false
	for _, c := range rr.Result().Cookies() {
		cleared = cleared || (c.Name == sessionCookie && c.MaxAge < 0)
	}
	if !cleared {
		t.Fatal("cookie must be cleared")
	}
	if e.do("GET", "/admin/", withCookie(ck)).Code != 303 {
		t.Fatal("replaying the old cookie after logout must fail (server-side invalidation)")
	}
	audit, _ := e.r.RecentAudit(context.Background(), 5)
	if audit[0].Action != "admin.logout" {
		t.Fatalf("%+v", audit)
	}
	// anonymous logout is harmless
	if rr := e.do("POST", "/admin/logout"); rr.Code != 303 {
		t.Fatalf("%d", rr.Code)
	}
}

// ---------- CSRF on every state-changing admin request ----------

func TestCSRFProtectionOnStateChangingRequests(t *testing.T) {
	e := newEnv(t)
	ck, csrf := e.admin1()
	_, otherCSRF := e.signIn("admin@example.com", "correct horse") // a second session of the same admin
	if csrf == otherCSRF {
		t.Fatal("each session has its own CSRF token")
	}
	if !strings.Contains(e.do("GET", "/admin/", withCookie(ck)).Body.String(), csrf) {
		t.Fatal("the token is embedded in pages")
	}

	for _, rt := range []struct{ method, path string }{{"POST", "/admin/test/act"}, {"DELETE", "/admin/test/del"}, {"PUT", "/admin/test/put"}} {
		key := rt.method + " " + rt.path
		run := func(opts ...opt) int {
			return e.do(rt.method, rt.path, append([]opt{withCookie(ck)}, opts...)...).Code
		}
		before := e.hits[key]
		for name, code := range map[string]int{
			"no token":                    run(),
			"wrong token":                 run(withHeader("X-CSRF-Token", "nope")),
			"another session's token":     run(withHeader("X-CSRF-Token", otherCSRF)),
			"empty form token":            run(withForm(url.Values{"csrf": {""}})),
			"valid token, cross-site":     run(withHeader("X-CSRF-Token", csrf), withHeader("Sec-Fetch-Site", "cross-site")),
			"valid token, same-site":      run(withHeader("X-CSRF-Token", csrf), withHeader("Sec-Fetch-Site", "same-site")),
			"valid token, foreign Origin": run(withHeader("X-CSRF-Token", csrf), withHeader("Origin", "https://evil.example")),
			"token in the query string":   e.do(rt.method, rt.path+"?csrf="+csrf, withCookie(ck)).Code,
		} {
			if code != 403 {
				t.Errorf("%s %s: %d, want 403", key, name, code)
			}
		}
		if e.hits[key] != before {
			t.Fatalf("%s: the handler ran although a check failed", key)
		}
		if run(withHeader("X-CSRF-Token", csrf)) != 204 {
			t.Errorf("%s: header token must work (htmx)", key)
		}
		want := 2
		if rt.method != "DELETE" { // browsers only submit forms with GET/POST; htmx sends DELETE with the header
			want = 3
			if run(withForm(url.Values{"csrf": {csrf}})) != 204 {
				t.Errorf("%s: form token must work", key)
			}
		}
		if run(withHeader("X-CSRF-Token", csrf), withHeader("Sec-Fetch-Site", "same-origin")) != 204 {
			t.Errorf("%s: explicit same-origin", key)
		}
		if e.hits[key] != before+want {
			t.Fatalf("%s: expected %d successful calls, got %d", key, want, e.hits[key]-before)
		}
	}
	// reading needs no token
	if rr := e.do("GET", "/admin/test/page", withCookie(ck)); rr.Code != 204 {
		t.Fatalf("GET: %d", rr.Code)
	}
	// failures are rendered as a page, not as a stack trace
	rr := e.do("POST", "/admin/test/act", withCookie(ck))
	if !strings.Contains(rr.Body.String(), "Security check failed") || strings.Contains(rr.Body.String(), "csrf="+csrf) {
		t.Fatalf("%s", rr.Body.String())
	}
	if !strings.Contains(e.logs.String(), "CSRF check failed") {
		t.Fatal("CSRF failures are logged")
	}
}

// ---------- rendering ----------

func TestFlashMessagesAreOneShotSignedAndEscaped(t *testing.T) {
	e := newEnv(t)
	ck, _ := e.admin1()
	rr := e.do("GET", "/admin/test/flash", withCookie(ck))
	if rr.Code != 303 || rr.Header().Get("Location") != "/admin/" {
		t.Fatalf("%d %v", rr.Code, rr.Header())
	}
	var flash *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == flashCookie {
			flash = c
		}
	}
	if flash == nil || !flash.HttpOnly || flash.Path != "/admin" {
		t.Fatalf("%+v", flash)
	}
	page := e.do("GET", "/admin/", withCookie(ck), withCookie(flash)).Body.String()
	if !strings.Contains(page, "Saved &lt;b&gt;ok&lt;/b&gt;") || strings.Contains(page, "<b>ok</b>") {
		t.Fatalf("flash text must be HTML-escaped: %s", page)
	}
	cleared := false
	for _, c := range e.do("GET", "/admin/", withCookie(ck), withCookie(flash)).Result().Cookies() {
		cleared = cleared || (c.Name == flashCookie && c.MaxAge < 0)
	}
	if !cleared {
		t.Fatal("the flash cookie must be consumed")
	}
	forged := &http.Cookie{Name: flashCookie, Value: "PGI-.AAAA"}
	if strings.Contains(e.do("GET", "/admin/", withCookie(ck), withCookie(forged)).Body.String(), "data-flash") {
		t.Fatal("an unsigned flash cookie must be ignored")
	}
	// htmx redirects navigate the whole page
	rr = e.do("GET", "/admin/test/htmx-redirect", withCookie(ck), withHeader("HX-Request", "true"))
	if rr.Code != 200 || rr.Header().Get("HX-Redirect") != "/admin/movies" {
		t.Fatalf("%d %v", rr.Code, rr.Header())
	}
}

func TestLayoutBrandBadgeAndEscaping(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ck, _ := e.admin1()
	page := e.do("GET", "/admin/", withCookie(ck)).Body.String()
	for _, want := range []string{`Move<span class="text-blue-500">X</span>`, `href="/admin/categories"`, `href="/admin/broken-links"`, `href="/admin/server"`,
		`href="/admin/movies/new"`, `href="/admin/users"`, `action="/admin/logout"`, `<title>Dashboard · MoveX Admin</title>`, `text-blue-500">`} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	if strings.Contains(page, "rounded-full\">0<") {
		t.Fatal("no badge when there are no reports")
	}
	// the active nav item is highlighted
	if !regexp.MustCompile(`href="/admin/" class="[^"]*text-blue-500`).MatchString(page) || regexp.MustCompile(`href="/admin/users" class="[^"]*text-blue-500`).MatchString(page) {
		t.Fatal("only the current page is highlighted")
	}

	// badge with open broken-link reports
	in := repo.MovieInput{Title: "T", Genres: nil}
	m, err := e.r.CreateMovie(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	_ = e.r.ReportBrokenLink(ctx, repo.BrokenLinkInput{MovieID: m.ID})
	if !strings.Contains(e.do("GET", "/admin/", withCookie(ck)).Body.String(), `rounded-full">1</span>`) {
		t.Fatal("the sidebar shows the number of open broken-link reports")
	}

	// the app name comes from settings and is escaped everywhere
	if err := e.r.SetSettings(ctx, map[string]string{"appName": "<img src=x onerror=alert(1)>Y"}); err != nil {
		t.Fatal(err)
	}
	page = e.do("GET", "/admin/", withCookie(ck)).Body.String()
	if strings.Contains(page, "<img src=x") || !strings.Contains(page, "&lt;img src=x onerror=alert(1)&gt;") || !strings.Contains(page, `<span class="text-blue-500">Y</span>`) {
		t.Fatalf("app name must be escaped: %s", page)
	}
	if login := e.do("GET", "/admin/login").Body.String(); strings.Contains(login, "<img src=x") {
		t.Fatal("login page escapes it too")
	}
}

func TestDashboardShowsLiveCounts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ck, _ := e.admin1()
	_, _ = e.r.CreateMovie(ctx, repo.MovieInput{Title: "A"})
	_, _ = e.r.CreateMovie(ctx, repo.MovieInput{Title: "B"})
	_, _, _ = e.r.EnsureUser(ctx, repo.UserProfile{UID: "u1"})
	page := e.do("GET", "/admin/", withCookie(ck)).Body.String()
	if !regexp.MustCompile(`Movies &amp; series</p><p class="[^"]*">2<`).MatchString(page) || !regexp.MustCompile(`Users</p><p class="[^"]*">1<`).MatchString(page) {
		t.Fatalf("%s", page)
	}
}

func TestNotFoundPageForSignedInAdmin(t *testing.T) {
	e := newEnv(t)
	ck, _ := e.admin1()
	rr := e.do("GET", "/admin/does/not/exist", withCookie(ck))
	if rr.Code != 404 || !strings.Contains(rr.Body.String(), "Page not found") || !strings.Contains(rr.Body.String(), "Back to dashboard") {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
}

// ---------- static files ----------

func TestStaticFiles(t *testing.T) {
	e := newEnv(t)
	for path, ctype := range map[string]string{
		"/admin/static/css/admin-theme.css":                  "text/css",
		"/admin/static/js/htmx.min.js":                       "javascript",
		"/admin/static/webfonts/fa-solid-900.woff2":          "font/woff2",
		"/admin/static/fonts/poppins-latin-400-normal.woff2": "font/woff2",
	} {
		rr := e.do("GET", path+"?v=abc") // no login needed: the login page uses them too
		if rr.Code != 200 || !strings.Contains(rr.Header().Get("Content-Type"), ctype) || rr.Header().Get("X-Content-Type-Options") != "nosniff" ||
			!strings.Contains(rr.Header().Get("Cache-Control"), "immutable") || rr.Body.Len() == 0 {
			t.Errorf("%s: %d %v", path, rr.Code, rr.Header())
		}
	}
	if cc := e.do("GET", "/admin/static/js/admin.js").Header().Get("Cache-Control"); cc != "public, max-age=3600" {
		t.Fatalf("unversioned URLs must not be cached for a year: %q", cc)
	}
	for _, p := range []string{"/admin/static/", "/admin/static/css/", "/admin/static/nope.css", "/admin/static/../../etc/passwd", "/admin/static/%2e%2e/admin.go"} {
		if rr := e.do("GET", p); rr.Code == 200 {
			t.Errorf("%s must not be served (got %d)", p, rr.Code)
		}
	}
	if rr := e.do("POST", "/admin/static/js/admin.js"); rr.Code == 200 {
		t.Fatal("static files are GET-only")
	}

	// every asset a rendered page refers to must exist
	ck, _ := e.admin1()
	for _, page := range []string{e.do("GET", "/admin/login").Body.String(), e.do("GET", "/admin/", withCookie(ck)).Body.String()} {
		refs := regexp.MustCompile(`(?:href|src)="(/admin/static/[^"]+)"`).FindAllStringSubmatch(page, -1)
		if len(refs) < 4 {
			t.Fatalf("expected the page to reference its assets, found %d", len(refs))
		}
		for _, m := range refs {
			if rr := e.do("GET", strings.ReplaceAll(m[1], "&amp;", "&")); rr.Code != 200 {
				t.Errorf("%s -> %d", m[1], rr.Code)
			}
		}
	}
	// the version in the URL changes with the content
	if e.a.ver["css/admin-theme.css"] == "" || e.a.ver["css/admin-theme.css"] == e.a.ver["css/fa.css"] {
		t.Fatal("content hashes")
	}
}

func TestAdminRoutesNeverGetCORSHeaders(t *testing.T) {
	e := newEnv(t)
	rr := e.do("GET", "/admin/login", withHeader("Origin", "https://evil.example"))
	if rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("the admin panel is not a cross-origin API")
	}
	_ = httpx.ClientIP
}

func TestBrokenLinksPageActionsAndCSRF(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ck, csrf := e.admin1()
	m, err := e.r.CreateMovie(ctx, repo.MovieInput{Title: "Broken Sample", ContentType: "series"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.r.ReportBrokenLink(ctx, repo.BrokenLinkInput{MovieID: m.ID, EpInfo: "Season 1 · Episode 2", ServerType: "Server 1"}); err != nil {
		t.Fatal(err)
	}

	page := e.do("GET", "/admin/broken-links?type=series&status=open", withCookie(ck))
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Broken Links Reports") || !strings.Contains(page.Body.String(), "Broken Sample") {
		t.Fatalf("broken links page: %d %s", page.Code, page.Body.String())
	}
	fragment := e.do("GET", "/admin/broken-links?status=open", withCookie(ck), withHeader("HX-Request", "true"))
	if fragment.Code != 200 || !strings.Contains(fragment.Body.String(), `id="broken-table-body"`) || !strings.Contains(fragment.Body.String(), `name="csrf"`) {
		t.Fatalf("htmx fragment must include CSRF fields: %d %s", fragment.Code, fragment.Body.String())
	}

	denied := e.do("POST", "/admin/broken-links/"+findBrokenID(t, e, m.ID)+"/resolve", withCookie(ck))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF should be denied: %d", denied.Code)
	}

	id := findBrokenID(t, e, m.ID)
	resolved := e.do("POST", "/admin/broken-links/"+id+"/resolve", withCookie(ck), withHeader("X-CSRF-Token", csrf), withHeader("HX-Request", "true"))
	if resolved.Code != http.StatusOK || resolved.Header().Get("HX-Redirect") != "/admin/broken-links" {
		t.Fatalf("resolve action: %d %v %s", resolved.Code, resolved.Header(), resolved.Body.String())
	}
	audit, err := e.r.RecentAudit(ctx, 5)
	if err != nil || len(audit) == 0 || audit[0].Action != "broken-link.resolve" {
		t.Fatalf("resolve audit missing: %+v %v", audit, err)
	}

	deleted := e.do("POST", "/admin/broken-links/"+id+"/delete", withCookie(ck), withHeader("X-CSRF-Token", csrf))
	if deleted.Code != http.StatusSeeOther {
		t.Fatalf("delete action: %d %s", deleted.Code, deleted.Body.String())
	}
	if _, total, err := e.r.ListBrokenLinks(ctx, repo.BrokenListOpts{}); err != nil || total != 0 {
		t.Fatalf("report should be deleted: total=%d err=%v", total, err)
	}
}

func findBrokenID(t *testing.T, e *env, movieID string) string {
	t.Helper()
	items, _, err := e.r.ListBrokenLinks(context.Background(), repo.BrokenListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.MovieID == movieID {
			return item.ID
		}
	}
	t.Fatal("broken link report not found")
	return ""
}

func TestSettingsSocialsAndNotificationsAdmin(t *testing.T) {
	e := newEnv(t)
	ck, csrf := e.admin1()
	page := e.do("GET", "/admin/settings", withCookie(ck))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Social links") || !strings.Contains(page.Body.String(), "Notifications") {
		t.Fatalf("settings page: %d %s", page.Code, page.Body.String())
	}
	if strings.Contains(page.Body.String(), "Server Settings") {
		t.Fatal("legacy server settings must remain outside the public settings page")
	}

	settings := url.Values{
		"csrf": {csrf}, "appName": {"MoveX Plus"}, "appLogo": {"https://example.com/logo.png"},
		"themeMode": {"auto"}, "themeColor": {"#123abc"}, "themeInterval": {"300"}, "pwdResetLimit": {"5"},
	}
	if rr := e.do("POST", "/admin/settings", withCookie(ck), withForm(settings)); rr.Code != http.StatusSeeOther {
		t.Fatalf("save settings: %d %s", rr.Code, rr.Body.String())
	}
	got, err := e.r.GetSettings(context.Background())
	if err != nil || got["appName"] != "MoveX Plus" || got["themeMode"] != "auto" || got["pwdResetLimit"] != "5" {
		t.Fatalf("stored settings: %#v, %v", got, err)
	}
	pub, err := e.r.PublicSettings(context.Background())
	if err != nil || pub["pwdResetLimit"] != "" || pub["appName"] != "MoveX Plus" {
		t.Fatalf("public settings leaked server-only value: %#v, %v", pub, err)
	}

	addSocial := url.Values{"csrf": {csrf}, "name": {"Telegram"}, "url": {"https://t.me/movex"}, "icon": {"fab fa-telegram"}}
	if rr := e.do("POST", "/admin/settings/socials", withCookie(ck), withForm(addSocial)); rr.Code != http.StatusSeeOther {
		t.Fatalf("create social: %d %s", rr.Code, rr.Body.String())
	}
	socials, err := e.r.ListSocialLinks(context.Background())
	if err != nil || len(socials) != 1 || socials[0].Name != "Telegram" {
		t.Fatalf("social links: %#v, %v", socials, err)
	}
	socialID := socials[0].ID
	editSocial := url.Values{"csrf": {csrf}, "name": {"Community"}, "url": {"https://t.me/movex"}, "icon": {"fab fa-telegram"}}
	if rr := e.do("POST", "/admin/settings/socials/"+socialID, withCookie(ck), withForm(editSocial)); rr.Code != http.StatusSeeOther {
		t.Fatalf("update social: %d %s", rr.Code, rr.Body.String())
	}
	if s, _ := e.r.GetSocialLink(context.Background(), socialID); s.Name != "Community" {
		t.Fatalf("social update did not persist: %+v", s)
	}
	if rr := e.do("POST", "/admin/settings/socials/"+socialID+"/delete", withCookie(ck), withForm(url.Values{"csrf": {csrf}})); rr.Code != http.StatusSeeOther {
		t.Fatalf("delete social: %d %s", rr.Code, rr.Body.String())
	}

	addNotification := url.Values{"csrf": {csrf}, "title": {"Maintenance"}, "message": {"The app will be updated."}}
	if rr := e.do("POST", "/admin/settings/notifications", withCookie(ck), withForm(addNotification)); rr.Code != http.StatusSeeOther {
		t.Fatalf("create notification: %d %s", rr.Code, rr.Body.String())
	}
	notifications, err := e.r.ListNotifications(context.Background(), 10)
	if err != nil || len(notifications) != 1 || notifications[0].Title != "Maintenance" {
		t.Fatalf("notifications: %#v, %v", notifications, err)
	}
	notificationID := notifications[0].ID
	editNotification := url.Values{"csrf": {csrf}, "title": {"Update"}, "message": {"Update completed."}}
	if rr := e.do("POST", "/admin/settings/notifications/"+notificationID, withCookie(ck), withForm(editNotification)); rr.Code != http.StatusSeeOther {
		t.Fatalf("update notification: %d %s", rr.Code, rr.Body.String())
	}
	if n, _ := e.r.GetNotification(context.Background(), notificationID); n.Title != "Update" {
		t.Fatalf("notification update did not persist: %+v", n)
	}
	if rr := e.do("POST", "/admin/settings/notifications/"+notificationID+"/delete", withCookie(ck), withForm(url.Values{"csrf": {csrf}})); rr.Code != http.StatusSeeOther {
		t.Fatalf("delete notification: %d %s", rr.Code, rr.Body.String())
	}
	if list, _ := e.r.ListNotifications(context.Background(), 10); len(list) != 0 {
		t.Fatalf("notification still exists: %+v", list)
	}
}

func TestServerPageShowsOnlySecretFreeSummary(t *testing.T) {
	e := newEnv(t)
	ck, _ := e.admin1()
	rr := e.do("GET", "/admin/server", withCookie(ck))
	body := rr.Body.String()
	if rr.Code != 200 || !strings.Contains(body, "Environment configuration") || !strings.Contains(body, "FIREBASE_API_KEY") {
		t.Fatalf("server page not rendered: %d %s", rr.Code, body)
	}
	if strings.Contains(body, testAPIKey) || strings.Contains(body, "0123456789abcdef0123456789abcdef") {
		t.Fatal("server page leaked a secret")
	}
	if !strings.Contains(body, "Telegram connection") || !strings.Contains(body, "TG_API_ID") {
		t.Fatal("server page must explain how to configure Telegram MTProto")
	}
}

func TestLocalBackupActionRequiresCSRFAndReportsUnavailable(t *testing.T) {
	e := newEnv(t)
	ck, csrf := e.admin1()
	if rr := e.do("POST", "/admin/server/backup", withCookie(ck)); rr.Code != http.StatusForbidden {
		t.Fatalf("backup without CSRF: %d %s", rr.Code, rr.Body.String())
	}
	rr := e.do("POST", "/admin/server/backup", withCookie(ck), withForm(url.Values{"csrf": {csrf}}))
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/admin/server" {
		t.Fatalf("unavailable backup action: %d %s", rr.Code, rr.Body.String())
	}
}

func TestLocalBackupDownloadRequiresAdminAndRejectsUnsafeNames(t *testing.T) {
	e := newEnv(t)
	backupDir := filepath.Join(e.cfg.DataDir, "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		t.Fatal(err)
	}
	name := "movex-20261107-012500.db.gz"
	content := []byte("compressed snapshot test")
	if err := os.WriteFile(filepath.Join(backupDir, name), content, 0o600); err != nil {
		t.Fatal(err)
	}
	if rr := e.do("GET", "/admin/server/backups/"+name); rr.Code != http.StatusSeeOther {
		t.Fatalf("anonymous download status = %d", rr.Code)
	}
	cookie, _ := e.admin1()
	got := e.do("GET", "/admin/server/backups/"+name, withCookie(cookie))
	if got.Code != http.StatusOK || !bytes.Equal(got.Body.Bytes(), content) {
		t.Fatalf("download status/body = %d %q", got.Code, got.Body.String())
	}
	if got.Header().Get("Content-Type") != "application/gzip" || !strings.Contains(got.Header().Get("Content-Disposition"), name) {
		t.Fatalf("download headers missing: %#v", got.Header())
	}
	for _, unsafe := range []string{"..%2Fsecret.db.gz", "not-a-backup.db.gz", "movex-20261107-012500.db.gz/extra"} {
		if rr := e.do("GET", "/admin/server/backups/"+unsafe, withCookie(cookie)); rr.Code != http.StatusNotFound {
			t.Errorf("unsafe name %q status = %d", unsafe, rr.Code)
		}
	}
}

func TestLocalBackupDownloadRejectsSymlink(t *testing.T) {
	e := newEnv(t)
	backupDir := filepath.Join(e.cfg.DataDir, "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(e.cfg.DataDir, "outside.db.gz")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	name := "movex-20261107-012501.db.gz"
	if err := os.Symlink(outside, filepath.Join(backupDir, name)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	cookie, _ := e.admin1()
	if rr := e.do("GET", "/admin/server/backups/"+name, withCookie(cookie)); rr.Code != http.StatusNotFound {
		t.Fatalf("symlink download status = %d", rr.Code)
	}
}
