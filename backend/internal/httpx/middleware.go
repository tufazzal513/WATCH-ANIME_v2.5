package httpx

import (
	"compress/gzip"
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// Chain applies middleware so that the first one is the outermost.
func Chain(h http.Handler, mw ...Middleware) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

// ---------- recording writer (status + bytes) ----------

type recorder struct {
	http.ResponseWriter
	status int
	bytes  int64
	wrote  bool
}

func (r *recorder) WriteHeader(code int) {
	if !r.wrote {
		r.status, r.wrote = code, true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.status, r.wrote = http.StatusOK, true
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// ---------- request id + access log ----------

// LogInfo lets inner handlers add fields (the authenticated uid) to the access-log line.
type LogInfo struct{ UID string }

// SetUID records the authenticated user for the access log.
func SetUID(r *http.Request, uid string) {
	if li, ok := r.Context().Value(keyLogInfo).(*LogInfo); ok {
		li.UID = uid
	}
}

// RequestID returns the id assigned to this request ("" outside the middleware).
func RequestID(r *http.Request) string {
	id, _ := r.Context().Value(keyRequestID).(string)
	return id
}

// AccessLog assigns a request id (X-Request-Id) and logs one line per request. It never logs the query string,
// headers or bodies, so tokens and search terms stay out of the logs.
func AccessLog(log *slog.Logger, trustProxy bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-Id")
			if !validRequestID(id) {
				id = newRequestID()
			}
			w.Header().Set("X-Request-Id", id)
			li := &LogInfo{}
			ctx := context.WithValue(context.WithValue(r.Context(), keyRequestID, id), keyLogInfo, li)
			rec := &recorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(rec, r.WithContext(ctx))
			attrs := []any{"id", id, "method", r.Method, "path", r.URL.Path, "status", rec.status,
				"bytes", rec.bytes, "ms", time.Since(start).Milliseconds(), "ip", ClientIP(r, trustProxy)}
			if li.UID != "" {
				attrs = append(attrs, "uid", li.UID)
			}
			level := slog.LevelInfo
			if rec.status >= 500 {
				level = slog.LevelError
			}
			log.Log(r.Context(), level, "request", attrs...)
		})
	}
}

// ---------- panic recovery ----------

func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				v := recover()
				if v == nil {
					return
				}
				if v == http.ErrAbortHandler {
					panic(v)
				}
				log.Error("panic in handler", "id", RequestID(r), "method", r.Method, "path", r.URL.Path, "panic", v, "stack", string(debug.Stack()))
				WriteError(w, http.StatusInternalServerError, "internal", "something went wrong")
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// ---------- security headers ----------

// APISecurityHeaders adds the headers every API response should carry.
func APISecurityHeaders() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "no-referrer")
			next.ServeHTTP(w, r)
		})
	}
}

// ---------- CORS ----------

const corsAllowHeaders = "Authorization, Content-Type, If-None-Match, X-Worker-Key"

// CORS allows only the listed origins ("*" = any). Credentials are never allowed (the API uses Bearer tokens).
// Preflight (OPTIONS) requests are answered here.
func CORS(origins []string) Middleware {
	any := false
	allowed := map[string]bool{}
	for _, o := range origins {
		if o == "*" {
			any = true
		}
		allowed[o] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Add("Vary", "Origin")
			origin := r.Header.Get("Origin")
			ok := origin != "" && (any || allowed[origin])
			if ok {
				if any {
					h.Set("Access-Control-Allow-Origin", "*")
				} else {
					h.Set("Access-Control-Allow-Origin", origin)
				}
				h.Set("Access-Control-Expose-Headers", "ETag, Retry-After, X-Request-Id")
			}
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				if ok {
					h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
					h.Set("Access-Control-Allow-Headers", corsAllowHeaders)
					h.Set("Access-Control-Max-Age", "86400")
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ---------- body limit ----------

func LimitBody(max int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil && r.Body != http.NoBody {
				r.Body = http.MaxBytesReader(w, r.Body, max)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ---------- gzip ----------

const gzipMinSize = 1024

var gzipPool = sync.Pool{New: func() any { return gzip.NewWriter(nil) }}

// Gzip compresses JSON/text responses larger than 1 KB for clients that accept it.
func Gzip() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodHead || !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				w.Header().Add("Vary", "Accept-Encoding")
				next.ServeHTTP(w, r)
				return
			}
			gw := &gzipWriter{ResponseWriter: w}
			defer gw.finish()
			next.ServeHTTP(gw, r)
		})
	}
}

