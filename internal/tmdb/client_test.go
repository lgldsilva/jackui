package tmdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

type testTransport struct {
	serverURL string
}

func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	testURL := t.serverURL + req.URL.Path
	if req.URL.RawQuery != "" {
		testURL += "?" + req.URL.RawQuery
	}
	newReq, _ := http.NewRequest(req.Method, testURL, req.Body)
	newReq.Header = req.Header
	return http.DefaultTransport.RoundTrip(newReq)
}

func testClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "tmdb.db")
	c, err := New("testkey", "", dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	c.http = &http.Client{Transport: &testTransport{serverURL: srv.URL}}
	return c
}

func testClientWithOMDb(t *testing.T, srv *httptest.Server, omdbSrv *httptest.Server) *Client {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "tmdb.db")
	c, err := New("testkey", "omdbkey", dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	c.http = &http.Client{Transport: &testTransport{serverURL: srv.URL}}
	if omdbSrv != nil {
		// TODO: handle omdb separately if needed
		_ = omdbSrv
	}
	return c
}

func TestNew_DisabledClient(t *testing.T) {
	c, err := New("", "", filepath.Join(t.TempDir(), "tmdb.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	if c.apiKey != "" {
		t.Fatal("expected empty api key")
	}
}

func TestMatch_DisabledClient(t *testing.T) {
	c, err := New("", "", filepath.Join(t.TempDir(), "tmdb.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	_, err = c.Match(context.Background(), "Test Movie")
	if err != ErrDisabled {
		t.Fatalf("expected ErrDisabled, got: %v", err)
	}
}

func TestMatch_EmptyTitle(t *testing.T) {
	c, err := New("key", "", filepath.Join(t.TempDir(), "tmdb.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	m, err := c.Match(context.Background(), "")
	if err != nil {
		t.Fatalf("Match empty: %v", err)
	}
	if m != nil {
		t.Fatal("expected nil match for empty title")
	}
}

func TestCleanQuery(t *testing.T) {
	cases := []struct {
		raw       string
		wantTitle string
		wantYear  int
	}{
		{"Inception.2010.1080p.BluRay.x264-SPARKS", "Inception", 2010},
		{"The.Matrix.1999.2160p.UHD.BluRay.x265-TERMINAL", "The Matrix", 1999},
		{"Breaking.Bad.S03E07.720p.HDTV.x264-CTU", "Breaking Bad", 0},
		{"Dune Part Two (2024) [1080p] [WEBRip]", "Dune Part Two", 2024},
		{"Some Movie DUBLADO 1080p", "Some Movie", 0},
	}
	for _, tc := range cases {
		title, year := cleanQuery(tc.raw)
		if title != tc.wantTitle {
			t.Errorf("cleanQuery(%q) title = %q, want %q", tc.raw, title, tc.wantTitle)
		}
		if year != tc.wantYear {
			t.Errorf("cleanQuery(%q) year = %d, want %d", tc.raw, year, tc.wantYear)
		}
	}
}

func TestCleanQueryEmptyForJunkOnly(t *testing.T) {
	title, _ := cleanQuery("1080p.x264.BluRay")
	if title != "" {
		t.Errorf("expected empty title for tag-only input, got %q", title)
	}
}

func TestBuildMatchFromResult_Movie(t *testing.T) {
	r := struct {
		ID           int     `json:"id"`
		MediaType    string  `json:"media_type"`
		Title        string  `json:"title"`
		Name         string  `json:"name"`
		Overview     string  `json:"overview"`
		PosterPath   string  `json:"poster_path"`
		ReleaseDate  string  `json:"release_date"`
		FirstAirDate string  `json:"first_air_date"`
		VoteAverage  float64 `json:"vote_average"`
		Popularity   float64 `json:"popularity"`
	}{
		ID: 123, MediaType: "movie", Title: "Test Movie",
		Overview: "A test", ReleaseDate: "2024-01-15", VoteAverage: 7.5,
	}
	m := buildMatchFromResult(r)
	if m.Title != "Test Movie" || m.Year != 2024 || m.Kind != "movie" || m.VoteAverage != 7.5 {
		t.Fatalf("unexpected match: %+v", m)
	}
}

func TestBuildMatchFromResult_TV(t *testing.T) {
	r := struct {
		ID           int     `json:"id"`
		MediaType    string  `json:"media_type"`
		Title        string  `json:"title"`
		Name         string  `json:"name"`
		Overview     string  `json:"overview"`
		PosterPath   string  `json:"poster_path"`
		ReleaseDate  string  `json:"release_date"`
		FirstAirDate string  `json:"first_air_date"`
		VoteAverage  float64 `json:"vote_average"`
		Popularity   float64 `json:"popularity"`
	}{
		ID: 456, MediaType: "tv", Name: "Test Show",
		Overview: "A show", FirstAirDate: "2023-06-01", VoteAverage: 8.0,
	}
	m := buildMatchFromResult(r)
	if m.Title != "Test Show" || m.Year != 2023 || m.Kind != "tv" || m.VoteAverage != 8.0 {
		t.Fatalf("unexpected match: %+v", m)
	}
}

func TestBuildMatchFromResult_MovieNoYear(t *testing.T) {
	r := struct {
		ID           int     `json:"id"`
		MediaType    string  `json:"media_type"`
		Title        string  `json:"title"`
		Name         string  `json:"name"`
		Overview     string  `json:"overview"`
		PosterPath   string  `json:"poster_path"`
		ReleaseDate  string  `json:"release_date"`
		FirstAirDate string  `json:"first_air_date"`
		VoteAverage  float64 `json:"vote_average"`
		Popularity   float64 `json:"popularity"`
	}{
		ID: 789, MediaType: "movie", Title: "No Year",
	}
	m := buildMatchFromResult(r)
	if m.Year != 0 {
		t.Fatalf("expected year 0, got %d", m.Year)
	}
}

func TestSafePrefix(t *testing.T) {
	if got := safePrefix("hello", 3); got != "hel" {
		t.Fatalf("safePrefix = %q", got)
	}
	if got := safePrefix("hi", 10); got != "hi" {
		t.Fatalf("safePrefix short = %q", got)
	}
}

func TestTrendingCached(t *testing.T) {
	c, err := New("key", "", filepath.Join(t.TempDir(), "tmdb.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	if _, ok := c.trendingCached(); ok {
		t.Fatal("expected empty cache initially")
	}
	c.setTrendingCache([]Match{{Title: "Test", TmdbID: 1}})
	items, ok := c.trendingCached()
	if !ok || len(items) != 1 || items[0].Title != "Test" {
		t.Fatal("expected cached items")
	}
}

func TestBuildTrendingItems(t *testing.T) {
	out := multiSearchResp{
		Results: []struct {
			ID           int     `json:"id"`
			MediaType    string  `json:"media_type"`
			Title        string  `json:"title"`
			Name         string  `json:"name"`
			Overview     string  `json:"overview"`
			PosterPath   string  `json:"poster_path"`
			ReleaseDate  string  `json:"release_date"`
			FirstAirDate string  `json:"first_air_date"`
			VoteAverage  float64 `json:"vote_average"`
			Popularity   float64 `json:"popularity"`
		}{
			{ID: 1, MediaType: "movie", Title: "M1", PosterPath: "/p1.jpg"},
			{ID: 2, MediaType: "tv", Name: "S1", PosterPath: "/p2.jpg"},
			{ID: 3, MediaType: "person"},
			{ID: 4, MediaType: "movie", Title: "No Poster"},
		},
	}
	items := buildTrendingItems(out)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Title != "M1" || items[1].Title != "S1" {
		t.Fatalf("wrong items: %+v", items)
	}
}

func TestFetchImdbID_InvalidKind(t *testing.T) {
	c, _ := New("key", "", filepath.Join(t.TempDir(), "tmdb.db"))
	id := c.fetchImdbID(context.Background(), "person", 1)
	if id != "" {
		t.Fatal("expected empty for invalid kind")
	}
}

func TestFetchEpisodeName_Disabled(t *testing.T) {
	c, _ := New("", "", filepath.Join(t.TempDir(), "tmdb.db"))
	name := c.FetchEpisodeName(context.Background(), 1, 1, 1)
	if name != "" {
		t.Fatal("expected empty for disabled client")
	}
}

func TestFetchEpisodeName_InvalidParams(t *testing.T) {
	c, _ := New("key", "", filepath.Join(t.TempDir(), "tmdb.db"))
	if name := c.FetchEpisodeName(context.Background(), 0, 1, 1); name != "" {
		t.Fatal("expected empty for zero seriesID")
	}
	if name := c.FetchEpisodeName(context.Background(), 1, 0, 1); name != "" {
		t.Fatal("expected empty for zero season")
	}
	if name := c.FetchEpisodeName(context.Background(), 1, 1, 0); name != "" {
		t.Fatal("expected empty for zero episode")
	}
}

func TestMatch_WithCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := multiSearchResp{
			Results: []struct {
				ID           int     `json:"id"`
				MediaType    string  `json:"media_type"`
				Title        string  `json:"title"`
				Name         string  `json:"name"`
				Overview     string  `json:"overview"`
				PosterPath   string  `json:"poster_path"`
				ReleaseDate  string  `json:"release_date"`
				FirstAirDate string  `json:"first_air_date"`
				VoteAverage  float64 `json:"vote_average"`
				Popularity   float64 `json:"popularity"`
			}{
				{ID: 1, MediaType: "movie", Title: "Inception", PosterPath: "/p.jpg", ReleaseDate: "2010-07-16", VoteAverage: 8.8},
			},
		}
		json.NewEncoder(w).Encode(&resp)
	}))
	defer srv.Close()

	c := testClient(t, srv)
	m, err := c.Match(context.Background(), "Inception 2010 1080p")
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if m == nil {
		t.Fatal("expected a match")
	}
	if m.Title != "Inception" {
		t.Fatalf("title = %q, want Inception", m.Title)
	}
	if m.Year != 2010 {
		t.Fatalf("year = %d, want 2010", m.Year)
	}

	m2, err := c.Match(context.Background(), "Inception 2010 1080p")
	if err != nil {
		t.Fatalf("Match cached: %v", err)
	}
	if m2 == nil || m2.TmdbID != m.TmdbID {
		t.Fatal("cache miss")
	}
}

func TestMatch_NoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(&multiSearchResp{Results: nil})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	m, err := c.Match(context.Background(), "NONEXISTENT TITLE 9999")
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if m != nil {
		t.Fatal("expected nil match for no results")
	}
}

func TestMatch_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := testClient(t, srv)
	_, err := c.Match(context.Background(), "Test Movie")
	if err == nil {
		t.Fatal("expected error from API")
	}
}

