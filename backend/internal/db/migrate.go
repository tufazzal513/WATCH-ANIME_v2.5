package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"time"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migration is one numbered SQL file, e.g. 001_meta.sql.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// ErrDBNewer means the database has migrations this binary does not know (e.g. a backup from a newer version).
var ErrDBNewer = errors.New("database schema is newer than this program; upgrade the program before using this database")

var fileRe = regexp.MustCompile(`^(\d{3,})_([a-z0-9_]+)\.sql$`)

// LoadMigrations reads NNN_name.sql files from dir. Versions must be unique and contiguous from 1.
func LoadMigrations(fsys fs.FS, dir string) ([]Migration, error) {
	ents, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	var ms []Migration
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		m := fileRe.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migration file %q must be named NNN_name.sql (lowercase, digits, underscore)", e.Name())
		}
		v, _ := strconv.Atoi(m[1])
		b, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		ms = append(ms, Migration{Version: v, Name: m[2], SQL: string(b)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].Version < ms[j].Version })
	for i, m := range ms {
		if m.Version != i+1 {
			return nil, fmt.Errorf("migrations must be numbered 001, 002, ... without gaps or duplicates (found version %d at position %d)", m.Version, i+1)
		}
	}
	return ms, nil
}

// Migrate applies the embedded migrations. Safe to call on every start (idempotent).
func Migrate(ctx context.Context, d *sql.DB) ([]int, error) {
	ms, err := LoadMigrations(migrationsFS, "migrations")
	if err != nil {
		return nil, err
	}
	return Apply(ctx, d, ms)
}

// Apply runs every migration that has not been applied yet, each in its own transaction, and returns the versions applied.
func Apply(ctx context.Context, d *sql.DB, ms []Migration) ([]int, error) {
	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TEXT NOT NULL)`); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}
	rows, err := d.QueryContext(ctx, `SELECT version, name FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	done := map[int]string{}
	for rows.Next() {
		var v int
		var n string
		if err := rows.Scan(&v, &n); err != nil {
			rows.Close()
			return nil, err
		}
		done[v] = n
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	known := map[int]string{}
	for _, m := range ms {
		known[m.Version] = m.Name
	}
	for v, n := range done {
		kn, ok := known[v]
		if !ok {
			return nil, fmt.Errorf("%w (database has version %d %q)", ErrDBNewer, v, n)
		}
		if kn != n {
			return nil, fmt.Errorf("migration %d is %q in the database but %q in this program", v, n, kn)
		}
	}

	var applied []int
	for _, m := range ms {
		if _, ok := done[m.Version]; ok {
			continue
		}
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			return applied, fmt.Errorf("begin migration %d: %w", m.Version, err)
		}
		if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
			_ = tx.Rollback()
			return applied, fmt.Errorf("migration %03d_%s failed: %w", m.Version, m.Name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)`,
			m.Version, m.Name, time.Now().UTC().Format(time.RFC3339)); err != nil {
			_ = tx.Rollback()
			return applied, fmt.Errorf("record migration %d: %w", m.Version, err)
		}
		if err := tx.Commit(); err != nil {
			return applied, fmt.Errorf("commit migration %d: %w", m.Version, err)
		}
		applied = append(applied, m.Version)
	}
	return applied, nil
}
