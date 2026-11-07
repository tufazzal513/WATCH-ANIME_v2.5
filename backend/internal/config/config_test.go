package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

var mainBase = map[string]string{
	"FIREBASE_PROJECT_ID": "proj",
	"FIREBASE_API_KEY":    "key",
	"ADMIN_UIDS":          "u1, u2",
}

func with(base map[string]string, kv ...string) map[string]string {
	m := map[string]string{}
	for k, v := range base {
		m[k] = v
	}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func TestMainDefaults(t *testing.T) {
	c, err := Load(env(mainBase))
	if err != nil {
		t.Fatal(err)
	}
	if c.Role != RoleMain || c.Port != 3000 || c.DataDir != "./data" || !c.CookieSecure || c.TrustProxy {
		t.Fatalf("bad defaults: %+v", c)
	}
	if c.BackupEveryHours != 12 || c.TGKeep != 30 || len(c.AdminUIDs) != 2 || c.AdminUIDs[1] != "u2" {
		t.Fatalf("bad main defaults: %+v", c)
	}
	if len(c.AllowedOrigins) != 1 || c.AllowedOrigins[0] != "*" {
		t.Fatalf("origins: %v", c.AllowedOrigins)
	}
	if c.TelegramConfigured() {
		t.Fatal("telegram must not be configured")
	}
	if c.Summary()["SYNC_NOTIFY_KEY"] != "not set" {
		t.Fatal("notification key summary must be secret-free")
	}
	c2, err := Load(env(with(mainBase, "SYNC_NOTIFY_KEY", strings.Repeat("n", 40))))
	if err != nil || c2.Summary()["SYNC_NOTIFY_KEY"] != "set" {
		t.Fatalf("valid notification key: %v", err)
	}
}

func TestMissingRequiredReportsAll(t *testing.T) {
	_, err := Load(env(map[string]string{}))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"FIREBASE_PROJECT_ID", "FIREBASE_API_KEY", "ADMIN_UIDS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s: %v", want, err)
		}
	}
}

func TestInvalidValues(t *testing.T) {
	cases := map[string][]string{
		"ROLE":            {"ROLE", "boss"},
		"PORT":            {"PORT", "70000"},
		"ALLOWED_ORIGINS": {"ALLOWED_ORIGINS", "https://a.com/path"},
		"COOKIE_SECURE":   {"COOKIE_SECURE", "maybe"},
		"SESSION_SECRET":  {"SESSION_SECRET", "short"},
		"TG_KEEP":         {"TG_KEEP", "1"},
		"SYNC_NOTIFY_KEY": {"SYNC_NOTIFY_KEY", "too-short"},
	}
	for name, kv := range cases {
		if _, err := Load(env(with(mainBase, kv...))); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: want error mentioning it, got %v", name, err)
		}
	}
}

func TestOriginsTrimmed(t *testing.T) {
	c, err := Load(env(with(mainBase, "ALLOWED_ORIGINS", "https://a.github.io/, http://localhost:5500")))
	if err != nil {
		t.Fatal(err)
	}
	if c.AllowedOrigins[0] != "https://a.github.io" || c.AllowedOrigins[1] != "http://localhost:5500" {
		t.Fatalf("%v", c.AllowedOrigins)
	}
}

func TestTelegramAllOrNone(t *testing.T) {
	if _, err := Load(env(with(mainBase, "TG_API_ID", "123456"))); err == nil {
		t.Fatal("partial telegram config must fail")
	}
	c, err := Load(env(with(mainBase, "TG_API_ID", "123456", "TG_API_HASH", "abc", "TG_CHAT_ID", "-1001234567890")))
	if err != nil || !c.TelegramConfigured() {
		t.Fatalf("full telegram config: %v %v", c, err)
	}
	if _, err := Load(env(with(mainBase, "TG_API_ID", "123456", "TG_API_HASH", "abc", "TG_CHAT_ID", "nope"))); err == nil {
		t.Fatal("bad chat id must fail")
	}
}

func TestDevInsecureAuthRefusedInProduction(t *testing.T) {
	_, err := Load(env(with(mainBase, "DEV_INSECURE_AUTH", "1")))
	if !errors.Is(err, ErrDevAuthInProduction) {
		t.Fatalf("want ErrDevAuthInProduction, got %v", err)
	}
	c, err := Load(env(with(mainBase, "DEV_INSECURE_AUTH", "1", "COOKIE_SECURE", "0")))
	if err != nil || !c.DevInsecureAuth {
		t.Fatalf("local dev must be allowed: %v", err)
	}
}

func TestWorker(t *testing.T) {
	if _, err := Load(env(map[string]string{"ROLE": "worker", "FIREBASE_PROJECT_ID": "p"})); err == nil {
		t.Fatal("worker without MAIN_URL/WORKER_KEY/PUBLIC_URL must fail")
	}
	c, err := Load(env(map[string]string{
		"ROLE": "worker", "FIREBASE_PROJECT_ID": "p",
		"MAIN_URL": "https://main.example.com/", "WORKER_KEY": "k", "PUBLIC_URL": "https://w1.example.com",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.MainURL != "https://main.example.com" || c.SyncEverySec != 60 || !c.IsWorker() {
		t.Fatalf("%+v", c)
	}
	if _, err := Load(env(map[string]string{
		"ROLE": "worker", "FIREBASE_PROJECT_ID": "p",
		"MAIN_URL": "main.example.com", "WORKER_KEY": "k", "PUBLIC_URL": "https://w1.example.com",
	})); err == nil {
		t.Fatal("MAIN_URL without scheme must fail")
	}
}

func TestSessionSecretGeneratedOnceAndReused(t *testing.T) {
	dir := t.TempDir()
	c1, _ := Load(env(with(mainBase, "DATA_DIR", dir)))
	if err := c1.ResolveSessionSecret(); err != nil {
		t.Fatal(err)
	}
	if len(c1.SessionSecret) != 32 {
		t.Fatalf("len=%d", len(c1.SessionSecret))
	}
	st, err := os.Stat(filepath.Join(dir, "session.key"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("session.key missing or wrong mode: %v %v", err, st)
	}
	c2, _ := Load(env(with(mainBase, "DATA_DIR", dir)))
	if err := c2.ResolveSessionSecret(); err != nil {
		t.Fatal(err)
	}
	if string(c1.SessionSecret) != string(c2.SessionSecret) {
		t.Fatal("secret must be stable across restarts")
	}
}

func TestSessionSecretFromEnvNotWritten(t *testing.T) {
	dir := t.TempDir()
	c, _ := Load(env(with(mainBase, "DATA_DIR", dir, "SESSION_SECRET", strings.Repeat("x", 40))))
	if err := c.ResolveSessionSecret(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "session.key")); !os.IsNotExist(err) {
		t.Fatal("no key file expected when SESSION_SECRET is given")
	}
}

func TestSummaryHasNoSecrets(t *testing.T) {
	c, _ := Load(env(with(mainBase, "SESSION_SECRET", strings.Repeat("S", 40), "TG_API_ID", "123456", "TG_API_HASH", "HASHVALUE", "TG_CHAT_ID", "@mychannel")))
	_ = c.ResolveSessionSecret()
	for k, v := range c.Summary() {
		for _, secret := range []string{"key", strings.Repeat("S", 40), "HASHVALUE"} {
			if v == secret && k != "FIREBASE_PROJECT_ID" {
				t.Errorf("Summary leaks a secret in %s", k)
			}
		}
	}
}
