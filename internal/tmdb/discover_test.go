package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiscover_MergesMovieAndTvByPopularity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only page 1 has results; page 2+ is empty (stops pagination).
		if r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`{"results":[]}`))
			return
		}
		switch {
		case strings.Contains(r.URL.Path, "/discover/movie"):
			_, _ = w.Write([]byte(`{"results":[{"id":1,"title":"Movie One","poster_path":"/m1.jpg","popularity":50,"release_date":"2024-05-01"}]}`))
		case strings.Contains(r.URL.Path, "/discover/tv"):
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
	if len(items) != 2 {
		t.Fatalf("expected 2 merged items, got %d", len(items))
	}
	// Sorted by popularity desc → the tv show (90) comes before the movie (50).
	if items[0].TmdbID != 2 || items[0].Kind != "tv" {
		t.Errorf("expected tv #2 first, got id=%d kind=%q", items[0].TmdbID, items[0].Kind)
	}
	if items[1].Kind != "movie" {
		t.Errorf("expected movie kind forced on discover/movie result, got %q", items[1].Kind)
	}
}

func TestDiscover_SkipsResultsWithoutPoster(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`{"results":[]}`))
			return
		}
		if strings.Contains(r.URL.Path, "/discover/movie") {
			_, _ = w.Write([]byte(`{"results":[{"id":1,"title":"No Poster","popularity":50},{"id":3,"title":"Has Poster","poster_path":"/p.jpg","popularity":40}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()
	c := testClient(t, srv)

	items, _ := c.Discover(context.Background(), 0, 18)
	if len(items) != 1 || items[0].TmdbID != 3 {
		t.Fatalf("expected only the poster-bearing result, got %+v", items)
	}
}

func TestGenres_MergesAndDedupes(t *testing.T) {
	genresMu.Lock()
	genresCache = nil // avoid cross-test cache leak (package-level cache)
	genresMu.Unlock()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/genre/movie/list"):
			_, _ = w.Write([]byte(`{"genres":[{"id":28,"name":"Ação"},{"id":18,"name":"Drama"}]}`))
		case strings.Contains(r.URL.Path, "/genre/tv/list"):
			_, _ = w.Write([]byte(`{"genres":[{"id":18,"name":"Drama"},{"id":10765,"name":"Sci-Fi & Fantasy"}]}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	c := testClient(t, srv)

	gs, err := c.Genres(context.Background())
	if err != nil {
		t.Fatalf("Genres: %v", err)
	}
	// 28, 18, 10765 — the duplicate 18 (Drama) is deduped → 3 unique.
	if len(gs) != 3 {
		t.Fatalf("expected 3 unique genres, got %d: %+v", len(gs), gs)
	}
	// Sorted by name: Ação, Drama, Sci-Fi…
	if gs[0].Name != "Ação" {
		t.Errorf("expected sorted by name, got first %q", gs[0].Name)
	}
}

func TestTrending_PaginatesAndTagsDirection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/trending/all/week") {
			_, _ = w.Write([]byte(`{"results":[]}`))
			return
		}
		if r.URL.Query().Get("page") == "1" {
			_, _ = w.Write([]byte(`{"results":[
				{"id":1,"media_type":"movie","title":"M1","poster_path":"/p1.jpg","popularity":50,"release_date":"2024-01-01"},
				{"id":2,"media_type":"tv","name":"T1","poster_path":"/p2.jpg","popularity":40,"first_air_date":"2023-01-01"},
				{"id":3,"media_type":"person","name":"Nobody","poster_path":"/p3.jpg"}
			]}`))
			return
		}
		_, _ = w.Write([]byte(`{"results":[]}`)) // page 2+ empty → stops pagination
	}))
	defer srv.Close()
	c := testClient(t, srv)

	items, err := c.Trending(context.Background())
	if err != nil {
		t.Fatalf("Trending: %v", err)
	}
	// person filtered out → 2 items; first run → all "new".
	if len(items) != 2 {
		t.Fatalf("expected 2 items (person dropped), got %d", len(items))
	}
	if items[0].Direction != "new" {
		t.Errorf("first run should tag items 'new', got %q", items[0].Direction)
	}
	// Second call hits the 6h in-memory cache (no HTTP) — still 2 items.
	cached, _ := c.Trending(context.Background())
	if len(cached) != 2 {
		t.Errorf("cached trending should return 2, got %d", len(cached))
	}
}

func TestDiscover_DisabledClient(t *testing.T) {
	c, _ := New("", "", t.TempDir()+"/tmdb.db")
	defer c.Close()
	if _, err := c.Discover(context.Background(), 2024, 0); err != ErrDisabled {
		t.Errorf("expected ErrDisabled, got %v", err)
	}
}
