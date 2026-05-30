package handlers

import (
	"net"
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

func TestIsBlockedFetchIP_Handlers(t *testing.T) {
	// Duplicate of streamer's test to ensure handler-local version matches
	if !isBlockedFetchIP(net.ParseIP("127.0.0.1")) {
		t.Error("loopback should be blocked")
	}
	if !isBlockedFetchIP(net.ParseIP("::1")) {
		t.Error("IPv6 loopback should be blocked")
	}
	if !isBlockedFetchIP(net.ParseIP("169.254.1.1")) {
		t.Error("link-local should be blocked")
	}
	if isBlockedFetchIP(net.ParseIP("8.8.8.8")) {
		t.Error("public DNS should not be blocked")
	}
}

func TestValidateFetchScheme_File(t *testing.T) {
	err := validateFetchScheme("file:///etc/passwd")
	if err == nil {
		t.Error("expected error for file:// scheme")
	}
}

func TestValidateFetchScheme_Invalid(t *testing.T) {
	err := validateFetchScheme("://invalid")
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestValidateFetchScheme_HTTPS(t *testing.T) {
	err := validateFetchScheme("https://example.com/t.torrent")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDownloadAndParseTorrent_InvalidScheme(t *testing.T) {
	_, _, cerr := downloadAndParseTorrent("file:///etc/passwd")
	if cerr == nil {
		t.Error("expected error for file:// scheme")
	}
}

func TestDownloadAndParseTorrent_BogusHost(t *testing.T) {
	_, _, cerr := downloadAndParseTorrent("http://192.0.2.1/nonexistent.torrent")
	if cerr != nil {
		// Timeout or connection refused is fine — the point is it doesn't panic
	}
}

func TestResolveTorrentFilename_Fallback(t *testing.T) {
	path := "/nonexistent/path.torrent"
	var h metainfo.Hash
	h.FromHexString("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	mi := &metainfo.Magnet{DisplayName: ""}
	filename := resolveTorrentFilename(path, h, mi)
	if filename != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.torrent" {
		t.Errorf("got %q, want hex hash .torrent", filename)
	}
}

func TestResolveTorrentFilename_FromMagnet(t *testing.T) {
	path := "/nonexistent/path.torrent"
	var h metainfo.Hash
	h.FromHexString("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	mi := &metainfo.Magnet{DisplayName: "TestName"}
	filename := resolveTorrentFilename(path, h, mi)
	if filename != "TestName.torrent" {
		t.Errorf("got %q, want 'TestName.torrent'", filename)
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
