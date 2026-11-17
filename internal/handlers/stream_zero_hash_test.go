package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/transcode"
)

// zeroHashRouter mounts the playlist + torrent HLS routes exactly as
// cmd/server registers them, behind gin.Recovery() like production, on a REAL
// anacrolix-backed streamer (the nil-client NewForTesting streamer never
// reaches the client's AddMagnet, which is where the all-zero hash blew up).
func zeroHashRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, _ := realStreamerEnv(t)
	t.Cleanup(s.Close)
	mgr, err := transcode.NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	r := gin.New()
	r.Use(gin.Recovery())
	api := r.Group("/api")
	api.GET("/stream/playlist/:hash/:file", StreamPlaylistM3U(s))
	api.GET("/stream/hls/:hash/:file/index.m3u8", StreamHLSMaster(s, mgr, nil, nil))
	api.GET("/stream/hls/:hash/:file/v/:variant/index.m3u8", StreamHLSVariant(s, mgr, nil))
	api.GET("/stream/hls/:hash/:file/v/:variant/:seg", StreamHLSSegment(s, mgr, nil))
	api.GET("/stream/hls/:hash/:file/a/:track/index.m3u8", StreamHLSAudio(s, mgr, nil))
	api.GET("/stream/hls/:hash/:file/a/:track/:seg", StreamHLSSegment(s, mgr, nil))
	api.GET("/stream/hls/:hash/:file/:seg", StreamHLSSegment(s, mgr, nil))
	return r
}

// TestStreamRoutes_ZeroInfoHashIs404 is the #216 regression: the all-zero info
// hash (the e2e suite's "well-formed but nonexistent" placeholder) made the
// bare-magnet auto-add panic inside the torrent client, which gin.Recovery()
// turned into a 500 with an EMPTY body on these seven routes. They must answer
// 404 with the standard JSON error instead.
func TestStreamRoutes_ZeroInfoHashIs404(t *testing.T) {
	r := zeroHashRouter(t)
	zero := strings.Repeat("0", 40)
	hls := "/api/stream/hls/" + zero + "/0"
	for _, path := range []string{
		"/api/stream/playlist/" + zero + "/0",
		hls + "/index.m3u8",
		hls + "/v/0/index.m3u8",
		hls + "/v/0/e2e0.ts",
		hls + "/a/0/index.m3u8",
		hls + "/a/0/e2e0.ts",
		hls + "/e2e0.ts",
	} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (body: %q)", w.Code, w.Body.String())
			}
			var body map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["error"] == "" {
				t.Fatalf("body = %q, want JSON {\"error\": ...} (unmarshal err: %v)", w.Body.String(), err)
			}
		})
	}
}
