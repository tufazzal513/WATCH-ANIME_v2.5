package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"movex/internal/auth"
	"movex/internal/config"
	"movex/internal/httpx"
)

const origin = "https://app.example.com"

func testCfg() *config.Config {
	return &config.Config{
		Role: config.RoleMain, Port: 0, AllowedOrigins: []string{origin},
		AdminUIDs: []string{"admin1"}, DevInsecureAuth: true, FirebaseProjectID: "p",
		SessionSecret: []byte("0123456789abcdef0123456789abcdef"), CookieSecure: true,
	}
}

func testServer(t *testing.T, seq func() int64) *Server {
	t.Helper()
	return newTestServer(t, Deps{Cfg: testCfg(), Seq: seq,
		Verifier: auth.NewVerifier(auth.Options{ProjectID: "p", DevInsecure: true, JWKSURL: "http://127.0.0.1:1/jwks"})})
}

func newTestServer(t *testing.T, d Deps) *Server {
	t.Helper()
	if d.Log == nil {
		d.Log = slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	}
	s := New(d)
	s.Private("GET /api/v1/test/private", func(w http.ResponseWriter, r *http.Request, p *Principal) {
		httpx.WriteJSON(w, 200, map[string]any{"uid": p.UID, "admin": p.IsAdmin, "status": p.Status})
	})
	s.AdminAPI("GET /api/v1/test/admin", func(w http.ResponseWriter, r *http.Request, p *Principal) {
		httpx.WriteJSON(w, 200, map[string]any{"uid": p.UID})
	})
	s.Public("POST /api/v1/test/anon", func(w http.ResponseWriter, r *http.Request) {
		if s.AnonWriteAllowed(w, r) {
			w.WriteHeader(204)
		}
	})
	return s
}

func do(s *Server, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = "192.0.2.10:5555"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	return rr
}

func errCode(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var e httpx.APIError
	if err := json.Unmarshal(rr.Body.Bytes(), &e); err != nil {
		t.Fatalf("not JSON: %q", rr.Body.String())
	}
	return e.Code
}

func asUser(uid string) map[string]string {
	return map[string]string{"Authorization": "Bearer dev:" + uid}
}

func TestHealth(t *testing.T) {
	s := testServer(t, func() int64 { return 42 })
	rr := do(s, "GET", "/api/v1/health", nil)
	var out map[string]any
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &out) != nil || out["ok"] != true || out["role"] != "main" || out["seq"].(float64) != 42 {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Cache-Control") != "no-store" || rr.Header().Get("X-Content-Type-Options") != "nosniff" || rr.Header().Get("X-Request-Id") == "" {
		t.Fatalf("%v", rr.Header())
	}
}

func TestAPINotFoundAndMethodNotAllowedAreJSON(t *testing.T) {
	s := testServer(t, nil)
	rr := do(s, "GET", "/api/v1/nope", nil)
	if rr.Code != 404 || errCode(t, rr) != "not_found" {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	rr = do(s, "POST", "/api/v1/health", nil)
	if rr.Code != 405 || errCode(t, rr) != "method_not_allowed" || rr.Header().Get("Allow") != "GET" {
		t.Fatalf("%d %v %s", rr.Code, rr.Header(), rr.Body.String())
	}
	rr = do(s, "DELETE", "/api/v1/test/private", nil)
	if rr.Code != 405 || rr.Header().Get("Allow") != "GET" {
		t.Fatalf("%d %v", rr.Code, rr.Header())
	}
}

func TestCORSOnAPI(t *testing.T) {
	s := testServer(t, nil)
	rr := do(s, "GET", "/api/v1/health", map[string]string{"Origin": origin})
	if rr.Header().Get("Access-Control-Allow-Origin") != origin {
		t.Fatalf("%v", rr.Header())
	}
	// 404s and auth failures must carry CORS headers too, or browsers hide the real error
	if rr = do(s, "GET", "/api/v1/nope", map[string]string{"Origin": origin}); rr.Header().Get("Access-Control-Allow-Origin") != origin {
		t.Fatal("CORS on 404")
	}
	if rr = do(s, "GET", "/api/v1/test/private", map[string]string{"Origin": origin}); rr.Code != 401 || rr.Header().Get("Access-Control-Allow-Origin") != origin {
		t.Fatal("CORS on 401")
	}
	// preflight to a real route and to an unknown one: answered, never routed to the 404/405 handler
	for _, path := range []string{"/api/v1/test/private", "/api/v1/whatever"} {
		rr = do(s, "OPTIONS", path, map[string]string{"Origin": origin, "Access-Control-Request-Method": "GET", "Access-Control-Request-Headers": "authorization"})
		if rr.Code != 204 || !strings.Contains(rr.Header().Get("Access-Control-Allow-Headers"), "Authorization") || rr.Header().Get("Access-Control-Allow-Origin") != origin {
			t.Fatalf("preflight %s: %d %v", path, rr.Code, rr.Header())
		}
	}
	if rr = do(s, "GET", "/api/v1/health", map[string]string{"Origin": "https://evil.example.com"}); rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("unlisted origin")
	}
}

