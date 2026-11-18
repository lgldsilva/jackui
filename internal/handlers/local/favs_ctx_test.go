package local

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/streamer"
)

func TestLocalListHidden_ClientGone_499(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	fav, err := streamer.NewFavorites(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}
	s.SetFavorites(fav)
	r := gin.New()
	r.GET("/h", LocalListHidden(s))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/h", nil).WithContext(ctx))
	if w.Code != 499 || w.Body.Len() != 0 {
		t.Fatalf("status %d body %q; want bodiless 499", w.Code, w.Body.String())
	}
}
