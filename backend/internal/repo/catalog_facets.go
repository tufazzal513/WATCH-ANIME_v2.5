package repo

import "context"

// FacetValue is a public catalog value and the number of public titles using it.
type FacetValue struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type CatalogFacets struct {
	Categories []FacetValue `json:"categories"`
	Industries []FacetValue `json:"industries"`
	Languages  []FacetValue `json:"languages"`
	Types      []FacetValue `json:"types"`
	Years      []FacetValue `json:"years"`
}

// ListPublicCatalogFacets calculates Explore counts from the authoritative public catalog.
// Each query excludes private titles and ignores empty facet values.
func (r *Repo) ListPublicCatalogFacets(ctx context.Context) (CatalogFacets, error) {
	var out CatalogFacets
	queries := []struct {
		dst *[]FacetValue
		sql string
	}{
		{&out.Categories, `SELECT c.name, COUNT(DISTINCT m.id) FROM categories c JOIN movie_categories mc ON mc.category_id=c.id JOIN movies m ON m.id=mc.movie_id WHERE m.is_private=0 GROUP BY c.id,c.name ORDER BY c.sort,c.name COLLATE NOCASE`},
		{&out.Industries, `SELECT industry, COUNT(*) FROM movies WHERE is_private=0 AND TRIM(industry)<>'' GROUP BY industry ORDER BY industry COLLATE NOCASE`},
		{&out.Languages, `SELECT ml.language, COUNT(DISTINCT m.id) FROM movie_languages ml JOIN movies m ON m.id=ml.movie_id WHERE m.is_private=0 AND TRIM(ml.language)<>'' GROUP BY ml.language ORDER BY ml.language COLLATE NOCASE`},
		{&out.Types, `SELECT type, COUNT(*) FROM movies WHERE is_private=0 AND TRIM(type)<>'' GROUP BY type ORDER BY type COLLATE NOCASE`},
		{&out.Years, `SELECT CAST(year AS TEXT), COUNT(*) FROM movies WHERE is_private=0 AND year IS NOT NULL GROUP BY year ORDER BY year DESC`},
	}
	for _, item := range queries {
		rows, err := r.db.QueryContext(ctx, item.sql)
		if err != nil {
			return CatalogFacets{}, err
		}
		values := make([]FacetValue, 0)
		for rows.Next() {
			var value FacetValue
			if err := rows.Scan(&value.Name, &value.Count); err != nil {
				rows.Close()
				return CatalogFacets{}, err
			}
			if value.Name != "" && value.Count > 0 {
				values = append(values, value)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return CatalogFacets{}, err
		}
		if err := rows.Close(); err != nil {
			return CatalogFacets{}, err
		}
		*item.dst = values
	}
	return out, nil
}
