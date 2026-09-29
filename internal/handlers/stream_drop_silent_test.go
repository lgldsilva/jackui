package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// ─── Regression for the 2026-09-28 incident ─────────────────────────────────
//
// Before: DELETE /api/stream/:hash replied 200 {"message":"dropped"} even when
// Streamer.Drop refused the drop (activeReadGuard fed by the *arr torrent-get
// poll) — the UI had no way to know nothing happened. Now: an explicit action
// drops even right after the poll, and a real refusal (viewer lease /
// background download) becomes 409 with the reason.

// The exact incident scenario — *arr poll (Get) followed immediately by the
// click — now ends with the torrent REMOVED and a truthful 200.
func TestStreamDrop_DropsEvenRightAfterArrPoll(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	h, cleanup := s.SeedActiveForTesting("incident-torrent", time.Now().Add(-2*time.Hour))
	defer cleanup()

	// *arr torrent-get → GetUntouched; a Get here simulates a real read — the
	// fix covers both, since the explicit path bypasses the read guard.
	if _, err := s.Get(h); err != nil {
		t.Fatalf("Get (*arr poll): %v", err)
	}

	router := gin.New()
	router.DELETE("/api/stream/:hash", StreamDrop(s, nil, nil))

	req := httptest.NewRequest("DELETE", "/api/stream/"+h.HexString(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["message"] != "dropped" {
		t.Fatalf("message = %q, want 'dropped'", resp["message"])
	}
	if got := len(s.ActiveList()); got != 0 {
		t.Fatalf("ActiveList = %d entries after DELETE 200, want 0 (the 200 is now truthful)", got)
	}
}

// Real refusal with honest feedback: open player (viewer lease) → 409 with
// the reason, torrent stays active, and the row is NOT marked seed-stopped.
func TestStreamDrop_Conflict_WhenViewerLeaseActive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	h, cleanup := s.SeedActiveForTesting("being-watched", time.Now().Add(-2*time.Hour))
	defer cleanup()
	s.AcquireViewer(h)

	router := gin.New()
	router.DELETE("/api/stream/:hash", StreamDrop(s, nil, nil))

	req := httptest.NewRequest("DELETE", "/api/stream/"+h.HexString(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", w.Code, w.Body.String())
	}
	if got := len(s.ActiveList()); got != 1 {
		t.Fatalf("ActiveList = %d, want 1 — a viewer lease must keep the torrent", got)
	}
}

// Control: with no recent read and no viewer, the DELETE really drops.
func TestStreamDrop_ActuallyDrops_WhenNoRecentMonitoringRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	h, cleanup := s.SeedActiveForTesting("droppable-torrent", time.Now().Add(-2*time.Hour))
	defer cleanup()

	router := gin.New()
	router.DELETE("/api/stream/:hash", StreamDrop(s, nil, nil))

	req := httptest.NewRequest("DELETE", "/api/stream/"+h.HexString(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := len(s.ActiveList()); got != 0 {
		t.Fatalf("ActiveList = %d entries after DELETE, want 0", got)
	}
}

// Batch: refusing ONE hash (viewer lease) must not abort the batch nor turn
// into a phantom "dropped" — the refused hash lands in failed (as the RAW
// string the client sent), the others really drop, and the viewer-held
// torrent stays active.
func TestStreamDropBatch_RefusedHashLandsInFailed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	watched, cleanupWatched := s.SeedActiveForTesting("batch-being-watched", time.Now().Add(-2*time.Hour))
	defer cleanupWatched()
	idle, cleanupIdle := s.SeedActiveForTesting("batch-idle", time.Now().Add(-2*time.Hour))
	defer cleanupIdle()
	s.AcquireViewer(watched)

	// *arr poll between the seed and the click: must not influence the outcome.
	if _, err := s.Get(idle); err != nil {
		t.Fatalf("Get (*arr poll): %v", err)
	}

	router := gin.New()
	router.POST("/api/stream/drop/batch", StreamDropBatch(s, nil, nil))

	// The watched hash goes in UPPERCASE to prove `failed` echoes the raw
	// string (that's how the client matches it back to the selection).
	rawWatched := strings.ToUpper(watched.HexString())
	body := `{"hashes":["` + rawWatched + `","` + idle.HexString() + `","` + idle.HexString() + `"]}`
	w := postDropBatch(t, router, body)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Dropped int      `json:"dropped"`
		Total   int      `json:"total"`
		Failed  []string `json:"failed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Dropped != 1 || resp.Total != 3 {
		t.Fatalf("dropped=%d total=%d, want 1/3 (duplicate deduped, refused not counted)", resp.Dropped, resp.Total)
	}
	if len(resp.Failed) != 1 || resp.Failed[0] != rawWatched {
		t.Fatalf("failed = %v, want [%s] (raw string of the refused hash)", resp.Failed, rawWatched)
	}

	active := s.ActiveList()
	if len(active) != 1 {
		t.Fatalf("ActiveList = %d, want 1 — only the viewer-leased torrent survives", len(active))
	}
	if _, err := s.GetUntouched(watched); err != nil {
		t.Fatalf("watched torrent should still be active: %v", err)
	}
	if _, err := s.GetUntouched(idle); err == nil {
		t.Fatal("idle torrent should have been dropped by the batch")
	}
}

// With a store: the dropped hash's row is marked seed-stopped (so the next
// boot does not resurrect the auto-seed), but the REFUSED hash's row stays
// intact — the torrent is still alive, so marking seed_stopped would lie to
// the next boot.
func TestStreamDropBatch_SeedStoppedOnlyForDroppedRows(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	s := streamer.NewForTesting()

	watched, cleanupWatched := s.SeedActiveForTesting("store-being-watched", time.Now().Add(-2*time.Hour))
	defer cleanupWatched()
	idle, cleanupIdle := s.SeedActiveForTesting("store-idle", time.Now().Add(-2*time.Hour))
	defer cleanupIdle()
	s.AcquireViewer(watched)

	rowWatched := mustCreateDownload(t, store, watched.HexString(), downloads.StatusCompleted)
	rowIdle := mustCreateDownload(t, store, idle.HexString(), downloads.StatusCompleted)

	router := gin.New()
	router.POST("/api/stream/drop/batch", StreamDropBatch(s, nil, store))

	body := `{"hashes":["` + watched.HexString() + `","` + idle.HexString() + `"]}`
	w := postDropBatch(t, router, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	gotIdle, err := store.Get(rowIdle.UserID, rowIdle.ID)
	if err != nil {
		t.Fatalf("Get(idle): %v", err)
	}
	if gotIdle.SeedStoppedAt == nil {
		t.Fatal("dropped hash's row should be seed-stopped")
	}
	gotWatched, err := store.Get(rowWatched.UserID, rowWatched.ID)
	if err != nil {
		t.Fatalf("Get(watched): %v", err)
	}
	if gotWatched.SeedStoppedAt != nil {
		t.Fatalf("REFUSED hash's row must not be seed-stopped (torrent still alive): %v", gotWatched.SeedStoppedAt)
	}
}
