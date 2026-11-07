package httpx

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func get(h http.Handler, method, target string, hdr map[string]string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, body)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestWriteJSONAndError(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteError(rr, 403, "forbidden", "no")
	var e APIError
	if err := json.Unmarshal(rr.Body.Bytes(), &e); err != nil || e.Code != "forbidden" || rr.Code != 403 || rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%v %+v %v", err, e, rr.Header())
	}
}

func TestETagAndNotModified(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { WriteJSONETag(w, r, map[string]int{"a": 1}) })
	first := get(h, "GET", "/", nil, nil)
	etag := first.Header().Get("ETag")
	if first.Code != 200 || !strings.HasPrefix(etag, `W/"`) || first.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("%d %v", first.Code, first.Header())
	}
	for _, inm := range []string{etag, strings.TrimPrefix(etag, "W/"), `"zzz", ` + etag, "*"} {
		if rr := get(h, "GET", "/", map[string]string{"If-None-Match": inm}, nil); rr.Code != 304 || rr.Body.Len() != 0 || rr.Header().Get("ETag") != etag {
			t.Errorf("If-None-Match %q must give 304, got %d", inm, rr.Code)
		}
	}
	if rr := get(h, "GET", "/", map[string]string{"If-None-Match": `W/"other"`}, nil); rr.Code != 200 {
		t.Fatal("different etag must give 200")
	}
	if rr := get(h, "HEAD", "/", nil, nil); rr.Code != 200 || rr.Body.Len() != 0 {
		t.Fatal("HEAD has no body")
	}
}

func TestDecodeJSON(t *testing.T) {
	type in struct{ A int }
	run := func(ct, body string, max int64) (int, in) {
		var v in
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		if !DecodeJSON(rr, req, &v, max) {
			return rr.Code, v
		}
		return 200, v
	}
	if c, v := run("application/json; charset=utf-8", `{"a":7}`, 100); c != 200 || v.A != 7 {
		t.Fatal("valid")
	}
	if c, _ := run("", `{"a":7}`, 100); c != 200 {
		t.Fatal("missing content-type is tolerated")
	}
	if c, _ := run("text/plain", `{"a":7}`, 100); c != 415 {
		t.Fatalf("content type: %d", c)
	}
	if c, _ := run("application/json", `{oops`, 100); c != 400 {
		t.Fatalf("bad json: %d", c)
	}
	if c, _ := run("application/json", `{"a":1} {"a":2}`, 100); c != 400 {
		t.Fatalf("trailing value: %d", c)
	}
	if c, _ := run("application/json", `{"a":"`+strings.Repeat("x", 200)+`"}`, 50); c != 413 {
		t.Fatalf("too large: %d", c)
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:4321"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9")
	if ClientIP(r, false) != "10.0.0.5" {
		t.Fatal("without TRUST_PROXY the header is ignored")
	}
	if got := ClientIP(r, true); got != "203.0.113.9" {
		t.Fatalf("right-most entry is the one our proxy appended; a forged left entry must not win: %s", got)
	}
	r.Header.Set("X-Forwarded-For", "not-an-ip")
	if ClientIP(r, true) != "10.0.0.5" {
		t.Fatal("garbage header falls back to the socket address")
	}
	r.RemoteAddr = "[2001:db8::1]:99"
	r.Header.Del("X-Forwarded-For")
	if ClientIP(r, true) != "2001:db8::1" {
		t.Fatal("ipv6")
	}
}

