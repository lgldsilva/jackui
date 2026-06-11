package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// POST /api/downloads with the whole-torrent sentinel must create ONE row with
// fileIndex preserved — this is the "Baixar tudo" path (1 request instead of
// 1-per-file).
func TestDownloadsCreate_WholeTorrentSentinel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.POST("/api/downloads", DownloadsCreate(store))

	body := map[string]interface{}{
		"infoHash":  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"magnet":    "magnet:?xt=urn:btih:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"name":      "Big Pack",
		"fileIndex": downloads.FileIndexWholeTorrent,
		"fileSize":  123456,
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/downloads", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var d downloads.Download
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.FileIndex != downloads.FileIndexWholeTorrent {
		t.Fatalf("fileIndex = %d, want %d", d.FileIndex, downloads.FileIndexWholeTorrent)
	}
	if d.Status != downloads.StatusQueued {
		t.Fatalf("status = %q, want queued", d.Status)
	}
	if d.FileSize != 123456 {
		t.Fatalf("fileSize = %d, want the aggregate 123456", d.FileSize)
	}

	// Idempotent: a second identical create returns the SAME row.
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/api/downloads", bytes.NewReader(b))
	req2.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w2, req2)
	var d2 downloads.Download
	_ = json.Unmarshal(w2.Body.Bytes(), &d2)
	if d2.ID != d.ID {
		t.Fatalf("re-enqueue created a new row: %d != %d", d2.ID, d.ID)
	}
}

// Recheck on a whole-torrent row must hit RecheckAllFiles (every file), while a
// per-file row keeps hitting RecheckFile. Both 502 here — the test streamer has
// no active torrent — which is exactly the branch split we want to pin.
func TestDownloadsRecheck_WholeTorrentUsesRecheckAllFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	s := streamer.NewForTesting()
	router := gin.New()
	router.POST("/api/downloads/:id/recheck", DownloadsRecheck(store, s))

	hash := strings.Repeat("ab", 20)
	// Magnet com scheme não suportado: EnsureActive falha RÁPIDO (sem tocar o
	// torrent client, que o NewForTesting não tem) e o handler segue pro recheck.
	whole, err := store.Create(downloads.Download{
		InfoHash: hash, FileIndex: downloads.FileIndexWholeTorrent,
		Magnet: "x-test://nope", Name: "W",
	})
	if err != nil {
		t.Fatalf("Create whole: %v", err)
	}
	perFile, err := store.Create(downloads.Download{
		InfoHash: hash, FileIndex: 0, Magnet: "x-test://nope", Name: "W",
	})
	if err != nil {
		t.Fatalf("Create per-file: %v", err)
	}

	for name, id := range map[string]int{"whole": whole.ID, "perFile": perFile.ID} {
		req := httptest.NewRequest("POST", fmt.Sprintf("/api/downloads/%d/recheck", id), nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadGateway {
			t.Fatalf("%s: status = %d, want 502 (torrent not active); body: %s", name, w.Code, w.Body.String())
		}
	}
}

// fileIndex below the sentinel floor is a malformed request → 400.
func TestDownloadsCreate_RejectsFileIndexBelowSentinel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)

	router := gin.New()
	router.POST("/api/downloads", DownloadsCreate(store))

	body := map[string]interface{}{
		"infoHash":  "cccccccccccccccccccccccccccccccccccccccc",
		"magnet":    "magnet:?xt=urn:btih:cccccccccccccccccccccccccccccccccccccccc",
		"name":      "Bad",
		"fileIndex": -3,
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/downloads", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}
