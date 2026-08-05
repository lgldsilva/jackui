package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/downloads"
	lh "github.com/lgldsilva/jackui/internal/handlers/local"
	"github.com/lgldsilva/jackui/internal/history"
	"github.com/lgldsilva/jackui/internal/library"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/playlists"
	"github.com/lgldsilva/jackui/internal/push"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
	"github.com/lgldsilva/jackui/internal/watchlist"
)

func invokeCoverageHandler(t *testing.T, h gin.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
	return invokeCoverageHandlerWithParams(t, h, method, path, body, gin.Params{{Key: "id", Value: "not-an-id"}})
}

func invokeCoverageHandlerWithParams(t *testing.T, h gin.HandlerFunc, method, path, body string, params gin.Params) *httptest.ResponseRecorder {
	return invokeCoverageHandlerWithClaims(t, h, method, path, body, params, nil)
}

func invokeCoverageHandlerWithClaims(t *testing.T, h gin.HandlerFunc, method, path, body string, params gin.Params, claims *auth.Claims) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Params = params
	if claims != nil {
		c.Set("auth.claims", claims)
	}
	h(c)
	return w
}

func TestDownloadHandlersValidationErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &downloads.Store{}

	// These handlers reject the path before touching the store. Covering each
	// endpoint keeps the shared error response contract exercised independently.
	for name, handler := range map[string]gin.HandlerFunc{
		"delete":   DownloadsDelete(store, nil),
		"pause":    DownloadsPause(store),
		"resume":   DownloadsResume(store),
		"priority": DownloadsSetPriority(store),
		"sources":  DownloadsSources(store),
		"recheck":  DownloadsRecheck(store, nil),
		"peers":    DownloadsPeers(store, nil),
		"details":  DownloadsDetails(store, nil),
	} {
		t.Run(name, func(t *testing.T) {
			w := invokeCoverageHandler(t, handler, http.MethodGet, "/api/downloads/not-an-id", "")
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}

	for name, handler := range map[string]gin.HandlerFunc{
		"create malformed":       DownloadsCreate(store, nil),
		"batch create malformed": DownloadsBatchCreate(store, nil),
		"batch pause malformed":  DownloadsBatchPause(store),
		"batch resume malformed": DownloadsBatchResume(store),
		"batch delete malformed": DownloadsBatchDelete(store, nil),
		"batch stop malformed":   DownloadsBatchStopSeed(store, nil),
	} {
		t.Run(name, func(t *testing.T) {
			w := invokeCoverageHandler(t, handler, http.MethodPost, "/api/downloads", "{")
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}

	for name, body := range map[string]string{
		"create missing fields":       `{}`,
		"create invalid file index":   `{"infoHash":"hash","magnet":"magnet:test","fileIndex":-3}`,
		"batch create missing fields": `{}`,
		"batch create empty files":    `{"infoHash":"hash","magnet":"magnet:test","files":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			h := DownloadsCreate(store, nil)
			if strings.HasPrefix(name, "batch") {
				h = DownloadsBatchCreate(store, nil)
			}
			w := invokeCoverageHandler(t, h, http.MethodPost, "/api/downloads", body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestAdditionalValidationErrorResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for name, handler := range map[string]gin.HandlerFunc{
		"mounts malformed":     MountsUpdate(&config.Config{}, "", nil),
		"push subscribe":       PushSubscribe(&push.Store{}),
		"push unsubscribe":     PushUnsubscribe(&push.Store{}),
		"watchlist create":     WatchlistCreate(&watchlist.Store{}, nil),
		"watchlist update":     WatchlistUpdate(&watchlist.Store{}),
		"stream set limits":    StreamSetLimits(streamer.NewForTesting()),
		"stream set priority":  StreamSetPriority(streamer.NewForTesting()),
		"stream file priority": StreamSetFilePriority(streamer.NewForTesting()),
	} {
		t.Run(name, func(t *testing.T) {
			params := gin.Params{{Key: "hash", Value: "0123456789012345678901234567890123456789"}, {Key: "idx", Value: "0"}}
			if name == "stream set priority" {
				params = gin.Params{{Key: "hash", Value: "0123456789012345678901234567890123456789"}}
			}
			w := invokeCoverageHandlerWithParams(t, handler, http.MethodPost, "/api/test", "{", params)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}

	// The nil store branch is intentionally distinct from malformed JSON: the
	// endpoint must remain a clean 503 when push is not configured.
	if got := invokeCoverageHandler(t, PushUnsubscribe(nil), http.MethodPost, "/api/push/unsubscribe", "{}").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("nil push store status = %d, want %d", got, http.StatusServiceUnavailable)
	}

	stream := streamer.NewForTesting()
	tooMany := `{"hashes":[` + strings.Repeat(`"x",`, 500) + `"x"]}`
	if got := invokeCoverageHandler(t, StreamMetadataBatch(stream), http.MethodPost, "/api/stream/metadata/batch", tooMany).Code; got != http.StatusRequestEntityTooLarge {
		t.Fatalf("too many hashes status = %d, want %d", got, http.StatusRequestEntityTooLarge)
	}
	if got := invokeCoverageHandler(t, StreamMetadataBatch(stream), http.MethodPost, "/api/stream/metadata/batch", `{"hashes":["x"]}`).Code; got != http.StatusServiceUnavailable {
		t.Fatalf("nil metadata cache status = %d, want %d", got, http.StatusServiceUnavailable)
	}

	browser := local.NewBrowser([]config.ExternalMount{{Name: "mount", Path: t.TempDir()}})
	for name, handler := range map[string]gin.HandlerFunc{
		"local delete":       lh.LocalDelete(browser, nil, nil),
		"local clean empty":  lh.LocalCleanEmptyDirs(browser),
		"local audio":        lh.LocalAudioMeta(browser, nil),
		"local play":         lh.LocalPlay(browser, nil),
		"local cache delete": lh.LocalCacheDelete(browser, nil),
	} {
		t.Run(name, func(t *testing.T) {
			w := invokeCoverageHandler(t, handler, http.MethodPost, "/api/local/test", "")
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}

	if got := invokeCoverageHandler(t, lh.LocalSetHidden(browser, stream), http.MethodPost, "/api/local/hidden", `{"mount":"mount","path":"file","hidden":true}`).Code; got != http.StatusServiceUnavailable {
		t.Fatalf("hidden without favorites status = %d, want %d", got, http.StatusServiceUnavailable)
	}

	hls := StreamHLSSubtitle(stream, (*transcode.HLSSessionManager)(nil), (*downloads.Store)(nil))
	params := gin.Params{{Key: "hash", Value: "0123456789012345678901234567890123456789"}, {Key: "file", Value: "0"}, {Key: "track", Value: "bad"}}
	if got := invokeCoverageHandlerWithParams(t, hls, http.MethodGet, "/api/stream/hls/sub", "", params).Code; got != http.StatusBadRequest {
		t.Fatalf("invalid subtitle track status = %d, want %d", got, http.StatusBadRequest)
	}
}

func TestStoreBackedErrorResponsesAfterPoolClose(t *testing.T) {
	pool := dbtest.NewDB(t)
	historyStore, err := history.New(pool)
	if err != nil {
		t.Fatalf("history.New: %v", err)
	}
	libraryStore, err := library.New(pool)
	if err != nil {
		t.Fatalf("library.New: %v", err)
	}
	playlistStore, err := playlists.New(pool)
	if err != nil {
		t.Fatalf("playlists.New: %v", err)
	}
	pool.Close()

	for name, handler := range map[string]gin.HandlerFunc{
		"history list":    GetHistory(historyStore),
		"history results": GetHistoryResults(historyStore, nil, nil),
		"history cache":   SearchCache(historyStore, nil, nil),
		"history query":   DeleteHistory(historyStore),
		"history all":     DeleteHistory(historyStore),
	} {
		t.Run(name, func(t *testing.T) {
			path := "/api/test?q=term"
			if name == "history all" {
				path = "/api/test"
			}
			w := invokeCoverageHandlerWithParams(t, handler, http.MethodGet, path, "", nil)
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
			}
		})
	}

	validID := gin.Params{{Key: "id", Value: "1"}, {Key: "itemId", Value: "2"}}
	for name, tc := range map[string]struct {
		handler gin.HandlerFunc
		method  string
		body    string
	}{
		"library list":   {LibraryList(libraryStore, nil), http.MethodGet, ""},
		"library get":    {LibraryGet(libraryStore), http.MethodGet, ""},
		"library update": {LibraryUpdateResume(libraryStore), http.MethodPatch, `{"resumeSeconds":1}`},
		"library delete": {LibraryDelete(libraryStore), http.MethodDelete, ""},
		"library all":    {LibraryDeleteAll(libraryStore), http.MethodDelete, ""},
	} {
		t.Run(name, func(t *testing.T) {
			params := validID
			if name == "library list" || name == "library all" {
				params = nil
			}
			w := invokeCoverageHandlerWithParams(t, tc.handler, tc.method, "/api/library", tc.body, params)
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
			}
		})
	}

	for name, tc := range map[string]struct {
		handler gin.HandlerFunc
		method  string
		body    string
	}{
		"playlist list":     {PlaylistsList(playlistStore), http.MethodGet, ""},
		"playlist create":   {PlaylistsCreate(playlistStore), http.MethodPost, `{"name":"p"}`},
		"playlist get":      {PlaylistsGet(playlistStore), http.MethodGet, ""},
		"playlist update":   {PlaylistsUpdate(playlistStore), http.MethodPatch, `{"name":"p"}`},
		"playlist delete":   {PlaylistsDelete(playlistStore), http.MethodDelete, ""},
		"playlist add item": {PlaylistsAddItem(playlistStore), http.MethodPost, `{"title":"x","magnet":"m","infoHash":"h"}`},
		"playlist remove":   {PlaylistsRemoveItem(playlistStore), http.MethodDelete, ""},
		"playlist reorder":  {PlaylistsReorderItem(playlistStore), http.MethodPatch, `{"position":1}`},
	} {
		t.Run(name, func(t *testing.T) {
			w := invokeCoverageHandlerWithParams(t, tc.handler, tc.method, "/api/playlists/1/items/2", tc.body, validID)
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
			}
		})
	}
}
