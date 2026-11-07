//go:build !nodriver

package repo

import (
	"context"
	"path/filepath"
	"testing"

	"movex/internal/db"
)

func newTestRepo(t *testing.T) *Repo {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(ctx, d); err != nil {
		t.Fatal(err)
	}
	r := New(d)
	if n, err := r.SeedCategories(ctx); err != nil || n != 69 {
		t.Fatalf("seed: %d %v", n, err)
	}
	return r
}

func intp(v int) *int           { return &v }
func floatp(v float64) *float64 { return &v }
func boolp(v bool) *bool        { return &v }

func basicMovie() MovieInput {
	return MovieInput{
		Title: "  The Test Movie ", Year: intp(2023), Rating: floatp(7.5),
		PosterURL: "https://img.example.com/p.jpg", Cast: "Alice, Bob", Description: "A film.",
		Industry: "Hollywood", Genres: []string{"Action", "sci-fi"}, Languages: []string{"english", "Hindi"},
		Links: []LinkInput{{URL: "https://cdn.example.com/m.m3u8"}},
	}
}

// changesAfter returns "tbl:row_id:op" entries logged after seq.
func changesAfter(t *testing.T, r *Repo, seq int64) []string {
	t.Helper()
	rows, err := r.db.Query(`SELECT tbl, row_id, op FROM changes WHERE seq > ? ORDER BY seq`, seq)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a, b, c string
		if err := rows.Scan(&a, &b, &c); err != nil {
			t.Fatal(err)
		}
		out = append(out, a+":"+b+":"+c)
	}
	return out
}

func mustSeq(t *testing.T, r *Repo) int64 {
	t.Helper()
	n, err := r.MaxSeq(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}
