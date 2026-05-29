package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/streamer"
)

// cov_hgG_test.go targets handler error paths that the original *_test.go and
// the sibling cov_hg{A..F}_test.go files leave uncovered: the `strconv.Atoi`
// bad-file-index guards on several /api/stream/* handlers, plus the deeper
// streamer-error branches (502/404) reached by passing a valid 40-char hex
// hash for a torrent that is not active in a NewForTesting streamer. Every
// identifier is prefixed with `hgG` to avoid collisions in this package.

// hgGValidHash is a syntactically valid (40-char hex) info hash that no torrent
// in a fresh NewForTesting streamer will know about, so the streamer methods
// return an error before touching ffmpeg — exercising the handlers' error tail.
const hgGValidHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// hgGDo wires a single GET route through gin and returns the recorder.
func hgGDo(t *testing.T, route, target string, h gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET(route, h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
	return w
}

// ---- bad file-index guards (strconv.Atoi failure → 400) ----

func Test_hgG_StreamProbe_BadFileIndex(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgGDo(t, "/p/:hash/:file", "/p/"+hgGValidHash+"/notanint", StreamProbe(s))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func Test_hgG_StreamSidecars_BadFileIndex(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgGDo(t, "/sc/:hash/:file", "/sc/"+hgGValidHash+"/notanint", StreamSidecars(s))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func Test_hgG_StreamSidecarRead_BadFileIndex(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgGDo(t, "/sr/:hash/:file", "/sr/"+hgGValidHash+"/notanint", StreamSidecarRead(s))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func Test_hgG_StreamSubtitleExtract_BadFileIndex(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgGDo(t, "/se/:hash/:file/:track", "/se/"+hgGValidHash+"/notanint/0", StreamSubtitleExtract(s))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func Test_hgG_StreamThumbnail_BadHash(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgGDo(t, "/th/:hash/:file", "/th/nothex/0", StreamThumbnail(s))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func Test_hgG_StreamThumbnail_BadFileIndex(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgGDo(t, "/th/:hash/:file", "/th/"+hgGValidHash+"/notanint", StreamThumbnail(s))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func Test_hgG_StreamArtwork_BadFileIndex(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgGDo(t, "/aw/:hash/:file", "/aw/"+hgGValidHash+"/notanint", StreamArtwork(s))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

// ---- deeper streamer-error branches (valid params, inactive torrent) ----

func Test_hgG_StreamProbe_InactiveTorrent(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgGDo(t, "/p/:hash/:file", "/p/"+hgGValidHash+"/0", StreamProbe(s))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", w.Code, w.Body.String())
	}
}

func Test_hgG_StreamSidecars_InactiveTorrent(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgGDo(t, "/sc/:hash/:file", "/sc/"+hgGValidHash+"/0", StreamSidecars(s))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", w.Code, w.Body.String())
	}
}

func Test_hgG_StreamSidecarRead_InactiveTorrent(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgGDo(t, "/sr/:hash/:file", "/sr/"+hgGValidHash+"/0", StreamSidecarRead(s))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", w.Code, w.Body.String())
	}
}

func Test_hgG_StreamSubtitleExtract_InactiveTorrent(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgGDo(t, "/se/:hash/:file/:track", "/se/"+hgGValidHash+"/0/0", StreamSubtitleExtract(s))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", w.Code, w.Body.String())
	}
}

func Test_hgG_StreamThumbnail_InactiveTorrent(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgGDo(t, "/th/:hash/:file", "/th/"+hgGValidHash+"/0?at=30", StreamThumbnail(s))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func Test_hgG_StreamArtwork_InactiveTorrent(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgGDo(t, "/aw/:hash/:file", "/aw/"+hgGValidHash+"/0", StreamArtwork(s))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

// ---- StreamMetadata: cache-miss path (404) for an inactive torrent ----

func Test_hgG_StreamMetadata_BadHash(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgGDo(t, "/md/:hash", "/md/nothex", StreamMetadata(s))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func Test_hgG_StreamMetadata_CacheMissOrDisabled(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgGDo(t, "/md/:hash", "/md/"+hgGValidHash, StreamMetadata(s))
	// NewForTesting has no metadata cache configured (503) or an empty one with
	// no entry for this hash (404); either way it's a non-2xx miss path.
	if w.Code != http.StatusServiceUnavailable && w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 503 or 404; body: %s", w.Code, w.Body.String())
	}
}

// ---- StreamSubtitleExtract: bad track index (valid hash + file) ----

func Test_hgG_StreamSubtitleExtract_BadTrackIndex(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgGDo(t, "/se/:hash/:file/:track", "/se/"+hgGValidHash+"/0/notanint", StreamSubtitleExtract(s))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}