func TestCORS(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	list := CORS([]string{"https://app.example.com"})(ok)

	rr := get(list, "GET", "/", map[string]string{"Origin": "https://app.example.com"}, nil)
	if rr.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" || !strings.Contains(rr.Header().Get("Vary"), "Origin") || rr.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("%v", rr.Header())
	}
	if rr = get(list, "GET", "/", map[string]string{"Origin": "https://evil.example.com"}, nil); rr.Header().Get("Access-Control-Allow-Origin") != "" || rr.Code != 200 {
		t.Fatal("unlisted origin gets no CORS headers")
	}
	if rr = get(list, "GET", "/", nil, nil); rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("no Origin, no headers")
	}
	// preflight
	pre := map[string]string{"Origin": "https://app.example.com", "Access-Control-Request-Method": "PUT"}
	rr = get(list, "OPTIONS", "/", pre, nil)
	if rr.Code != 204 || !strings.Contains(rr.Header().Get("Access-Control-Allow-Headers"), "Authorization") || !strings.Contains(rr.Header().Get("Access-Control-Allow-Methods"), "PUT") || rr.Header().Get("Access-Control-Max-Age") != "86400" {
		t.Fatalf("%d %v", rr.Code, rr.Header())
	}
	pre["Origin"] = "https://evil.example.com"
	if rr = get(list, "OPTIONS", "/", pre, nil); rr.Code != 204 || rr.Header().Get("Access-Control-Allow-Origin") != "" || rr.Header().Get("Access-Control-Allow-Methods") != "" {
		t.Fatal("preflight from an unlisted origin is answered without permission headers")
	}
	// wildcard
	rr = get(CORS([]string{"*"})(ok), "GET", "/", map[string]string{"Origin": "https://anything.example.com"}, nil)
	if rr.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("wildcard")
	}
}

func gunzip(t *testing.T, b []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestGzip(t *testing.T) {
	big := strings.Repeat("0123456789", 300) // 3000 bytes
	mk := func(ct, body string, status int) http.Handler {
		return Gzip()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ct != "" {
				w.Header().Set("Content-Type", ct)
			}
			w.Header().Set("Content-Length", "999999") // a wrong length must never reach the client when compressing
			if status != 0 {
				w.WriteHeader(status)
			}
			// write in several chunks to exercise the buffering
			for i := 0; i < len(body); i += 700 {
				_, _ = io.WriteString(w, body[i:min(i+700, len(body))])
			}
		}))
	}
	ae := map[string]string{"Accept-Encoding": "gzip, deflate"}

	rr := get(mk("application/json", big, 0), "GET", "/", ae, nil)
	if rr.Header().Get("Content-Encoding") != "gzip" || rr.Header().Get("Content-Length") != "" || !strings.Contains(rr.Header().Get("Vary"), "Accept-Encoding") || gunzip(t, rr.Body.Bytes()) != big {
		t.Fatalf("large JSON must be gzipped: %v", rr.Header())
	}
	rr = get(mk("application/json", "small", 0), "GET", "/", ae, nil)
	if rr.Header().Get("Content-Encoding") != "" || rr.Body.String() != "small" || !strings.Contains(rr.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("small bodies stay plain: %v %q", rr.Header(), rr.Body.String())
	}
	if rr = get(mk("application/json", big, 0), "GET", "/", nil, nil); rr.Header().Get("Content-Encoding") != "" || rr.Body.String() != big {
		t.Fatal("no Accept-Encoding, no gzip")
	}
	if rr = get(mk("image/png", big, 0), "GET", "/", ae, nil); rr.Header().Get("Content-Encoding") != "" || rr.Body.String() != big {
		t.Fatal("binary types are not compressed")
	}
	if rr = get(mk("application/json", big, 0), "HEAD", "/", ae, nil); rr.Header().Get("Content-Encoding") != "" {
		t.Fatal("HEAD")
	}
	rr = get(mk("application/json", "", http.StatusNotModified), "GET", "/", ae, nil)
	if rr.Code != 304 || rr.Header().Get("Content-Encoding") != "" {
		t.Fatalf("304: %d %v", rr.Code, rr.Header())
	}
	rr = get(mk("application/json", big, http.StatusCreated), "GET", "/", ae, nil)
	if rr.Code != 201 || rr.Header().Get("Content-Encoding") != "gzip" || gunzip(t, rr.Body.Bytes()) != big {
		t.Fatalf("status must be kept: %d", rr.Code)
	}
	// a handler that already set Content-Encoding is left alone
	pre := Gzip()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "br")
		_, _ = io.WriteString(w, big)
	}))
	if rr = get(pre, "GET", "/", ae, nil); rr.Header().Get("Content-Encoding") != "br" || rr.Body.String() != big {
		t.Fatal("no double encoding")
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(3, time.Minute)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("request %d should pass", i)
		}
	}
	ok, wait := l.Allow("a")
	if ok || wait < 19*time.Second || wait > 21*time.Second { // 3 per minute = 1 token / 20 s
		t.Fatalf("4th must be refused with a ~20 s wait: %v %v", ok, wait)
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("keys are independent")
	}
	now = now.Add(21 * time.Second)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("a token is back after 20 s")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("only one token came back")
	}
	now = now.Add(time.Hour) // refill is capped at max
	n := 0
	for ok, _ := l.Allow("a"); ok && n < 10; ok, _ = l.Allow("a") {
		n++
	}
	if n != 3 {
		t.Fatalf("burst after a long idle is the max (3), got %d", n)
	}
}

