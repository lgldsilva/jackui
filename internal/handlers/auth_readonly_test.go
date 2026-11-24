package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/dbtest"
)

// readOnlyAuthStore mimics a degraded edge: a single-connection pool switched to
// read-only after the fixtures are written, so every write fails with 25006
// exactly like it would on a hot standby.
func readOnlyAuthStore(t *testing.T) (*auth.Store, *sql.DB) {
	t.Helper()
	pool := dbtest.NewDB(t)
	pool.SetMaxOpenConns(1)
	store, err := auth.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	return store, pool
}

func setReadOnly(t *testing.T, pool *sql.DB) {
	t.Helper()
	if _, err := pool.ExecContext(context.Background(), `SET default_transaction_read_only = on`); err != nil {
		t.Fatal(err)
	}
}

func postRefresh(t *testing.T, store *auth.Store, tm *auth.TokenManager, refresh string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body, _ := json.Marshal(map[string]string{"refresh": refresh})
	c.Request = httptest.NewRequest(http.MethodPost, "/api/auth/refresh", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	Refresh(store, tm)(c)
	return w
}

func TestRefresh_ReadOnlyReissuesAccessWithoutRotation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, pool := readOnlyAuthStore(t)
	user := createTestUser(t, store, "edgeuser", "pass")
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"), 15*time.Minute)
	refresh, err := store.CreateRefreshToken(context.Background(), user.ID, time.Hour, false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	setReadOnly(t, pool)

	w := postRefresh(t, store, tm, refresh)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp tokenResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Access == "" {
		t.Fatal("expected a new access token")
	}
	if resp.Refresh != refresh {
		t.Fatal("degraded refresh must hand back the SAME refresh token (no rotation)")
	}
	if _, err := tm.ParseAccess(resp.Access); err != nil {
		t.Fatalf("issued access token does not verify: %v", err)
	}
	// Still usable: a second degraded refresh with the same token works too.
	if w2 := postRefresh(t, store, tm, refresh); w2.Code != http.StatusOK {
		t.Fatalf("second degraded refresh = %d", w2.Code)
	}
}

func TestRefresh_ReadOnlyStillRejectsInvalid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, pool := readOnlyAuthStore(t)
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"), 15*time.Minute)
	setReadOnly(t, pool)
	if w := postRefresh(t, store, tm, "nope"); w.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token in degraded mode = %d, want 401", w.Code)
	}
}

func TestPeekRefreshTokenDoesNotConsume(t *testing.T) {
	store := newAuthStore(t)
	user := createTestUser(t, store, "peeker", "pass")
	refresh, err := store.CreateRefreshToken(context.Background(), user.ID, time.Hour, true, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		u, remember, outcome, err := store.PeekRefreshToken(context.Background(), refresh, time.Second)
		if err != nil || u == nil || u.ID != user.ID || !remember || outcome != auth.RefreshRotated {
			t.Fatalf("peek #%d = %v %v %v %v", i, u, remember, outcome, err)
		}
	}
	// Rotation still works afterwards (peek didn't consume it).
	if _, _, outcome, err := store.RotateRefreshToken(context.Background(), refresh, time.Second); err != nil || outcome != auth.RefreshRotated {
		t.Fatalf("rotate after peek = %v %v", outcome, err)
	}
	// Now consumed and past grace → reuse.
	time.Sleep(1100 * time.Millisecond)
	if _, _, outcome, _ := store.PeekRefreshToken(context.Background(), refresh, time.Second); outcome != auth.RefreshReuse {
		t.Fatalf("peek of consumed token = %v, want reuse", outcome)
	}
}
