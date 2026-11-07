// Package repo holds every SQL query of the application, one file per area.
// Replication note: the `changes` log is written by database triggers (see migrations/002_catalog.sql),
// so any write made through this package (or anywhere else) is logged in the same transaction.
package repo

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrNotFound = errors.New("not found")
)

// ValidationError is returned for bad input; handlers map it to HTTP 400 / a form error.
type ValidationError struct {
	Field string
	Msg   string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Msg }

func invalid(field, format string, a ...any) error {
	return &ValidationError{Field: field, Msg: fmt.Sprintf(format, a...)}
}

// IsValidation reports whether err is (or wraps) a ValidationError.
func IsValidation(err error) bool {
	var v *ValidationError
	return errors.As(err, &v)
}

type Repo struct {
	db  *sql.DB
	now func() time.Time
}

func New(db *sql.DB) *Repo { return &Repo{db: db, now: time.Now} }

// DB exposes the handle for packages that need raw access (backup, replication).
func (r *Repo) DB() *sql.DB { return r.db }

func (r *Repo) nowMs() int64 { return r.now().UnixMilli() }

type queryer interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

// inTx runs fn in one write transaction (BEGIN IMMEDIATE via the DSN) and rolls back on error.
func (r *Repo) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

const idAlphabet = "0123456789abcdefghjkmnpqrstvwxyz" // Crockford base32, lowercase

// newID returns a 20-char id: 10 chars of millisecond time (sortable by creation) + 10 random chars.
func newID(t time.Time) string {
	var b [20]byte
	ms := uint64(t.UnixMilli())
	for i := 9; i >= 0; i-- {
		b[i] = idAlphabet[ms&31]
		ms >>= 5
	}
	var rnd [10]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	for i, x := range rnd {
		b[10+i] = idAlphabet[x&31]
	}
	return string(b[:])
}

func (r *Repo) newID() string { return newID(r.now()) }

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func idsToArgs(ids []string) []any {
	a := make([]any, len(ids))
	for i, v := range ids {
		a[i] = v
	}
	return a
}

// MaxSeq returns the newest assigned replication sequence, including rows already pruned from changes.
func (r *Repo) MaxSeq(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.QueryRowContext(ctx, `
		SELECT MAX(
			COALESCE((SELECT MAX(seq) FROM changes), 0),
			COALESCE((SELECT seq FROM sqlite_sequence WHERE name='changes'), 0)
		)
	`).Scan(&n)
	return n, err
}

// SeqFunc adapts MaxSeq for the server's health endpoint (errors read as 0).
func (r *Repo) SeqFunc() func() int64 {
	return func() int64 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		n, _ := r.MaxSeq(ctx)
		return n
	}
}

func trimLen(field, v string, max int) (string, error) {
	v = strings.TrimSpace(v)
	if len([]rune(v)) > max {
		return "", invalid(field, "must be at most %d characters", max)
	}
	return v, nil
}

func isHTTPURL(u string) bool {
	l := strings.ToLower(u)
	return (strings.HasPrefix(l, "http://") && len(l) > 7) || (strings.HasPrefix(l, "https://") && len(l) > 8)
}

// cleanURL trims and checks length and scheme (only http/https: no javascript:, data:, etc.).
func cleanURL(field, v string, required bool) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		if required {
			return "", invalid(field, "is required")
		}
		return "", nil
	}
	if len(v) > 2000 {
		return "", invalid(field, "is too long (max 2000 characters)")
	}
	if strings.ContainsAny(v, " \t\r\n") || !isHTTPURL(v) {
		return "", invalid(field, "must be a full http(s) URL")
	}
	return v, nil
}
