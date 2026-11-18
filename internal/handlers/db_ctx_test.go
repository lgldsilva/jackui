package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/history"
	"github.com/lgldsilva/jackui/internal/jackett"
)

// The handler must hand the request's own context to the store: if it used a
// detached context the query would succeed (200 + data) despite the dead
// client. With the request ctx the DB call is refused, and the handler turns
// that into a bodiless 499 instead of a 500 carrying the driver's message.
func TestGetHistory_ClientGoneAbortsWithoutLeakingDBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := history.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), "q", []jackett.Result{{Title: "T", InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}, 1, false); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/history", nil).WithContext(ctx)
	setAuth(c, 1, false)

	GetHistory(store)(c)

	if w.Code != 499 {
		t.Fatalf("status = %d, want 499", w.Code)
	}
	if body := w.Body.String(); body != "" {
		t.Fatalf("body leaked on client disconnect: %q", body)
	}
}

func TestGetHistory_DBErrorWithLiveClientStill500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := seededPool(t)
	store, err := history.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	_ = pool.Close() // live request ctx, dead pool: a genuine server-side failure

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/history", nil)
	setAuth(c, 1, false)

	GetHistory(store)(c)

	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "error") {
		t.Fatalf("status/body = %d %q, want 500 with error", w.Code, w.Body.String())
	}
}
