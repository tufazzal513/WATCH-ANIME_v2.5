package db

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

func fsOf(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for k, v := range files {
		m["migrations/"+k] = &fstest.MapFile{Data: []byte(v)}
	}
	return m
}

func TestLoadMigrationsOrdersAndValidates(t *testing.T) {
	ms, err := LoadMigrations(fsOf(map[string]string{"002_b.sql": "B", "001_a.sql": "A"}), "migrations")
	if err != nil || len(ms) != 2 || ms[0].Version != 1 || ms[0].SQL != "A" || ms[1].Name != "b" {
		t.Fatalf("%v %+v", err, ms)
	}
	bad := []map[string]string{
		{"001_a.sql": "x", "003_c.sql": "x"}, // gap
		{"001_a.sql": "x", "001_b.sql": "x"}, // duplicate
		{"1_a.sql": "x"},                     // bad name
		{"001_A.sql": "x"},                   // uppercase
		{"001_a.txt": "x"},                   // wrong extension
	}
	for i, b := range bad {
		if _, err := LoadMigrations(fsOf(b), "migrations"); err == nil {
			t.Errorf("case %d must fail", i)
		}
	}
}

func TestEmbeddedMigrationsAreValid(t *testing.T) {
	ms, err := LoadMigrations(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 || ms[0].Name != "meta" {
		t.Fatalf("expected 001_meta first: %+v", ms)
	}
	for _, m := range ms {
		if strings.TrimSpace(m.SQL) == "" {
			t.Errorf("migration %d is empty", m.Version)
		}
	}
}

func TestDSN(t *testing.T) {
	d := DSN("data/mo vex?.db")
	if !strings.HasPrefix(d, "file:data/mo vex%3f.db?") || !strings.Contains(d, "journal_mode%28WAL%29") {
		t.Fatalf("dsn: %s", d)
	}
}

func TestErrDBNewerIsSentinel(t *testing.T) {
	if !errors.Is(errors.Join(ErrDBNewer), ErrDBNewer) {
		t.Fatal("sentinel")
	}
}
