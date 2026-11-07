package admin

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"movex/internal/auth"
	"movex/internal/config"
	"movex/internal/repo"
)

// The CSP forbids inline script, inline style and inline event handlers. This test keeps every template honest,
// so a page that would silently break in the browser fails here instead.
func TestTemplatesRespectTheCSP(t *testing.T) {
	scriptTag := regexp.MustCompile(`(?is)<script\b[^>]*>`)
	hasSrc := regexp.MustCompile(`(?i)\ssrc\s*=`)
	handler := regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
	style := regexp.MustCompile(`(?i)<style\b|\sstyle\s*=`)
	js := regexp.MustCompile(`(?i)javascript:`)
	thirdParty := regexp.MustCompile(`(?i)(href|src)\s*=\s*["']https?://`)
	n := 0
	err := fs.WalkDir(templatesFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		n++
		b, _ := templatesFS.ReadFile(path)
		src := string(b)
		for _, tag := range scriptTag.FindAllString(src, -1) {
			if !hasSrc.MatchString(tag) {
				t.Errorf("%s: contains an inline <script>: %q", path, tag)
			}
		}
		for name, re := range map[string]*regexp.Regexp{
			"inline event handler (onclick=...)": handler,
			"inline style":                       style, "javascript: URL": js, "third-party host": thirdParty,
		} {
			if m := re.FindString(src); m != "" {
				t.Errorf("%s: contains %s: %q", path, name, strings.TrimSpace(m))
			}
		}
		if strings.Contains(src, "template.HTML") || strings.Contains(src, "| safe") {
			t.Errorf("%s: must never bypass auto-escaping", path)
		}
		return nil
	})
	if err != nil || n < 6 {
		t.Fatalf("scanned %d templates: %v", n, err)
	}
	// the one first-party script must not use eval-like constructs either
	b, _ := staticFS.ReadFile("static/js/admin.js")
	for _, bad := range []string{"eval(", "new Function", "innerHTML", "document.write", "setTimeout(\""} {
		if strings.Contains(string(b), bad) {
			t.Errorf("admin.js uses %q", bad)
		}
	}
}

func TestTailwindCSSMatchesTheTemplates(t *testing.T) {
	want, err := sourcesHash()
	if err != nil {
		t.Fatal(err)
	}
	b, err := staticFS.ReadFile("static/css/tailwind.css.sources")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(b)); got != want {
		t.Fatalf("templates or admin.js changed since tailwind.css was built.\nRun: backend/internal/admin/build-css.sh\n built from %s\n now        %s", got, want)
	}
	css, _ := staticFS.ReadFile("static/css/tailwind.css")
	for _, class := range []string{".bg-gray-900", ".grid-cols-2", ".rounded-xl", ".text-\\[10px\\]", ".hover\\:text-gray-200", ".bg-red-500\\/10"} {
		if !strings.Contains(string(css), class) {
			t.Errorf("tailwind.css lacks %s: the build did not pick up the templates", class)
		}
	}
}

func TestEmbeddedStaticFilesAreReferencedAndSmall(t *testing.T) {
	for _, f := range []string{"css/tailwind.css", "css/fa.css", "css/fonts.css", "css/admin-theme.css", "js/htmx.min.js", "js/admin.js",
		"webfonts/fa-solid-900.woff2", "fonts/poppins-latin-400-normal.woff2"} {
		if _, err := staticFS.ReadFile("static/" + f); err != nil {
			t.Errorf("missing static file %s", f)
		}
	}
	fa, _ := staticFS.ReadFile("static/css/fa.css")
	for _, ref := range regexp.MustCompile(`url\(\.\./webfonts/([a-z0-9-]+\.[a-z0-9]+)\)`).FindAllStringSubmatch(string(fa), -1) {
		if _, err := staticFS.ReadFile("static/webfonts/" + ref[1]); err != nil {
			t.Errorf("fa.css points at %s which is not shipped", ref[1])
		}
	}
	fonts, _ := staticFS.ReadFile("static/css/fonts.css")
	for _, ref := range regexp.MustCompile(`url\(\.\./fonts/([a-z0-9-]+\.woff2)\)`).FindAllStringSubmatch(string(fonts), -1) {
		if _, err := staticFS.ReadFile("static/fonts/" + ref[1]); err != nil {
			t.Errorf("fonts.css points at %s which is not shipped", ref[1])
		}
	}
}

func TestNewRefusesUnsafeConfiguration(t *testing.T) {
	verifier := auth.NewVerifier(auth.Options{ProjectID: "p"})
	rp := repo.New(nil)
	ok := &config.Config{SessionSecret: []byte(strings.Repeat("s", 32))}
	if _, err := New(Deps{Cfg: ok, Repo: rp, Verifier: verifier}); err != nil {
		t.Fatalf("a valid configuration must start: %v", err)
	}
	for name, d := range map[string]Deps{
		"no secret":    {Cfg: &config.Config{}, Repo: rp, Verifier: verifier},
		"short secret": {Cfg: &config.Config{SessionSecret: []byte("short")}, Repo: rp, Verifier: verifier},
		"no repo":      {Cfg: ok, Verifier: verifier},
		"no verifier":  {Cfg: ok, Repo: rp},
		"no config":    {Repo: rp, Verifier: verifier},
	} {
		if _, err := New(d); err == nil {
			t.Errorf("%s: New must fail", name)
		}
	}
}

func TestSafeNext(t *testing.T) {
	good := map[string]string{
		"/admin/movies":          "/admin/movies",
		"/admin/movies?page=2":   "/admin/movies?page=2",
		"/admin/movies/abc/edit": "/admin/movies/abc/edit",
		"/admin":                 "/admin",
	}
	for in, want := range good {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
	for _, evil := range []string{
		"", "//evil.com", "https://evil.com", "http://evil.com/admin", "/\\evil.com", "/admin\\..\\x", "/other", "/administrator",
		"/admin/login", "/admin/logout", "/admin/\r\nSet-Cookie: x=1", "javascript:alert(1)", "/admin//evil.com", strings.Repeat("/admin/a", 100),
	} {
		got := safeNext(evil)
		if got != "/admin/" && !(strings.HasPrefix(got, "/admin/") || got == "/admin") {
			t.Errorf("safeNext(%q) = %q escapes /admin", evil, got)
		}
		if strings.Contains(got, "//") && got != evil {
			t.Errorf("safeNext(%q) = %q", evil, got)
		}
	}
	if got := safeNext("//evil.com"); got != "/admin/" {
		t.Errorf("protocol-relative URL must be refused, got %q", got)
	}
	if got := safeNext("/admin/login"); got != "/admin/" {
		t.Errorf("never bounce back to the login page: %q", got)
	}
}
