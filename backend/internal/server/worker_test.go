package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"movex/internal/config"
)

func TestWorkerProxyForwardsAuthPathQueryAndClientIP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/me/list" || r.URL.Query().Get("page") != "2" {
			t.Errorf("unexpected target: %s", r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer example-token" {
			t.Errorf("authorization not forwarded")
		}
		if r.Header.Get("X-Forwarded-For") != "198.51.100.8" {
			t.Errorf("bad forwarded ip: %q", r.Header.Get("X-Forwarded-For"))
		}
		if r.Header.Get("X-Worker-Key") != "" {
			t.Errorf("worker key leaked")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		io.WriteString(w, `{"proxied":true}`)
	}))
	defer upstream.Close()
	cfg := &config.Config{Role: config.RoleWorker, MainURL: upstream.URL, AllowedOrigins: []string{"*"}}
	s := New(Deps{Cfg: cfg})
	req := httptest.NewRequest("GET", "/api/v1/me/list?page=2", nil)
	req.RemoteAddr = "198.51.100.8:1234"
	req.Header.Set("Authorization", "Bearer example-token")
	req.Header.Set("X-Worker-Key", "never-forward-this")
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted || !strings.Contains(rr.Body.String(), `"proxied":true`) {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
}

func TestWorkerProxyReturns503WhenMainIsUnavailable(t *testing.T) {
	cfg := &config.Config{Role: config.RoleWorker, MainURL: "http://127.0.0.1:1", AllowedOrigins: []string{"*"}}
	s := New(Deps{Cfg: cfg})
	req := httptest.NewRequest("POST", "/api/v1/me/list/abc", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d: %s", rr.Code, rr.Body.String())
	}
}
