// Package server wires routing, the middleware chain, authentication and graceful shutdown.
package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"movex/internal/admin"
	"movex/internal/auth"
	"movex/internal/backup"
	"movex/internal/config"
	"movex/internal/httpx"
	"movex/internal/presence"
	"movex/internal/replica"
	"movex/internal/repo"
)

// Version is set at build time: -ldflags "-X movex/internal/server.Version=1.0.0".
var Version = "dev"

// Default limits (same as the old server): per IP, per signed-in user, and writes without a token.
const (
	IPPerMinute        = 900
	UIDPerMinute       = 300
	AnonWritesPerMin   = 20
	DefaultBodyLimit   = 1 << 20 // 1 MB for API requests
	authUnavailableSec = 30
)

type Deps struct {
	Cfg      *config.Config
	DB       *sql.DB
	Log      *slog.Logger
	Presence *presence.Store
	Repo     *repo.Repo
	Verifier *auth.Verifier
	// FirebaseURL is the Identity Toolkit base URL (default https://identitytoolkit.googleapis.com); tests point it at a fake.
	FirebaseURL string
	HTTPClient  *http.Client
	// Seq returns the current replication sequence (nil => 0).
	Seq           func() int64
	Backup        *backup.Manager
	TelegramLogin *backup.LoginFlow
}

type Server struct {
	deps    Deps
	mux     *http.ServeMux
	handler http.Handler
	http    *http.Server

	admin *admin.Admin // nil on workers

	ipLimit   *httpx.Limiter
	uidLimit  *httpx.Limiter
	anonLimit *httpx.Limiter
	viewLimit *httpx.Limiter // one counted view per user per title per 6 hours
	hitLimit  *httpx.Limiter // search-hit counting per user

	errLimit     *httpx.Limiter // client error reports per user
	brokenLimit  *httpx.Limiter // broken-link reports per user
	brokenDup    *httpx.Limiter // the same user reporting the same title again
	resetLimit   *httpx.Limiter // password-reset requests per IP
	admins       map[string]bool
	workerSyncer *replica.Syncer
}

func New(d Deps) *Server {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	s := &Server{
		deps: d, mux: http.NewServeMux(),
		ipLimit:   httpx.NewLimiter(IPPerMinute, time.Minute),
		uidLimit:  httpx.NewLimiter(UIDPerMinute, time.Minute),
		anonLimit: httpx.NewLimiter(AnonWritesPerMin, time.Minute),
		viewLimit: httpx.NewLimiter(1, 6*time.Hour),
		hitLimit:  httpx.NewLimiter(30, time.Minute),

		errLimit:    httpx.NewLimiter(20, time.Minute),
		brokenLimit: httpx.NewLimiter(10, time.Minute),
		brokenDup:   httpx.NewLimiter(1, time.Hour),
		resetLimit:  httpx.NewLimiter(10, 10*time.Minute),
		admins:      map[string]bool{},
	}
	for _, u := range d.Cfg.AdminUIDs {
		s.admins[u] = true
	}
	s.routes()
	s.handler = httpx.Chain(s.mux, httpx.AccessLog(d.Log, d.Cfg.TrustProxy), httpx.Recover(d.Log))
	s.http = &http.Server{
		Addr:              net.JoinHostPort("0.0.0.0", strconv.Itoa(d.Cfg.Port)),
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    32 << 10,
		ErrorLog:          slog.NewLogLogger(d.Log.Handler(), slog.LevelWarn),
	}
	return s
}

// Handler is the full handler (middleware included); used by tests.
func (s *Server) Handler() http.Handler { return s.handler }

// Admin returns the admin panel (nil on workers) so other parts can add pages to it.
func (s *Server) Admin() *admin.Admin { return s.admin }

// Mux lets later parts register routes. Wrap API handlers with Public / Private / AdminAPI.
func (s *Server) Mux() *http.ServeMux { return s.mux }

func (s *Server) Log() *slog.Logger { return s.deps.Log }

// ---------- routing ----------

// api wraps a handler with the API middleware: security headers, CORS (listed origins only), per-IP rate limit,
// gzip and a body size limit. CORS is outside the rate limiter so that 429 responses stay readable by browsers.
func (s *Server) api(h http.Handler) http.Handler {
	return httpx.Chain(h,
		httpx.APISecurityHeaders(),
		httpx.CORS(s.deps.Cfg.AllowedOrigins),
		httpx.RateLimitIP(s.ipLimit, s.deps.Cfg.TrustProxy),
		httpx.Gzip(),
		httpx.LimitBody(DefaultBodyLimit),
	)
}

// Public registers an API route that needs no login.
func (s *Server) Public(pattern string, fn http.HandlerFunc) { s.mux.Handle(pattern, s.api(fn)) }

