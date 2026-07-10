package jackett

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestListIndexers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") != "indexers" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		resp := listIndexersResponse{
			Indexers: []struct {
				ID         string `xml:"id,attr"`
				Configured string `xml:"configured,attr"`
				Title      string `xml:"title"`
				Language   string `xml:"language"`
				Type       string `xml:"type"`
			}{
				{ID: "1337x", Configured: "true", Title: "1337x", Language: "en", Type: "public"},
				{ID: "rarbg", Configured: "false", Title: "RARBG", Language: "en", Type: "public"},
				{ID: "nyaa", Configured: "true", Title: "Nyaa", Language: "ja", Type: "public"},
			},
		}
		w.Header().Set("Content-Type", "application/xml")
		xml.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	indexers, err := client.ListIndexers()
	if err != nil {
		t.Fatalf("ListIndexers: %v", err)
	}
	if len(indexers) != 2 {
		t.Fatalf("expected 2 indexers, got %d", len(indexers))
	}
	if indexers[0].ID != "1337x" || indexers[1].ID != "nyaa" {
		t.Fatalf("wrong indexers: %+v", indexers)
	}
}

func TestListIndexers_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "error")
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	_, err := client.ListIndexers()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSearchOnIndexer(t *testing.T) {
	var capturedIndexer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedIndexer = r.URL.Path
		w.Write([]byte(`{"Results":[{"Title":"Result 1","Tracker":"Tracker1","Seeders":10,"Peers":12,"Size":500}]}`))
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	results, err := client.SearchOnIndexer(context.Background(), "1337x", "test", "")
	if err != nil {
		t.Fatalf("SearchOnIndexer: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Title != "Result 1" {
		t.Fatalf("title = %q", results[0].Title)
	}
	if !strings.Contains(capturedIndexer, "1337x") {
		t.Fatalf("expected '1337x' in path, got %q", capturedIndexer)
	}
}

func TestSearchOnIndexer_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	_, err := client.SearchOnIndexer(context.Background(), "1337x", "test", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSearchOnIndexer_ContextCancel(t *testing.T) {
	// ctx is cancelled before the call, so the handler should never be reached.
	// If it ever is (a race), block until cleanup instead of racing to a 200 that
	// would mask the cancellation — no wall-clock wait when the handler isn't hit.
	block := make(chan struct{})
	defer close(block)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.SearchOnIndexer(ctx, "1337x", "test", "")
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

func TestStreamSearch_EmptyIndexers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := listIndexersResponse{Indexers: nil}
		w.Header().Set("Content-Type", "application/xml")
		xml.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	var hits []IndexerHit
	err := client.StreamSearch(context.Background(), "test", "", nil, time.Second, func(h IndexerHit) {
		hits = append(hits, h)
	})
	if err != nil {
		t.Fatalf("StreamSearch: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected 0 hits, got %d", len(hits))
	}
}

// TestStreamSearch_BoundsConcurrency guards the fix for the "Jackett tem N
// trackers mas o JackUI só mostra ~7" bug: StreamSearch must NOT fire every
// indexer at once (that saturates Jackett and the slow ones time out and are
// dropped). With many configured indexers, the number of in-flight per-indexer
// requests must never exceed maxConcurrentIndexerSearches.
func TestStreamSearch_BoundsConcurrency(t *testing.T) {
	const numIndexers = 40
	var inFlight, maxSeen, waiting int32
	// gate makes overlap deterministically observable without a sleep: every
	// per-indexer handler holds its slot until `gate` closes, and `gate` only
	// closes once maxConcurrentIndexerSearches handlers are simultaneously
	// in-flight. That proves the cap is actually reached; handlers scheduled
	// after the gate closes pass through immediately, so a non-divisible
	// remainder (40 % 12) can't deadlock.
	gate := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "indexers" {
			resp := listIndexersResponse{}
			for i := 0; i < numIndexers; i++ {
				resp.Indexers = append(resp.Indexers, struct {
					ID         string `xml:"id,attr"`
					Configured string `xml:"configured,attr"`
					Title      string `xml:"title"`
					Language   string `xml:"language"`
					Type       string `xml:"type"`
				}{ID: fmt.Sprintf("idx%d", i), Configured: "true", Title: fmt.Sprintf("Idx %d", i), Language: "en", Type: "public"})
			}
			w.Header().Set("Content-Type", "application/xml")
			_ = xml.NewEncoder(w).Encode(resp)
			return
		}
		cur := atomic.AddInt32(&inFlight, 1)
		for {
			old := atomic.LoadInt32(&maxSeen)
			if cur <= old || atomic.CompareAndSwapInt32(&maxSeen, old, cur) {
				break
			}
		}
		if atomic.AddInt32(&waiting, 1) == maxConcurrentIndexerSearches {
			once.Do(func() { close(gate) })
		}
		<-gate // hold the slot so overlap is observable
		atomic.AddInt32(&inFlight, -1)
		_, _ = w.Write([]byte(`{"Results":[]}`))
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	var done int32
	err := client.StreamSearch(context.Background(), "q", "", nil, 5*time.Second, func(IndexerHit) {
		atomic.AddInt32(&done, 1)
	})
	if err != nil {
		t.Fatalf("StreamSearch: %v", err)
	}
	if got := atomic.LoadInt32(&done); got != numIndexers {
		t.Fatalf("expected %d indexer hits, got %d", numIndexers, got)
	}
	if peak := atomic.LoadInt32(&maxSeen); peak == 0 {
		t.Fatal("no per-indexer requests observed")
	} else if peak > maxConcurrentIndexerSearches {
		t.Fatalf("peak concurrency %d exceeded cap %d", peak, maxConcurrentIndexerSearches)
	}
}

func TestStreamSearch_WithIndexers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "indexers" {
			resp := listIndexersResponse{
				Indexers: []struct {
					ID         string `xml:"id,attr"`
					Configured string `xml:"configured,attr"`
					Title      string `xml:"title"`
					Language   string `xml:"language"`
					Type       string `xml:"type"`
				}{
					{ID: "idx1", Configured: "true", Title: "Indexer 1"},
				},
			}
			w.Header().Set("Content-Type", "application/xml")
			xml.NewEncoder(w).Encode(resp)
			return
		}
		w.Write([]byte(`{"Results":[{"Title":"Stream Result","Tracker":"Tracker1","Seeders":5,"Peers":7,"Size":1000}]}`))
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	var hits []IndexerHit
	err := client.StreamSearch(context.Background(), "test", "", nil, time.Second, func(h IndexerHit) {
		hits = append(hits, h)
	})
	if err != nil {
		t.Fatalf("StreamSearch: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	if hits[0].Err != nil {
		t.Fatalf("unexpected error: %v", hits[0].Err)
	}
	if len(hits[0].Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(hits[0].Results))
	}
}

func TestStreamSearch_ListError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	err := client.StreamSearch(context.Background(), "test", "", nil, time.Second, func(h IndexerHit) {})
	if err == nil {
		t.Fatal("expected error listing indexers")
	}
}

