package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/transfer"
)

type transfersResp struct {
	Transfers []transfer.Snapshot `json:"transfers"`
}

func doTransfersList(t *testing.T, tr *transfer.Tracker) transfersResp {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/transfers", nil)
	TransfersList(tr)(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp transfersResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v; body: %s", err, w.Body.String())
	}
	return resp
}

// A nil tracker must still respond with an empty (non-null) list so the dock can
// poll safely before downloads are wired.
func TestTransfersList_NilTrackerEmpty(t *testing.T) {
	resp := doTransfersList(t, nil)
	if resp.Transfers == nil {
		t.Fatal("transfers must serialize as [] not null")
	}
	if len(resp.Transfers) != 0 {
		t.Fatalf("want 0 transfers, got %d", len(resp.Transfers))
	}
}

// A running job appears in the list with its label, kind and totals.
func TestTransfersList_ReportsActiveJob(t *testing.T) {
	tr := transfer.New()
	job := tr.Start("Movie.mkv", "local-move", 1, 1000)
	job.AddBytes(500)

	resp := doTransfersList(t, tr)
	if len(resp.Transfers) != 1 {
		t.Fatalf("want 1 transfer, got %d", len(resp.Transfers))
	}
	got := resp.Transfers[0]
	if got.Label != "Movie.mkv" || got.Kind != "local-move" {
		t.Fatalf("label/kind = %q/%q", got.Label, got.Kind)
	}
	if got.BytesDone != 500 || got.BytesTotal != 1000 {
		t.Fatalf("bytes = %d/%d, want 500/1000", got.BytesDone, got.BytesTotal)
	}
	if got.Status != transfer.StatusRunning {
		t.Fatalf("status = %q, want running", got.Status)
	}
}
