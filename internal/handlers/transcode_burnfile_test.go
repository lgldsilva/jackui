package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
)

const burnTestHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// burnSubServe runs a GET /api/stream/transcode/:hash/:file request through a
// real router so path params + query parsing behave like production.
func burnSubServe(t *testing.T, handler gin.HandlerFunc, query string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/stream/transcode/:hash/:file", handler)
	req := httptest.NewRequest(http.MethodGet, "/api/stream/transcode/"+burnTestHash+"/0"+query, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// TestResolveBurnSubFile pins the guard chain of the burnsub param: cache
// enabled → numeric file ID → containment → file present. status 0 means
// "proceed" (path is the resolved cache path).
func TestResolveBurnSubFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "999.vtt"), []byte("WEBVTT\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		root      string
		fileID    string
		wantPath  string
		wantStat  int
		wantInErr string
	}{
		{"cache disabled", "", "123", "", http.StatusBadRequest, "subtitle cache disabled"},
		{"non-numeric id", root, "abc", "", http.StatusBadRequest, "invalid burnsub file id"},
		{"traversal via slash", root, "12/34", "", http.StatusBadRequest, "invalid burnsub file id"},
		{"traversal via dots", root, "..", "", http.StatusBadRequest, "invalid burnsub file id"},
		{"negative id", root, "-1", "", http.StatusBadRequest, "invalid burnsub file id"},
		{"file not cached", root, "777", "", http.StatusBadRequest, "subtitle not cached"},
		{"cached ok", root, "999", filepath.Join(root, "999.vtt"), 0, ""},
	}
	for _, tc := range cases {
		path, status, err := resolveBurnSubFile(tc.root, tc.fileID)
		if status != tc.wantStat {
			t.Errorf("%s: status = %d, want %d (err=%v)", tc.name, status, tc.wantStat, err)
		}
		if path != tc.wantPath {
			t.Errorf("%s: path = %q, want %q", tc.name, path, tc.wantPath)
		}
		if tc.wantInErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantInErr)) {
			t.Errorf("%s: err = %v, want containing %q", tc.name, err, tc.wantInErr)
		}
		if tc.wantInErr == "" && err != nil {
			t.Errorf("%s: err = %v, want nil", tc.name, err)
		}
	}
}

func TestTranscodeStreamBurnSub_CacheDisabled(t *testing.T) {
	w := burnSubServe(t, TranscodeStream(nil, nil, ""), "?burnsub=123")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "subtitle cache disabled") {
		t.Errorf("body missing clear reason: %s", w.Body.String())
	}
}

func TestTranscodeStreamBurnSub_InvalidID(t *testing.T) {
	w := burnSubServe(t, TranscodeStream(nil, nil, t.TempDir()), "?burnsub=not-numeric")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid burnsub file id") {
		t.Errorf("body missing clear reason: %s", w.Body.String())
	}
}

// TestTranscodeStreamBurnSub_NotCached covers the 404-style 400: a valid ID
// that was never downloaded must NOT fall back to silently burning nothing.
func TestTranscodeStreamBurnSub_NotCached(t *testing.T) {
	w := burnSubServe(t, TranscodeStream(nil, nil, t.TempDir()), "?burnsub=777")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "subtitle not cached") {
		t.Errorf("body missing clear reason: %s", w.Body.String())
	}
}

// TestTranscodeStreamBurnSub_NoLibassFilter pins the typed degradation: an
// ffmpeg build without the libass subtitles filter must 400 BEFORE streaming
// so the web player can fall back to the sidecar <track> path.
func TestTranscodeStreamBurnSub_NoLibassFilter(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "999.vtt"), []byte("WEBVTT\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	transcode.SetCachedForTesting(&transcode.Capabilities{FFmpegPath: "/usr/bin/ffmpeg", HasSubtitlesFilter: false})
	defer transcode.ResetCachedForTesting()

	w := burnSubServe(t, TranscodeStream(streamer.NewForTesting(), nil, root), "?burnsub=999")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "libass") {
		t.Errorf("body missing typed filter error: %s", w.Body.String())
	}
}

