package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/history"
	"github.com/lgldsilva/jackui/internal/jackett"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// Writes that must survive a client that already left: each runs its handler
// (or helper) with an already-cancelled request ctx and asserts the row landed.

func goneCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func goneContext(method, target, body string) (*httptest.ResponseRecorder, *gin.Context) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body)).WithContext(goneCtx())
	return w, c
}

func countRows(t *testing.T, pool *sql.DB, q string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPersistEmitted_SavesAfterClientAborted(t *testing.T) {
	pool := seededPool(t)
	store, err := history.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	_, c := goneContext("GET", "/api/search/stream", "")
	persistEmitted(c, store, "q", []jackett.Result{{Title: "x", InfoHash: hashA}}, 1)
	if n := countRows(t, pool, `SELECT COUNT(*) FROM results`); n != 1 {
		t.Fatalf("rows = %d, want 1: aborted search must still cache what indexers returned", n)
	}
}

func TestSaveHistory_SavesAfterClientAborted(t *testing.T) {
	pool := seededPool(t)
	store, err := history.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	_, c := goneContext("GET", "/api/search", "")
	saveHistory(store, "q", []jackett.Result{{Title: "x", InfoHash: hashA}}, 1, nil, c)
	for i := 0; i < 100 && countRows(t, pool, `SELECT COUNT(*) FROM results`) == 0; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if n := countRows(t, pool, `SELECT COUNT(*) FROM results`); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

func TestLibraryUpdateResume_PersistsAfterClientAborted(t *testing.T) {
	lib := hgELibrary(t)
	id := hgESeedLibrary(t, lib, 1)
	w, c := goneContext("PATCH", "/api/library/"+strconv.Itoa(id), `{"resumeSeconds":42,"durationSeconds":100}`)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(id)}}
	setAuth(c, 1, false)

	LibraryUpdateResume(lib)(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body %q", w.Code, w.Body.String())
	}
	e, err := lib.GetByID(context.Background(), id, 1, false)
	if err != nil || e.ResumeSeconds != 42 {
		t.Fatalf("resume = %+v err %v, want 42", e, err)
	}
}

func TestIncognitoCleanup_RunsAfterClientAborted(t *testing.T) {
	for name, run := range map[string]func(c *gin.Context, h *history.Store){
		"Logout":         func(c *gin.Context, h *history.Store) { Logout(nil, h)(c) },
		"ClearIncognito": func(c *gin.Context, h *history.Store) { ClearIncognito(h)(c) },
	} {
		t.Run(name, func(t *testing.T) {
			pool := seededPool(t)
			h, err := history.New(pool)
			if err != nil {
				t.Fatal(err)
			}
			if err := h.Save(context.Background(), "q", []jackett.Result{{Title: "x", InfoHash: hashA}}, 1, true); err != nil {
				t.Fatal(err)
			}
			if countRows(t, pool, `SELECT COUNT(*) FROM results WHERE incognito = 1`) != 1 {
				t.Fatal("seed failed")
			}
			_, c := goneContext("POST", "/x", "")
			setAuth(c, 1, false)
			run(c, h)
			if n := countRows(t, pool, `SELECT COUNT(*) FROM results WHERE incognito = 1`); n != 0 {
				t.Fatalf("incognito rows left = %d, want 0", n)
			}
		})
	}
}

func TestPersistStreamLibrary_UpsertsAfterClientAborted(t *testing.T) {
	lib := hgELibrary(t)
	_, c := goneContext("POST", "/api/stream/add", "")
	setAuth(c, 1, false)
	persistStreamLibrary(c, lib, streamAddReq{Magnet: MagnetPrefix + hgEHash, Kind: "video"},
		&streamer.TorrentInfo{InfoHash: hgEHash, Name: "Movie", TotalSize: 10})
	list, err := lib.List(context.Background(), 1, false, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("library rows = %d err %v, want 1", len(list), err)
	}
	persistStreamLibrary(c, nil, streamAddReq{}, nil) // nil store is a no-op
}

func TestLibraryList_ClientGoneAbortsWithoutLeakingDBError(t *testing.T) {
	lib := hgELibrary(t)
	w, c := goneContext("GET", "/api/library", "")
	setAuth(c, 1, false)
	LibraryList(lib, nil)(c)
	if w.Code != 499 || w.Body.String() != "" {
		t.Fatalf("status/body = %d %q, want 499 and empty", w.Code, w.Body.String())
	}
}
