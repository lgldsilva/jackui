package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/jackett"
)

func TestSearchSSE_NoQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.GET("/api/search/stream", SearchSSE(nil, nil, nil, nil))

	req := httptest.NewRequest("GET", "/api/search/stream", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestDedupKey_HashLessEntryUsesTrackerTitleSize(t *testing.T) {
	// Regression: jackui results have no infoHash/magnet. dedupKey
	// previously returned "" for them, so the same result was emitted by
	// both the cache phase and the live phase. The fix uses tracker|title|size
	// as the key so cache+live duplicates are correctly suppressed.
	r := jackett.Result{
		Tracker: "Amigos Share Club",
		Title:   "Filme Teste 2024 1080p",
		Size:    4_000_000_000,
	}
	got := dedupKey(r)
	want := fmt.Sprintf("%s|%s|%d", "amigos share club", "filme teste 2024 1080p", r.Size)
	if got != want {
		t.Errorf("dedupKey = %q, want %q", got, want)
	}
}

func TestDedupKey_HashBearingEntryUsesHash(t *testing.T) {
	h := "c12fe1c06bba254a9dc9f519b335aa7c1367a88a"
	r := jackett.Result{
		Tracker:  "ThePirateBay",
		Title:    "Filme Teste 2024 1080p",
		InfoHash: h,
	}
	got := dedupKey(r)
	if got != h {
		t.Errorf("dedupKey = %q, want %q", got, h)
	}
}

func TestDedupKey_EmptyReturnedWhenNoData(t *testing.T) {
	r := jackett.Result{}
	if got := dedupKey(r); got != "" {
		t.Errorf("dedupKey = %q, want empty string", got)
	}
}


