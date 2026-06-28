package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/downloads"
)

// fakeRemover records Remove() calls so we can assert the delete handler
// notified the worker (authoritative teardown) for each deleted row.
type fakeRemover struct {
	mu    sync.Mutex
	calls []struct {
		id       int
		infoHash string
	}
}

func (f *fakeRemover) Remove(id int, infoHash string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, struct {
		id       int
		infoHash string
	}{id, infoHash})
}

func (f *fakeRemover) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// claimsMW injects auth claims into the gin context for a test request.
func claimsMW(claims *auth.Claims) gin.HandlerFunc {
	return func(c *gin.Context) {
		if claims != nil {
			c.Set("auth.claims", claims)
		}
		c.Next()
	}
}

const delTestHash = "0123456789abcdef0123456789abcdef01234567"

// CONFIRMED ROOT CAUSE: an admin in the "all users" view deletes another user's
// row. The old store.Delete(adminID, otherUsersRowID) matched 0 rows (scoped to
// the admin's user_id) → 500/silent → the 2s poll re-showed the row. With the
// admin-aware DeleteScoped, the admin can remove it: 204, row gone, worker
// notified.
func TestDownloadsDelete_AdminCrossUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	// Row owned by user 7.
	d, err := store.Create(downloads.Download{UserID: 7, InfoHash: delTestHash, FileIndex: 0, Magnet: "m", Name: "Others"})
	if err != nil {
		t.Fatal(err)
	}
	rm := &fakeRemover{}

	router := gin.New()
	// Admin (user 1) acting in the all-users view.
	router.Use(claimsMW(&auth.Claims{UserID: 1, Role: auth.RoleAdmin}))
	router.DELETE("/api/downloads/:id", DownloadsDelete(store, rm))

	req := httptest.NewRequest("DELETE", "/api/downloads/"+itoa(d.ID), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204; body=%s", w.Code, w.Body.String())
	}
	if got, _ := store.Get(7, d.ID); got != nil {
		t.Error("admin delete must remove another user's row")
	}
	if rm.count() != 1 {
		t.Errorf("worker.Remove calls=%d want 1 (authoritative teardown)", rm.count())
	}
	if rm.calls[0].infoHash != delTestHash {
		t.Errorf("worker.Remove infoHash=%q want %q", rm.calls[0].infoHash, delTestHash)
	}
}

// A NON-admin must NOT delete another user's row: the scoped delete matches 0
// rows, returns 204 (idempotent — "no such row for you"), the owner's row
// SURVIVES, and the worker is NOT notified (nothing was removed).
func TestDownloadsDelete_NonAdminCannotCrossUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	d, err := store.Create(downloads.Download{UserID: 7, InfoHash: delTestHash, FileIndex: 0, Magnet: "m", Name: "Others"})
	if err != nil {
		t.Fatal(err)
	}
	rm := &fakeRemover{}

	router := gin.New()
	router.Use(claimsMW(&auth.Claims{UserID: 2, Role: auth.RoleUser})) // not the owner, not admin
	router.DELETE("/api/downloads/:id", DownloadsDelete(store, rm))

	req := httptest.NewRequest("DELETE", "/api/downloads/"+itoa(d.ID), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204; body=%s", w.Code, w.Body.String())
	}
	if got, _ := store.Get(7, d.ID); got == nil {
		t.Error("a non-owner's delete must NOT remove the owner's row")
	}
	if rm.count() != 0 {
		t.Errorf("worker.Remove calls=%d want 0 (nothing removed)", rm.count())
	}
}

// The owner deletes their own row: 204, gone, worker notified.
func TestDownloadsDelete_OwnerHappyPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	d, err := store.Create(downloads.Download{UserID: 5, InfoHash: delTestHash, FileIndex: 0, Magnet: "m", Name: "Mine"})
	if err != nil {
		t.Fatal(err)
	}
	rm := &fakeRemover{}

	router := gin.New()
	router.Use(claimsMW(&auth.Claims{UserID: 5, Role: auth.RoleUser}))
	router.DELETE("/api/downloads/:id", DownloadsDelete(store, rm))

	req := httptest.NewRequest("DELETE", "/api/downloads/"+itoa(d.ID), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204; body=%s", w.Code, w.Body.String())
	}
	if got, _ := store.Get(5, d.ID); got != nil {
		t.Error("owner delete must remove the row")
	}
	if rm.count() != 1 {
		t.Errorf("worker.Remove calls=%d want 1", rm.count())
	}
}

// A nil worker (worker not running) must not panic — the delete still succeeds.
func TestDownloadsDelete_NilWorkerNoPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	d, _ := store.Create(downloads.Download{UserID: 5, InfoHash: delTestHash, FileIndex: 0, Magnet: "m", Name: "Mine"})

	router := gin.New()
	router.Use(claimsMW(&auth.Claims{UserID: 5, Role: auth.RoleUser}))
	router.DELETE("/api/downloads/:id", DownloadsDelete(store, nil))

	req := httptest.NewRequest("DELETE", "/api/downloads/"+itoa(d.ID), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", w.Code)
	}
}

