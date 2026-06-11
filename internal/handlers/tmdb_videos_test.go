package handlers

import (
	"net/http"
	"strings"
	"testing"
)

func TestTmdbVideos_BadParams400(t *testing.T) {
	for _, path := range []string{"/videos", "/videos?kind=movie", "/videos?id=5", "/videos?kind=book&id=5", "/videos?kind=movie&id=0"} {
		w := hgHGET(t, "/videos", TmdbVideos(nil), path)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d want 400", path, w.Code)
		}
	}
}

func TestTmdbVideos_NilClient503(t *testing.T) {
	w := hgHGET(t, "/videos", TmdbVideos(nil), "/videos?kind=movie&id=5")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", w.Code)
	}
}

func TestTmdbVideos_DisabledClient503(t *testing.T) {
	c := hgHDisabledTMDB(t)
	w := hgHGET(t, "/videos", TmdbVideos(c), "/videos?kind=tv&id=5")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), ErrTMDBDisabled) {
		t.Errorf("body should mention tmdb disabled; got %s", w.Body.String())
	}
}
