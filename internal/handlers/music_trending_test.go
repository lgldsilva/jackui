package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/musictrending"
)

const feedJSON = `{"feed":{"results":[{"artistName":"A","name":"Album One","artworkUrl100":"https://x/100x100bb.jpg","url":"https://music/1"}]}}`

func serve(t *testing.T, mc *musictrending.Client) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/music/trending", MusicTrending(mc))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/music/trending?country=br&limit=5", nil))
	return w
}

func TestMusicTrendingNilClient(t *testing.T) {
	if w := serve(t, nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil client: want 503, got %d", w.Code)
	}
}

func TestMusicTrendingSuccess(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(feedJSON))
	}))
	defer up.Close()
	w := serve(t, musictrending.NewForTest(up.URL))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Album One") {
		t.Fatalf("body missing album: %s", w.Body.String())
	}
}

func TestMusicTrendingUpstreamError(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer up.Close()
	if w := serve(t, musictrending.NewForTest(up.URL)); w.Code != http.StatusBadGateway {
		t.Fatalf("upstream error: want 502, got %d", w.Code)
	}
}
