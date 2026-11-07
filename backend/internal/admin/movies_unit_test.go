package admin

import (
	"net/url"
	"testing"
)

func TestParseMovieForm(t *testing.T) {
	v := url.Values{"title": {"Example Film"}, "content_type": {"movie"}, "type": {"Movie"}, "year": {"2024"}, "rating": {"8.4"}, "poster_url": {"https://cdn.example/poster.jpg"}, "video_url": {"https://cdn.example/video.m3u8"}, "genres": {"Action", "Drama"}, "languages": {"Hindi", "Bengali"}, "dubbed": {"on"}, "is_private": {"on"}}
	in, m, err := parseMovieForm(v)
	if err != nil {
		t.Fatal(err)
	}
	if in.Title != "Example Film" || in.ContentType != "movie" || in.Year == nil || *in.Year != 2024 || in.Rating == nil || *in.Rating != 8.4 {
		t.Fatalf("unexpected parsed metadata: %#v", in)
	}
	if len(in.Genres) != 2 || len(in.Languages) != 2 || len(in.Links) != 1 || !in.Dubbed || !in.IsPrivate {
		t.Fatalf("unexpected parsed collections/flags: %#v", in)
	}
	if len(m.Links) != 1 || m.Links[0].URL != "https://cdn.example/video.m3u8" {
		t.Fatalf("link preview not preserved: %#v", m.Links)
	}
}

func TestParseMovieFormRejectsInvalidNumbersAndMissingArtwork(t *testing.T) {
	base := url.Values{"title": {"Film"}, "poster_url": {"https://cdn.example/poster.jpg"}}
	v := url.Values{}
	for k, vals := range base {
		v[k] = append([]string(nil), vals...)
	}
	v.Set("year", "twenty")
	if _, _, err := parseMovieForm(v); err == nil {
		t.Fatal("invalid year accepted")
	}
	v.Set("year", "")
	v.Set("rating", "eleven")
	if _, _, err := parseMovieForm(v); err == nil {
		t.Fatal("invalid rating accepted")
	}
	v.Set("rating", "")
	v.Set("poster_url", "")
	if _, _, err := parseMovieForm(v); err == nil {
		t.Fatal("missing poster accepted")
	}
}

func TestParseMovieFormUpcomingRequiresReleaseDate(t *testing.T) {
	v := url.Values{"title": {"Future Film"}, "poster_url": {"https://cdn.example/poster.jpg"}, "is_upcoming": {"on"}}
	if _, _, err := parseMovieForm(v); err == nil {
		t.Fatal("upcoming item without release date accepted")
	}
	v.Set("release_date", "2027-01-02T12:30")
	if _, _, err := parseMovieForm(v); err != nil {
		t.Fatalf("valid upcoming form rejected: %v", err)
	}
}

func TestParseMovieFormSeasonEpisodeAndMultipleLinks(t *testing.T) {
	v := url.Values{
		"title":            {"Example Series"},
		"content_type":     {"series"},
		"type":             {"Series"},
		"poster_url":       {"https://cdn.example/poster.jpg"},
		"seasons_json":     {`[{"Name":"Season 1","Episodes":[{"Name":"Episode 1","Links":[{"Kind":"stream","Quality":"1080p","Label":"Main","URL":"https://cdn.example/e1.m3u8"},{"Kind":"download","Quality":"720p","Label":"Mirror","URL":"https://cdn.example/e1.mp4"}]}]}]`},
		"movie_links_json": {`[{"Kind":"stream","Quality":"4K","Label":"Trailer","URL":"https://cdn.example/trailer.mp4"}]`},
	}
	in, m, err := parseMovieForm(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Seasons) != 1 || len(in.Seasons[0].Episodes) != 1 || len(in.Seasons[0].Episodes[0].Links) != 2 {
		t.Fatalf("season/episode/link tree not parsed: %#v", in.Seasons)
	}
	if len(in.Links) != 1 || in.Links[0].Quality != "4K" {
		t.Fatalf("movie links not parsed: %#v", in.Links)
	}
	if len(m.Seasons) != 1 || len(m.Seasons[0].Episodes[0].Links) != 2 {
		t.Fatalf("season preview not preserved: %#v", m.Seasons)
	}
}

func TestParseMovieFormRejectsMalformedSeasonJSON(t *testing.T) {
	v := url.Values{"title": {"Series"}, "content_type": {"series"}, "poster_url": {"https://cdn.example/poster.jpg"}, "seasons_json": {"not-json"}}
	if _, _, err := parseMovieForm(v); err == nil {
		t.Fatal("malformed season JSON accepted")
	}
}
