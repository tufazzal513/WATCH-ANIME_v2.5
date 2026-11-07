package taxonomy

import "testing"

func TestListsMatchLegacy(t *testing.T) {
	if len(Genres) != 69 || len(Types) != 5 || len(Industries) != 36 || len(Languages) != 19 {
		t.Fatalf("counts changed: genres=%d types=%d industries=%d languages=%d", len(Genres), len(Types), len(Industries), len(Languages))
	}
	for _, p := range PopularGenres {
		if !contains(Genres, p) {
			t.Errorf("popular genre %q is not in Genres", p)
		}
	}
	for _, p := range PopularIndustries {
		if !IsIndustry(p) {
			t.Errorf("popular industry %q unknown", p)
		}
	}
	for _, p := range PopularLanguages {
		if !IsLanguage(p) {
			t.Errorf("popular language %q unknown", p)
		}
	}
	if !IsType("Anime") || IsType("anime") || IsIndustry("") {
		t.Fatal("membership checks wrong")
	}
}
