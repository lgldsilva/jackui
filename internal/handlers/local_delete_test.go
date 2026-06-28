package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/local"
)

// TestLocalDeleteRemovesLinkedTorrent verifies that deleting a local file also
// removes the download row whose file_path points at it (the "remove tudo"
// behavior). Streamer is nil here, so Drop/ClearEntry/favorite are skipped — we
// assert the path-linking + row removal, which is the core of the feature.
func TestLocalDeleteRemovesLinkedTorrent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	meus := t.TempDir()
	file := filepath.Join(meus, "filme.mkv")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	dls, err := downloads.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}
	defer dls.Close()
	if _, err := dls.Create(downloads.Download{
		UserID:    1,
		InfoHash:  "aabbccddeeff00112233445566778899aabbccdd",
		FileIndex: 0,
		FilePath:  file,
		Name:      "filme.mkv",
		Magnet:    "magnet:?xt=urn:btih:aabbccddeeff00112233445566778899aabbccdd",
	}); err != nil {
		t.Fatal(err)
	}

	b := local.NewBrowser([]config.ExternalMount{{Name: "Meus downloads", Path: meus}})
	router := gin.New()
	router.DELETE("/api/local/file", LocalDelete(b, dls, nil))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/local/file?mount=Meus+downloads&path=filme.mkv", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		TorrentsRemoved int `json:"torrentsRemoved"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.TorrentsRemoved != 1 {
		t.Errorf("torrentsRemoved=%d, want 1", resp.TorrentsRemoved)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("file was not deleted from disk")
	}
	rows, _ := dls.List(1)
	if len(rows) != 0 {
		t.Errorf("download row still present (%d), linked torrent not removed", len(rows))
	}
}
