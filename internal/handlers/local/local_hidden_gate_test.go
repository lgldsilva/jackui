package local

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/middleware"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// runLocalGate drives LocalHiddenGate as `userID` and returns the HTTP status:
// 404 when the gate blocks, 200 when it passes through to the dummy handler.
func runLocalGate(s *streamer.Streamer, userID int, mount, path string, reveal bool) int {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("auth.claims", &auth.Claims{UserID: userID, Username: "u", Role: auth.RoleUser})
		c.Next()
	})
	router.Use(middleware.RevealHidden())
	router.GET("/api/local/play", LocalHiddenGate(s), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/api/local/play?mount="+mount+"&path="+path, nil)
	if reveal {
		req.Header.Set("X-JackUI-Reveal-Hidden", "1")
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w.Code
}

func TestLocalHiddenGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	fav, err := streamer.NewFavorites(seededPool(t))
	if err != nil {
		t.Fatalf("NewFavorites: %v", err)
	}
	t.Cleanup(func() { fav.Close() })
	s.SetFavorites(fav)

	const uid = 7
	if err := fav.SetLocalPathHidden(uid, "Movies", "secret", true); err != nil {
		t.Fatalf("SetLocalPathHidden: %v", err)
	}

	cases := []struct {
		name   string
		mount  string
		path   string
		reveal bool
		want   int
	}{
		{"file inside hidden folder, curtain closed → blocked", "Movies", "secret/movie.mkv", false, http.StatusNotFound},
		{"exact hidden folder, curtain closed → blocked", "Movies", "secret", false, http.StatusNotFound},
		{"hidden, curtain open → passes", "Movies", "secret/movie.mkv", true, http.StatusOK},
		{"non-hidden path → passes", "Movies", "public/movie.mkv", false, http.StatusOK},
		{"prefix-only sibling not matched → passes", "Movies", "secret-extra/movie.mkv", false, http.StatusOK},
		{"missing path → gate passes through", "Movies", "", false, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runLocalGate(s, uid, tc.mount, tc.path, tc.reveal); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}
