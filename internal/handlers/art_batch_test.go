package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/streamer"
)

func TestResolveArtBatch_EmptyItems(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	router := gin.New()
	router.POST("/api/stream/art/resolve/batch", ResolveArtBatch(s, nil, nil, nil))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, batchReq("/api/stream/art/resolve/batch", `{"items":[]}`))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestResolveArtBatch_TooMany(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	router := gin.New()
	router.POST("/api/stream/art/resolve/batch", ResolveArtBatch(s, nil, nil, nil))

	items := make([]map[string]string, 51)
	for i := range items {
		items[i] = map[string]string{"hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	}
	body, _ := json.Marshal(map[string]any{"items": items})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, batchReq("/api/stream/art/resolve/batch", string(body)))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413; body: %s", w.Code, w.Body.String())
	}
}

func TestResolveArtBatch_ReusedArt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	cache, err := streamer.NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	s.SetMetadataCache(cache)

	const hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	_ = cache.SetArt(hash, &streamer.CachedArt{Source: "torrent", Path: "art.jpg"})

	router := gin.New()
	router.POST("/api/stream/art/resolve/batch", ResolveArtBatch(s, nil, nil, nil))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, batchReq("/api/stream/art/resolve/batch", `{"items":[{"hash":"`+hash+`","name":"Movie"}]}`))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Results map[string]map[string]any `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	r, ok := resp.Results[hash]
	if !ok {
		t.Fatalf("hash missing from results: %v", resp.Results)
	}
	if r["source"] != "torrent" {
		t.Errorf("source = %v, want torrent", r["source"])
	}
	if r["reused"] != true {
		t.Errorf("reused = %v, want true", r["reused"])
	}
}