func TestAuthRequired(t *testing.T) {
	s := testServer(t, nil)
	for name, hdr := range map[string]map[string]string{
		"no header":      nil,
		"wrong scheme":   {"Authorization": "Basic dXNlcjpwYXNz"},
		"empty bearer":   {"Authorization": "Bearer "},
		"bad token":      {"Authorization": "Bearer not.a.token"},
		"dev bad uid":    {"Authorization": "Bearer dev:a b"},
		"token in query": nil,
	} {
		path := "/api/v1/test/private"
		if name == "token in query" {
			path += "?access_token=dev:user1" // tokens in URLs must never work
		}
		rr := do(s, "GET", path, hdr)
		if rr.Code != 401 || errCode(t, rr) != "unauthorized" || rr.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%s: %d %s", name, rr.Code, rr.Body.String())
		}
	}
	rr := do(s, "GET", "/api/v1/test/private", asUser("user1"))
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if rr.Code != 200 || out["uid"] != "user1" || out["admin"] != false {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	if rr = do(s, "GET", "/api/v1/test/private", map[string]string{"Authorization": "bearer dev:user1"}); rr.Code != 200 {
		t.Fatal("scheme is case-insensitive")
	}
}

func TestDevTokensRefusedWhenNotEnabled(t *testing.T) {
	cfg := testCfg()
	cfg.DevInsecureAuth = false
	s := newTestServer(t, Deps{Cfg: cfg, Verifier: auth.NewVerifier(auth.Options{ProjectID: "p", JWKSURL: "http://127.0.0.1:1/jwks"})})
	if rr := do(s, "GET", "/api/v1/test/private", asUser("admin1")); rr.Code != 401 {
		t.Fatalf("dev token must not authenticate in production mode: %d", rr.Code)
	}
}

func TestAdminOnly(t *testing.T) {
	s := testServer(t, nil)
	if rr := do(s, "GET", "/api/v1/test/admin", nil); rr.Code != 401 {
		t.Fatal("no token")
	}
	if rr := do(s, "GET", "/api/v1/test/admin", asUser("user1")); rr.Code != 403 || errCode(t, rr) != "forbidden" {
		t.Fatalf("ordinary user: %d", rr.Code)
	}
	if rr := do(s, "GET", "/api/v1/test/admin", asUser("admin1")); rr.Code != 200 {
		t.Fatalf("admin: %d", rr.Code)
	}
}

func TestAuthUnavailableWhenKeysCannotBeLoaded(t *testing.T) {
	s := testServer(t, nil)
	// a structurally valid RS256 token: header {"alg":"RS256","kid":"k"}; the JWKS URL is unreachable
	tok := "eyJhbGciOiJSUzI1NiIsImtpZCI6ImsifQ.eyJzdWIiOiJ4In0.c2ln"
	rr := do(s, "GET", "/api/v1/test/private", map[string]string{"Authorization": "Bearer " + tok})
	if rr.Code != 503 || errCode(t, rr) != "auth_unavailable" || rr.Header().Get("Retry-After") == "" {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
}

func TestPerUserAndPerIPRateLimits(t *testing.T) {
	s := testServer(t, nil)
	s.uidLimit = httpx.NewLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if rr := do(s, "GET", "/api/v1/test/private", asUser("user1")); rr.Code != 200 {
			t.Fatalf("request %d: %d", i, rr.Code)
		}
	}
	rr := do(s, "GET", "/api/v1/test/private", asUser("user1"))
	if rr.Code != 429 || rr.Header().Get("Retry-After") == "" || errCode(t, rr) != "rate_limited" {
		t.Fatalf("%d %v", rr.Code, rr.Header())
	}
	if rr = do(s, "GET", "/api/v1/test/private", asUser("user2")); rr.Code != 200 {
		t.Fatal("limits are per user")
	}

	// per-IP limit: routes capture the limiter when they are registered, so set it before registering
	s2 := New(Deps{Cfg: testCfg(), Log: s.Log()})
	s2.mux = http.NewServeMux()
	s2.ipLimit = httpx.NewLimiter(2, time.Minute)
	s2.Public("GET /api/v1/x", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	s2.handler = s2.mux
	for i, want := range []int{200, 200, 429} {
		if rr := do(s2, "GET", "/api/v1/x", map[string]string{"Origin": origin}); rr.Code != want {
			t.Fatalf("request %d: got %d want %d", i, rr.Code, want)
		} else if want == 429 && rr.Header().Get("Access-Control-Allow-Origin") != origin {
			t.Fatal("429 must carry CORS headers so the browser can read it")
		}
	}
}

func TestAnonymousWritesAreLimited(t *testing.T) {
	s := testServer(t, nil)
	s.anonLimit = httpx.NewLimiter(2, time.Minute)
	got := []int{}
	for i := 0; i < 3; i++ {
		got = append(got, do(s, "POST", "/api/v1/test/anon", nil).Code)
	}
	if got[0] != 204 || got[1] != 204 || got[2] != 429 {
		t.Fatalf("%v", got)
	}
}

func TestAccessLogHasUIDButNoToken(t *testing.T) {
	var logs bytes.Buffer
	s := newTestServer(t, Deps{Cfg: testCfg(), Log: slog.New(slog.NewJSONHandler(&logs, nil)),
		Verifier: auth.NewVerifier(auth.Options{ProjectID: "p", DevInsecure: true})})
	do(s, "GET", "/api/v1/test/private?term=secret", asUser("user7"))
	out := logs.String()
	if !strings.Contains(out, `"uid":"user7"`) || strings.Contains(out, "dev:user7") || strings.Contains(out, "secret") {
		t.Fatalf("%s", out)
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	s := testServer(t, nil)
	s.http.Addr = "127.0.0.1:0"
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down")
	}
}