// TestTranscodeStreamBurnSub_GuardPassesReachesStreamer proves a fully valid
// burnsub request gets PAST the guards and into the normal streaming flow
// (404 = streamer has no such file — the same terminal code as without
// burnsub), i.e. the option is set rather than rejected.
func TestTranscodeStreamBurnSub_GuardPassesReachesStreamer(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "999.vtt"), []byte("WEBVTT\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	transcode.SetCachedForTesting(&transcode.Capabilities{FFmpegPath: "/usr/bin/ffmpeg", HasSubtitlesFilter: true})
	defer transcode.ResetCachedForTesting()

	w := burnSubServe(t, TranscodeStream(streamer.NewForTesting(), nil, root), "?burnsub=999")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (guards passed, streamer miss); body: %s", w.Code, w.Body.String())
	}
}

// TestTranscodeStreamNoBurnSub_Unchanged pins the additive contract: absent or
// empty burnsub keeps the exact previous behavior regardless of the cache root.
func TestTranscodeStreamNoBurnSub_Unchanged(t *testing.T) {
	for _, root := range []string{"", t.TempDir()} {
		w := burnSubServe(t, TranscodeStream(streamer.NewForTesting(), nil, root), "")
		if w.Code != http.StatusNotFound {
			t.Errorf("root=%q: status = %d, want 404 (unchanged flow); body: %s", root, w.Code, w.Body.String())
		}
	}
}

// TestTranscodeRunStatus_BurnSubSentinelMaps400 pins the Run-error-site status
// mapping: the libass sentinel is a client-input problem (400, same as the
// early Cached() check), not a 500 — including the wrapped form, which is how
// ValidateOptions emits it and how the boot-time probe race delivers it to Run.
func TestTranscodeRunStatus_BurnSubSentinelMaps400(t *testing.T) {
	wrapped := transcode.ValidateOptions(&transcode.Capabilities{HasSubtitlesFilter: false}, transcode.Options{SubBurnFile: "/cache/999.vtt"})
	if wrapped == nil || !errors.Is(wrapped, transcode.ErrSubtitlesFilterUnavailable) {
		t.Fatalf("ValidateOptions err = %v, want the wrapped sentinel", wrapped)
	}
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"bare sentinel", transcode.ErrSubtitlesFilterUnavailable, http.StatusBadRequest},
		{"ValidateOptions wrap", wrapped, http.StatusBadRequest},
		{"plain error", errors.New("ffmpeg failed"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		if got := transcodeRunStatus(tc.err); got != tc.want {
			t.Errorf("%s: transcodeRunStatus = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestTryServeFromCompleted_BurnSubNoLibassIs400 pins the 400 mapping END-TO-END
// at the completed-download Run site: a no-libass sentinel arriving from Run's
// re-validation (sentinel wrapped by ValidateOptions) must surface as the typed
// 400 naming libass, never a mid-stream 500.
func TestTryServeFromCompleted_BurnSubNoLibassIs400(t *testing.T) {
	store, s, destDir := wholeTestEnv(t)
	transcode.SetCachedForTesting(&transcode.Capabilities{FFmpegPath: "/usr/bin/ffmpeg", HasSubtitlesFilter: false})
	defer transcode.ResetCachedForTesting()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	h, _ := parseHash(wholeHexHash)

	opts := transcode.Options{SubBurnFile: filepath.Join(destDir, "Sub", "a.bin")}
	if !tryServeFromCompleted(c, store, wholeHexHash, 0, opts, s.FileRelPath(h, 0), 0) {
		t.Fatal("expected the completed whole-torrent file to claim the request")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "libass") {
		t.Errorf("body missing typed filter error: %s", w.Body.String())
	}
}