func TestLimiterCleanup(t *testing.T) {
	l := NewLimiter(5, time.Second)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	for i := 0; i < 500; i++ {
		l.Allow(strings.Repeat("k", 1) + string(rune('a'+i%26)) + string(rune('A'+i/26)))
	}
	now = now.Add(time.Minute)
	for i := 0; i < 600; i++ { // crossing the 1000-call mark prunes idle (full) buckets
		l.Allow("hot")
	}
	if len(l.buckets) > 5 {
		t.Fatalf("idle buckets must be dropped, still have %d", len(l.buckets))
	}
}

func TestRateLimitIPMiddleware(t *testing.T) {
	l := NewLimiter(2, time.Minute)
	h := RateLimitIP(l, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	for i := 0; i < 2; i++ {
		if rr := get(h, "GET", "/", nil, nil); rr.Code != 200 {
			t.Fatal("within limit")
		}
	}
	rr := get(h, "GET", "/", nil, nil)
	if rr.Code != 429 || rr.Header().Get("Retry-After") == "" {
		t.Fatalf("%d %v", rr.Code, rr.Header())
	}
	var e APIError
	_ = json.Unmarshal(rr.Body.Bytes(), &e)
	if e.Code != "rate_limited" {
		t.Fatal("error code")
	}
	if rr = get(h, "OPTIONS", "/", nil, nil); rr.Code != 200 {
		t.Fatal("preflights are not rate limited")
	}
}

func TestRecoverAndAccessLog(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetUID(r, "user-42")
		if r.URL.Path == "/boom" {
			panic("kaboom")
		}
		w.WriteHeader(201)
		_, _ = io.WriteString(w, "hello")
	}), AccessLog(log, false), Recover(log))

	rr := get(h, "GET", "/ok?q=secret-search-term", map[string]string{"Authorization": "Bearer SECRET-TOKEN", "X-Request-Id": "abc-123"}, nil)
	if rr.Code != 201 || rr.Header().Get("X-Request-Id") != "abc-123" {
		t.Fatalf("%d %v", rr.Code, rr.Header())
	}
	out := logs.String()
	for _, secret := range []string{"secret-search-term", "SECRET-TOKEN"} {
		if strings.Contains(out, secret) {
			t.Fatalf("log must not contain %q: %s", secret, out)
		}
	}
	if !strings.Contains(out, `"uid":"user-42"`) || !strings.Contains(out, `"status":201`) || !strings.Contains(out, `"bytes":5`) || !strings.Contains(out, `"path":"/ok"`) {
		t.Fatalf("log line: %s", out)
	}

	logs.Reset()
	rr = get(h, "GET", "/boom", map[string]string{"X-Request-Id": "bad id with spaces"}, nil)
	var e APIError
	_ = json.Unmarshal(rr.Body.Bytes(), &e)
	if rr.Code != 500 || e.Code != "internal" || strings.Contains(rr.Body.String(), "kaboom") {
		t.Fatalf("panic must become a generic 500: %d %s", rr.Code, rr.Body.String())
	}
	if id := rr.Header().Get("X-Request-Id"); id == "bad id with spaces" || len(id) != 16 {
		t.Fatalf("invalid inbound request ids are replaced: %q", id)
	}
	if !strings.Contains(logs.String(), "kaboom") || !strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Fatalf("panic must be logged with its stack: %s", logs.String())
	}
}

func TestLimitBodyAndHeaders(t *testing.T) {
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			WriteError(w, 413, "too_large", "")
			return
		}
		w.WriteHeader(204)
	}), APISecurityHeaders(), LimitBody(10))
	if rr := get(h, "POST", "/", nil, strings.NewReader("short")); rr.Code != 204 || rr.Header().Get("X-Content-Type-Options") != "nosniff" || rr.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("%d %v", rr.Code, rr.Header())
	}
	if rr := get(h, "POST", "/", nil, strings.NewReader(strings.Repeat("x", 11))); rr.Code != 413 {
		t.Fatalf("body over the limit: %d", rr.Code)
	}
}
