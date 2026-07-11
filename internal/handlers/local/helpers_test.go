package local

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/dbtest"
	lb "github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
)

// Test helpers mirrored from package handlers. They can't be shared directly:
// package local's internal test binary can't import handlers (that would close
// the handlers→local import cycle), so the small helpers are duplicated here.

func postJSON(t *testing.T, router *gin.Engine, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func setAuth(c *gin.Context, userID int, isAdmin bool) {
	role := auth.RoleUser
	if isAdmin {
		role = auth.RoleAdmin
	}
	c.Set("auth.claims", &auth.Claims{UserID: userID, Username: "test", Role: role})
}

func seededPool(t *testing.T, ids ...int64) *sql.DB {
	t.Helper()
	pool := dbtest.NewDB(t)
	if len(ids) == 0 {
		ids = []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	}
	dbtest.SeedUsers(t, pool, ids...)
	return pool
}

func newTestCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	return c, w
}

func waitForLocalFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		<-time.After(2 * time.Millisecond) // cede a CPU à goroutine que cria o arquivo
	}
	t.Fatalf("file %q did not appear within %s", path, timeout)
}

type stubSource struct {
	data []byte
	ct   string
}

func (stubSource) Name() string { return "stub" }

func (s stubSource) Find(context.Context, string) ([]byte, string, error) {
	return s.data, s.ct, nil
}

func newCurtainStreamer(t *testing.T) (*streamer.Streamer, *streamer.FavoritesStore) {
	t.Helper()
	s := streamer.NewForTesting()
	fav, err := streamer.NewFavorites(seededPool(t))
	if err != nil {
		t.Fatalf("NewFavorites: %v", err)
	}
	t.Cleanup(func() { fav.Close() })
	s.SetFavorites(fav)
	return s, fav
}

func hgBBrowser(t *testing.T) (*lb.Browser, string) {
	t.Helper()
	dir := t.TempDir()
	b := lb.NewBrowser([]config.ExternalMount{{Name: "Test", Path: dir}})
	return b, dir
}

func hgBGET(t *testing.T, r *gin.Engine, url string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func hgBManager(t *testing.T) *transcode.HLSSessionManager {
	t.Helper()
	mgr, err := transcode.NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	return mgr
}

type hgCFakeFileInfo struct{ os.FileInfo }

func (hgCFakeFileInfo) Mode() os.FileMode { return 0o644 }
func (hgCFakeFileInfo) IsDir() bool       { return false }