// Private registers an API route that needs a valid Firebase token (and an account that is not frozen/deleted).
func (s *Server) Private(pattern string, fn func(http.ResponseWriter, *http.Request, *Principal)) {
	s.mux.Handle(pattern, s.api(s.Authed(fn)))
}

// AdminAPI registers an API route that needs a token whose uid is in ADMIN_UIDS.
func (s *Server) AdminAPI(pattern string, fn func(http.ResponseWriter, *http.Request, *Principal)) {
	s.mux.Handle(pattern, s.api(s.Authed(func(w http.ResponseWriter, r *http.Request, p *Principal) {
		if !p.IsAdmin {
			httpx.WriteError(w, http.StatusForbidden, "forbidden", "admin only")
			return
		}
		fn(w, r, p)
	})))
}

func (s *Server) routes() {
	s.Public("GET /api/v1/health", s.health)
	if s.deps.Repo != nil {
		s.replicationRoutes()
		s.catalogRoutes()
		if s.deps.Cfg.IsMain() { // private data lives on main only; workers proxy these routes (P16)
			s.privateRoutes()
		}
	}
	// The admin panel exists on ROLE=main only (workers answer 404 for /admin).
	if s.deps.Cfg.IsMain() && s.deps.Repo != nil && s.deps.Verifier != nil {
		a, err := admin.New(admin.Deps{
			Cfg: s.deps.Cfg, Repo: s.deps.Repo, Verifier: s.deps.Verifier, Presence: s.deps.Presence, Log: s.deps.Log,
			FirebaseURL: s.deps.FirebaseURL, HTTPClient: s.deps.HTTPClient, Backup: s.deps.Backup, TelegramLogin: s.deps.TelegramLogin,
		})
		if err != nil {
			panic("admin panel: " + err.Error()) // a configuration/programming error: fail loudly at start-up
		}
		a.Register(s.mux)
		s.admin = a
	}
	s.mux.Handle("OPTIONS /api/", s.api(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })))
	s.mux.Handle("/api/", s.api(http.HandlerFunc(s.apiNotFound)))
}

// apiNotFound answers unknown API paths with JSON: 405 + Allow when the path exists for other methods, else 404.
func (s *Server) apiNotFound(w http.ResponseWriter, r *http.Request) {
	if s.deps.Cfg.IsWorker() {
		s.proxyWorkerAPI(w, r)
		return
	}
	var allow []string
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if m == r.Method {
			continue
		}
		probe := r.Clone(r.Context())
		probe.Method = m
		if _, pattern := s.mux.Handler(probe); pattern != "" && pattern != "/api/" {
			allow = append(allow, m)
		}
	}
	if len(allow) > 0 {
		w.Header().Set("Allow", strings.Join(allow, ", "))
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	httpx.WriteError(w, http.StatusNotFound, "not_found", "")
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	var seq int64
	if s.deps.Seq != nil {
		seq = s.deps.Seq()
	}
	dbOK := true
	if s.deps.DB != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		dbOK = s.deps.DB.PingContext(ctx) == nil
	}
	status := http.StatusOK
	if !dbOK {
		status = http.StatusServiceUnavailable
	}
	httpx.WriteJSON(w, status, map[string]any{"ok": dbOK, "role": string(s.deps.Cfg.Role), "version": Version, "seq": seq})
}

// ---------- authentication ----------

// Principal is the authenticated caller of an API request.
type Principal struct {
	UID     string
	Email   string
	Name    string
	Picture string
	Status  string // account status; "" when the user has no record yet (first login)
	IsAdmin bool   // uid is listed in ADMIN_UIDS
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// authenticate verifies the token, applies the per-user rate limit and refuses frozen/deleted accounts.
// When it returns false the error response has already been written.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (*Principal, bool) {
	return s.authenticateOpts(w, r, false)
}