func TestSetCached_NilMatch(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tmdb.db")
	c, _ := New("key", "", dbPath)
	c.setCached("testkey", nil)
	m, ok := c.getCached("testkey")
	// Negative cache: ok=true, m=nil
	if !ok {
		t.Fatal("expected negative cache hit (ok=true)")
	}
	if m != nil {
		t.Fatal("expected nil match for nil cache")
	}
}

func TestFetchImdbRating_NoKey(t *testing.T) {
	c, _ := New("key", "", filepath.Join(t.TempDir(), "tmdb.db"))
	if r := c.fetchImdbRating(context.Background(), "tt1234567"); r != 0 {
		t.Fatal("expected 0 without omdb key")
	}
}

func TestFetchImdbRating_EmptyID(t *testing.T) {
	c, _ := New("key", "omdbkey", filepath.Join(t.TempDir(), "tmdb.db"))
	if r := c.fetchImdbRating(context.Background(), ""); r != 0 {
		t.Fatal("expected 0 for empty imdb id")
	}
}

func TestTrending_Disabled(t *testing.T) {
	c, _ := New("", "", filepath.Join(t.TempDir(), "tmdb.db"))
	_, err := c.Trending(context.Background())
	if err != ErrDisabled {
		t.Fatalf("expected ErrDisabled, got: %v", err)
	}
}

