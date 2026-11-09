package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// viewerRouter mounts the viewer + drop routes behind a fake auth middleware
// that takes the user id from the X-Test-User header.
func viewerRouter(s *streamer.Streamer) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		switch c.GetHeader("X-Test-User") {
		case "1":
			c.Set("auth.claims", &auth.Claims{UserID: 1})
		case "2":
			c.Set("auth.claims", &auth.Claims{UserID: 2})
		}
		c.Next()
	})
	r.POST("/api/stream/:hash/viewer", StreamViewerOpen(s))
	r.DELETE("/api/stream/:hash/viewer", StreamViewerClose(s, nil))
	r.DELETE("/api/stream/:hash", StreamDrop(s, nil, nil))
	return r
}

func viewerDo(r *gin.Engine, method, path, user string) int {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("X-Test-User", user)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// TestStreamViewerClose_ForeignReleaseKeepsLease: user 2 sending DELETE
// /viewer (any number of times) must not end user 1's lease — the drop must
// still be refused with 409 because user 1 is watching.
func TestStreamViewerClose_ForeignReleaseKeepsLease(t *testing.T) {
	s := streamer.NewForTesting()
	h, cleanup := s.SeedActiveForTesting("shared-watch", time.Now().Add(-2*time.Hour))
	defer cleanup()
	r := viewerRouter(s)
	base := "/api/stream/" + h.HexString()

	if code := viewerDo(r, http.MethodPost, base+"/viewer", "1"); code != http.StatusOK {
		t.Fatalf("acquire = %d", code)
	}
	for i := 0; i < 2; i++ {
		if code := viewerDo(r, http.MethodDelete, base+"/viewer", "2"); code != http.StatusOK {
			t.Fatalf("foreign release = %d", code)
		}
	}
	if code := viewerDo(r, http.MethodDelete, base, "1"); code != http.StatusConflict {
		t.Fatalf("drop after foreign releases = %d, want 409 (user 1 still watching)", code)
	}
}
