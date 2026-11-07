// Package admin is the server-rendered admin panel (html/template + htmx), mounted under /admin on ROLE=main.
//
// Security model: Firebase email+password login done by the server, a server-side session (random id in an
// HMAC-signed HttpOnly cookie, SHA-256 of it in the database), a per-session CSRF token plus Origin /
// Sec-Fetch-Site checks on every state-changing request, admin membership re-checked on every request, and a
// strict CSP (no inline script, no inline style, no third-party hosts).
package admin

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"movex/internal/auth"
	"movex/internal/backup"
	"movex/internal/config"
	"movex/internal/httpx"
	"movex/internal/presence"
	"movex/internal/repo"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

func init() {
	// not every OS ships these in its mime table
	_ = mime.AddExtensionType(".woff2", "font/woff2")
	_ = mime.AddExtensionType(".js", "text/javascript; charset=utf-8")
	_ = mime.AddExtensionType(".css", "text/css; charset=utf-8")
}

const (
	sessionCookie  = "mx_admin"
	flashCookie    = "mx_flash"
	sessionTTL     = 12 * time.Hour
	maxFormBytes   = 8 << 20 // admin forms can carry long season/episode lists
	loginBodyLimit = 8 << 10
	loginAttempts  = 8                // per IP and per email ...
	loginWindow    = 10 * time.Minute // ... per 10 minutes
)

type Deps struct {
	Cfg      *config.Config
	Repo     *repo.Repo
	Verifier *auth.Verifier
	Presence *presence.Store
	Log      *slog.Logger
	// FirebaseURL is the Identity Toolkit base URL (default https://identitytoolkit.googleapis.com); tests use a fake.
	FirebaseURL   string
	HTTPClient    *http.Client
	Backup        *backup.Manager
	TelegramLogin *backup.LoginFlow
}

type Admin struct {
	deps   Deps
	admins map[string]bool
	tpl    map[string]*template.Template
	ver    map[string]string // static file -> content hash, for cache busting
	static http.Handler

	loginIP    *httpx.Limiter
	loginEmail *httpx.Limiter
	now        func() time.Time
	mx         *http.ServeMux // set by Register
}

