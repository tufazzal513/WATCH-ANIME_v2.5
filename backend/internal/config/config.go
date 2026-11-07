// Package config parses and validates every setting from environment variables.
// All settings live in the environment; nothing is editable from the admin panel.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type Role string

const (
	RoleMain   Role = "main"
	RoleWorker Role = "worker"
)

// ErrDevAuthInProduction is returned when DEV_INSECURE_AUTH is enabled while COOKIE_SECURE=1 (production mode).
var ErrDevAuthInProduction = errors.New("DEV_INSECURE_AUTH is set while COOKIE_SECURE=1 (production); refusing to start")

type Config struct {
	Role    Role
	Port    int
	DataDir string

	FirebaseProjectID string
	FirebaseAPIKey    string
	AdminUIDs         []string

	AllowedOrigins []string
	TrustProxy     bool
	CookieSecure   bool
	SessionSecret  []byte // filled by ResolveSessionSecret

	BackupEveryHours int
	TGAPIID          int
	TGAPIHash        string
	TGChatID         string
	TGKeep           int

	MainURL       string
	WorkerKey     string
	SyncNotifyKey string
	PublicURL     string
	SyncEverySec  int

	DevInsecureAuth bool
}

func (c *Config) IsMain() bool   { return c.Role == RoleMain }
func (c *Config) IsWorker() bool { return c.Role == RoleWorker }

// TelegramConfigured reports whether all three MTProto settings are present.
func (c *Config) TelegramConfigured() bool {
	return c.TGAPIID != 0 && c.TGAPIHash != "" && c.TGChatID != ""
}

func (c *Config) DBPath() string          { return filepath.Join(c.DataDir, "movex.db") }
func (c *Config) TelegramSession() string { return filepath.Join(c.DataDir, "telegram.session") }
func (c *Config) SessionKeyPath() string  { return filepath.Join(c.DataDir, "session.key") }

var (
	originRe = regexp.MustCompile(`^https?://[^\s/?#]+$`)
	tgChatRe = regexp.MustCompile(`^(-?\d{5,}|@\w{4,})$`)
)

