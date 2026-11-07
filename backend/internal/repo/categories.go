package repo

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"movex/internal/taxonomy"
)

type Category struct {
	ID         string
	Name       string
	Popular    bool
	Sort       int
	MovieCount int
}

var slugRe = regexp.MustCompile(`[^\p{L}\p{M}\p{N}]+`)

func slugify(name string) string {
	s := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if s == "" {
		s = "category"
	}
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	return s
}

func cleanCategoryName(name string) (string, error) {
	name, err := trimLen("name", name, 60)
	if err != nil {
		return "", err
	}
	if name == "" {
		return "", invalid("name", "is required")
	}
	return name, nil
}

func (r *Repo) ListCategories(ctx context.Context) ([]Category, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT c.id, c.name, c.popular, c.sort,
		(SELECT COUNT(*) FROM movie_categories mc WHERE mc.category_id = c.id)
		FROM categories c ORDER BY c.sort, c.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Category
	for rows.Next() {
		var c Category
		var pop int
		if err := rows.Scan(&c.ID, &c.Name, &pop, &c.Sort, &c.MovieCount); err != nil {
			return nil, err
		}
		c.Popular = pop == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListPublicCategories is ListCategories for the user app: the count only includes titles that are not private.
func (r *Repo) ListPublicCategories(ctx context.Context) ([]Category, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT c.id, c.name, c.popular, c.sort,
		(SELECT COUNT(*) FROM movie_categories mc JOIN movies m ON m.id = mc.movie_id WHERE mc.category_id = c.id AND m.is_private = 0)
		FROM categories c ORDER BY c.sort, c.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Category
	for rows.Next() {
		var c Category
		var pop int
		if err := rows.Scan(&c.ID, &c.Name, &pop, &c.Sort, &c.MovieCount); err != nil {
			return nil, err
		}
		c.Popular = pop == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Repo) GetCategory(ctx context.Context, id string) (*Category, error) {
	var c Category
	var pop int
	err := r.db.QueryRowContext(ctx, `SELECT id, name, popular, sort,
		(SELECT COUNT(*) FROM movie_categories WHERE category_id = categories.id) FROM categories WHERE id = ?`, id).
		Scan(&c.ID, &c.Name, &pop, &c.Sort, &c.MovieCount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	c.Popular = pop == 1
	return &c, err
}

func (r *Repo) uniqueCategoryID(ctx context.Context, q queryer, name string) (string, error) {
	base := slugify(name)
	for i := 0; i < 1000; i++ {
		id := base
		if i > 0 {
			id = base + "-" + strconv.Itoa(i+1)
		}
		var n int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM categories WHERE id = ?`, id).Scan(&n); err != nil {
			return "", err
		}
		if n == 0 {
			return id, nil
		}
	}
	return r.newID(), nil
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}

// CreateCategory adds a genre. It is placed after the existing ones when sort is 0.
func (r *Repo) CreateCategory(ctx context.Context, name string, popular bool, sort int) (*Category, error) {
	name, err := cleanCategoryName(name)
	if err != nil {
		return nil, err
	}
	var id string
	err = r.inTx(ctx, func(tx *sql.Tx) error {
		if id, err = r.uniqueCategoryID(ctx, tx, name); err != nil {
			return err
		}
		if sort == 0 {
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort), 0) + 10 FROM categories`).Scan(&sort); err != nil {
				return err
			}
		}
		now := r.nowMs()
		_, err := tx.ExecContext(ctx, `INSERT INTO categories(id, name, popular, sort, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
			id, name, b2i(popular), sort, now, now)
		return err
	})
	if isUniqueViolation(err) {
		return nil, invalid("name", "a category named %q already exists", name)
	}
	if err != nil {
		return nil, err
	}
	return r.GetCategory(ctx, id)
}

// UpdateCategory renames / re-flags a genre. A rename also refreshes the search text of its movies.
func (r *Repo) UpdateCategory(ctx context.Context, id, name string, popular bool, sort int) (*Category, error) {
	name, err := cleanCategoryName(name)
	if err != nil {
		return nil, err
	}
	err = r.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE categories SET name=?, popular=?, sort=?, updated_at=? WHERE id=?`,
			name, b2i(popular), sort, r.nowMs(), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return r.refreshSearchForCategory(ctx, tx, id)
	})
	if isUniqueViolation(err) {
		return nil, invalid("name", "a category named %q already exists", name)
	}
	if err != nil {
		return nil, err
	}
	return r.GetCategory(ctx, id)
}

func (r *Repo) movieIDsOfCategory(ctx context.Context, q queryer, id string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT movie_id FROM movie_categories WHERE category_id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		ids = append(ids, m)
	}
	return ids, rows.Err()
}

func (r *Repo) refreshSearchForCategory(ctx context.Context, q queryer, id string) error {
	ids, err := r.movieIDsOfCategory(ctx, q, id)
	if err != nil {
		return err
	}
	for _, m := range ids {
		if err := refreshSearch(ctx, q, m); err != nil {
			return err
		}
	}
	return nil
}

// DeleteCategory removes a genre from the list and from every movie that used it.
func (r *Repo) DeleteCategory(ctx context.Context, id string) error {
	return r.inTx(ctx, func(tx *sql.Tx) error {
		ids, err := r.movieIDsOfCategory(ctx, tx, id)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM categories WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		for _, m := range ids {
			if err := refreshSearch(ctx, tx, m); err != nil {
				return err
			}
		}
		return nil
	})
}

// ReorderCategories puts the listed categories first in the given order; the rest keep their order after them.
func (r *Repo) ReorderCategories(ctx context.Context, ids []string) error {
	return r.reorder(ctx, "categories", ids)
}

// SeedCategories fills an empty categories table with the standard genre list (idempotent).
// It goes through normal inserts, so the rows are also written to the replication log.
func (r *Repo) SeedCategories(ctx context.Context) (int, error) {
	n := 0
	err := r.inTx(ctx, func(tx *sql.Tx) error {
		var have int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM categories`).Scan(&have); err != nil {
			return err
		}
		if have > 0 {
			return nil
		}
		now := r.nowMs()
		for i, g := range taxonomy.Genres {
			id, err := r.uniqueCategoryID(ctx, tx, g)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO categories(id, name, popular, sort, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
				id, g, b2i(taxonomy.IsPopularGenre(g)), (i+1)*10, now, now); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}