// authenticateOpts is authenticate; allowDeleted lets accounts with status "deleted" through (only DELETE /me).
func (s *Server) authenticateOpts(w http.ResponseWriter, r *http.Request, allowDeleted bool) (*Principal, bool) {
	deny := func() (*Principal, bool) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="movex"`)
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "sign in required")
		return nil, false
	}
	token := bearer(r)
	if token == "" || s.deps.Verifier == nil {
		return deny()
	}
	c, err := s.deps.Verifier.Verify(r.Context(), token)
	switch {
	case errors.Is(err, auth.ErrKeysUnavailable):
		w.Header().Set("Retry-After", strconv.Itoa(authUnavailableSec))
		httpx.WriteError(w, http.StatusServiceUnavailable, "auth_unavailable", "sign-in check is temporarily unavailable")
		return nil, false
	case err != nil:
		return deny()
	}
	if ok, wait := s.uidLimit.Allow(c.UID); !ok {
		httpx.TooMany(w, wait)
		return nil, false
	}
	p := &Principal{UID: c.UID, Email: c.Email, Name: c.Name, Picture: c.Picture, IsAdmin: s.admins[c.UID]}

	// Frozen and deleted accounts are refused everywhere. Workers hold no user records (those live on main),
	// so the check only runs where the users table is authoritative.
	if s.deps.Cfg.IsMain() && s.deps.Repo != nil {
		status, found, err := s.deps.Repo.UserStatus(r.Context(), c.UID)
		if err != nil {
			s.fail(w, r, err)
			return nil, false
		}
		if found {
			p.Status = status
			if repo.Blocked(status) && !(allowDeleted && status == repo.StatusDeleted) {
				httpx.SetUID(r, c.UID)
				httpx.WriteJSON(w, http.StatusForbidden, httpx.APIError{Code: "account_blocked", Status: status,
					Message: "this account is " + status})
				return nil, false
			}
		}
	}
	httpx.SetUID(r, c.UID)
	return p, true
}

// Authed wraps a handler that needs a signed-in user.
func (s *Server) Authed(fn func(http.ResponseWriter, *http.Request, *Principal)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := s.authenticate(w, r); ok {
			fn(w, r, p)
		}
	})
}

// AnonWriteAllowed rate-limits write requests that carry no valid token (e.g. client error reports) per IP.
func (s *Server) AnonWriteAllowed(w http.ResponseWriter, r *http.Request) bool {
	if ok, wait := s.anonLimit.Allow(httpx.ClientIP(r, s.deps.Cfg.TrustProxy)); !ok {
		httpx.TooMany(w, wait)
		return false
	}
	return true
}

// ---------- errors ----------

// fail maps an internal error to a response: validation -> 400, not found -> 404, too large -> 413, rest -> 500.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ve *repo.ValidationError
	var mbe *http.MaxBytesError
	switch {
	case errors.As(err, &ve):
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.APIError{Code: "validation", Field: ve.Field, Message: ve.Msg})
	case errors.Is(err, repo.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", "")
	case errors.As(err, &mbe):
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, "too_large", "request body is too large")
	case errors.Is(err, context.Canceled):
		// the client went away; nothing to send
	default:
		s.deps.Log.Error("request failed", "id", httpx.RequestID(r), "method", r.Method, "path", r.URL.Path, "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "something went wrong")
	}
}

// ---------- lifecycle ----------

// Run serves until ctx is cancelled, then shuts down gracefully (up to 15 seconds for in-flight requests).
func (s *Server) Run(ctx context.Context) error {
	if s.deps.Cfg.IsMain() {
		if s.deps.Repo != nil {
			if _, err := s.deps.Repo.EnsureEpoch(ctx); err != nil {
				return errors.New("initialize replication epoch: " + err.Error())
			}
		}
		go s.maintenanceLoop(ctx)
		go s.notificationLoop(ctx)
		if s.deps.Backup != nil && s.deps.Cfg.TelegramConfigured() {
			s.deps.Backup.StartScheduler(ctx, time.Duration(s.deps.Cfg.BackupEveryHours)*time.Hour)
		}
	}
	if s.deps.Cfg.IsWorker() {
		syncer, err := replica.NewSyncer(s.deps.Cfg.MainURL, s.deps.Cfg.WorkerKey, time.Duration(s.deps.Cfg.SyncEverySec)*time.Second, s.deps.Repo, s.deps.HTTPClient, s.deps.Log)
		if err != nil {
			return fmt.Errorf("initialize worker replication: %w", err)
		}
		s.workerSyncer = syncer
		initialCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		err = syncer.SyncOnce(initialCtx)
		cancel()
		if err != nil {
			epoch, _, cursorErr := s.deps.Repo.ReplicaCursor(ctx)
			if cursorErr != nil {
				return fmt.Errorf("read worker replication state: %w", cursorErr)
			}
			if epoch == "" {
				return fmt.Errorf("initial worker catalog sync failed; refusing to serve an empty replica: %w", err)
			}
			s.deps.Log.Warn("initial worker sync failed; serving last committed replica and retrying", "err", err)
		}
		go syncer.Run(ctx)
	}
	errc := make(chan error, 1)
	go func() {
		s.deps.Log.Info("listening", "addr", s.http.Addr, "role", string(s.deps.Cfg.Role), "version", Version)
		errc <- s.http.ListenAndServe()
	}()
	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s.deps.Log.Info("shutting down")
	if err := s.http.Shutdown(sctx); err != nil {
		_ = s.http.Close()
		return err
	}
	return nil
}
