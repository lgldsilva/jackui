package handlers

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/downloads"
)

func TestDownloadsTrackers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	store, err := downloads.New(pool)
	if err != nil {
		t.Fatalf("downloads.New: %v", err)
	}

	w := invokeCoverageHandlerWithClaims(t, DownloadsTrackers(store), http.MethodGet, "/api/downloads/trackers", "", nil, &auth.Claims{UserID: 1, Username: "admin"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestDownloadsCategories(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	store, err := downloads.New(pool)
	if err != nil {
		t.Fatalf("downloads.New: %v", err)
	}

	w := invokeCoverageHandlerWithClaims(t, DownloadsCategories(store), http.MethodGet, "/api/downloads/categories", "", nil, &auth.Claims{UserID: 1, Username: "admin"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestDownloadsPauseAll(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	store, err := downloads.New(pool)
	if err != nil {
		t.Fatalf("downloads.New: %v", err)
	}

	w := invokeCoverageHandlerWithClaims(t, DownloadsPauseAll(store), http.MethodPatch, "/api/downloads/pause-all", "", nil, &auth.Claims{UserID: 1, Username: "admin"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestDownloadsResumeAll(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	store, err := downloads.New(pool)
	if err != nil {
		t.Fatalf("downloads.New: %v", err)
	}

	w := invokeCoverageHandlerWithClaims(t, DownloadsResumeAll(store), http.MethodPatch, "/api/downloads/resume-all", "", nil, &auth.Claims{UserID: 1, Username: "admin"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestDownloadsUsers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	store, err := downloads.New(pool)
	if err != nil {
		t.Fatalf("downloads.New: %v", err)
	}

	w := invokeCoverageHandler(t, DownloadsUsers(store, nil), http.MethodGet, "/api/downloads/users", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}
