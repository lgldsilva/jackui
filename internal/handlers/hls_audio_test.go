package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
)

const testHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// hlsSessionKeyFromReq: a/:track → -ao{track}; v/:variant → -v{variant}; neither → base.
// Exercised through a real gin router to work out the path-param reading.
func TestHlsSessionKeyFromReqRouting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	keyOf := func(c *gin.Context) {
		var h [20]byte
		copy(h[:], mustHash(t))
		c.String(http.StatusOK, hlsSessionKeyFromReq(c, h, 3))
	}
	r.GET("/api/stream/hls/:hash/:file/a/:track/index.m3u8", keyOf)
	r.GET("/api/stream/hls/:hash/:file/v/:variant/index.m3u8", keyOf)
	r.GET("/api/stream/hls/:hash/:file/index.m3u8", keyOf)

	cases := []struct {
		path, wantSuffix string
	}{
		{"/api/stream/hls/" + testHash + "/3/a/2/index.m3u8", "-ao2"},
		{"/api/stream/hls/" + testHash + "/3/v/1/index.m3u8", "-v1"},
		{"/api/stream/hls/" + testHash + "/3/index.m3u8", "-3"},
		{"/api/stream/hls/" + testHash + "/3/index.m3u8?playback=viewer-a", "-pviewer-a"},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, c.path, nil))
		if !strings.HasSuffix(w.Body.String(), c.wantSuffix) {
			t.Errorf("%s → key %q, want sufixo %q", c.path, w.Body.String(), c.wantSuffix)
		}
	}
}

func mustHash(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 20)
	for i := range b {
		b[i] = 0xaa
	}
	return b
}

// StreamHLSAudio (a/:track) matches the route and runs the audio-only glue without a
// torrent (source doesn't resolve → non-200, but not 'variant out of range' nor NoRoute).
func TestStreamHLSAudioResolves(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr, err := transcode.NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	r := gin.New()
	r.NoRoute(func(c *gin.Context) { c.String(599, "NOROUTE") })
	r.GET("/api/stream/hls/:hash/:file/a/:track/index.m3u8", StreamHLSAudio(streamer.NewForTesting(), mgr, nil))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/stream/hls/"+testHash+"/0/a/2/index.m3u8", nil))
	if w.Code == 599 {
		t.Errorf("a/:track route did not match (NoRoute)")
	}
	if w.Code == http.StatusOK {
		t.Errorf("without a torrent it should not be 200; got %d", w.Code)
	}
}

// StreamHLSSubtitle serves the WebVTT mini-playlist (probe fails → fallback
// duration, but the body is valid and points at the subtrack with the token).
func TestStreamHLSSubtitle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr, err := transcode.NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	r := gin.New()
	r.GET("/api/stream/hls/:hash/:file/sub/:track/index.m3u8", StreamHLSSubtitle(streamer.NewForTesting(), mgr, nil))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/stream/hls/"+testHash+"/0/sub/3/index.m3u8?token=T", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"#EXT-X-PLAYLIST-TYPE:VOD", "#EXT-X-ENDLIST", "/api/stream/subtrack/" + testHash + "/0/3?token=T"} {
		if !strings.Contains(body, want) {
			t.Errorf("sub playlist without %q:\n%s", want, body)
		}
	}
}

// StreamHLSMaster without a torrent: goes through serveMasterIfMultiVariant (probe
// fails → fallback) + serveHLSMediaPlaylist. Covers the single-variant glue.
func TestStreamHLSMasterFallbackNoTorrent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr, err := transcode.NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	r := gin.New()
	r.GET("/api/stream/hls/:hash/:file/index.m3u8", StreamHLSMaster(streamer.NewForTesting(), mgr, nil, nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/stream/hls/"+testHash+"/0/index.m3u8", nil))
	if w.Code == http.StatusOK {
		t.Errorf("without a torrent it should not be 200; got %d", w.Code)
	}
}
