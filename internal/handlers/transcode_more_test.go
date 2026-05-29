package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/streamer"
	"github.com/luizg/jackui/internal/transcode"
)

func TestTranscodeCapabilities_Handler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/transcode/capabilities", nil)

	TranscodeCapabilities(c)

	// May return 200 with probed caps or 500 if probe fails
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 200 or 500; body: %s", w.Code, w.Body.String())
	}
}

func TestTranscodeCapabilities_WithRefresh(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/transcode/capabilities?refresh=1", nil)

	TranscodeCapabilities(c)

	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 200 or 500; body: %s", w.Code, w.Body.String())
	}
}

func TestTranscodeStream_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/stream/transcode/nothex/0", nil)

	TranscodeStream(nil, nil)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestTranscodeStream_BadFileIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/stream/transcode/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/notanumber", nil)

	TranscodeStream(nil, nil)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestTranscodeStream_ReaderNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = []gin.Param{
		{Key: "hash", Value: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{Key: "file", Value: "0"},
	}
	c.Request = httptest.NewRequest("GET", "/api/stream/transcode/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/0", nil)

	TranscodeStream(s, nil)(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestTranscodeActive_NilManager(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/transcode/active", nil)

	TranscodeActive(nil)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestTranscodeActive_WithManager(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr, _ := transcode.NewHLSManager(t.TempDir())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/transcode/active", nil)

	TranscodeActive(mgr)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestTranscodeKill_ValidKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr, _ := transcode.NewHLSManager(t.TempDir())

	router := gin.New()
	router.DELETE("/api/transcode/active/:key", TranscodeKill(mgr))

	req := httptest.NewRequest("DELETE", "/api/transcode/active/testkey", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
}

func TestGetGPUStats_ReturnsDefault(t *testing.T) {
	stats := getGPUStats()
	if stats == nil {
		t.Fatal("expected non-nil GPUInfo")
	}
	if stats.Type != "nvidia" && stats.Type != "vaapi" && stats.Type != "none" {
		t.Errorf("unexpected GPU type: %s", stats.Type)
	}
}

func TestTryServeFromCompleted_NilStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	result := tryServeFromCompleted(c, nil, "hash", 0, transcode.Options{})

	if result {
		t.Error("expected false for nil store")
	}
}

func TestBuildVODPlaylist_ZeroDuration(t *testing.T) {
	playlist := buildVODPlaylist(0, "")
	if len(playlist) == 0 {
		t.Error("expected non-empty playlist")
	}
	if !bytes.Contains(playlist, []byte("#EXT-X-ENDLIST")) {
		t.Error("expected EXT-X-ENDLIST")
	}
}

func TestBuildVODPlaylist_WithToken(t *testing.T) {
	playlist := buildVODPlaylist(10, "mytoken")
	if !bytes.Contains(playlist, []byte("?token=mytoken")) {
		t.Error("expected token in playlist URLs")
	}
}

func TestBuildVODPlaylist_SegmentCount(t *testing.T) {
	playlist := buildVODPlaylist(10, "")
	// 10 seconds / 4s segments = 3 segments (ceil)
	if !bytes.Contains(playlist, []byte("seg_00002")) {
		t.Error("expected at least seg_00002")
	}
}

func TestBuildLocalVODPlaylist_ZeroDuration(t *testing.T) {
	segURL := func(name string) string { return "/seg/" + name }
	playlist := buildLocalVODPlaylist(0, segURL)
	if len(playlist) == 0 {
		t.Error("expected non-empty playlist")
	}
	if !bytes.Contains(playlist, []byte("#EXT-X-ENDLIST")) {
		t.Error("expected EXT-X-ENDLIST")
	}
}

func TestBuildLocalVODPlaylist_WithSegments(t *testing.T) {
	segURL := func(name string) string { return "/hls/" + name }
	playlist := buildLocalVODPlaylist(10, segURL)
	if !bytes.Contains(playlist, []byte("/hls/seg_00000")) {
		t.Error("expected segment URL in playlist")
	}
}