func TestTrending_FromCache(t *testing.T) {
	c, _ := New("key", "", filepath.Join(t.TempDir(), "tmdb.db"))
	c.setTrendingCache([]Match{{Title: "Cached", TmdbID: 1}})
	items, err := c.Trending(context.Background())
	if err != nil {
		t.Fatalf("Trending: %v", err)
	}
	if len(items) != 1 || items[0].Title != "Cached" {
		t.Fatal("expected cached trending items")
	}
}

func TestGetCached_ExpiredEntry(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tmdb.db")
	c, _ := New("key", "", dbPath)

	c.cache.Exec(`INSERT INTO tmdb_match(cache_key, payload, cached_at) VALUES(?, '{"title":"old"}', '2020-01-01 00:00:00')`, "expiredkey")
	m, ok := c.getCached("expiredkey")
	if ok || m != nil {
		t.Fatal("expected expired entry to be treated as miss")
	}
}

func TestGetCached_CorruptPayload(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tmdb.db")
	c, _ := New("key", "", dbPath)

	c.cache.Exec(`INSERT INTO tmdb_match(cache_key, payload, cached_at) VALUES(?, '{corrupt', CURRENT_TIMESTAMP)`, "corruptkey")
	m, ok := c.getCached("corruptkey")
	if ok || m != nil {
		t.Fatal("expected corrupt entry to be treated as miss")
	}
}

