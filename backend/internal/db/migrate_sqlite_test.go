//go:build !nodriver

package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// Real-SQLite checks. Run with plain `go test ./...` (needs the modernc.org/sqlite module).
func TestMigrateIdempotentOnRealSQLite(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	first, err := Migrate(ctx, d)
	if err != nil || len(first) == 0 {
		t.Fatalf("first run: %v %v", first, err)
	}
	second, err := Migrate(ctx, d)
	if err != nil || len(second) != 0 {
		t.Fatalf("second run must apply nothing: %v %v", second, err)
	}

	var mode string
	if err := d.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode=%q err=%v", mode, err)
	}
	var fk int
	if err := d.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys=%d err=%v", fk, err)
	}
	if _, err := d.ExecContext(ctx, `INSERT INTO meta(k,v) VALUES('x','1')`); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseNewerThanProgramIsRefused(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := Migrate(ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `INSERT INTO schema_migrations(version,name,applied_at) VALUES(999,'future','x')`); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(ctx, d); !errors.Is(err, ErrDBNewer) {
		t.Fatalf("want ErrDBNewer, got %v", err)
	}
}

func TestFailedMigrationRollsBack(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	_, err = Apply(ctx, d, []Migration{
		{Version: 1, Name: "ok", SQL: `CREATE TABLE a(x)`},
		{Version: 2, Name: "boom", SQL: `CREATE TABLE b(x); THIS IS NOT SQL;`},
	})
	if err == nil {
		t.Fatal("expected failure")
	}
	var n int
	_ = d.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name='b'`).Scan(&n)
	if n != 0 {
		t.Fatal("failed migration must leave no partial table")
	}
	_ = d.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&n)
	if n != 1 {
		t.Fatalf("only version 1 recorded, got %d", n)
	}
}
