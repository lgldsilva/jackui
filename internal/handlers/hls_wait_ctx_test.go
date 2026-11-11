package handlers

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
)

// A client that disconnected while the first segment encodes gets no answer
// and no stall diagnosis: the wait returns on the request context.
func TestWaitForMasterPlaylist_ClientGone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest("GET", "/", nil).WithContext(ctx)

	hc := &hlsCtx{c: c, s: streamer.NewForTesting()}
	if waitForMasterPlaylist(hc, &transcode.HLSSession{Dir: t.TempDir()}) {
		t.Fatal("wait must fail when the client is gone")
	}
	if c.Writer.Written() {
		t.Fatalf("no response expected for a gone client, got %d %s", w.Code, w.Body.String())
	}
}
