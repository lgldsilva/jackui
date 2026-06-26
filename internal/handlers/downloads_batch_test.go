package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/downloads"
)

func batchRouter(store *downloads.Store) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// dests=nil is fine: Resolve returns ("","",nil) for an empty destBase.
	r.POST("/api/downloads/batch", DownloadsBatchCreate(store, nil))
	return r
}

func postBatch(t *testing.T, r *gin.Engine, body map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/downloads/batch", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// The happy path: one POST enqueues every selected file of one torrent and
// returns the created rows.
func TestDownloadsBatchCreate_CreatesAllFiles(t *testing.T) {
	store := newDownloadsStore(t)
	r := batchRouter(store)

	w := postBatch(t, r, map[string]interface{}{
		"infoHash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"magnet":   "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"name":     "Season Pack",
		"files": []map[string]interface{}{
			{"fileIndex": 0, "filePath": "S01/E01.mkv", "fileSize": 100},
			{"fileIndex": 1, "filePath": "S01/E02.mkv", "fileSize": 200},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Created  []downloads.Download `json:"created"`
		Requeued int                  `json:"requeued"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Created) != 2 || resp.Requeued != 0 {
		t.Fatalf("created=%d requeued=%d, want 2/0", len(resp.Created), resp.Requeued)
	}
	if resp.Created[0].FileIndex != 0 || resp.Created[1].FileIndex != 1 {
		t.Fatalf("file indices not preserved in order: %d, %d", resp.Created[0].FileIndex, resp.Created[1].FileIndex)
	}
	// Both rows persisted.
	all, _ := store.List(0)
	if len(all) != 2 {
		t.Fatalf("store has %d rows, want 2", len(all))
	}
}

func TestDownloadsBatchCreate_MissingInfoHash(t *testing.T) {
	r := batchRouter(newDownloadsStore(t))
	w := postBatch(t, r, map[string]interface{}{
		"magnet": "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"files":  []map[string]interface{}{{"fileIndex": 0}},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDownloadsBatchCreate_MissingMagnet(t *testing.T) {
	r := batchRouter(newDownloadsStore(t))
	w := postBatch(t, r, map[string]interface{}{
		"infoHash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"files":    []map[string]interface{}{{"fileIndex": 0}},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDownloadsBatchCreate_EmptyFiles(t *testing.T) {
	r := batchRouter(newDownloadsStore(t))
	w := postBatch(t, r, map[string]interface{}{
		"infoHash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"magnet":   "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"files":    []map[string]interface{}{},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for empty files", w.Code)
	}
}

// A re-POST of an already-enqueued file counts as requeued, not created — the
// idempotency carries through the batch handler.
func TestDownloadsBatchCreate_Requeued(t *testing.T) {
	store := newDownloadsStore(t)
	r := batchRouter(store)
	body := map[string]interface{}{
		"infoHash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"magnet":   "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"name":     "Pack",
		"files":    []map[string]interface{}{{"fileIndex": 0, "filePath": "E01.mkv", "fileSize": 100}},
	}
	if w := postBatch(t, r, body); w.Code != http.StatusOK {
		t.Fatalf("first POST status = %d", w.Code)
	}
	w := postBatch(t, r, body)
	var resp struct {
		Created  []downloads.Download `json:"created"`
		Requeued int                  `json:"requeued"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Requeued != 1 || len(resp.Created) != 1 {
		t.Fatalf("created=%d requeued=%d, want created 1 / requeued 1", len(resp.Created), resp.Requeued)
	}
	if all, _ := store.List(0); len(all) != 1 {
		t.Fatalf("duplicate row inserted: %d rows", len(all))
	}
}