// Load reads settings through getenv (pass os.Getenv in production) and validates them.
// All problems are reported together so the operator can fix them in one go.
func Load(getenv func(string) string) (*Config, error) {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	get := func(k string) string { return strings.TrimSpace(getenv(k)) }

	intVar := func(k string, def, min, max int) int {
		s := get(k)
		if s == "" {
			return def
		}
		n, err := strconv.Atoi(s)
		if err != nil || n < min || n > max {
			bad("%s must be an integer between %d and %d (got %q)", k, min, max, s)
			return def
		}
		return n
	}
	boolVar := func(k string, def bool) bool {
		switch strings.ToLower(get(k)) {
		case "":
			return def
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
		bad("%s must be 0 or 1 (got %q)", k, get(k))
		return def
	}
	listVar := func(k string) []string {
		var out []string
		for _, p := range strings.Split(get(k), ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}

	c := &Config{DataDir: "./data"}

	switch r := strings.ToLower(get("ROLE")); r {
	case "", "main":
		c.Role = RoleMain
	case "worker":
		c.Role = RoleWorker
	default:
		bad("ROLE must be \"main\" or \"worker\" (got %q)", r)
		c.Role = RoleMain
	}

	c.Port = intVar("PORT", 3000, 1, 65535)
	if d := get("DATA_DIR"); d != "" {
		c.DataDir = d
	}

	c.FirebaseProjectID = get("FIREBASE_PROJECT_ID")
	if c.FirebaseProjectID == "" {
		bad("FIREBASE_PROJECT_ID is required")
	}

	c.AllowedOrigins = listVar("ALLOWED_ORIGINS")
	if len(c.AllowedOrigins) == 0 {
		c.AllowedOrigins = []string{"*"}
	}
	for i, o := range c.AllowedOrigins {
		o = strings.TrimRight(o, "/")
		c.AllowedOrigins[i] = o
		if o != "*" && !originRe.MatchString(o) {
			bad("ALLOWED_ORIGINS entry %q must be \"*\" or look like https://example.com (no path)", o)
		}
	}

	c.TrustProxy = boolVar("TRUST_PROXY", false)
	c.CookieSecure = boolVar("COOKIE_SECURE", true)
	c.DevInsecureAuth = boolVar("DEV_INSECURE_AUTH", false)
	if c.DevInsecureAuth && c.CookieSecure {
		errs = append(errs, ErrDevAuthInProduction)
	}

	if s := get("SESSION_SECRET"); s != "" {
		if len(s) < 32 {
			bad("SESSION_SECRET must be at least 32 characters (or leave it empty to auto-generate)")
		} else {
			c.SessionSecret = []byte(s)
		}
	}

	switch c.Role {
	case RoleMain:
		c.FirebaseAPIKey = get("FIREBASE_API_KEY")
		if c.FirebaseAPIKey == "" {
			bad("FIREBASE_API_KEY is required for ROLE=main (admin login)")
		}
		c.AdminUIDs = listVar("ADMIN_UIDS")
		if len(c.AdminUIDs) == 0 {
			bad("ADMIN_UIDS is required for ROLE=main (comma-separated Firebase UIDs)")
		}
		c.BackupEveryHours = intVar("BACKUP_EVERY_HOURS", 12, 1, 720)
		c.TGKeep = intVar("TG_KEEP", 30, 2, 500)
		c.TGAPIID = intVar("TG_API_ID", 0, 0, 1<<31-1)
		c.TGAPIHash = get("TG_API_HASH")
		c.TGChatID = get("TG_CHAT_ID")
		set := 0
		for _, v := range []bool{c.TGAPIID != 0, c.TGAPIHash != "", c.TGChatID != ""} {
			if v {
				set++
			}
		}
		if set != 0 && set != 3 {
			bad("TG_API_ID, TG_API_HASH and TG_CHAT_ID must be set together (or all left empty)")
		}
		if c.TGChatID != "" && !tgChatRe.MatchString(c.TGChatID) {
			bad("TG_CHAT_ID must look like -100123456789 or @channelname (got %q)", c.TGChatID)
		}
	case RoleWorker:
		c.MainURL = strings.TrimRight(get("MAIN_URL"), "/")
		c.PublicURL = strings.TrimRight(get("PUBLIC_URL"), "/")
		c.WorkerKey = get("WORKER_KEY")
		for k, v := range map[string]string{"MAIN_URL": c.MainURL, "PUBLIC_URL": c.PublicURL} {
			if v == "" {
				bad("%s is required for ROLE=worker", k)
				continue
			}
			if u, err := url.Parse(v); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				bad("%s must be a full http(s) URL (got %q)", k, v)
			}
		}
		if c.WorkerKey == "" {
			bad("WORKER_KEY is required for ROLE=worker (shown once when the worker is added in the main admin panel)")
		}
		c.SyncEverySec = intVar("SYNC_EVERY_SEC", 60, 5, 3600)
	}

	c.SyncNotifyKey = get("SYNC_NOTIFY_KEY")
	if c.SyncNotifyKey != "" && len(c.SyncNotifyKey) < 32 {
		bad("SYNC_NOTIFY_KEY must contain at least 32 characters when configured")
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return c, nil
}

// ResolveSessionSecret makes sure SessionSecret is set. When SESSION_SECRET was not provided, a random key is
// created once in DATA_DIR/session.key (mode 0600) and reused on every start (and travels with the data volume).
func (c *Config) ResolveSessionSecret() error {
	if len(c.SessionSecret) > 0 {
		return nil
	}
	if err := os.MkdirAll(c.DataDir, 0o700); err != nil {
		return fmt.Errorf("create DATA_DIR: %w", err)
	}
	p := c.SessionKeyPath()
	if b, err := os.ReadFile(p); err == nil {
		if k, derr := hex.DecodeString(strings.TrimSpace(string(b))); derr == nil && len(k) >= 32 {
			c.SessionSecret = k
			return nil
		}
		// corrupt file: fall through and regenerate
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read session key: %w", err)
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return fmt.Errorf("generate session key: %w", err)
	}
	if err := os.WriteFile(p, []byte(hex.EncodeToString(k)+"\n"), 0o600); err != nil {
		return fmt.Errorf("write session key: %w", err)
	}
	c.SessionSecret = k
	return nil
}

// Summary is a secret-free view of the configuration (for the admin "Server" page and the startup log).
// Secrets are reported only as "set" / "not set".
func (c *Config) Summary() map[string]string {
	yn := func(b bool) string {
		if b {
			return "set"
		}
		return "not set"
	}
	m := map[string]string{
		"ROLE":                string(c.Role),
		"PORT":                strconv.Itoa(c.Port),
		"DATA_DIR":            c.DataDir,
		"FIREBASE_PROJECT_ID": c.FirebaseProjectID,
		"ALLOWED_ORIGINS":     strings.Join(c.AllowedOrigins, ","),
		"TRUST_PROXY":         strconv.FormatBool(c.TrustProxy),
		"COOKIE_SECURE":       strconv.FormatBool(c.CookieSecure),
		"SESSION_SECRET":      yn(len(c.SessionSecret) > 0),
		"SYNC_NOTIFY_KEY":     yn(c.SyncNotifyKey != ""),
	}
	if c.IsMain() {
		m["FIREBASE_API_KEY"] = yn(c.FirebaseAPIKey != "")
		m["ADMIN_UIDS"] = strconv.Itoa(len(c.AdminUIDs)) + " UID(s)"
		m["BACKUP_EVERY_HOURS"] = strconv.Itoa(c.BackupEveryHours)
		m["TG_KEEP"] = strconv.Itoa(c.TGKeep)
		m["TG_API_ID"] = yn(c.TGAPIID != 0)
		m["TG_API_HASH"] = yn(c.TGAPIHash != "")
		m["TG_CHAT_ID"] = yn(c.TGChatID != "")
	} else {
		m["MAIN_URL"] = c.MainURL
		m["PUBLIC_URL"] = c.PublicURL
		m["WORKER_KEY"] = yn(c.WorkerKey != "")
		m["SYNC_EVERY_SEC"] = strconv.Itoa(c.SyncEverySec)
	}
	return m
}
