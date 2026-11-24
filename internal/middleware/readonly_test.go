package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestReadOnlyGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	degraded := false
	r := gin.New()
	r.Use(ReadOnlyGuard(func() bool { return degraded }))
	r.Any("/*path", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	do := func(method, path string) int {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w.Code
	}
	if got := do(http.MethodPost, "/api/favorites"); got != http.StatusNoContent {
		t.Fatalf("healthy write blocked: %d", got)
	}
	degraded = true
	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/api/favorites", http.StatusServiceUnavailable},
		{http.MethodPut, "/api/library/abc", http.StatusServiceUnavailable},
		{http.MethodDelete, "/api/playlists/1", http.StatusServiceUnavailable},
		{http.MethodPost, "/api/auth/login", http.StatusServiceUnavailable},
		{http.MethodGet, "/api/library", http.StatusNoContent},
		{http.MethodHead, "/api/stream/x/0", http.StatusNoContent},
		{http.MethodPost, "/api/auth/refresh", http.StatusNoContent},
		{http.MethodPost, "/api/stream/abc/viewer", http.StatusNoContent},
		{http.MethodDelete, "/api/stream/abc", http.StatusNoContent},
		{http.MethodPost, "/api/diag/log", http.StatusNoContent},
		{http.MethodPost, "/api/user/incognito/heartbeat", http.StatusNoContent},
		{http.MethodPost, "/api/tmdb/match/batch", http.StatusNoContent},
		// Read-only POSTs: playback needs the media token, and logout must still
		// clear the cookie.
		{http.MethodPost, "/api/auth/media-token", http.StatusNoContent},
		{http.MethodPost, "/api/auth/logout", http.StatusNoContent},
		{http.MethodPost, "/api/auth/sessions", http.StatusNoContent},
		{http.MethodPost, "/api/watchlists/schedule/parse", http.StatusNoContent},
		{http.MethodPost, "/api/auth/sessions/revoke-others", http.StatusServiceUnavailable},
		{http.MethodPost, "/api/auth/media-token-x", http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		if got := do(tc.method, tc.path); got != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, got, tc.want)
		}
	}
}
