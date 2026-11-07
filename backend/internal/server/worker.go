package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"movex/internal/httpx"
)

// proxyWorkerAPI forwards worker API requests that are not safely served from the local catalog.
// The worker never forwards hop-by-hop headers and never lets a caller override the upstream host.
func (s *Server) proxyWorkerAPI(w http.ResponseWriter, r *http.Request) {
	if !s.deps.Cfg.IsWorker() || s.deps.Cfg.MainURL == "" {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "")
		return
	}
	target, err := url.Parse(s.deps.Cfg.MainURL)
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, "main_unavailable", "main server URL is invalid")
		return
	}
	target.Path = strings.TrimRight(target.Path, "/") + r.URL.Path
	target.RawQuery = r.URL.RawQuery
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	out, err := http.NewRequestWithContext(ctx, r.Method, target.String(), r.Body)
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, "proxy_failed", "could not prepare upstream request")
		return
	}
	for k, vv := range r.Header {
		lk := strings.ToLower(k)
		if lk == "connection" || lk == "keep-alive" || lk == "proxy-authenticate" || lk == "proxy-authorization" || lk == "te" || lk == "trailer" || lk == "transfer-encoding" || lk == "upgrade" || lk == "host" || lk == "x-worker-key" {
			continue
		}
		for _, v := range vv {
			out.Header.Add(k, v)
		}
	}
	// Send one validated client address. Do not blindly trust a caller-supplied X-Forwarded-For chain.
	clientIP := httpx.ClientIP(r, s.deps.Cfg.TrustProxy)
	if clientIP != "" {
		if host, _, e := net.SplitHostPort(r.RemoteAddr); e == nil && host != "" && host != clientIP {
			out.Header.Set("X-Forwarded-For", clientIP+", "+host)
		} else {
			out.Header.Set("X-Forwarded-For", clientIP)
		}
	}
	client := s.deps.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 27 * time.Second}
	}
	resp, err := client.Do(out)
	if err != nil {
		w.Header().Set("Retry-After", "5")
		httpx.WriteError(w, http.StatusServiceUnavailable, "main_unavailable", "main server is unavailable; write requests are not accepted by this worker")
		return
	}
	defer resp.Body.Close()
	for _, k := range []string{"Content-Type", "Cache-Control", "ETag", "Last-Modified", "Retry-After", "WWW-Authenticate", "Allow"} {
		if v := resp.Header.Values(k); len(v) > 0 {
			w.Header()[http.CanonicalHeaderKey(k)] = v
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 8<<20))
}

// proxyHotCatalog requests must be evaluated on main because movie_stats/search_stats are intentionally private.
func (s *Server) proxyHotCatalog(w http.ResponseWriter, r *http.Request) bool {
	if !s.deps.Cfg.IsWorker() {
		return false
	}
	s.proxyWorkerAPI(w, r)
	return true
}
