package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestDedupeMatches(t *testing.T) {
	in := []Match{
		{TmdbID: 1, Kind: "movie", Title: "A"},
		{TmdbID: 2, Kind: "tv", Title: "B"},
		{TmdbID: 1, Kind: "movie", Title: "A (dup)"}, // repeat of #1 → dropped
		{TmdbID: 1, Kind: "tv", Title: "C"},          // movie/tv share an id space → kept
		{TmdbID: 2, Kind: "tv", Title: "B (dup)"},    // repeat of the tv #2 → dropped
	}
	out := dedupeMatches(in)

	if len(out) != 3 {
		t.Fatalf("expected 3 unique matches, got %d: %+v", len(out), out)
	}
	// First occurrence wins and original order is preserved.
	want := []struct {
		id   int
		kind string
	}{{1, "movie"}, {2, "tv"}, {1, "tv"}}
	for i, w := range want {
		if out[i].TmdbID != w.id || out[i].Kind != w.kind {
			t.Errorf("out[%d] = (%d,%q), want (%d,%q)", i, out[i].TmdbID, out[i].Kind, w.id, w.kind)
		}
	}
	if out[0].Title != "A" {
		t.Errorf("first occurrence should win, got title %q", out[0].Title)
	}
	// Input is never mutated.
	if len(in) != 5 {
		t.Errorf("dedupeMatches mutated its input: len now %d", len(in))
	}
}

func TestDedupeMatches_Empty(t *testing.T) {
	if out := dedupeMatches(nil); len(out) != 0 {
		t.Errorf("nil input should yield empty, got %+v", out)
	}
}

// TestTrending_DedupesRepeatsAcrossPages guards the reported Discover bug: TMDB
// can return the same title on more than one trending page, which reached the UI
// as two cards sharing one React key. The aggregated list must be deduped.
func TestTrending_DedupesRepeatsAcrossPages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/trending/all/week") {
			_, _ = w.Write([]byte(`{"results":[]}`))
			return
		}
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = w.Write([]byte(`{"results":[
				{"id":1,"media_type":"movie","title":"M1","poster_path":"/p1.jpg","popularity":50,"release_date":"2024-01-01"},
				{"id":2,"media_type":"tv","name":"T1","poster_path":"/p2.jpg","popularity":40,"first_air_date":"2023-01-01"}
			]}`))
		case "2":
			// id 1 (movie) repeats here → must be deduped; id 3 is new.
			_, _ = w.Write([]byte(`{"results":[
				{"id":1,"media_type":"movie","title":"M1 again","poster_path":"/p1.jpg","popularity":50,"release_date":"2024-01-01"},
				{"id":3,"media_type":"movie","title":"M3","poster_path":"/p3.jpg","popularity":30,"release_date":"2024-02-01"}
			]}`))
		default:
			_, _ = w.Write([]byte(`{"results":[]}`))
		}
	}))
	defer srv.Close()
	c := testClient(t, srv)

	items, err := c.Trending(context.Background())
	if err != nil {
		t.Fatalf("Trending: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 unique items (repeat deduped), got %d: %+v", len(items), items)
	}
	assertNoDupMatches(t, items)
}

func TestDiscover_DedupesRepeatsAcrossPages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		switch {
		case strings.Contains(r.URL.Path, "/discover/movie") && page == "1":
			_, _ = w.Write([]byte(`{"results":[{"id":1,"title":"Movie One","poster_path":"/m1.jpg","popularity":50,"release_date":"2024-05-01"}]}`))
		case strings.Contains(r.URL.Path, "/discover/movie") && page == "2":
			// id 1 repeats on page 2 (popularity ranking shifted) → deduped; id 5 new.
			_, _ = w.Write([]byte(`{"results":[
				{"id":1,"title":"Movie One again","poster_path":"/m1.jpg","popularity":50,"release_date":"2024-05-01"},
				{"id":5,"title":"Movie Five","poster_path":"/m5.jpg","popularity":10,"release_date":"2024-06-01"}
			]}`))
		case strings.Contains(r.URL.Path, "/discover/tv") && page == "1":
			_, _ = w.Write([]byte(`{"results":[{"id":2,"name":"Show Two","poster_path":"/t2.jpg","popularity":90,"first_air_date":"2024-03-01"}]}`))
		default:
			_, _ = w.Write([]byte(`{"results":[]}`))
		}
	}))
	defer srv.Close()
	c := testClient(t, srv)

	items, err := c.Discover(context.Background(), 2024, 28)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	// Unique: movie 1, tv 2, movie 5 → 3 (the movie-1 repeat is dropped).
	if len(items) != 3 {
		t.Fatalf("expected 3 unique items, got %d: %+v", len(items), items)
	}
	assertNoDupMatches(t, items)
}

func assertNoDupMatches(t *testing.T, items []Match) {
	t.Helper()
	seen := map[string]bool{}
	for _, m := range items {
		k := m.Kind + ":" + strconv.Itoa(m.TmdbID)
		if seen[k] {
			t.Errorf("duplicate match in result: %s", k)
		}
		seen[k] = true
	}
}
