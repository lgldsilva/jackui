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

// hlsSessionKeyFromReq: a/:track → -ao{track}; v/:variant → -v{variant}; nu → base.
// Testado via gin real pra exercitar a leitura dos path params.
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

// StreamHLSAudio (a/:track) casa a rota e roda o glue audio-only sem torrent
// (fonte não resolve → não-200, mas não é 'variant out of range' nem NoRoute).
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
		t.Errorf("rota a/:track não casou (NoRoute)")
	}
	if w.Code == http.StatusOK {
		t.Errorf("sem torrent não deveria dar 200; got %d", w.Code)
	}
}

// StreamHLSMaster sem torrent: passa por serveMasterIfMultiVariant (probe falha
// → fallback) + serveHLSMediaPlaylist. Cobre o glue single-variant.
func TestStreamHLSMasterFallbackNoTorrent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr, err := transcode.NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	r := gin.New()
	r.GET("/api/stream/hls/:hash/:file/index.m3u8", StreamHLSMaster(streamer.NewForTesting(), mgr, nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/stream/hls/"+testHash+"/0/index.m3u8", nil))
	if w.Code == http.StatusOK {
		t.Errorf("sem torrent não deveria dar 200; got %d", w.Code)
	}
}
