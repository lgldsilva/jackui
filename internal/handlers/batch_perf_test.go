package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/tmdb"
)

// batchReq builds a POST request with a JSON body for the batch handlers.
func batchReq(path, body string) *http.Request {
	req := httptest.NewRequest("POST", path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestStreamHealthBatch_EmptyHashes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	router := gin.New()
	router.POST("/api/stream/health/batch", StreamHealthBatch(s))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, batchReq("/api/stream/health/batch", `{"hashes":[]}`))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestStreamHealthBatch_TooMany(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	router := gin.New()
	router.POST("/api/stream/health/batch", StreamHealthBatch(s))

	hashes := make([]string, 301)
	for i := range hashes {
		hashes[i] = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	}
	body, _ := json.Marshal(map[string][]string{"hashes": hashes})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, batchReq("/api/stream/health/batch", string(body)))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413; body: %s", w.Code, w.Body.String())
	}
}

func TestStreamHealthBatch_UnknownAndInvalid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	router := gin.New()
	router.POST("/api/stream/health/batch", StreamHealthBatch(s))

	// One valid (but unknown to the streamer) hash + one malformed hash that must
	// be skipped without failing the whole batch.
	const valid = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, batchReq("/api/stream/health/batch", `{"hashes":["`+valid+`","nothex"]}`))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Results map[string]map[string]any `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := resp.Results[valid]; !ok {
		t.Errorf("valid hash missing from results: %v", resp.Results)
	}
	if _, ok := resp.Results["nothex"]; ok {
		t.Errorf("invalid hash should have been skipped: %v", resp.Results)
	}
	if known := resp.Results[valid]["known"]; known != false {
		t.Errorf("unknown torrent should report known=false, got %v", known)
	}
}

func TestTmdbMatchBatch_EmptyTitles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = batchReq("/api/tmdb/match/batch", `{"titles":[]}`)

	TmdbMatchBatch(nil)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestTmdbMatchBatch_NilClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = batchReq("/api/tmdb/match/batch", `{"titles":["Inception"]}`)

	TmdbMatchBatch(nil)(c)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
}

func TestTmdbMatchBatch_TooMany(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// Empty-key client: non-nil (so it passes the nil guard) but Match returns
	// ErrDisabled instantly — no network.
	cl, err := tmdb.New("", "", nil)
	if err != nil {
		t.Fatalf("tmdb.New: %v", err)
	}
	t.Cleanup(func() { cl.Close() })

	titles := make([]string, 101)
	for i := range titles {
		titles[i] = "x"
	}
	body, _ := json.Marshal(map[string][]string{"titles": titles})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = batchReq("/api/tmdb/match/batch", string(body))

	TmdbMatchBatch(cl)(c)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413; body: %s", w.Code, w.Body.String())
	}
}

func TestTmdbMatchBatch_ResolvesEmptyOnDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// Empty-key client → every Match returns ErrDisabled instantly, so the batch
	// completes (exercising the concurrency loop + JSON) with an empty map.
	cl, err := tmdb.New("", "", nil)
	if err != nil {
		t.Fatalf("tmdb.New: %v", err)
	}
	t.Cleanup(func() { cl.Close() })

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = batchReq("/api/tmdb/match/batch", `{"titles":["Inception","","The Matrix"]}`)

	TmdbMatchBatch(cl)(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "matches") {
		t.Errorf("body missing matches key: %s", body)
	}
}

func TestStreamMetadataBatch_EmptyHashes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	router := gin.New()
	router.POST("/api/stream/metadata/batch", StreamMetadataBatch(s))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, batchReq("/api/stream/metadata/batch", `{"hashes":[]}`))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestStreamMetadataBatch_CacheHit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	mc, err := streamer.NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	s.SetMetadataCache(mc)
	const hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := mc.Set(&streamer.TorrentInfo{
		InfoHash: hash, Name: "Batch Test", TotalSize: 42,
		Files: []streamer.FileInfo{{Index: 0, Path: "x.mkv", Size: 42, IsVideo: true}},
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	router := gin.New()
	router.POST("/api/stream/metadata/batch", StreamMetadataBatch(s))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, batchReq("/api/stream/metadata/batch", `{"hashes":["`+hash+`"]}`))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Results map[string]map[string]any `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Results[hash]["name"] != "Batch Test" {
		t.Errorf("results = %v", resp.Results)
	}
}
