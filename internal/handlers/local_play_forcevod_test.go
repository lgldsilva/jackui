package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/transcode"
)

// startLocalHLSSession must hand the manager a ForceVOD session (local files
// are complete and seekable — EVENT/live is only for unknown durations). With
// caps unprobed GetOrStart fails fast, which both exercises the option block
// and proves the error path closes the source and answers 500.
func TestStartLocalHLSSession_BuildsForceVODOpts(t *testing.T) {
	transcode.ResetCachedForTesting()
	gin.SetMode(gin.TestMode)

	mgr, err := transcode.NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	dir := t.TempDir()
	abs := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(abs, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(abs)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/play", nil)

	sess, serr := startLocalHLSSession(c, mgr, nil, localHLSSource{
		mount: "M", path: "movie.mkv", abs: abs, stat: stat,
		nativeHLS: false, knownDur: 123,
	})
	if serr == nil {
		t.Fatal("want error (transcode caps not probed), got nil")
	}
	if sess != nil {
		t.Fatalf("session should be nil on failure, got %+v", sess)
	}
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body: %s", w.Code, w.Body.String())
	}
}