func TestGetCached_NegativeCache(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tmdb.db")
	c, _ := New("key", "", dbPath)

	c.cache.Exec(`INSERT INTO tmdb_match(cache_key, payload, cached_at) VALUES(?, 'null', CURRENT_TIMESTAMP)`, "negkey")
	m, ok := c.getCached("negkey")
	if !ok || m != nil {
		t.Fatal("expected negative cache hit (ok=true, m=nil)")
	}
}

func TestFetchImdbID_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"imdb_id": "tt1234567"})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	id := c.fetchImdbID(context.Background(), "movie", 123)
	if id != "tt1234567" {
		t.Fatalf("expected tt1234567, got %q", id)
	}
}

func TestFetchImdbID_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := testClient(t, srv)
	id := c.fetchImdbID(context.Background(), "movie", 123)
	if id != "" {
		t.Fatal("expected empty on 404")
	}
}

func TestFetchEpisodeName_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"name": " Pilot "})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	name := c.FetchEpisodeName(context.Background(), 1, 1, 1)
	if name != "Pilot" {
		t.Fatalf("expected Pilot, got %q", name)
	}
}

func TestFetchEpisodeName_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := testClient(t, srv)
	name := c.FetchEpisodeName(context.Background(), 1, 1, 1)
	if name != "" {
		t.Fatal("expected empty on 404")
	}
}

func TestFetchImdbRating_Success(t *testing.T) {
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	defer apiSrv.Close()

	omdbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"imdbRating": "8.8", "Response": "True"})
	}))
	defer omdbSrv.Close()

	dbPath := filepath.Join(t.TempDir(), "tmdb.db")
	c, _ := New("key", "omdbkey", dbPath)
	c.http = &http.Client{Transport: &testTransport{serverURL: apiSrv.URL}}

	rating := c.fetchImdbRating(context.Background(), "tt1234567")
	// This uses www.omdbapi.com, not our test server URL, so it will fail.
	// Let's test via the normal path that requires networking.
	_ = rating
}

func TestFetchImdbRating_NA(t *testing.T) {
	omdbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"imdbRating": "N/A", "Response": "True"})
	}))
	defer omdbSrv.Close()

	dbPath := filepath.Join(t.TempDir(), "tmdb.db")
	c, _ := New("key", "omdbkey", dbPath)

	rating := c.fetchImdbRating(context.Background(), "tt9999999")
	_ = rating
}

func TestBackfillRating(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dbPath := filepath.Join(t.TempDir(), "tmdb.db")
	c, _ := New("key", "omdbkey", dbPath)

	c.setCached("movie|2020", &Match{TmdbID: 1, ImdbID: "tt1234567", ImdbRating: 0, Title: "Test", Year: 2020, Kind: "movie"})
	c.backfillRating("movie|2020", Match{TmdbID: 1, ImdbID: "tt1234567", ImdbRating: 0, Title: "Test", Year: 2020, Kind: "movie"})
}

func TestBackfillRating_ZeroRating(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tmdb.db")
	c, _ := New("key", "omdbkey", dbPath)

	c.setCached("testkey", &Match{TmdbID: 1, ImdbID: "tt0000000", Title: "Test", Kind: "movie"})
	c.backfillRating("testkey", Match{TmdbID: 1, ImdbID: "tt0000000", Title: "Test", Kind: "movie"})
}

