package main

import (
	"database/sql"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/subtitles"
)

// newAutoSubRouteTestStreamer builds the lightweight testing streamer used to
// register the routes (no request is dispatched — registration is the target).
func newAutoSubRouteTestStreamer(t *testing.T) *streamer.Streamer {
	t.Helper()
	return streamer.NewForTesting()
}

// newAutoSubRouteTestPool opens the migrated per-process test database.
func newAutoSubRouteTestPool(t *testing.T) *sql.DB {
	t.Helper()
	return dbtest.NewDB(t)
}

// TestRouteRegistration_AutoSubtitles proves the new auto-subtitle routes
// register WITHOUT gin tree conflicts while the routes they must coexist with
// are present — notably GET /stream/:hash/:file (static child "subtitles" under
// the :file param node) and GET /local/subtitles/auto (param children under the
// static "auto" leaf). gin panics at registration time on any conflict, so a
// panic-free run IS the assertion; the route table is then checked for presence.
func TestRouteRegistration_AutoSubtitles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	deps := &appDeps{
		cfg:            &config.Config{},
		streamSrv:      newAutoSubRouteTestStreamer(t),
		subtitleClient: subtitles.New("test-api-key", "", "", t.TempDir()),
	}
	// The full pipeline: links store over the test pool + a real AutoFetcher so
	// every guard takes the "available" branch.
	links, err := subtitles.NewLinksStore(newAutoSubRouteTestPool(t))
	if err != nil {
		t.Fatalf("NewLinksStore: %v", err)
	}
	deps.subtitleLinks = links
	deps.subtitlesAuto = subtitles.NewAutoFetcher(deps.subtitleClient, links, nil,
		func() (string, bool) { return "pt-BR,pt", true })
	deps.subtitleAutoMeta = streamerTorrentMeta{s: deps.streamSrv}

	r := gin.New()
	api := r.Group("/api")
	// registerStreamRoutes fans out to registerLocalRoutes/DownloadRoutes/
	// HLSRoutes/PreviewRoutes — calling registerLocalRoutes here too would
	// double-register /api/local/* (gin panics on duplicate paths).
	registerStreamRoutes(api, api, deps)
	registerSubtitleRoutes(api, deps)

	want := map[string]bool{
		"GET /api/stream/:hash/:file":                    false,
		"GET /api/stream/:hash/:file/subtitles/auto":     false,
		"GET /api/local/subtitles/auto":                  false,
		"GET /api/local/subtitles/auto/:hash/:file":      false,
		"POST /api/stream/:hash/viewer":                  false,
		"DELETE /api/stream/:hash/viewer":                false,
		"GET /api/local/play":                            false,
	}
	for _, ri := range r.Routes() {
		key := ri.Method + " " + ri.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("route %q not registered", key)
		}
	}
}
