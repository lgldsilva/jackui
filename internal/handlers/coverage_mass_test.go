package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
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

	const hash = "0123456789012345678901234567890123456789"
	handlers := []struct {
		name     string
		h        gin.HandlerFunc
		method   string
		path     string
		body     string
		params   gin.Params
		wantCode int
		wantErr  string // exact error message
	}{
		// subtitles.go — disabled client rejects before any store/network access
		{"subtitles search missing key", SubtitlesSearch(subClient), http.MethodGet, "/api/subtitles/search", "", nil, http.StatusBadRequest, "query parameter 'q' is required"},
		{"subtitles download missing key", SubtitlesDownload(subClient), http.MethodPost, "/api/subtitles/download", "{}", nil, http.StatusBadRequest, "fileId required"},
		// download.go — invalid path ID rejected before store access
		{"download delete invalid id", DownloadsDelete(store, nil), http.MethodDelete, "/api/downloads/not-an-id", "", gin.Params{{Key: "id", Value: "not-an-id"}}, http.StatusBadRequest, "invalid id"},
		// downloads_batch.go — malformed JSON rejected before store access
		{"batch create malformed", DownloadsBatchCreate(store, nil), http.MethodPost, "/api/downloads/batch", "{", nil, http.StatusBadRequest, "unexpected EOF"},
		{"batch pause malformed", DownloadsBatchPause(store), http.MethodPost, "/api/downloads/batch/pause", "{", nil, http.StatusBadRequest, "unexpected EOF"},
		{"batch resume malformed", DownloadsBatchResume(store), http.MethodPost, "/api/downloads/batch/resume", "{", nil, http.StatusBadRequest, "unexpected EOF"},
		{"batch delete malformed", DownloadsBatchDelete(store, nil), http.MethodPost, "/api/downloads/batch/delete", "{", nil, http.StatusBadRequest, "unexpected EOF"},
		// lyrics.go — disabled client rejects before network access
		{"lyrics no key", LyricsGet(lyrClient), http.MethodGet, "/api/lyrics", "", nil, http.StatusBadRequest, "title is required"},
		// stream_playback.go — malformed JSON rejected before streamer access
		{"stream set limits malformed", StreamSetLimits(s), http.MethodPost, "/api/stream/limits", "{", nil, http.StatusBadRequest, "unexpected EOF"},
		{"stream set priority malformed", StreamSetPriority(s), http.MethodPost, "/api/stream/priority", "{", gin.Params{{Key: "hash", Value: hash}}, http.StatusBadRequest, "unexpected EOF"},
		// stream_settings.go — malformed JSON rejected before streamer access
		{"stream file priority malformed", StreamSetFilePriority(s), http.MethodPost, "/api/stream/file-priority", "{", gin.Params{{Key: "hash", Value: hash}, {Key: "idx", Value: "0"}}, http.StatusBadRequest, "unexpected EOF"},
		// passkey.go — no authenticated user rejected before store access
		{"passkey register begin unauthenticated", PasskeyRegisterBegin(&auth.Store{}, nil), http.MethodPost, "/api/auth/passkey/register/begin", "", nil, http.StatusUnauthorized, "not authenticated"},
	}

	for _, tc := range handlers {
		t.Run(tc.name, func(t *testing.T) {
			w := invokeCoverageHandlerWithClaims(t, tc.h, tc.method, tc.path, tc.body, tc.params, nil)
			if w.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.wantCode, w.Body.String())
			}
			var got struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("body is not JSON: %v (%s)", err, w.Body.String())
			}
			if got.Error != tc.wantErr {
				t.Errorf("error = %q, want %q", got.Error, tc.wantErr)
			}
		})
	}
}

// TestMassHandlerConstructors dispatches the store-backed list handlers against
// a real (empty) database and asserts they answer 200 with an empty JSON array,
// and the early-rejecting ones with their exact status. A constructor that
// returned a no-op handler (or one that 500s) no longer passes.
func TestMassHandlerConstructors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	pool := seededPool(t)
	store, err := downloads.New(pool)
	if err != nil {
		t.Fatalf("downloads.New: %v", err)
	}
	lib, err := library.New(pool)
	if err != nil {
		t.Fatalf("library.New: %v", err)
	}
	browser := local.NewBrowser([]config.ExternalMount{{Name: "M", Path: t.TempDir()}})
	tmdbClient, err := tmdb.New("", "", nil)
	if err != nil {
		t.Fatalf("tmdb.New: %v", err)
	}

	lists := map[string]gin.HandlerFunc{
		"downloads list":     DownloadsList(store, s, browser, nil, ""),
		"downloads filtered": DownloadsListFiltered(store, s, browser, nil),
		"downloads all":      DownloadsListAll(store, nil, s, browser),
		"recommendations":    Recommendations(lib, s, tmdbClient),
	}
	for name, h := range lists {
		t.Run(name, func(t *testing.T) {
			w := invokeCoverageHandler(t, h, http.MethodGet, "/x", "")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
			}
			if got := strings.TrimSpace(w.Body.String()); got != "[]" {
				t.Errorf("body = %s, want []", got)
			}
		})
	}
}
