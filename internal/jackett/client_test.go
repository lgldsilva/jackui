package jackett

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStripAPIKey(t *testing.T) {
	in := "http://127.0.0.1:9117/dl/idx/?jackett_apikey=keep&apikey=SECRET&path=abc&file=x"
	out := stripAPIKey(in)
	if strings.Contains(out, "apikey=SECRET") {
		t.Fatalf("apikey leaked: %q", out)
	}
	// Path + other params (incl. the differently-named jackett_apikey) survive.
	for _, keep := range []string{"path=abc", "file=x", "jackett_apikey=keep"} {
		if !strings.Contains(out, keep) {
			t.Fatalf("dropped %q from %q", keep, out)
		}
	}
	if got := stripAPIKey("http://x/dl?path=1"); got != "http://x/dl?path=1" {
		t.Fatalf("unexpected change: %q", got)
	}
	if stripAPIKey("") != "" {
		t.Fatal("empty should stay empty")
	}
}

// --- formatAge ---

func TestFormatAge_EmptyString(t *testing.T) {
	if got := formatAge(""); got != "unknown" {
		t.Errorf("formatAge('') = %q, want 'unknown'", got)
	}
}

func TestFormatAge_InvalidDate(t *testing.T) {
	input := "not-a-date"
	if got := formatAge(input); got != input {
		t.Errorf("formatAge(%q) = %q, want input back unchanged", input, got)
	}
}

func TestFormatAge_Table(t *testing.T) {
	cases := []struct {
		name     string
		offset   time.Duration
		contains string
	}{
		{"minutes", -30 * time.Minute, "m ago"},
		{"hours", -3 * time.Hour, "h ago"},
		{"days", -4 * 24 * time.Hour, "d ago"},
		{"weeks", -14 * 24 * time.Hour, "w ago"},
		{"months", -60 * 24 * time.Hour, "mo ago"},
		{"years", -400 * 24 * time.Hour, "y ago"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			date := time.Now().Add(tc.offset).Format(time.RFC3339)
			got := formatAge(date)
			if !strings.Contains(got, tc.contains) {
				t.Errorf("formatAge(%q) = %q, want it to contain %q", date, got, tc.contains)
			}
		})
	}
}

// --- Search ---

func makeJackettServer(t *testing.T, results []jackettResult) (*httptest.Server, *Client) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("apikey") == "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		json.NewEncoder(w).Encode(jackettResponse{Results: results})
	}))
	t.Cleanup(srv.Close)
	return srv, New(srv.URL, "testkey")
}

// TestSearchOnIndexer_PropagatesTrackerID guards the regression where the
// live-search (SSE) result mapping dropped TrackerId — the frontend discovers
// selectable indexers by trackerId, so without it an indexer like "amigosshare"
// got a wrong slug id ("jackui-club") that no longer matched Jackett.
func TestSearchOnIndexer_PropagatesTrackerID(t *testing.T) {
	_, client := makeJackettServer(t, []jackettResult{
		{Title: "X", Tracker: "Amigos Share Club", TrackerId: "amigosshare", InfoHash: "h", MagnetUri: "magnet:?xt=urn:btih:h"},
	})
	results, err := client.SearchOnIndexer(context.Background(), "amigosshare", "q", "")
	if err != nil {
		t.Fatalf("SearchOnIndexer failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].TrackerID != "amigosshare" {
		t.Errorf("TrackerID = %q, want %q (frontend keys selectable indexers off this)", results[0].TrackerID, "amigosshare")
	}
}

func TestSearch_ParsesResults(t *testing.T) {
	publishDate := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)

	_, client := makeJackettServer(t, []jackettResult{
		{
			Title:        "Ubuntu 24.04 LTS",
			Tracker:      "PublicHD",
			CategoryDesc: "TV",
			Category:     []int{5000},
			Size:         1024 * 1024 * 1024,
			Seeders:      100,
			Peers:        115,
			MagnetUri:    "magnet:?xt=urn:btih:abc123",
			Link:         "http://tracker.example.com/abc.torrent",
			InfoHash:     "abc123",
			PublishDate:  publishDate,
		},
	})

	results, err := client.Search("ubuntu", "", nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}

	r := results[0]
	if r.Title != "Ubuntu 24.04 LTS" {
		t.Errorf("Title = %q", r.Title)
	}
	if r.Tracker != "PublicHD" {
		t.Errorf("Tracker = %q", r.Tracker)
	}
	if r.CategoryID != 5000 {
		t.Errorf("CategoryID = %d, want 5000", r.CategoryID)
	}
	if r.Size != 1024*1024*1024 {
		t.Errorf("Size = %d", r.Size)
	}
	if r.Seeders != 100 {
		t.Errorf("Seeders = %d, want 100", r.Seeders)
	}
	// Leechers = Peers - Seeders
	if r.Leechers != 15 {
		t.Errorf("Leechers = %d, want 15 (peers 115 - seeders 100)", r.Leechers)
	}
	if r.MagnetURI != "magnet:?xt=urn:btih:abc123" {
		t.Errorf("MagnetURI = %q", r.MagnetURI)
	}
	if r.InfoHash != "abc123" {
		t.Errorf("InfoHash = %q", r.InfoHash)
	}
	if !strings.Contains(r.Age, "ago") {
		t.Errorf("Age = %q, want relative time", r.Age)
	}
}

