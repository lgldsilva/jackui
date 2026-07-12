package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
)

// A COMPLETED download must be served from the on-disk file (complete=true →
// ForceVOD) instead of re-activating the torrent. With transcode caps not
// probed yet the session can't start, so the handler answers 500 — what matters
// here is that the source resolution took the completed-store path and the
// session start was attempted with it (no torrent involved at all).
func TestStreamHLSMaster_CompletedDownloadUsesFile(t *testing.T) {
	transcode.ResetCachedForTesting()
	gin.SetMode(gin.TestMode)

	s := streamer.NewForTesting()
	mgr, err := transcode.NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	st, err := downloads.New(seededPool(t))
	if err != nil {
		t.Fatalf("downloads.New: %v", err)
	}
	defer st.Close()

	hash := "0123456789abcdef0123456789abcdef01234567"
	media := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(media, []byte("not-really-media"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := st.Create(downloads.Download{
		UserID: 1, InfoHash: hash, FileIndex: 0,
		Magnet: "magnet:?xt=urn:btih:" + hash, Name: "video",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := st.SetFilePath(1, d.ID, media); err != nil {
		t.Fatalf("SetFilePath: %v", err)
	}
	if _, err := st.SetStatusForUser(1, downloads.StatusCompleted); err != nil {
		t.Fatalf("SetStatusForUser: %v", err)
	}

	r := gin.New()
	r.GET("/hls/:hash/:file/master.m3u8", StreamHLSMaster(s, mgr, st, nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/hls/"+hash+"/0/master.m3u8", nil))

	// Caps were reset → the session can't launch. 500 proves the handler got
	// PAST source resolution (a missing source would have been a 404).
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (caps not probed); body: %s", w.Code, w.Body.String())
	}
}