// Batch delete: admin removes a mix of own + other-users rows + a missing id.
// `deleted` counts every non-error (missing id is idempotent), `failed` is
// empty, and the worker is notified once per actually-removed row.
func TestDownloadsBatchDelete_AdminMixed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	a, _ := store.Create(downloads.Download{UserID: 7, InfoHash: delTestHash, FileIndex: 0, Magnet: "m", Name: "A"})
	b, _ := store.Create(downloads.Download{UserID: 9, InfoHash: delTestHash, FileIndex: 1, Magnet: "m", Name: "B"})
	rm := &fakeRemover{}

	router := gin.New()
	router.Use(claimsMW(&auth.Claims{UserID: 1, Role: auth.RoleAdmin}))
	router.POST("/api/downloads/batch/delete", DownloadsBatchDelete(store, rm))

	body, _ := json.Marshal(map[string][]int{"ids": {a.ID, b.ID, 999999}})
	req := httptest.NewRequest("POST", "/api/downloads/batch/delete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", w.Code)
	}
	var resp struct {
		Deleted int   `json:"deleted"`
		Total   int   `json:"total"`
		Failed  []int `json:"failed"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Deleted != 3 || resp.Total != 3 || len(resp.Failed) != 0 {
		t.Errorf("deleted=%d total=%d failed=%v want 3/3/[]", resp.Deleted, resp.Total, resp.Failed)
	}
	// Two real rows actually removed → two worker notifications (the missing id
	// returns a nil row → notifyRemoved is a no-op).
	if rm.count() != 2 {
		t.Errorf("worker.Remove calls=%d want 2", rm.count())
	}
	if got, _ := store.Get(7, a.ID); got != nil {
		t.Error("row A must be gone")
	}
	if got, _ := store.Get(9, b.ID); got != nil {
		t.Error("row B must be gone")
	}
}

// A genuine store error (DB closed) surfaces as 500, not a swallowed no-op —
// the frontend can then show the error instead of silently re-showing the row.
func TestDownloadsDelete_StoreError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := seededPool(t)
	store, err := downloads.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := store.Create(downloads.Download{UserID: 5, InfoHash: delTestHash, FileIndex: 0, Magnet: "m", Name: "x"})
	pool.Close() // force the lookup/delete to error
	rm := &fakeRemover{}

	router := gin.New()
	router.Use(claimsMW(&auth.Claims{UserID: 5, Role: auth.RoleUser}))
	router.DELETE("/api/downloads/:id", DownloadsDelete(store, rm))

	req := httptest.NewRequest("DELETE", "/api/downloads/"+itoa(d.ID), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500 on store error", w.Code)
	}
	if rm.count() != 0 {
		t.Error("worker must NOT be notified when the store delete errored")
	}
}

// Batch delete on a closed store: every id errors, so `failed` carries them all
// and the worker is never notified.
func TestDownloadsBatchDelete_AllFailed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := seededPool(t)
	store, err := downloads.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := store.Create(downloads.Download{UserID: 5, InfoHash: delTestHash, FileIndex: 0, Magnet: "m", Name: "a"})
	b, _ := store.Create(downloads.Download{UserID: 5, InfoHash: delTestHash, FileIndex: 1, Magnet: "m", Name: "b"})
	pool.Close()
	rm := &fakeRemover{}

	router := gin.New()
	router.Use(claimsMW(&auth.Claims{UserID: 5, Role: auth.RoleUser}))
	router.POST("/api/downloads/batch/delete", DownloadsBatchDelete(store, rm))

	body, _ := json.Marshal(map[string][]int{"ids": {a.ID, b.ID}})
	req := httptest.NewRequest("POST", "/api/downloads/batch/delete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", w.Code)
	}
	var resp struct {
		Deleted int   `json:"deleted"`
		Failed  []int `json:"failed"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Deleted != 0 || len(resp.Failed) != 2 {
		t.Errorf("deleted=%d failed=%v want 0/[2 ids]", resp.Deleted, resp.Failed)
	}
	if rm.count() != 0 {
		t.Error("worker must not be notified for failed deletes")
	}
}

// A malformed batch body is rejected with 400.
func TestDownloadsBatchDelete_BadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	router := gin.New()
	router.Use(claimsMW(&auth.Claims{UserID: 5, Role: auth.RoleUser}))
	router.POST("/api/downloads/batch/delete", DownloadsBatchDelete(store, &fakeRemover{}))

	req := httptest.NewRequest("POST", "/api/downloads/batch/delete", bytes.NewReader([]byte("{not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}
