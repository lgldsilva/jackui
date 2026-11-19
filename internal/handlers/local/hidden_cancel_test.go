package local

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/middleware"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// The hidden-path gate must not depend on the request staying connected: a
// request whose context is already done on a hidden path is still refused before
// any handler work, for a resolve (GET) and for a mutation (DELETE).
func TestLocalHiddenGate_CancelledRequest_StillBlocks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	fav, err := streamer.NewFavorites(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}
	s.SetFavorites(fav)
	const uid = 7
	if err := fav.SetLocalPathHidden(context.Background(), uid, "Movies", "secret", true); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		reached := false
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Set("auth.claims", &auth.Claims{UserID: uid, Username: "u", Role: auth.RoleUser})
			c.Next()
		})
		r.Use(middleware.RevealHidden())
		r.Handle(method, "/x", LocalHiddenGate(s), func(c *gin.Context) {
			reached = true
			c.Status(http.StatusOK)
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/x?mount=Movies&path=secret/a.mkv", nil).WithContext(ctx)
		r.ServeHTTP(w, req)
		if reached || w.Code != http.StatusNotFound {
			t.Errorf("%s: status=%d handlerReached=%v; want 404 and handler not reached", method, w.Code, reached)
		}
	}
}

// A hidden-set read that FAILS (here: a store whose pool is closed) closes the
// gate instead of reading as "nothing hidden".
func TestIsLocalPathHidden_ReadError_FailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := seededPool(t)
	fav, err := streamer.NewFavorites(pool)
	if err != nil {
		t.Fatal(err)
	}
	s := streamer.NewForTesting()
	s.SetFavorites(fav)
	_ = pool.Close()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	c.Set("auth.claims", &auth.Claims{UserID: 7, Username: "u", Role: auth.RoleUser})
	if !IsLocalPathHidden(c, s, "Movies", "public/a.mkv") {
		t.Fatal("a failed hidden-set read must close the gate, not open it")
	}
}
