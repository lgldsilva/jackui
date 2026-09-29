package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/downloads"
)

func pauseRouter(store *downloads.Store) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PATCH("/api/downloads/:id/pause", DownloadsPause(store))
	router.PATCH("/api/downloads/batch/pause", DownloadsBatchPause(store))
	return router
}

// PATCH /downloads/:id/pause on a completed item must be refused: letting it
// through turned `completed` into `paused`, and then the card lost the completed
// actions (Promote / Stop and remove / Open location) — the item got stuck
// in the list with no way to remove it while keeping the files on disk.
func TestDownloadsPause_RejectsCompleted(t *testing.T) {
	store := hgAStore(t)
	d := mustCreateDownload(t, store, hgAValidHash, downloads.StatusCompleted)
	router := pauseRouter(store)

	w := hgADo(router, "PATCH", "/api/downloads/"+strconv.Itoa(d.ID)+"/pause", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409", w.Code)
	}

	got, err := store.Get(d.UserID, d.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != downloads.StatusCompleted {
		t.Errorf("status=%q, want %q — a row must not leave completed", got.Status, downloads.StatusCompleted)
	}
}

// A download in progress remains pausable — the guard only covers terminal states.
func TestDownloadsPause_AllowsDownloading(t *testing.T) {
	store := hgAStore(t)
	d := mustCreateDownload(t, store, hgAValidHash, downloads.StatusDownloading)
	router := pauseRouter(store)

	w := hgADo(router, "PATCH", "/api/downloads/"+strconv.Itoa(d.ID)+"/pause", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", w.Code)
	}

	got, _ := store.Get(d.UserID, d.ID)
	if got.Status != downloads.StatusPaused {
		t.Errorf("status=%q, want %q", got.Status, downloads.StatusPaused)
	}
}

// The batch must not fail as a whole because of a terminal item in the selection: it
// pauses what it can and reports `affected` with the real count (the frontend uses that number).
func TestDownloadsBatchPause_SkipsCompletedRows(t *testing.T) {
	store := hgAStore(t)
	secondHash := "c1c2c3c4c5c6c7c8c9c0c1c2c3c4c5c6c7c8c9c0c1c2c3c4"
	active := mustCreateDownload(t, store, hgAValidHash, downloads.StatusDownloading)
	completed := mustCreateDownload(t, store, secondHash, downloads.StatusCompleted)
	router := pauseRouter(store)

	body, _ := json.Marshal(map[string]any{"ids": []int{active.ID, completed.ID}})
	w := hgADo(router, "PATCH", "/api/downloads/batch/pause", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", w.Code)
	}
	var resp struct {
		Affected int `json:"affected"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Affected != 1 {
		t.Errorf("affected=%d, want 1 — only the active row can be paused", resp.Affected)
	}

	gotCompleted, _ := store.Get(completed.UserID, completed.ID)
	if gotCompleted.Status != downloads.StatusCompleted {
		t.Errorf("completed turned into %q on batch pause", gotCompleted.Status)
	}
}
