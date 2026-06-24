package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
)

func doRefresh(t *testing.T, store *auth.Store, tm *auth.TokenManager, refresh string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body, _ := json.Marshal(map[string]string{"refresh": refresh})
	c.Request = httptest.NewRequest("POST", "/api/auth/refresh", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	Refresh(store, tm)(c)
	return w
}

// A concurrent refresh of the SAME token (the multi-tab / post-deploy burst)
// must NOT revoke the session — both calls succeed (the second via the grace
// window). This is the re-login-after-deploy bug.
func TestRefresh_ConcurrentSameToken_DoesNotRevoke(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	user := createTestUser(t, store, "tabby", "pass")
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"), 15*time.Minute)
	refresh, err := store.CreateRefreshToken(user.ID, time.Hour, false, "", "")
	if err != nil {
		t.Fatal(err)
	}

	w1 := doRefresh(t, store, tm, refresh)
	if w1.Code != http.StatusOK {
		t.Fatalf("first refresh status = %d; body=%s", w1.Code, w1.Body.String())
	}
	// Same (now-rotated) token again, within grace → still 200, no revoke.
	w2 := doRefresh(t, store, tm, refresh)
	if w2.Code != http.StatusOK {
		t.Fatalf("concurrent refresh status = %d, want 200 (grace reissue); body=%s", w2.Code, w2.Body.String())
	}
	// The new token from the first refresh must still work (family not revoked).
	var resp map[string]string
	_ = json.Unmarshal(w1.Body.Bytes(), &resp)
	w3 := doRefresh(t, store, tm, resp["refresh"])
	if w3.Code != http.StatusOK {
		t.Fatalf("rotated token after concurrent refresh status = %d, want 200 (sessions intact)", w3.Code)
	}
}
