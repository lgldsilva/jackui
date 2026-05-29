package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/streamer"
)

func TestResolveTorrentFilename_FromUnknownPath(t *testing.T) {
	name := resolveTorrentFilename("/nonexistent/path.torrent", metainfo.Hash{}, &metainfo.Magnet{DisplayName: "TestName"})
	if name != "TestName.torrent" {
		t.Errorf("got %q, want 'TestName.torrent'", name)
	}
}

func TestResolveTorrentFilename_NoMetainfoNoDisplayName(t *testing.T) {
	var h metainfo.Hash
	h.FromHexString("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	name := resolveTorrentFilename("/nonexistent/path.torrent", h, &metainfo.Magnet{})
	expected := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.torrent"
	if name != expected {
		t.Errorf("got %q, want %q", name, expected)
	}
}

func TestResolveTorrentFilename_DisplayNameFallback(t *testing.T) {
	var h metainfo.Hash
	h.FromHexString("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	name := resolveTorrentFilename("/nonexistent/path.torrent", h, &metainfo.Magnet{DisplayName: "FallbackName"})
	expected := "FallbackName.torrent"
	if name != expected {
		t.Errorf("got %q, want %q", name, expected)
	}
}

func TestParseIntOr_Empty(t *testing.T) {
	got := parseIntOr("", 5)
	if got != 5 {
		t.Errorf("got %d, want 5", got)
	}
}

func TestParseIntOr_Invalid(t *testing.T) {
	got := parseIntOr("abc", 5)
	if got != 5 {
		t.Errorf("got %d, want 5", got)
	}
}

func TestParseIntOr_Valid(t *testing.T) {
	got := parseIntOr("42", 5)
	if got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestEnsureMetainfo_AlreadyExists(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	var h metainfo.Hash
	h.FromHexString("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	// Create a fake metainfo file
	os.WriteFile(s.MetainfoPath(h), []byte("dummy"), 0644)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/convert/magnet2torrent?magnet=notamagnet", nil)

	result := ensureMetainfo(c, s, h, "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if result {
		t.Error("expected false (metainfo already exists)")
	}
}

func TestServeTorrentFile_FileNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	var h metainfo.Hash
	h.FromHexString("dddddddddddddddddddddddddddddddddddddddd")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/convert/magnet2torrent", nil)

	serveTorrentFile(c, s, h, &metainfo.Magnet{})

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500; body: %s", w.Code, w.Body.String())
	}
}

func TestBuildMagnetFromMetainfo_EmptyAnnounceList(t *testing.T) {
	mi := &metainfo.MetaInfo{}
	infoHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	magnet := buildMagnetFromMetainfo(mi, infoHash, "test")
	if magnet != "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&dn=test" {
		t.Errorf("got %q, want magnet with no trackers", magnet)
	}
}


