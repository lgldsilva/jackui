package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/streamer"
)

func TestServeFromCompletedStoreNilStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	ok := serveFromCompletedStore(c, nil, metainfo.Hash{}, 0)
	if ok {
		t.Error("expected false with nil store")
	}
}

func TestServeFromStreamerNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	s := streamer.NewForTesting()
	h := metainfo.Hash{0x01}
	serveFromStreamer(c, s, h, 0)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

func TestStreamerGetNotFound(t *testing.T) {
	s := streamer.NewForTesting()
	h := metainfo.Hash{0x01}

	_, err := s.Get(h)
	if err == nil {
		t.Error("expected error for inactive torrent")
	}
}

func TestStreamInfoNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/info/:hash", StreamInfo(s))

	req := httptest.NewRequest("GET", "/api/stream/info/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestStreamFileNilStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/file/:hash/:file", StreamFile(s, nil))

	req := httptest.NewRequest("GET", "/api/stream/file/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/0", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

func TestStreamSubtitleExtractBadTrack(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.Default()
	router.GET("/api/stream/subtitle/:hash/:file/:track", StreamSubtitleExtract(s))

	req := httptest.NewRequest("GET", "/api/stream/subtitle/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/0/0", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", w.Code)
	}
}

func TestStreamSidecarReadBadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.Default()
	router.GET("/api/stream/sidecar/:hash/:file", StreamSidecarRead(s))

	req := httptest.NewRequest("GET", "/api/stream/sidecar/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/0", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502, body=%s", w.Code, w.Body.String())
	}
}

func TestStreamFavoritesUninitialized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.Default()
	router.GET("/api/stream/favorites", StreamFavorites(s))

	req := httptest.NewRequest("GET", "/api/stream/favorites", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