type gzipWriter struct {
	http.ResponseWriter
	status  int
	buf     []byte
	started bool // decision made and headers sent
	zw      *gzip.Writer
}

func (g *gzipWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

func (g *gzipWriter) WriteHeader(code int) {
	if g.started || g.status != 0 {
		return
	}
	g.status = code
	if code == http.StatusNoContent || code == http.StatusNotModified || code < 200 { // never has a body
		g.started = true
		g.ResponseWriter.Header().Add("Vary", "Accept-Encoding")
		g.ResponseWriter.WriteHeader(code)
	}
}

func compressible(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.HasPrefix(ct, "application/json") || strings.HasPrefix(ct, "text/") ||
		strings.HasPrefix(ct, "application/javascript") || strings.HasPrefix(ct, "image/svg+xml")
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	if g.status == 0 {
		g.status = http.StatusOK
	}
	if g.started {
		if g.zw != nil {
			return g.zw.Write(b)
		}
		return g.ResponseWriter.Write(b)
	}
	g.buf = append(g.buf, b...)
	if len(g.buf) > gzipMinSize {
		g.start(true)
		if g.zw != nil {
			if _, err := g.zw.Write(g.buf); err != nil {
				return 0, err
			}
		} else if _, err := g.ResponseWriter.Write(g.buf); err != nil {
			return 0, err
		}
		g.buf = nil
	}
	return len(b), nil
}

// start sends the headers; large==true means the body passed the size threshold.
func (g *gzipWriter) start(large bool) {
	g.started = true
	h := g.ResponseWriter.Header()
	h.Add("Vary", "Accept-Encoding")
	if large && h.Get("Content-Encoding") == "" && compressible(h.Get("Content-Type")) {
		h.Set("Content-Encoding", "gzip")
		h.Del("Content-Length")
		g.ResponseWriter.WriteHeader(g.status)
		zw := gzipPool.Get().(*gzip.Writer)
		zw.Reset(g.ResponseWriter)
		g.zw = zw
		return
	}
	g.ResponseWriter.WriteHeader(g.status)
}

func (g *gzipWriter) finish() {
	if !g.started {
		if g.status == 0 {
			g.status = http.StatusOK
		}
		g.start(false)
		if len(g.buf) > 0 {
			_, _ = g.ResponseWriter.Write(g.buf)
		}
		return
	}
	if g.zw != nil {
		_ = g.zw.Close()
		gzipPool.Put(g.zw)
		g.zw = nil
	}
}

// ---------- rate limiter (token bucket) ----------

type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter allows `max` events per `per` for each key, smoothly refilled (token bucket).
type Limiter struct {
	mu      sync.Mutex
	max     float64
	rate    float64 // tokens per second
	buckets map[string]*bucket
	calls   int
	now     func() time.Time
}

func NewLimiter(max int, per time.Duration) *Limiter {
	return &Limiter{max: float64(max), rate: float64(max) / per.Seconds(), buckets: map[string]*bucket{}, now: time.Now}
}

// Allow takes one token for key. When it is refused, retryAfter says when the next token arrives.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.max, last: now}
		l.buckets[key] = b
	} else {
		b.tokens = min(l.max, b.tokens+now.Sub(b.last).Seconds()*l.rate)
		b.last = now
	}
	if l.calls++; l.calls%1000 == 0 { // drop buckets that are full again (idle callers)
		for k, o := range l.buckets {
			if k != key && min(l.max, o.tokens+now.Sub(o.last).Seconds()*l.rate) >= l.max {
				delete(l.buckets, k)
			}
		}
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1-b.tokens)/l.rate*float64(time.Second)) + time.Millisecond
}

// TooMany writes a 429 with Retry-After.
func TooMany(w http.ResponseWriter, retryAfter time.Duration) {
	secs := int(retryAfter.Seconds()) + 1
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many requests, slow down")
}

// RateLimitIP limits requests per client IP.
func RateLimitIP(l *Limiter, trustProxy bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodOptions {
				if ok, wait := l.Allow(ClientIP(r, trustProxy)); !ok {
					TooMany(w, wait)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
