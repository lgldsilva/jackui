package local

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/transcode"
)

func TestWaitLocalPlaylist_ClientGone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest("GET", "/", nil).WithContext(ctx)

	if waitLocalPlaylist(c, &transcode.HLSSession{Dir: t.TempDir()}) {
		t.Fatal("wait must fail when the client is gone")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}
