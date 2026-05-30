package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/streamer"
)

func TestConvertTorrentToMagnet_NoURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/convert/torrent2magnet", nil)

	ConvertTorrentToMagnet()(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestConvertTorrentToMagnet_InvalidURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/convert/torrent2magnet?url=://invalid", nil)

	ConvertTorrentToMagnet()(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestConvertMagnetToTorrent_NoMagnet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/convert/magnet2torrent", nil)

	ConvertMagnetToTorrent(s)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestConvertMagnetToTorrent_InvalidMagnet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/convert/magnet2torrent?magnet=notamagnet", nil)

	ConvertMagnetToTorrent(s)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestBuildMagnetFromMetainfo(t *testing.T) {
	mi := &metainfo.MetaInfo{
		Announce: "http://tracker.example.com/announce",
		AnnounceList: [][]string{
			{"http://tracker1.example.com/announce"},
			{"http://tracker2.example.com/announce"},
		},
	}
	infoHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	name := "Test Torrent"

	magnet := buildMagnetFromMetainfo(mi, infoHash, name)

	if !strings.HasPrefix(magnet, "magnet:?xt=urn:btih:") {
		t.Errorf("magnet should start with magnet:?xt=urn:btih:, got %q", magnet)
	}
	if !strings.Contains(magnet, "dn=Test+Torrent") {
		t.Errorf("magnet should contain dn=Test+Torrent, got %q", magnet)
	}
	if !strings.Contains(magnet, "tr=http%3A%2F%2Ftracker1.example.com%2Fannounce") {
		t.Errorf("magnet should contain tracker1, got %q", magnet)
	}
	if !strings.Contains(magnet, "tr=http%3A%2F%2Ftracker2.example.com%2Fannounce") {
		t.Errorf("magnet should contain tracker2, got %q", magnet)
	}
}

func TestBuildMagnetFromMetainfo_NoName(t *testing.T) {
	mi := &metainfo.MetaInfo{
		Announce: "http://tracker.example.com/announce",
	}
	infoHash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	magnet := buildMagnetFromMetainfo(mi, infoHash, "")

	if strings.Contains(magnet, "dn=") {
		t.Errorf("magnet should not have dn= when name is empty, got %q", magnet)
	}
	if !strings.Contains(magnet, "tr=http%3A%2F%2Ftracker.example.com%2Fannounce") {
		t.Errorf("magnet should contain announce tracker, got %q", magnet)
	}
}

func TestBuildMagnetFromMetainfo_FallbackToAnnounce(t *testing.T) {
	mi := &metainfo.MetaInfo{
		Announce: "http://single-tracker.example.com/announce",
	}
	infoHash := "cccccccccccccccccccccccccccccccccccccccc"

	magnet := buildMagnetFromMetainfo(mi, infoHash, "test")

	if !strings.Contains(magnet, "tr=http%3A%2F%2Fsingle-tracker.example.com%2Fannounce") {
		t.Errorf("magnet should contain single announce tracker, got %q", magnet)
	}
}
