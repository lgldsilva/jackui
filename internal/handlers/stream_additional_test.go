package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/streamer"
)

func TestStreamDrop_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.DELETE("/api/stream/:hash", StreamDrop(s))

	req := httptest.NewRequest("DELETE", "/api/stream/nothex", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestStreamDrop_Valid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.DELETE("/api/stream/:hash", StreamDrop(s))

	req := httptest.NewRequest("DELETE", "/api/stream/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["message"] != "dropped" {
		t.Errorf("message = %q, want 'dropped'", resp["message"])
	}
}

func TestStreamPrefetch_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.POST("/api/stream/prefetch/:hash/:file", StreamPrefetch(s))

	req := httptest.NewRequest("POST", "/api/stream/prefetch/nothex/0", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestStreamPrefetch_BadFileIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.POST("/api/stream/prefetch/:hash/:file", StreamPrefetch(s))

	req := httptest.NewRequest("POST", "/api/stream/prefetch/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/notanumber", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestStreamPrefetch_NotActive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.POST("/api/stream/prefetch/:hash/:file", StreamPrefetch(s))

	req := httptest.NewRequest("POST", "/api/stream/prefetch/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/0", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestStreamProbe_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/probe/:hash/:file", StreamProbe(s))

	req := httptest.NewRequest("GET", "/api/stream/probe/nothex/0", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestStreamProbe_BadFileIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/probe/:hash/:file", StreamProbe(s))

	req := httptest.NewRequest("GET", "/api/stream/probe/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/notanumber", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestStreamSidecars_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/sidecars/:hash/:file", StreamSidecars(s))

	req := httptest.NewRequest("GET", "/api/stream/sidecars/nothex/0", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestStreamSidecarRead_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/sidecar/:hash/:file", StreamSidecarRead(s))

	req := httptest.NewRequest("GET", "/api/stream/sidecar/nothex/0", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestStreamSubtitleExtract_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/subtrack/:hash/:file/:track", StreamSubtitleExtract(s))

	req := httptest.NewRequest("GET", "/api/stream/subtrack/nothex/0/0", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestStreamSubtitleExtract_BadTrackIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/subtrack/:hash/:file/:track", StreamSubtitleExtract(s))

	req := httptest.NewRequest("GET", "/api/stream/subtrack/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/0/notanumber", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestStreamMetadata_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/metadata/:hash", StreamMetadata(s))

	req := httptest.NewRequest("GET", "/api/stream/metadata/nothex", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestStreamMetadata_NoCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/metadata/:hash", StreamMetadata(s))

	req := httptest.NewRequest("GET", "/api/stream/metadata/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound && w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 404 or 503; body: %s", w.Code, w.Body.String())
	}
}

func TestStreamArtwork_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/artwork/:hash/:file", StreamArtwork(s))

	req := httptest.NewRequest("GET", "/api/stream/artwork/nothex/0", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestStreamArtwork_BadFileIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/artwork/:hash/:file", StreamArtwork(s))

	req := httptest.NewRequest("GET", "/api/stream/artwork/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/notanumber", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestStreamHealth_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/health/:hash", StreamHealth(s))

	req := httptest.NewRequest("GET", "/api/stream/health/nothex", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestStreamHealth_UnknownHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/health/:hash", StreamHealth(s))

	req := httptest.NewRequest("GET", "/api/stream/health/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["known"] != false {
		t.Errorf("known = %v, want false", resp["known"])
	}
}

func TestStreamCacheStats_ReturnsStats(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/cache", StreamCacheStats(s))

	req := httptest.NewRequest("GET", "/api/stream/cache", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestStreamRateStats_ReturnsStats(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/rate", StreamRateStats(s))

	req := httptest.NewRequest("GET", "/api/stream/rate", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestStreamCacheClear_All(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.DELETE("/api/stream/cache", StreamCacheClear(s))

	req := httptest.NewRequest("DELETE", "/api/stream/cache", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestStreamCacheClear_ByEntry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.DELETE("/api/stream/cache", StreamCacheClear(s))

	req := httptest.NewRequest("DELETE", "/api/stream/cache?entry=test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestStreamFavorite_NoBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.POST("/api/stream/favorite", StreamFavorite(s))

	req := httptest.NewRequest("POST", "/api/stream/favorite", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestStreamFavorite_Uninitialized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.POST("/api/stream/favorite", StreamFavorite(s))

	body := map[string]string{"name": "Test", "infoHash": "aaa", "magnet": "magnet:?xt=urn:btih:aaa"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/stream/favorite", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
}

func TestStreamUnfavorite_Uninitialized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.DELETE("/api/stream/favorite/:name", StreamUnfavorite(s))

	req := httptest.NewRequest("DELETE", "/api/stream/favorite/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
}

func TestStreamFavorites_Uninitialized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/favorites", StreamFavorites(s))

	req := httptest.NewRequest("GET", "/api/stream/favorites", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var list []interface{}
	json.Unmarshal(w.Body.Bytes(), &list)
	if list == nil {
		t.Error("expected non-nil empty array")
	}
}

func TestStreamInfo_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/info/:hash", StreamInfo(s))

	req := httptest.NewRequest("GET", "/api/stream/info/nothex", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestStreamInfo_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/info/:hash", StreamInfo(s))

	req := httptest.NewRequest("GET", "/api/stream/info/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestStreamFile_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/:hash/:file", StreamFile(s, nil))

	req := httptest.NewRequest("GET", "/api/stream/nothex/0", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestStreamFile_BadFileIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/:hash/:file", StreamFile(s, nil))

	req := httptest.NewRequest("GET", "/api/stream/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/notanumber", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestStreamPlaylistM3U_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/playlist/:hash/:file.m3u", StreamPlaylistM3U(s))

	req := httptest.NewRequest("GET", "/api/stream/playlist/nothex/0.m3u", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestStreamSetFilePriority_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.POST("/api/stream/:hash/files/:idx/priority", StreamSetFilePriority(s))

	body := []byte(`{"priority":"high"}`)
	req := httptest.NewRequest("POST", "/api/stream/nothex/files/0/priority", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestStreamSetFilePriority_BadFileIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.POST("/api/stream/:hash/files/:idx/priority", StreamSetFilePriority(s))

	body := []byte(`{"priority":"high"}`)
	req := httptest.NewRequest("POST", "/api/stream/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/files/notanumber/priority", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestThumbnail_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/thumb/:hash/:file", StreamThumbnail(s))

	req := httptest.NewRequest("GET", "/api/stream/thumb/nothex/0", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestThumbnail_BadFileIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/thumb/:hash/:file", StreamThumbnail(s))

	req := httptest.NewRequest("GET", "/api/stream/thumb/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/notanumber", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestM3UFilename(t *testing.T) {
	cases := []struct {
		path     string
		expected string
	}{
		{"Season 1/S01E01 - Pilot.mkv", "S01E01 - Pilot"},
		{"movie.mp4", "movie"},
		{"path/to/file.with.dots.mkv", "file.with.dots"},
		{".hidden", ".hidden"}, // dot at position 0 means no extension to strip
		{"\"bad\"\\chars\r\n.mkv", "badchars"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			info := &streamer.TorrentInfo{
				Files: []streamer.FileInfo{{Path: tc.path}},
			}
			got := m3uFilename(info, 0)
			if got != tc.expected {
				t.Errorf("m3uFilename(%q) = %q, want %q", tc.path, got, tc.expected)
			}
		})
	}
}

func TestBuildStreamURL_NoToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/stream/aaa/0", nil)
	c.Request.Host = "localhost:8989"

	var h metainfo.Hash
	h.FromHexString("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	url := buildStreamURL(c, h, 0)

	if !strings.HasPrefix(url, "http://localhost:8989/api/stream/") {
		t.Errorf("url = %q, want http://localhost:8989/api/stream/...", url)
	}
	if strings.Contains(url, "token=") {
		t.Errorf("url = %q, should not contain token", url)
	}
}

func TestBuildStreamURL_WithTranscode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/stream/aaa/0?transcode=h264", nil)
	c.Request.Host = "localhost:8989"

	var h metainfo.Hash
	h.FromHexString("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	url := buildStreamURL(c, h, 0)

	if !strings.Contains(url, "transcode") {
		t.Errorf("url = %q, should contain transcode path", url)
	}
}

func TestAddTorrentRequest_NoMagnet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.POST("/api/stream/add", StreamAdd(s, nil))

	req := httptest.NewRequest("POST", "/api/stream/add", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] == "" {
		t.Error("expected error message")
	}
}

func TestAddTorrentFile_NoFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.POST("/api/stream/add-file", StreamAddTorrentFile(s))

	req := httptest.NewRequest("POST", "/api/stream/add-file", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestParseHash_Invalid(t *testing.T) {
	_, err := parseHash("nothex")
	if err == nil {
		t.Error("expected error for invalid hex")
	}
}

func TestParseHash_Valid(t *testing.T) {
	h, err := parseHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if h.HexString() != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("hex = %q", h.HexString())
	}
}

func TestDownloadsList_WithPromoted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/downloads", DownloadsList(store, s, "/downloads"))

	req := httptest.NewRequest("GET", "/api/downloads", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestDownloadsListAll_WithAuthStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dlStore := newDownloadsStore(t)

	router := gin.New()
	router.GET("/api/downloads/all", DownloadsListAll(dlStore, nil, nil))

	req := httptest.NewRequest("GET", "/api/downloads/all", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestDownloadsPause_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.PATCH("/api/downloads/:id/pause", DownloadsPause(store))

	req := httptest.NewRequest("PATCH", "/api/downloads/999/pause", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Get will fail for non-existent, returns 404
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestDownloadsResume_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.PATCH("/api/downloads/:id/resume", DownloadsResume(store))

	req := httptest.NewRequest("PATCH", "/api/downloads/999/resume", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}
