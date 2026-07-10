package handlers

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/history"
	"github.com/lgldsilva/jackui/internal/jackett"
)

// hgCListIndexersXML mirrors the XML shape Jackett returns for ?t=indexers so
// the fake server can advertise a single configured indexer.
type hgCListIndexersXML struct {
	XMLName  xml.Name `xml:"indexers"`
	Indexers []struct {
		ID         string `xml:"id,attr"`
		Configured string `xml:"configured,attr"`
		Title      string `xml:"title"`
		Language   string `xml:"language"`
		Type       string `xml:"type"`
	} `xml:"indexer"`
}

// hgCJackettServer returns an httptest server that answers the indexers list
// and a single search result, so SearchSSE can run its full live flow.
func hgCJackettServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "indexers" {
			var resp hgCListIndexersXML
			resp.Indexers = append(resp.Indexers, struct {
				ID         string `xml:"id,attr"`
				Configured string `xml:"configured,attr"`
				Title      string `xml:"title"`
				Language   string `xml:"language"`
				Type       string `xml:"type"`
			}{ID: "idx1", Configured: "true", Title: "Indexer 1"})
			w.Header().Set("Content-Type", "application/xml")
			_ = xml.NewEncoder(w).Encode(resp)
			return
		}
		_, _ = w.Write([]byte(`{"Results":[{"Title":"Live Result","Tracker":"T1","Seeders":3,"Peers":5,"Size":1000,"InfoHash":"live123"}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func Test_hgC_SearchSSE_FullFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := hgCJackettServer(t)
	client := jackett.New(srv.URL, "key")

	store, err := history.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}
	// Seed one cached result so emitCachedResults runs its loop (>0 path).
	if err := store.Save("matrix", []jackett.Result{
		{Title: "Cached Matrix", Tracker: "T0", InfoHash: "cachedhash"},
	}, 0, false); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/api/search/stream", SearchSSE(client, store, nil, nil))

	req := httptest.NewRequest("GET", "/api/search/stream?q=matrix", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: done") {
		t.Errorf("missing done event; body: %s", body)
	}
	if !strings.Contains(body, "Cached Matrix") {
		t.Errorf("expected cached result emitted; body: %s", body)
	}
	if !strings.Contains(body, "Live Result") {
		t.Errorf("expected live result emitted; body: %s", body)
	}
}

func Test_hgC_SearchSSE_ListIndexersError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// Server always 500s → ListIndexers fails → StreamSearch returns error →
	// SearchSSE emits an `error` event (cachedCount==0 && live==0 path).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	client := jackett.New(srv.URL, "key")

	router := gin.New()
	router.GET("/api/search/stream", SearchSSE(client, nil, nil, nil))

	req := httptest.NewRequest("GET", "/api/search/stream?q=anything", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (SSE already started); body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: error") {
		t.Errorf("expected error event; body: %s", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Errorf("expected done event; body: %s", body)
	}
}

func Test_hgC_EmitCachedResults_WithStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := history.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("dune", []jackett.Result{
		{Title: "Dune A", InfoHash: "h1"},
		{Title: "Dune B", InfoHash: ""},
	}, 0, false); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	setSSEHeaders(c)

	enricher := buildEnricher(nil, nil, 0, false)
	seen, count := emitCachedResults(c, store, "dune", 0, false, nil, enricher)
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
	// Only the result with a non-empty InfoHash is recorded in seen.
	if !seen["h1"] {
		t.Errorf("seen missing h1: %v", seen)
	}
	if len(seen) != 1 {
		t.Errorf("len(seen) = %d, want 1", len(seen))
	}
}

func Test_hgC_EmitCachedResults_SkipsWhenIndexersScoped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := history.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("dune", []jackett.Result{
		{Title: "Dune A", InfoHash: "h1"},
		{Title: "Dune B", InfoHash: "h2"},
	}, 0, false); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	setSSEHeaders(c)

	enricher := buildEnricher(nil, nil, 0, false)
	// Scoped to specific indexers → cache phase is skipped (cached rows carry no
	// indexer id, so emitting them would leak other providers). Live search
	// handles the scoped query instead.
	seen, count := emitCachedResults(c, store, "dune", 0, false, []string{"knaben"}, enricher)
	if count != 0 {
		t.Errorf("count = %d, want 0 (cache skipped when indexers scoped)", count)
	}
	if len(seen) != 0 {
		t.Errorf("len(seen) = %d, want 0", len(seen))
	}
}

// hgCFakeFileInfo satisfies os.FileInfo enough for copyFileAndRemove's Mode()
// call on the error path (the file open fails before mode matters).
type hgCFakeFileInfo struct{ os.FileInfo }

func (hgCFakeFileInfo) Mode() os.FileMode { return 0o644 }
func (hgCFakeFileInfo) IsDir() bool       { return false }

// waitForLocalFile polls until path exists or the deadline passes.
func waitForLocalFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		<-time.After(2 * time.Millisecond) // cede a CPU à goroutine que cria o arquivo
	}
	t.Fatalf("file %q did not appear within %s", path, timeout)
}
