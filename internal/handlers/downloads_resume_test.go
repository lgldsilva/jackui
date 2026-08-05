package handlers

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
)

func TestDownloadsResume_NotFound_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	store, err := downloads.New(pool)
	if err != nil {
		t.Fatalf("downloads.New: %v", err)
	}

	params := gin.Params{{Key: "id", Value: "999"}}
	w := invokeCoverageHandlerWithParams(t, DownloadsResume(store), http.MethodPatch, "/api/downloads/999/resume", "", params)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestDownloadsResume_InvalidID_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	store, err := downloads.New(pool)
	if err != nil {
		t.Fatalf("downloads.New: %v", err)
	}

	params := gin.Params{{Key: "id", Value: "not-an-id"}}
	w := invokeCoverageHandlerWithParams(t, DownloadsResume(store), http.MethodPatch, "/api/downloads/not-an-id/resume", "", params)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDownloadsSetPriority_NotFound_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	store, err := downloads.New(pool)
	if err != nil {
		t.Fatalf("downloads.New: %v", err)
	}

	params := gin.Params{{Key: "id", Value: "999"}}
	w := invokeCoverageHandlerWithParams(t, DownloadsSetPriority(store), http.MethodPatch, "/api/downloads/999/priority", `{"priority":"high"}`, params)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestDownloadsSetPriority_InvalidID_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	store, err := downloads.New(pool)
	if err != nil {
		t.Fatalf("downloads.New: %v", err)
	}

	params := gin.Params{{Key: "id", Value: "not-an-id"}}
	w := invokeCoverageHandlerWithParams(t, DownloadsSetPriority(store), http.MethodPatch, "/api/downloads/not-an-id/priority", `{"priority":"high"}`, params)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDownloadsSources_NotFound_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	store, err := downloads.New(pool)
	if err != nil {
		t.Fatalf("downloads.New: %v", err)
	}

	params := gin.Params{{Key: "id", Value: "999"}}
	w := invokeCoverageHandlerWithParams(t, DownloadsSources(store), http.MethodGet, "/api/downloads/999/sources", "", params)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestDownloadsSources_InvalidID_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	store, err := downloads.New(pool)
	if err != nil {
		t.Fatalf("downloads.New: %v", err)
	}

	params := gin.Params{{Key: "id", Value: "not-an-id"}}
	w := invokeCoverageHandlerWithParams(t, DownloadsSources(store), http.MethodGet, "/api/downloads/not-an-id/sources", "", params)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDownloadsRecheck_NotFound_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	store, err := downloads.New(pool)
	if err != nil {
		t.Fatalf("downloads.New: %v", err)
	}
	s := streamer.NewForTesting()

	params := gin.Params{{Key: "id", Value: "999"}}
	w := invokeCoverageHandlerWithParams(t, DownloadsRecheck(store, s), http.MethodPost, "/api/downloads/999/recheck", "", params)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestDownloadsPeers_NotFound_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	store, err := downloads.New(pool)
	if err != nil {
		t.Fatalf("downloads.New: %v", err)
	}
	s := streamer.NewForTesting()

	params := gin.Params{{Key: "id", Value: "999"}}
	w := invokeCoverageHandlerWithParams(t, DownloadsPeers(store, s), http.MethodGet, "/api/downloads/999/peers", "", params)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestDownloadsDetails_NotFound_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	store, err := downloads.New(pool)
	if err != nil {
		t.Fatalf("downloads.New: %v", err)
	}
	s := streamer.NewForTesting()

	params := gin.Params{{Key: "id", Value: "999"}}
	w := invokeCoverageHandlerWithParams(t, DownloadsDetails(store, s), http.MethodGet, "/api/downloads/999/details", "", params)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}
