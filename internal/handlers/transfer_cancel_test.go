package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/transfer"
)

func TestTransfersCancel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tr := transfer.New(2)
	job := tr.Start("move", "download-move", 1, 100)

	router := gin.New()
	router.DELETE("/api/transfers/:id", TransfersCancel(tr))

	// Cancel an existing job → 204 + job canceled.
	req := httptest.NewRequest(http.MethodDelete, "/api/transfers/"+job.ID(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	if !job.Canceled() {
		t.Error("job should be canceled after the DELETE")
	}

	// Unknown id → 404.
	req2 := httptest.NewRequest(http.MethodDelete, "/api/transfers/does-not-exist", nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("unknown id status = %d, want 404", w2.Code)
	}
}
