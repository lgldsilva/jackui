package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/streamer"
)

func TestBuildArtQuery_FromCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	cache, err := streamer.NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	s.SetMetadataCache(cache)

	_ = cache.Set(&streamer.TorrentInfo{
		InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Name:     "Test Movie 2024",
	})

	w := httptest.NewRecorder()
	cGin, _ := gin.CreateTestContext(w)
	cGin.Request = httptest.NewRequest("GET", "/api/stream/art/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/resolve", nil)

	a := &artResolveCtx{
		resp:  cGin,
		s:     s,
		cache: cache,
		hash:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	buildArtQuery(a)
	if a.rawName != "Test Movie 2024" {
		t.Errorf("rawName = %q, want 'Test Movie 2024'", a.rawName)
	}
	if a.query != "Test Movie 2024" {
		t.Errorf("query = %q, want 'Test Movie 2024'", a.query)
	}
}

func TestBuildArtQuery_FallbackToNameParam(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	cache, err := streamer.NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	s.SetMetadataCache(cache)

	w := httptest.NewRecorder()
	cGin, _ := gin.CreateTestContext(w)
	cGin.Request = httptest.NewRequest("GET", "/api/stream/art/hash/resolve?name=User+Supplied", nil)

	a := &artResolveCtx{
		resp:  cGin,
		s:     s,
		cache: cache,
		hash:  "hash",
	}
	buildArtQuery(a)
	if a.rawName != "User Supplied" {
		t.Errorf("rawName = %q, want 'User Supplied'", a.rawName)
	}
}

func TestIsAudioOnlyMeta_EmptyFiles(t *testing.T) {
	meta := &streamer.CachedMeta{Files: nil}
	if isAudioOnlyMeta(meta) {
		t.Error("expected false for nil files")
	}
}

func TestIsAudioOnlyMeta_HasVideo(t *testing.T) {
	meta := &streamer.CachedMeta{
		Files: []streamer.CachedFile{
			{IsVideo: true, Path: "movie.mp4"},
		},
	}
	if isAudioOnlyMeta(meta) {
		t.Error("expected false when video file exists")
	}
}

func TestIsAudioOnlyMeta_AudioOnly(t *testing.T) {
	meta := &streamer.CachedMeta{
		Files: []streamer.CachedFile{
			{IsVideo: false, Path: "song.mp3"},
			{IsVideo: false, Path: "cover.jpg"},
		},
	}
	if !isAudioOnlyMeta(meta) {
		t.Error("expected true when no video files")
	}
}

func TestBuildArtQuery_NilAIClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	cache, err := streamer.NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	s.SetMetadataCache(cache)

	w := httptest.NewRecorder()
	cGin, _ := gin.CreateTestContext(w)
	cGin.Request = httptest.NewRequest("GET", "/api/stream/art/hash/resolve?name=Test+Movie", nil)

	a := &artResolveCtx{
		resp:     cGin,
		s:        s,
		cache:    cache,
		hash:     "hash",
		aiClient: nil,
	}
	buildArtQuery(a)
	if a.query != "Test Movie" {
		t.Errorf("query = %q, want 'Test Movie'", a.query)
	}
}

func TestResolveArt_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.POST("/api/stream/art/:hash/resolve", ResolveArt(s, nil, nil, nil))

	req := httptest.NewRequest("POST", "/api/stream/art/nothex/resolve", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestResolveArt_NoCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.POST("/api/stream/art/:hash/resolve", ResolveArt(s, nil, nil, nil))

	req := httptest.NewRequest("POST", "/api/stream/art/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/resolve", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
}

func TestResolveArt_AlreadyHasTorrentArt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	cache, err := streamer.NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	s.SetMetadataCache(cache)

	_ = cache.SetArt("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", &streamer.CachedArt{
		Source: "torrent",
		Path:   "some/path.jpg",
	})

	router := gin.New()
	router.POST("/api/stream/art/:hash/resolve", ResolveArt(s, nil, nil, nil))

	req := httptest.NewRequest("POST", "/api/stream/art/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/resolve?file=0", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["source"] != "torrent" {
		t.Errorf("source = %v, want 'torrent'", resp["source"])
	}
	if resp["reused"] != true {
		t.Errorf("reused = %v, want true", resp["reused"])
	}
}

func TestStreamArt_TMDBRedirect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	cache, err := streamer.NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	s.SetMetadataCache(cache)

	_ = cache.SetArt("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", &streamer.CachedArt{
		Source:    "tmdb",
		PosterURL: "https://image.tmdb.org/poster.jpg",
	})

	router := gin.New()
	router.GET("/api/stream/art/:hash", StreamArt(s))

	req := httptest.NewRequest("GET", "/api/stream/art/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("status = %d, want 302; body: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if loc != "https://image.tmdb.org/poster.jpg" {
		t.Errorf("Location = %q, want TMDB URL", loc)
	}
}

func TestStreamArt_NoContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	cache, err := streamer.NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	s.SetMetadataCache(cache)

	router := gin.New()
	router.GET("/api/stream/art/:hash", StreamArt(s))

	req := httptest.NewRequest("GET", "/api/stream/art/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
}

func TestResolveArtHandler_NoResolvers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	cache, err := streamer.NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	s.SetMetadataCache(cache)

	router := gin.New()
	router.POST("/api/stream/art/:hash/resolve", ResolveArt(s, nil, nil, nil))

	req := httptest.NewRequest("POST", "/api/stream/art/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/resolve", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
}

func TestResolveTorrentArt_ExistingRankTooHigh(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	cache, err := streamer.NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	w := httptest.NewRecorder()
	cGin, _ := gin.CreateTestContext(w)
	cGin.Request = httptest.NewRequest("POST", "/", nil)

	a := &artResolveCtx{
		resp:         cGin,
		s:            s,
		cache:        cache,
		hash:         "hash",
		existingRank: streamer.ArtSourceRank("torrent") + 1,
	}
	if resolveTorrentArt(a) {
		t.Error("expected false when existingRank >= torrent rank")
	}
}

func TestResolveTorrentArt_TorrentNotActive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	cache, err := streamer.NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	w := httptest.NewRecorder()
	cGin, _ := gin.CreateTestContext(w)
	cGin.Request = httptest.NewRequest("POST", "/", nil)

	a := &artResolveCtx{
		resp:  cGin,
		s:     s,
		cache: cache,
		hash:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ctx:   cGin.Request.Context(),
	}
	if resolveTorrentArt(a) {
		t.Error("expected false when torrent is not active")
	}
}