// New parses the embedded templates and prepares the handlers. It fails when the configuration cannot support
// safe sessions (no SESSION_SECRET) or a template is broken.
func New(d Deps) (*Admin, error) {
	if d.Cfg == nil || d.Repo == nil || d.Verifier == nil {
		return nil, fmt.Errorf("admin: config, repo and verifier are required")
	}
	if len(d.Cfg.SessionSecret) < 32 {
		return nil, fmt.Errorf("admin: SESSION_SECRET must be resolved (32+ bytes) before the admin panel starts")
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	a := &Admin{
		deps: d, admins: map[string]bool{}, now: time.Now,
		loginIP:    httpx.NewLimiter(loginAttempts, loginWindow),
		loginEmail: httpx.NewLimiter(loginAttempts, loginWindow),
	}
	for _, u := range d.Cfg.AdminUIDs {
		a.admins[u] = true
	}
	ver, err := hashStatic()
	if err != nil {
		return nil, err
	}
	a.ver = ver
	if a.tpl, err = a.loadTemplates(); err != nil {
		return nil, err
	}
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	a.static = http.StripPrefix("/admin/static/", http.FileServerFS(sub))
	return a, nil
}

// IsAdmin reports whether uid is listed in ADMIN_UIDS (checked on every request, so editing the env revokes access).
func (a *Admin) IsAdmin(uid string) bool { return a.admins[uid] }

// Register mounts every /admin route on mux. Later parts add their pages through Handle.
func (a *Admin) Register(mux *http.ServeMux) {
	a.mx = mux
	mux.Handle("GET /admin/static/", a.staticHandler())
	mux.Handle("GET /admin/login", a.secure(http.HandlerFunc(a.loginPage)))
	mux.Handle("POST /admin/login", a.secure(http.HandlerFunc(a.loginSubmit)))
	mux.Handle("POST /admin/logout", a.secure(http.HandlerFunc(a.logout)))
	mux.Handle("GET /admin", http.RedirectHandler("/admin/", http.StatusMovedPermanently))
	a.Handle("GET /admin/{$}", a.dashboard)
	a.Handle("GET /admin/categories", a.categoriesPage)
	a.Handle("POST /admin/categories", a.saveCategory)
	a.Handle("POST /admin/categories/{id}", a.saveCategory)
	a.Handle("POST /admin/categories/{id}/delete", a.deleteCategory)
	a.Handle("POST /admin/categories/reorder", a.reorderCategories)
	a.Handle("GET /admin/users", a.usersPage)
	a.Handle("POST /admin/users/{uid}/status", a.setUserStatus)
	a.Handle("GET /admin/banners", a.bannersPage)
	a.Handle("POST /admin/banners", a.saveBanner)
	a.Handle("POST /admin/banners/{id}", a.saveBanner)
	a.Handle("POST /admin/banners/{id}/delete", a.deleteBanner)
	a.Handle("POST /admin/banners/reorder", a.reorderBanners)
	a.Handle("GET /admin/settings", a.settingsPage)
	a.Handle("GET /admin/server", a.serverPage)
	a.Handle("POST /admin/server/telegram/login", a.telegramLoginBegin)
	a.Handle("POST /admin/server/telegram/code", a.telegramLoginCode)
	a.Handle("POST /admin/server/telegram/password", a.telegramLoginPassword)
	a.Handle("POST /admin/server/telegram/cancel", a.telegramLoginCancel)
	a.Handle("POST /admin/server/backup", a.createLocalBackup)
	a.Handle("POST /admin/server/restore", a.stageRestore)
	a.Handle("POST /admin/server/restore/cancel", a.cancelRestore)
	a.Handle("GET /admin/server/backups/{name}", a.downloadLocalBackup)
	a.Handle("GET /admin/workers", a.workersPage)
	a.Handle("POST /admin/workers", a.createWorker)
	a.Handle("POST /admin/workers/{id}/delete", a.deleteWorker)
	a.Handle("POST /admin/settings", a.saveSettings)
	a.Handle("POST /admin/settings/socials", a.saveSocial)
	a.Handle("POST /admin/settings/socials/{id}", a.saveSocial)
	a.Handle("POST /admin/settings/socials/{id}/delete", a.deleteSocial)
	a.Handle("POST /admin/settings/notifications", a.saveNotification)
	a.Handle("POST /admin/settings/notifications/{id}", a.saveNotification)
	a.Handle("POST /admin/settings/notifications/{id}/delete", a.deleteNotification)
	a.Handle("GET /admin/broken-links", a.brokenLinks)
	a.Handle("POST /admin/broken-links/{id}/resolve", a.resolveBrokenLink)
	a.Handle("POST /admin/broken-links/{id}/delete", a.deleteBrokenLink)
	a.Handle("GET /admin/movies", a.allMoviesPage)
	a.Handle("POST /admin/movies/bulk", a.bulkMovies)
	a.Handle("GET /admin/movies/new", a.newMoviePage)
	a.Handle("POST /admin/movies/new", a.saveMovie)
	a.Handle("GET /admin/movies/{id}/edit", a.editMoviePage)
	a.Handle("POST /admin/movies/{id}/edit", a.saveMovie)
	a.Handle("POST /admin/movies/{id}/delete", a.deleteMovie)
	mux.Handle("/admin/", a.secure(a.requireAdmin(func(c *Ctx) { c.Error(http.StatusNotFound, "Page not found") })))
}

// Handle registers an admin page or action. It requires a signed-in admin; for POST/PUT/PATCH/DELETE it also
// enforces the same-origin check and the CSRF token. fn only runs when all checks pass. Call it after Register.
func (a *Admin) Handle(pattern string, fn func(*Ctx)) {
	if a.mx == nil {
		panic("admin: Handle called before Register")
	}
	a.mx.Handle(pattern, a.secure(a.requireAdmin(fn)))
}