func TestSearch_EmptyResultsFromJackett(t *testing.T) {
	_, client := makeJackettServer(t, nil)

	results, err := client.Search("noresults", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearch_UsesAllIndexerWhenNoneSpecified(t *testing.T) {
	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		json.NewEncoder(w).Encode(jackettResponse{})
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	_, err := client.Search("test", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(capturedPath, "/all/") {
		t.Errorf("expected 'all' in path, got %q", capturedPath)
	}
}

func TestSearch_UsesSpecificIndexer(t *testing.T) {
	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		json.NewEncoder(w).Encode(jackettResponse{})
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	_, err := client.Search("test", "", []string{"1337x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(capturedPath, "1337x") {
		t.Errorf("expected '1337x' in path, got %q", capturedPath)
	}
}

func TestSearch_SendsCategoryParam(t *testing.T) {
	var capturedCategory string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCategory = r.URL.Query().Get("Category[]")
		json.NewEncoder(w).Encode(jackettResponse{})
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	_, err := client.Search("test", "2000", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedCategory != "2000" {
		t.Errorf("Category[] = %q, want '2000'", capturedCategory)
	}
}

func TestSearch_OmitsCategoryParamWhenAll(t *testing.T) {
	var capturedCategory string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCategory = r.URL.Query().Get("Category[]")
		json.NewEncoder(w).Encode(jackettResponse{})
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	_, err := client.Search("test", "all", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedCategory != "" {
		t.Errorf("expected no Category[] param for 'all', got %q", capturedCategory)
	}
}

func TestSearch_ReturnsErrorOnHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	_, err := client.Search("test", "", nil)
	if err == nil {
		t.Error("expected error on 500 response, got nil")
	}
}

func TestSearch_NoCategoryEmitsNoParam(t *testing.T) {
	var capturedCategory string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCategory = r.URL.Query().Get("Category[]")
		json.NewEncoder(w).Encode(jackettResponse{})
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	_, _ = client.Search("test", "", nil)

	if capturedCategory != "" {
		t.Errorf("expected no Category[] param when empty, got %q", capturedCategory)
	}
}

// --- GetIndexers ---

func TestGetIndexers_OnlyReturnsConfigured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		indexers := []jackettIndexer{
			{ID: "1337x", Name: "1337x", Configured: true},
			{ID: "rarbg", Name: "RARBG", Configured: false},
			{ID: "nyaa", Name: "Nyaa", Configured: true},
		}
		json.NewEncoder(w).Encode(indexers)
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	indexers, err := client.GetIndexers()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(indexers) != 2 {
		t.Fatalf("len(indexers) = %d, want 2 configured", len(indexers))
	}

	ids := make(map[string]bool)
	for _, idx := range indexers {
		ids[idx.ID] = true
		if !idx.Configured {
			t.Errorf("indexer %q has Configured=false but was returned", idx.ID)
		}
	}
	if !ids["1337x"] || !ids["nyaa"] {
		t.Errorf("wrong indexers returned: %v", indexers)
	}
	if ids["rarbg"] {
		t.Error("unconfigured indexer 'rarbg' should not be returned")
	}
}

func TestGetIndexers_EmptyList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]jackettIndexer{})
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	indexers, err := client.GetIndexers()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(indexers) != 0 {
		t.Errorf("expected 0 indexers, got %d", len(indexers))
	}
}

// --- TestConnection ---

func TestTestConnection_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]jackettIndexer{})
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	if err := client.TestConnection(); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestTestConnection_InvalidKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	client := New(srv.URL, "wrongkey")
	if err := client.TestConnection(); err == nil {
		t.Error("expected error on forbidden response, got nil")
	}
}

func TestTestConnection_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	if err := client.TestConnection(); err == nil {
		t.Error("expected error on unauthorized response, got nil")
	}
}