func TestResolveTMDBArt_NilClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	cache, err := streamer.NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	w := httptest.NewRecorder()
	cGin, _ := gin.CreateTestContext(w)
	cGin.Request = httptest.NewRequest("POST", "/", nil)

	a := &artResolveCtx{
		resp:         cGin,
		s:            s,
		cache:        cache,
		hash:         "hash",
		tmdbClient:   nil,
		existingRank: streamer.ArtSourceRank("tmdb") - 1,
		query:        "Test Movie",
	}
	if resolveTMDBArt(a) {
		t.Error("expected false with nil tmdbClient")
	}
}

func TestResolveTMDBArt_ExistingRankTooHigh(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	cache, err := streamer.NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	w := httptest.NewRecorder()
	cGin, _ := gin.CreateTestContext(w)
	cGin.Request = httptest.NewRequest("POST", "/", nil)

	a := &artResolveCtx{
		resp:         cGin,
		s:            s,
		cache:        cache,
		hash:         "hash",
		tmdbClient:   nil,
		existingRank: streamer.ArtSourceRank("tmdb"),
		query:        "Test",
	}
	if resolveTMDBArt(a) {
		t.Error("expected false when existingRank >= tmdb rank")
	}
}

func TestResolveWebArt_NilSearch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	cache, err := streamer.NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	w := httptest.NewRecorder()
	cGin, _ := gin.CreateTestContext(w)
	cGin.Request = httptest.NewRequest("POST", "/", nil)

	a := &artResolveCtx{
		resp:         cGin,
		s:            s,
		cache:        cache,
		hash:         "hash",
		webSearch:    nil,
		existingRank: streamer.ArtSourceRank("web") - 1,
		query:        "Test Movie",
	}
	if resolveWebArt(a) {
		t.Error("expected false with nil webSearch")
	}
}

func TestResolveWebArt_ExistingRankTooHigh(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	cache, err := streamer.NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	w := httptest.NewRecorder()
	cGin, _ := gin.CreateTestContext(w)
	cGin.Request = httptest.NewRequest("POST", "/", nil)

	a := &artResolveCtx{
		resp:         cGin,
		s:            s,
		cache:        cache,
		hash:         "hash",
		webSearch:    nil,
		existingRank: streamer.ArtSourceRank("web"),
		query:        "Test",
	}
	if resolveWebArt(a) {
		t.Error("expected false when existingRank >= web rank")
	}
}

func TestResolveFrameCapture_NegativeFileIdx(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	cache, err := streamer.NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	s.SetMetadataCache(cache)

	w := httptest.NewRecorder()
	cGin, _ := gin.CreateTestContext(w)
	cGin.Request = httptest.NewRequest("POST", "/api/stream/art/hash/resolve", nil)

	a := &artResolveCtx{
		resp:    cGin,
		s:       s,
		cache:   cache,
		hash:    "hash",
		ctx:     cGin.Request.Context(),
		fileIdx: -1,
	}
	frameJobs := &sync.Map{}
	a.frameJobs = frameJobs

	if resolveFrameCapture(a) {
		t.Error("expected false for negative fileIdx")
	}
}

func TestResolveFrameCapture_AlreadyBusy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	cache, err := streamer.NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	s.SetMetadataCache(cache)

	frameJobs := &sync.Map{}
	frameJobs.Store("hash", true)

	w := httptest.NewRecorder()
	cGin, _ := gin.CreateTestContext(w)
	cGin.Request = httptest.NewRequest("POST", "/api/stream/art/hash/resolve?file=0", nil)

	a := &artResolveCtx{
		resp:      cGin,
		s:         s,
		cache:     cache,
		hash:      "hash",
		ctx:       cGin.Request.Context(),
		fileIdx:   0,
		frameJobs: frameJobs,
	}

	if !resolveFrameCapture(a) {
		t.Error("expected true (accepted) when already busy")
	}
	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202; body: %s", w.Code, w.Body.String())
	}
}
