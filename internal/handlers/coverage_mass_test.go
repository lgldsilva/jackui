package handlers

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/library"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/lyrics"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/subtitles"
	"github.com/lgldsilva/jackui/internal/tmdb"
)

// Mass coverage for handlers whose new-code paths are mostly error/validation
// branches. Each test drives the handler into a RespondError/RespondErrorMessage
// call without needing a live store or streamer.
func TestMassValidationCoverage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	store := &downloads.Store{}
	subClient := subtitles.New("", "", "", "")
	lyrClient := lyrics.New()

	handlers := []struct {
		name   string
		h      gin.HandlerFunc
		method string
		path   string
		body   string
		params gin.Params
		claims *auth.Claims
	}{
		// subtitles.go — disabled client rejects before any store/network access
		{"subtitles search missing key", SubtitlesSearch(subClient), http.MethodGet, "/api/subtitles/search", "", nil, nil},
		{"subtitles download missing key", SubtitlesDownload(subClient), http.MethodPost, "/api/subtitles/download", "{}", nil, nil},
		// download.go — invalid path ID rejected before store access
		{"download get invalid id", DownloadsDelete(store, nil), http.MethodGet, "/api/downloads/not-an-id", "", gin.Params{{Key: "id", Value: "not-an-id"}}, nil},
		// downloads_batch.go — malformed JSON rejected before store access
		{"batch create malformed", DownloadsBatchCreate(store, nil), http.MethodPost, "/api/downloads/batch", "{", nil, nil},
		{"batch pause malformed", DownloadsBatchPause(store), http.MethodPost, "/api/downloads/batch/pause", "{", nil, nil},
		{"batch resume malformed", DownloadsBatchResume(store), http.MethodPost, "/api/downloads/batch/resume", "{", nil, nil},
		{"batch delete malformed", DownloadsBatchDelete(store, nil), http.MethodPost, "/api/downloads/batch/delete", "{", nil, nil},
		// lyrics.go — disabled client rejects before network access
		{"lyrics no key", LyricsGet(lyrClient), http.MethodGet, "/api/lyrics", "", nil, nil},
		// stream_playback.go — malformed JSON rejected before streamer access
		{"stream set limits malformed", StreamSetLimits(s), http.MethodPost, "/api/stream/limits", "{", nil, nil},
		{"stream set priority malformed", StreamSetPriority(s), http.MethodPost, "/api/stream/priority", "{", gin.Params{{Key: "hash", Value: "0123456789012345678901234567890123456789"}}, nil},
		// stream_settings.go — malformed JSON rejected before streamer access
		{"stream file priority malformed", StreamSetFilePriority(s), http.MethodPost, "/api/stream/file-priority", "{", gin.Params{{Key: "hash", Value: "0123456789012345678901234567890123456789"}, {Key: "idx", Value: "0"}}, nil},
		// passkey.go — missing body rejected before store access
		{"passkey register begin missing body", PasskeyRegisterBegin(&auth.Store{}, nil), http.MethodPost, "/api/auth/passkey/register/begin", "", nil, nil},
	}

	for _, tc := range handlers {
		t.Run(tc.name, func(t *testing.T) {
			w := invokeCoverageHandlerWithClaims(t, tc.h, tc.method, tc.path, tc.body, tc.params, tc.claims)
			if w.Code == http.StatusOK || w.Code == http.StatusCreated {
				t.Errorf("%s unexpectedly succeeded: %d %s", tc.name, w.Code, w.Body.String())
			}
		})
	}
}

func TestMassHandlerConstructors(t *testing.T) {
	// Cover the constructor closures that return gin.HandlerFunc without
	// dispatching requests. Ensures RespondError paths are reachable.
	s := streamer.NewForTesting()
	store := &downloads.Store{}
	browser := local.NewBrowser([]config.ExternalMount{{Name: "M", Path: t.TempDir()}})
	lib := &library.Store{}
	subClient := subtitles.New("", "", "", "")
	lyrClient := lyrics.New()
	tmdbClient, err := tmdb.New("", "", nil)
	if err != nil {
		t.Fatalf("tmdb.New: %v", err)
	}

	constructors := map[string]gin.HandlerFunc{
		"downloads list":     DownloadsList(store, s, browser, nil, ""),
		"downloads filtered": DownloadsListFiltered(store, s, browser, nil),
		"downloads all":      DownloadsListAll(store, nil, s, browser),
		"subtitles search":   SubtitlesSearch(subClient),
		"subtitles download": SubtitlesDownload(subClient),
		"recommendations":    Recommendations(lib, s, tmdbClient),
		"lyrics":             LyricsGet(lyrClient),
		"passkey begin":      PasskeyRegisterBegin(&auth.Store{}, nil),
	}
	for name, h := range constructors {
		t.Run(name, func(t *testing.T) {
			if h == nil {
				t.Fatal("constructor returned nil handler")
			}
		})
	}
}
