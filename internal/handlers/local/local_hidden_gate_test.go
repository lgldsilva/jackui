package local

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	lb "github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/middleware"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// runLocalGate drives LocalHiddenGate as `userID` and returns the HTTP status:
// 404 when the gate blocks, 200 when it passes through to the dummy handler.
func runLocalGate(s *streamer.Streamer, userID int, route, mount, path string, reveal bool) int {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("auth.claims", &auth.Claims{UserID: userID, Username: "u", Role: auth.RoleUser})
		c.Next()
	})
	router.Use(middleware.RevealHidden())
	router.GET(route, LocalHiddenGate(s), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	q := url.Values{}
	q.Set("mount", mount)
	if path != "" {
		q.Set("path", path)
	}
	if reveal {
		// Native media elements cannot set X-JackUI-Reveal-Hidden; this is the
		// exact query-string path used by <video>, HLS and preview resources.
		q.Set("revealHidden", "1")
	}
	req := httptest.NewRequest("GET", route+"?"+q.Encode(), nil)
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
		route  string
		mount  string
		path   string
		reveal bool
		want   int
	}{
		{"play: file inside hidden folder, curtain closed → blocked", "/api/local/play", "Movies", "secret/movie.mkv", false, http.StatusNotFound},
		{"play: exact hidden folder, curtain closed → blocked", "/api/local/play", "Movies", "secret", false, http.StatusNotFound},
		{"play: hidden, curtain open → passes", "/api/local/play", "Movies", "secret/movie.mkv", true, http.StatusOK},
		{"play: non-hidden path → passes", "/api/local/play", "Movies", "public/movie.mkv", false, http.StatusOK},
		{"play: prefix-only sibling not matched → passes", "/api/local/play", "Movies", "secret-extra/movie.mkv", false, http.StatusOK},
		{"play: missing path → gate passes through", "/api/local/play", "Movies", "", false, http.StatusOK},
		// List used to only drop *children* of the current dir — deep-linking into
		// a hidden folder still listed its contents. Gate must close that hole.
		{"list: hidden folder path → blocked", "/api/local/list", "Movies", "secret", false, http.StatusNotFound},
		{"list: nested under hidden → blocked", "/api/local/list", "Movies", "secret/sub", false, http.StatusNotFound},
		{"list: mount root (empty path) → passes", "/api/local/list", "Movies", "", false, http.StatusOK},
		{"list: hidden with reveal → passes", "/api/local/list", "Movies", "secret", true, http.StatusOK},
		{"file: hidden media path → blocked", "/api/local/file", "Movies", "secret/a.mkv", false, http.StatusNotFound},
		{"walk: hidden tree → blocked", "/api/local/walk", "Movies", "secret", false, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runLocalGate(s, uid, tc.route, tc.mount, tc.path, tc.reveal); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestFilterHiddenLocalTree(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	fav, err := streamer.NewFavorites(seededPool(t))
	if err != nil {
		t.Fatalf("NewFavorites: %v", err)
	}
	t.Cleanup(func() { fav.Close() })
	s.SetFavorites(fav)
	const uid = 9
	if err := fav.SetLocalPathHidden(uid, "M", "secret", true); err != nil {
		t.Fatalf("hide: %v", err)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("auth.claims", &auth.Claims{UserID: uid, Username: "u", Role: auth.RoleUser})
	c.Request = httptest.NewRequest("GET", "/", nil)

	ents := []lb.Entry{
		{Path: "public/a.mkv"},
		{Path: "secret/b.mkv"},
		{Path: "secret/sub/c.mkv"},
	}
	got := filterHiddenLocalTree(c, s, "M", ents)
	if len(got) != 1 || got[0].Path != "public/a.mkv" {
		t.Fatalf("filter = %+v, want only public/a.mkv", got)
	}
}

func TestAbortIfLocalPathHidden(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	fav, err := streamer.NewFavorites(seededPool(t))
	if err != nil {
		t.Fatalf("NewFavorites: %v", err)
	}
	t.Cleanup(func() { fav.Close() })
	s.SetFavorites(fav)
	const uid = 3
	if err := fav.SetLocalPathHidden(uid, "M", "secret", true); err != nil {
		t.Fatalf("hide: %v", err)
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set("auth.claims", &auth.Claims{UserID: uid, Username: "u", Role: auth.RoleUser})
	c.Request = httptest.NewRequest("POST", "/", nil)

	if !AbortIfLocalPathHidden(c, s, "M", "secret/x") {
		t.Fatal("expected abort for hidden path")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if IsLocalPathHidden(c, s, "M", "public") {
		t.Fatal("public path must not be hidden")
	}
	if IsLocalPathHidden(c, nil, "M", "secret") {
		t.Fatal("nil streamer must not hide")
	}
}
