package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
)

// TestRegisterHLSRoutesNoConflict mounts the real HLS routes on a gin engine and
// guarantees there is NO route-tree conflict panic — the gin "conflicts with
// existing wildcard" scenario Phase 2 would introduce if `v/:variant` collided
// with the legacy `:seg`. Without this test the panic would only appear at
// server boot (runtime), never in CI. It also confirms precedence: a request to
// the variant playlist matches a handler (does not fall to NoRoute).
func TestRegisterHLSRoutesNoConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mgr, err := transcode.NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	deps := &appDeps{
		cfg:       &config.Config{},
		hlsMgr:    mgr,
		streamSrv: streamer.NewForTesting(),
	}

	r := gin.New()
	// NoRoute sentinel: distinguishes "no route matched" from "handler ran and
	// answered 404".
	r.NoRoute(func(c *gin.Context) { c.String(599, "NOROUTE") })

	api := r.Group("/api")
	adminAPI := r.Group("/api")
	// A route-conflict panic would blow up HERE (test fails).
	registerHLSRoutes(api, adminAPI, deps)

	// The tree must contain the new variant routes + the legacy one.
	wantPaths := map[string]bool{
		"/api/stream/hls/:hash/:file/index.m3u8":            false,
		"/api/stream/hls/:hash/:file/v/:variant/index.m3u8": false,
		"/api/stream/hls/:hash/:file/v/:variant/:seg":       false,
		"/api/stream/hls/:hash/:file/a/:track/index.m3u8":   false,
		"/api/stream/hls/:hash/:file/a/:track/:seg":         false,
		"/api/stream/hls/:hash/:file/sub/:track/index.m3u8": false,
		"/api/stream/hls/:hash/:file/:seg":                  false,
	}
	for _, ri := range r.Routes() {
		if _, ok := wantPaths[ri.Path]; ok {
			wantPaths[ri.Path] = true
		}
	}
	for p, found := range wantPaths {
		if !found {
			t.Errorf("HLS route not registered: %s", p)
		}
	}

	// Precedence: /v/0/index.m3u8 (3 segments after :file) matches the variant
	// route — NOT the NoRoute (599). The static `v` takes priority over `:seg`.
	const hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, path := range []string{
		"/api/stream/hls/" + hash + "/0/v/0/index.m3u8",
		"/api/stream/hls/" + hash + "/0/v/0/seg_00000.ts",
		"/api/stream/hls/" + hash + "/0/a/2/index.m3u8",
		"/api/stream/hls/" + hash + "/0/a/2/seg_00000.ts",
		"/api/stream/hls/" + hash + "/0/index.m3u8",
		"/api/stream/hls/" + hash + "/0/seg_00000.ts",
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		r.ServeHTTP(w, req)
		if w.Code == 599 {
			t.Errorf("%s fell to NoRoute (no route matched)", path)
		}
	}
}
