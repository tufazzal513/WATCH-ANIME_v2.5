package admin

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"time"

	"movex/internal/repo"
)

// Page describes one rendered page. Layout is "base" (sidebar, header, bottom nav) or "bare" (login).
type Page struct {
	Layout string
	Title  string
	Active string // nav key: dashboard, movies, ...
	Status int    // HTTP status, default 200
	Data   any
}

type FlashMsg struct{ Kind, Text string } // Kind: "success" or "error"

// Brand is the app name split the way the old logo styled it: everything but the last letter, then the last letter.
type Brand struct{ Pre, Hi string }

// View is what every template receives: the Page fields plus the shared chrome data.
type View struct {
	Page
	CSRF        string
	Flash       *FlashMsg
	AppName     string
	Brand       Brand
	BrokenBadge int
}

// Ctx is the request context handed to admin handlers.
type Ctx struct {
	W       http.ResponseWriter
	R       *http.Request
	Admin   *Admin
	Session *Session // nil on the login page
}

func (c *Ctx) UID() string {
	if c.Session == nil {
		return ""
	}
	return c.Session.UID
}

func (c *Ctx) IsHTMX() bool { return c.R.Header.Get("HX-Request") == "true" }

// Audit records an admin action (who: the signed-in admin).
func (c *Ctx) Audit(action, target, detail string) {
	if err := c.Admin.deps.Repo.Audit(c.R.Context(), c.UID(), action, target, detail); err != nil {
		c.Admin.deps.Log.Warn("admin: audit write failed", "action", action, "err", err)
	}
}

// Redirect sends the browser to a local /admin path (303; htmx gets HX-Redirect so the whole page navigates).
func (c *Ctx) Redirect(path string) {
	path = safeNext(path)
	if c.IsHTMX() {
		c.W.Header().Set("HX-Redirect", path)
		c.W.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(c.W, c.R, path, http.StatusSeeOther)
}

// Error renders the error page with the given status.
func (c *Ctx) Error(status int, msg string) {
	c.Render("error", Page{Title: http.StatusText(status), Status: status, Data: msg})
}

// Render writes a full page. The whole page is rendered into memory first, so a template error never produces
// half a page.
func (c *Ctx) Render(name string, p Page) {
	a := c.Admin
	t := a.tpl[name]
	if t == nil {
		a.deps.Log.Error("admin: unknown template", "name", name)
		http.Error(c.W, "internal error", http.StatusInternalServerError)
		return
	}
	if p.Layout == "" {
		p.Layout = "base"
	}
	if p.Status == 0 {
		p.Status = http.StatusOK
	}
	v := &View{Page: p, AppName: "MoveX", Flash: a.takeFlash(c.W, c.R)}
	if c.Session != nil {
		v.CSRF = c.Session.CSRF
		if n, err := a.deps.Repo.CountOpenBrokenLinks(c.R.Context()); err == nil {
			v.BrokenBadge = n
		}
	}
	if kv, err := a.deps.Repo.GetSettings(c.R.Context()); err == nil && strings.TrimSpace(kv["appName"]) != "" {
		v.AppName = kv["appName"]
	}
	r := []rune(v.AppName)
	v.Brand = Brand{Pre: string(r[:len(r)-1]), Hi: string(r[len(r)-1:])}

	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, p.Layout+".html", v); err != nil {
		a.deps.Log.Error("admin: template failed", "name", name, "err", err)
		http.Error(c.W, "internal error", http.StatusInternalServerError)
		return
	}
	h := c.W.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	c.W.WriteHeader(p.Status)
	if c.R.Method != http.MethodHead {
		_, _ = c.W.Write(buf.Bytes())
	}
}