func TestNew_TrimsTrailingSlash(t *testing.T) {
	c := New("http://jackett:9117/", "key")
	if c.URL != "http://jackett:9117" {
		t.Fatalf("URL = %q", c.URL)
	}
	if c.APIKey != "key" {
		t.Fatalf("APIKey = %q", c.APIKey)
	}
}

func TestFormatAge_DifferentFormats(t *testing.T) {
	now := time.Now().Add(-time.Hour)
	formats := []string{
		now.Format(time.RFC3339),
		now.Format("2006-01-02T15:04:05Z"),
		now.Format("2006-01-02T15:04:05"),
	}
	for _, f := range formats {
		got := formatAge(f)
		if !strings.Contains(got, "h ago") {
			t.Errorf("formatAge(%q) = %q, expected h ago", f, got)
		}
	}
}

func TestGetIndexers_RedirectToLogin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/UI/Login")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	indexers, err := client.GetIndexers()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(indexers) != 0 {
		t.Fatal("expected empty list on redirect")
	}
}

func TestGetIndexers_500Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	_, err := client.GetIndexers()
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestTestConnection_Redirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/UI/Login")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	err := client.TestConnection()
	if err == nil {
		t.Fatal("expected error on redirect")
	}
}

func TestTestConnection_Default(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	err := client.TestConnection()
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestSearchOnIndexer_WithCategory(t *testing.T) {
	var capturedCategory string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCategory = r.URL.Query().Get("Category[]")
		w.Write([]byte(`{"Results":[]}`))
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	_, err := client.SearchOnIndexer(context.Background(), "idx1", "test", "2000")
	if err != nil {
		t.Fatalf("SearchOnIndexer: %v", err)
	}
	if capturedCategory != "2000" {
		t.Fatalf("Category[] = %q", capturedCategory)
	}
}

func TestSearchOnIndexer_SkipsAllCategory(t *testing.T) {
	var capturedCategory string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCategory = r.URL.Query().Get("Category[]")
		w.Write([]byte(`{"Results":[]}`))
	}))
	defer srv.Close()

	client := New(srv.URL, "key")
	_, err := client.SearchOnIndexer(context.Background(), "idx1", "test", "all")
	if err != nil {
		t.Fatalf("SearchOnIndexer: %v", err)
	}
	if capturedCategory != "" {
		t.Fatalf("expected no category for 'all', got %q", capturedCategory)
	}
}
