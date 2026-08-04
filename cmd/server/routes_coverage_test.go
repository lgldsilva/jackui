package main

import (
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/history"
	"github.com/lgldsilva/jackui/internal/library"
	"github.com/lgldsilva/jackui/internal/mailer"
	"github.com/lgldsilva/jackui/internal/playlists"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/subtitles"
	"github.com/lgldsilva/jackui/internal/transcode"
	"github.com/lgldsilva/jackui/internal/watchlist"
)

func routeTestGroup() *gin.RouterGroup {
	return gin.New().Group("/api")
}

func TestRouteRegistrationHelpers(t *testing.T) {
	gin.SetMode(gin.TestMode)

	minimal := &appDeps{cfg: &config.Config{}}
	// Exercise the optional-dependency exits as well as the unconditional
	// registrations. Keeping each helper on its own engine avoids route-name
	// collisions while making the registration itself the assertion target.
	registerAdminRoutes(routeTestGroup(), minimal)
	registerHistoryRoutes(routeTestGroup(), minimal)
	registerStreamRoutes(routeTestGroup(), routeTestGroup(), minimal)
	registerPreviewRoutes(routeTestGroup(), minimal)
	registerLocalRoutes(routeTestGroup(), minimal)
	registerDownloadRoutes(routeTestGroup(), minimal)
	registerHLSRoutes(routeTestGroup(), routeTestGroup(), minimal)
	registerTMDBRoutes(routeTestGroup(), minimal)
	registerLibraryRoutes(routeTestGroup(), minimal)
	registerWatchlistRoutes(routeTestGroup(), minimal)
	registerPlaylistRoutes(routeTestGroup(), minimal)
	registerSidecarRoutes(routeTestGroup(), minimal)
	registerSubtitleRoutes(routeTestGroup(), minimal)
	registerTranscodeRoutes(routeTestGroup())
	registerAuthRoutes(routeTestGroup(), minimal)

	mgr, err := transcode.NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	stream := streamer.NewForTesting()
	full := &appDeps{
		cfg:            &config.Config{},
		mlr:            mailer.New(config.SMTPConfig{}),
		streamSrv:      stream,
		hlsMgr:         mgr,
		subtitleClient: subtitles.New("", "", "", t.TempDir()),
		historyStore:   &history.Store{},
		libraryStore:   &library.Store{},
		downloadsStore: &downloads.Store{},
		playlistsStore: &playlists.Store{},
		watchlistStore: &watchlist.Store{},
		authStore:      &auth.Store{},
	}

	// setupRouter runs every production registration helper in its normal
	// composition. The zero-value stores are sufficient because constructors
	// only capture them; no request is dispatched by this test.
	router := setupRouter(full)
	if len(router.Routes()) == 0 {
		t.Fatal("setupRouter registered no routes")
	}
}
