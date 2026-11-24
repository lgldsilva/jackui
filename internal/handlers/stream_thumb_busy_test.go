package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/streamer"
)

type fakeThumbs struct {
	data []byte
	err  error
}

func (f fakeThumbs) TryExtractThumbnail(context.Context, metainfo.Hash, int, int) ([]byte, bool, error) {
	return f.data, false, f.err
}

func serveThumb(t *testing.T, x thumbnailExtractor) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/th/:hash/:file", StreamThumbnail(x))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/th/"+hgGValidHash+"/0?at=30", nil))
	return w
}

// A full extraction gate must answer an uncacheable 204, not a cached miss or 404.
func TestStreamThumbnail_BusyIsUncacheable204(t *testing.T) {
	w := serveThumb(t, fakeThumbs{err: streamer.ErrThumbnailBusy})
	if w.Code != http.StatusNoContent || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("busy gate = %d cache=%q, want 204 no-store", w.Code, w.Header().Get("Cache-Control"))
	}
}

func TestStreamThumbnail_ServesJPEG(t *testing.T) {
	w := serveThumb(t, fakeThumbs{data: []byte{0xff, 0xd8, 0xff}})
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("thumb = %d %q", w.Code, w.Header().Get("Content-Type"))
	}
}