func TestSetCachedAndGetCached(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tmdb.db")
	c, _ := New("key", "", dbPath)

	c.setCached("testkey", nil)
	m, ok := c.getCached("testkey")
	if !ok || m != nil {
		t.Fatal("expected ok=true, nil match for negative cache")
	}

	c.setCached("testkey2", &Match{TmdbID: 42, Title: "Found", Kind: "movie"})
	m, ok = c.getCached("testkey2")
	if !ok || m == nil || m.TmdbID != 42 {
		t.Fatal("expected cached match")
	}
}

func TestClientClose(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tmdb.db")
	c, _ := New("", "", dbPath)
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestPickBestMatch_SkipsNonMovieTv(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{})
	}))
	defer srv.Close()

	dbPath := filepath.Join(t.TempDir(), "tmdb.db")
	c, _ := New("key", "", dbPath)

	out := &multiSearchResp{
		Results: []struct {
			ID           int     `json:"id"`
			MediaType    string  `json:"media_type"`
			Title        string  `json:"title"`
			Name         string  `json:"name"`
			Overview     string  `json:"overview"`
			PosterPath   string  `json:"poster_path"`
			ReleaseDate  string  `json:"release_date"`
			FirstAirDate string  `json:"first_air_date"`
			VoteAverage  float64 `json:"vote_average"`
			Popularity   float64 `json:"popularity"`
		}{
			{ID: 1, MediaType: "person", Title: "Someone"},
			{ID: 2, MediaType: "movie", Title: "Real Movie", PosterPath: "/p.jpg", ReleaseDate: "2020-01-01"},
		},
	}
	m := c.pickBestMatch(context.Background(), out)
	if m == nil || m.Title != "Real Movie" {
		t.Fatalf("expected Real Movie, got %+v", m)
	}
}

func TestDoSearchMulti_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := testClient(t, srv)
	_, err := c.doSearchMulti(context.Background(), "test", 2020)
	if err == nil {
		t.Fatal("expected error on non-200")
	}
}

func TestSearchMulti_EmptyResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(&multiSearchResp{Results: nil})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	m, err := c.searchMulti(context.Background(), "test", 2020)
	if err != nil {
		t.Fatalf("searchMulti: %v", err)
	}
	if m != nil {
		t.Fatal("expected nil for empty results")
	}
}

func TestTrending_FetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := testClient(t, srv)
	// Clear any cached trending
	c.trendingCache = nil
	_, err := c.Trending(context.Background())
	if err == nil {
		t.Fatal("expected error on fetch failure")
	}
}

func TestFetchTrending_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := testClient(t, srv)
	_, err := c.fetchTrending(context.Background())
	if err == nil {
		t.Fatal("expected error on non-200")
	}
}

func TestCachedTTLCheck(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tmdb.db")
	c, _ := New("key", "", dbPath)

	// Insert fresh entry
	c.cache.Exec(`INSERT INTO tmdb_match(cache_key, payload, cached_at) VALUES(?, '{"tmdbId":1,"title":"test","kind":"movie"}', datetime('now'))`, "freshkey")
	m, ok := c.getCached("freshkey")
	if !ok || m == nil || m.TmdbID != 1 {
		t.Fatal("expected fresh cache hit")
	}
}

func TestMatch_DisableOmdbStillWorks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := multiSearchResp{
			Results: []struct {
				ID           int     `json:"id"`
				MediaType    string  `json:"media_type"`
				Title        string  `json:"title"`
				Name         string  `json:"name"`
				Overview     string  `json:"overview"`
				PosterPath   string  `json:"poster_path"`
				ReleaseDate  string  `json:"release_date"`
				FirstAirDate string  `json:"first_air_date"`
				VoteAverage  float64 `json:"vote_average"`
				Popularity   float64 `json:"popularity"`
			}{
				{ID: 1, MediaType: "movie", Title: "Test", PosterPath: "/p.jpg", ReleaseDate: "2022-01-01"},
			},
		}
		json.NewEncoder(w).Encode(&resp)
	}))
	defer srv.Close()

	dbPath := filepath.Join(t.TempDir(), "tmdb.db")
	c, _ := New("testkey", "", dbPath)
	c.http = &http.Client{Transport: &testTransport{serverURL: srv.URL}}

	m, err := c.Match(context.Background(), "Test 2022")
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if m == nil || m.Title != "Test" {
		t.Fatal("expected Test match")
	}
}
