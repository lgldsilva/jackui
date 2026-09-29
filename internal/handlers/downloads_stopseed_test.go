package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// stopSeedRecorder records the IDs the handler asked the worker to tear down.
type stopSeedRecorder struct {
	ids []int
}

func (r *stopSeedRecorder) Remove(id int, infoHash string) {
	r.ids = append(r.ids, id)
}

// stopSeedRouter wires the stop-seed routes against a testing streamer and a
// recorder for the worker teardown. userID defaults to 0 (no auth middleware),
// matching rows created without UserID — same convention as the cov_hgA tests.
func stopSeedRouter(store *downloads.Store, remover *stopSeedRecorder) *gin.Engine {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	router := gin.New()
	router.POST("/api/downloads/:id/stop-seed", DownloadsStopSeed(store, s, remover))
	router.POST("/api/downloads/batch/stop-seed", DownloadsBatchStopSeed(store, s, remover))
	return router
}

func mustCreateDownload(t *testing.T, store *downloads.Store, infoHash, status string) downloads.Download {
	t.Helper()
	d, err := store.Create(downloads.Download{InfoHash: infoHash, Magnet: MagnetPrefix + infoHash, Name: "x"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if status != "" {
		if err := store.SetStatus(d.UserID, d.ID, status); err != nil {
			t.Fatalf("SetStatus(%s): %v", status, err)
		}
	}
	return *d
}

// "Stop" must remove the item from the downloads list: the user expects the
// torrent to vanish from the downloads area, not to merely migrate from "Seeding"
// to "On disk". The original bug: stop-seed only set seed_stopped_at and the row
// kept being returned by GET /api/downloads forever.
func TestDownloadsStopSeed_CompletedRowRemovedFromList(t *testing.T) {
	store := hgAStore(t)
	d := mustCreateDownload(t, store, hgAValidHash, downloads.StatusCompleted)
	remover := &stopSeedRecorder{}
	router := stopSeedRouter(store, remover)

	w := hgADo(router, "POST", "/api/downloads/"+strconv.Itoa(d.ID)+"/stop-seed", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", w.Code)
	}
	if _, err := store.Get(d.UserID, d.ID); err == nil {
		t.Error("completed row must be deleted from the downloads list after stop-seed")
	}
	rows, err := store.List(d.UserID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("downloads list must be empty after stop-seed, got %d rows", len(rows))
	}
	if len(remover.ids) != 1 || remover.ids[0] != d.ID {
		t.Errorf("worker must tear down row %d, got %v", d.ID, remover.ids)
	}
}

// Pausing before stopping must not void the action: the details modal offers
// "Stop seeding" for any status, so the pause→stop flow must also
// remove the row. The original bug: StopSeed only wrote with
// status='completed', so the paused row wasn't even marked.
func TestDownloadsStopSeed_PausedRowRemovedFromList(t *testing.T) {
	store := hgAStore(t)
	d := mustCreateDownload(t, store, hgAValidHash, downloads.StatusPaused)
	remover := &stopSeedRecorder{}
	router := stopSeedRouter(store, remover)

	w := hgADo(router, "POST", "/api/downloads/"+strconv.Itoa(d.ID)+"/stop-seed", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", w.Code)
	}
	if _, err := store.Get(d.UserID, d.ID); err == nil {
		t.Error("paused row must be deleted from the downloads list after stop-seed")
	}
}

// Batch stop-seed follows the same semantics as the single endpoint: the rows disappear.
func TestDownloadsBatchStopSeed_RemovesRows(t *testing.T) {
	store := hgAStore(t)
	secondHash := "c1c2c3c4c5c6c7c8c9c0c1c2c3c4c5c6c7c8c9c0c1c2c3c4"
	d1 := mustCreateDownload(t, store, hgAValidHash, downloads.StatusCompleted)
	d2 := mustCreateDownload(t, store, secondHash, downloads.StatusCompleted)
	remover := &stopSeedRecorder{}
	router := stopSeedRouter(store, remover)

	body, _ := json.Marshal(map[string]any{"ids": []int{d1.ID, d2.ID}})
	w := hgADo(router, "POST", "/api/downloads/batch/stop-seed", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", w.Code)
	}
	var resp struct {
		Affected int   `json:"affected"`
		Failed   []int `json:"failed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Affected != 2 || len(resp.Failed) != 0 {
		t.Errorf("affected=%d failed=%v, want affected=2 failed=[]", resp.Affected, resp.Failed)
	}
	rows, err := store.List(d1.UserID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("downloads list must be empty after batch stop-seed, got %d rows", len(rows))
	}
	if len(remover.ids) != 2 {
		t.Errorf("worker must tear down both rows, got %v", remover.ids)
	}
}
