package local

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	lb "github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/localcache"
)

// stubFFmpeg prepends a fake `ffmpeg` to PATH that always emits a tiny WebVTT
// on stdout, so the success paths run deterministically with no real encoder.
// t.Setenv also guards against t.Parallel misuse.
func stubFFmpeg(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf 'WEBVTT\\n\\n00:00.000 --> 00:01.000\\nstub\\n'\n"
	if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// No cache infra → the legacy synchronous extract must run and serve the VTT.
func TestLocalSubtitleExtract_LegacySyncWithoutCache(t *testing.T) {
	stubFFmpeg(t)
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "movie.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := lb.NewBrowser([]config.ExternalMount{{Name: "M", Path: dir}})

	r := gin.New()
	r.GET("/sub", LocalSubtitleExtract(b, nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/sub?mount=M&path=movie.mkv&track=2", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if !strings.HasPrefix(w.Body.String(), "WEBVTT") {
		t.Errorf("body should be the extracted VTT, got %q", w.Body.String()[:min(40, w.Body.Len())])
	}
	if ct := w.Header().Get(httpshared.ContentType); ct != httpshared.MIMEVTT {
		t.Errorf("Content-Type = %q, want %q", ct, httpshared.MIMEVTT)
	}
}

// A path that escapes the mount must be rejected by ResolvePath with a 400.
func TestLocalSubtitleExtract_TraversalPathRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	b := lb.NewBrowser([]config.ExternalMount{{Name: "M", Path: dir}})

	r := gin.New()
	r.GET("/sub", LocalSubtitleExtract(b, nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/sub?mount=M&path=..%2F..%2Fetc%2Fpasswd&track=0", nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

// persistVTT must be a silent no-op when the cache dir can't be created (the
// parent is a FILE) and when the temp file can't be written (a DIRECTORY
// already sits at the .tmp path). Playback must never break over cache writes.
func TestPersistVTT_SilentOnUnwritablePaths(t *testing.T) {
	dir := t.TempDir()

	// MkdirAll error: parent path component is a regular file.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	persistVTT(filepath.Join(blocker, "subs", "a.vtt"), []byte("WEBVTT"))

	// WriteFile error: a directory occupies the temp path.
	vtt := filepath.Join(dir, "b.vtt")
	if err := os.MkdirAll(vtt+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	persistVTT(vtt, []byte("WEBVTT"))
	if _, err := os.Stat(vtt); !os.IsNotExist(err) {
		t.Errorf("no VTT should have been persisted, stat err = %v", err)
	}
}

// The background extraction must dedupe by vttPath (second call is a no-op
// while the first is in flight) and persist the VTT when ffmpeg succeeds.
func TestStartBgSubExtract_DedupesAndPersists(t *testing.T) {
	stubFFmpeg(t)
	dir := t.TempDir()
	abs := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(abs, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		t.Fatal(err)
	}
	cache, err := localcache.New(t.TempDir(), 1)
	if err != nil {
		t.Fatalf("localcache.New: %v", err)
	}
	defer cache.Close()

	// Dedupe: pre-register the job key → the call must bail out before ffmpeg.
	deduped := filepath.Join(dir, "deduped.vtt")
	subExtractJobs.LoadOrStore(deduped, struct{}{})
	startBgSubExtract(cache, "M", "movie.mkv", abs, st, 0, deduped)
	subExtractJobs.Delete(deduped)
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(deduped); !os.IsNotExist(err) {
		t.Errorf("deduped job must not extract; stat err = %v", err)
	}

	// Happy path: the goroutine extracts (stub ffmpeg) and persists the VTT.
	vtt := filepath.Join(dir, "ok.vtt")
	startBgSubExtract(cache, "M", "movie.mkv", abs, st, 0, vtt)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(vtt); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background extraction never persisted the VTT")
		}
		time.Sleep(25 * time.Millisecond)
	}
	data, err := os.ReadFile(vtt)
	if err != nil || !strings.HasPrefix(string(data), "WEBVTT") {
		t.Errorf("persisted VTT invalid: err=%v data=%q", err, string(data))
	}
}
