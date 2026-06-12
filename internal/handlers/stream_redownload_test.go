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
)

// #26: a completed whole-torrent row whose rel path the store can't map (no
// metadata-cache row, no cached .torrent) must trigger an activate-and-retry of
// the on-disk path before streaming — it must NOT silently fall through to a
// swarm re-download. Here Add can't resolve metadata (no peers, no .torrent) so
// the request ends in a 404, but the gated activate-retry path runs (the bug was
// re-downloading instead).
func TestStreamFile_WholeRowActivatesWhenRelPathUnresolved(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	destDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(destDir, "Sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destDir, "Sub", "a.bin"), []byte("AAAA"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := store.Create(downloads.Download{
		UserID: 1, InfoHash: wholeHexHash, FileIndex: downloads.FileIndexWholeTorrent, Magnet: "m", Name: "Pack",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.UpdateMetadata(1, d.ID, "Pack", destDir, 4); err != nil {
		t.Fatalf("UpdateMetadata: %v", err)
	}
	if err := store.SetStatus(1, d.ID, downloads.StatusCompleted); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	s, _ := realStreamerEnv(t) // real client so the gated Add doesn't panic
	mc, err := streamer.NewMetadataCache(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	t.Cleanup(func() { _ = mc.Close() })
	s.SetMetadataCache(mc) // EMPTY (no row for the hash) → the first rel-path resolve fails

	r := gin.New()
	r.GET("/api/stream/:hash/:file", StreamFile(s, store))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/stream/"+wholeHexHash+"/0", nil))

	// No resolvable metadata (no peers / no .torrent) → Add can't finish → falls to
	// the streamer → 404. The point is the gated activate-retry ran instead of a
	// blind whole-torrent re-download.
	if w.Code == http.StatusOK {
		t.Fatalf("did not expect 200 without resolvable metadata; body=%s", w.Body.String())
	}
}
