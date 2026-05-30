package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/auth"
	"github.com/luizg/jackui/internal/downloads"
	"github.com/luizg/jackui/internal/streamer"
)

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func TestUserCacheGetWithNilStore(t *testing.T) {
	uc := userCache{}
	if s := uc.get(nil, 1); s != "" {
		t.Errorf("get(nil store) = %q, want empty", s)
	}
}

func TestUserCacheGetWithStore(t *testing.T) {
	store, err := auth.New(t.TempDir() + "/auth_cover.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	uc := userCache{}
	uid, err := store.CreateUser("coveruser", "pass", auth.RoleUser)
	if err != nil {
		t.Fatal(err)
	}

	if s := uc.get(store, uid); s != "coveruser" {
		t.Errorf("get() = %q, want 'coveruser'", s)
	}
	if s := uc.get(store, uid); s != "coveruser" {
		t.Errorf("get() cached = %q, want 'coveruser'", s)
	}
}

func TestUserCacheGetWithMissingUser(t *testing.T) {
	store, err := auth.New(t.TempDir() + "/auth_cover2.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	uc := userCache{}
	if s := uc.get(store, 9999); s != "" {
		t.Errorf("get() for non-existent user = %q, want empty", s)
	}
}

func TestEnrichETASkipsEmptyHash(t *testing.T) {
	s := streamer.NewForTesting()
	d := &downloads.Download{InfoHash: "", FileSize: 1000}
	enrichETA(d, s)
}

func TestEnrichETASkipsZeroSize(t *testing.T) {
	s := streamer.NewForTesting()
	d := &downloads.Download{InfoHash: "abc", FileSize: 0}
	enrichETA(d, s)
}

func TestMarkPromotedWithEmptyDir(t *testing.T) {
	list := []downloads.Download{
		{Status: downloads.StatusCompleted, FilePath: "/some/other/path"},
	}
	markPromoted(list, "")
	if list[0].Promoted {
		t.Error("should not be promoted when download dir is empty")
	}
}

func TestMarkPromotedNotCompleted(t *testing.T) {
	list := []downloads.Download{
		{Status: downloads.StatusDownloading, FilePath: "/library/movie.mp4"},
	}
	markPromoted(list, "/downloads")
	if list[0].Promoted {
		t.Error("should not be promoted when not completed")
	}
}

func TestMarkPromotedInsideDownloadDir(t *testing.T) {
	list := []downloads.Download{
		{Status: downloads.StatusCompleted, FilePath: "/downloads/movie.mp4"},
	}
	markPromoted(list, "/downloads")
	if list[0].Promoted {
		t.Error("should not be promoted inside download dir")
	}
}

func TestMarkPromotedOutsideDownloadDir(t *testing.T) {
	list := []downloads.Download{
		{Status: downloads.StatusCompleted, FilePath: "/library/movie.mp4"},
	}
	markPromoted(list, "/downloads")
	if !list[0].Promoted {
		t.Error("should be promoted outside download dir")
	}
}

func TestEnrichETAListNilStreamer(t *testing.T) {
	list := []downloads.Download{{InfoHash: "abc"}}
	enrichETAList(list, nil)
}

func TestBuildVODPlaylist(t *testing.T) {
	data := buildVODPlaylist(10.0, "")
	if len(data) == 0 {
		t.Fatal("expected non-empty playlist")
	}
	str := string(data)
	if !contains(str, "EXTM3U") {
		t.Error("missing EXTM3U")
	}
	if !contains(str, "EXT-X-ENDLIST") {
		t.Error("missing ENDLIST")
	}
}

func TestBuildVODPlaylistWithToken(t *testing.T) {
	data := buildVODPlaylist(4.0, "mytoken")
	str := string(data)
	if !contains(str, "?token=mytoken") {
		t.Error("missing token in playlist")
	}
}

func TestResolveTorrentFilenameByDisplayName(t *testing.T) {
	h := metainfo.Hash{0x01}
	mi := &metainfo.Magnet{DisplayName: "My Movie"}
	got := resolveTorrentFilename("/nonexistent/path", h, mi)
	if got != "My Movie.torrent" {
		t.Errorf("got %q, want 'My Movie.torrent'", got)
	}
}

func TestResolveTorrentFilenameFallsBackToHash(t *testing.T) {
	h := metainfo.Hash{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a,
		0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14}
	mi := &metainfo.Magnet{}
	got := resolveTorrentFilename("/nonexistent", h, mi)
	want := "0102030405060708090a0b0c0d0e0f1011121314.torrent"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestServeTorrentFileMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	s := streamer.NewForTesting()
	h := metainfo.Hash{0x01}
	mi := &metainfo.Magnet{}

	serveTorrentFile(c, s, h, mi)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500, got body=%s", w.Code, w.Body.String())
	}
}

func TestConvertBuildMagnetFromMetainfo(t *testing.T) {
	mi := &metainfo.MetaInfo{
		AnnounceList: [][]string{
			{"udp://tracker.example.com:80/announce"},
		},
	}
	magnet := buildMagnetFromMetainfo(mi, "abc123", "Test Movie")
	if !contains(magnet, "urn:btih:abc123") {
		t.Errorf("magnet missing info hash: %s", magnet)
	}
	if !contains(magnet, "dn=Test+Movie") {
		t.Errorf("magnet missing display name: %s", magnet)
	}
	if !contains(magnet, "tr=") {
		t.Errorf("magnet missing tracker: %s", magnet)
	}
}

func TestConvertBuildMagnetEmptyName(t *testing.T) {
	mi := &metainfo.MetaInfo{}
	magnet := buildMagnetFromMetainfo(mi, "abc123", "")
	if contains(magnet, "dn=") {
		t.Errorf("should not have dn when name is empty: %s", magnet)
	}
}

func TestConvertBuildMagnetAnnounceFallback(t *testing.T) {
	mi := &metainfo.MetaInfo{
		Announce: "udp://tracker.old.com:80/announce",
	}
	magnet := buildMagnetFromMetainfo(mi, "abc123", "Test")
	if !contains(magnet, "tr=") {
		t.Errorf("should have tracker from Announce: %s", magnet)
	}
}