// RenderBlock writes only one named block of a page (an htmx fragment such as a table body).
func (c *Ctx) RenderBlock(page, block string, data any) {
	t := c.Admin.tpl[page]
	if t == nil {
		http.Error(c.W, "internal error", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, block, data); err != nil {
		c.Admin.deps.Log.Error("admin: block failed", "page", page, "block", block, "err", err)
		http.Error(c.W, "internal error", http.StatusInternalServerError)
		return
	}
	c.W.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = c.W.Write(buf.Bytes())
}

// ---------- flash messages ----------

// Flash queues a message for the next page (survives one redirect). kind: "success" or "error".
func (c *Ctx) Flash(kind, text string) {
	if kind != "error" {
		kind = "success"
	}
	if r := []rune(text); len(r) > 300 {
		text = string(r[:300])
	}
	payload := base64.RawURLEncoding.EncodeToString([]byte(kind + "|" + text))
	http.SetCookie(c.W, &http.Cookie{
		Name: flashCookie, Value: payload + "." + c.Admin.mac("flash", payload), Path: "/admin", MaxAge: 120,
		HttpOnly: true, Secure: c.Admin.deps.Cfg.CookieSecure, SameSite: http.SameSiteStrictMode,
	})
}

func (a *Admin) takeFlash(w http.ResponseWriter, r *http.Request) *FlashMsg {
	ck, err := r.Cookie(flashCookie)
	if err != nil {
		return nil
	}
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "", Path: "/admin", MaxAge: -1, HttpOnly: true,
		Secure: a.deps.Cfg.CookieSecure, SameSite: http.SameSiteStrictMode})
	payload, sig, ok := strings.Cut(ck.Value, ".")
	if !ok || !a.macOK("flash", payload, sig) {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil
	}
	kind, text, ok := strings.Cut(string(raw), "|")
	if !ok {
		return nil
	}
	return &FlashMsg{Kind: kind, Text: text}
}

// ---------- templates ----------

func (a *Admin) funcs() template.FuncMap {
	return template.FuncMap{
		// static returns a cache-busted URL for an embedded file, e.g. {{static "css/admin-theme.css"}}
		"static": func(name string) string { return "/admin/static/" + name + "?v=" + a.ver[name] },
		// ms formats a unix-millisecond timestamp (UTC)
		"ms": func(v int64) string {
			if v <= 0 {
				return "never"
			}
			return time.UnixMilli(v).UTC().Format("2006-01-02 15:04")
		},
		"add": func(a, b int) int { return a + b },
		"has": func(items []string, want string) bool {
			for _, item := range items {
				if item == want {
					return true
				}
			}
			return false
		},
		"firstStream": func(links []repo.Link) string { return repo.FirstStream(links) },
		"json":        func(v any) string { b, _ := json.Marshal(v); return string(b) },
	}
}

// loadTemplates parses every page together with the layouts (base.html, bare.html) and the partials (_*.html).
// Class names must appear literally in the templates so the Tailwind build can see them.
func (a *Admin) loadTemplates() (map[string]*template.Template, error) {
	entries, err := fs.ReadDir(templatesFS, "templates")
	if err != nil {
		return nil, err
	}
	var shared, pages []string
	for _, e := range entries {
		n := e.Name()
		switch {
		case n == "base.html" || n == "bare.html" || strings.HasPrefix(n, "_"):
			shared = append(shared, "templates/"+n)
		case strings.HasSuffix(n, ".html"):
			pages = append(pages, n)
		}
	}
	out := map[string]*template.Template{}
	for _, p := range pages {
		files := append(append([]string{}, shared...), "templates/"+p)
		t, err := template.New(p).Funcs(a.funcs()).ParseFS(templatesFS, files...)
		if err != nil {
			return nil, fmt.Errorf("admin: parse %s: %w", p, err)
		}
		out[strings.TrimSuffix(p, ".html")] = t
	}
	return out, nil
}

// ---------- static files ----------

func hashStatic() (map[string]string, error) {
	out := map[string]string{}
	err := fs.WalkDir(staticFS, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := staticFS.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		out[strings.TrimPrefix(path, "static/")] = hex.EncodeToString(sum[:])[:10]
		return nil
	})
	return out, err
}

func (a *Admin) staticHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") { // no directory listings
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		if r.URL.Query().Get("v") != "" {
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "public, max-age=3600")
		}
		a.static.ServeHTTP(w, r)
	})
}

// sourcesHash is the fingerprint of everything the Tailwind build reads (templates and admin.js, in byte order).
// build-css.sh stores it next to tailwind.css; a test fails when a template changed but the CSS was not rebuilt.
func sourcesHash() (string, error) {
	entries, err := fs.ReadDir(templatesFS, "templates")
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".html") {
			names = append(names, "templates/"+e.Name())
		}
	}
	sort.Strings(names)
	names = append(names, "static/js/admin.js")
	h := sha256.New()
	for _, n := range names {
		b, err := fs.ReadFile(sourcesFS(), n)
		if err != nil {
			return "", err
		}
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
