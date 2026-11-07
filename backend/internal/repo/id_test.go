package repo

import (
	"sort"
	"strings"
	"testing"
	"time"
)

func TestNewIDSortableAndUnique(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var ids []string
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		id := newID(base.Add(time.Duration(i) * time.Millisecond))
		if len(id) != 20 || strings.Trim(id, idAlphabet) != "" {
			t.Fatalf("bad id %q", id)
		}
		if seen[id] {
			t.Fatal("duplicate id")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatal("ids created later must sort later")
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{"Sci-Fi": "sci-fi", "LGBTQ+": "lgbtq", "Slice of Life": "slice-of-life", "  ": "category", "রোমান্স": "রোমান্স"}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestURLValidation(t *testing.T) {
	for _, bad := range []string{"javascript:alert(1)", "data:text/html,x", "ftp://x.com/a", "http://", "https://a b.com", "//cdn.example.com/x"} {
		if _, err := cleanURL("u", bad, true); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
	if u, err := cleanURL("u", "  https://cdn.example.com/a.m3u8?x=1  ", true); err != nil || u != "https://cdn.example.com/a.m3u8?x=1" {
		t.Fatalf("%q %v", u, err)
	}
	if u, err := cleanURL("u", "", false); err != nil || u != "" {
		t.Fatal("empty optional URL is fine")
	}
}
