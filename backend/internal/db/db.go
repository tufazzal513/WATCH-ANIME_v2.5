// Package db opens the SQLite database (WAL) and runs embedded SQL migrations.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// DriverName is the database/sql driver name. It is provided by driver_modernc.go (pure Go, no CGO).
const DriverName = "sqlite"

var dsnEscaper = strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23")

// DSN builds a connection string with the pragmas every connection needs.
func DSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_txlock", "immediate")
	return "file:" + dsnEscaper.Replace(filepath.ToSlash(path)) + "?" + q.Encode()
}

// Open opens (creating if needed) the database file and verifies the connection.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	d, err := sql.Open(DriverName, DSN(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	d.SetMaxOpenConns(8)
	d.SetConnMaxIdleTime(5 * time.Minute)
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := d.PingContext(pctx); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("connect sqlite %s: %w", path, err)
	}
	return d, nil
}
