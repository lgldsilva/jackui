package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/tmdb"
	"github.com/lgldsilva/jackui/internal/transcode"
)

// hgH targets handler branches that the other cov_*_test.go files leave red:
//
//   - the streamer-error (502/404) branches of the per-file stream handlers
//     (StreamProbe / StreamSidecars / StreamSidecarRead / StreamSubtitleExtract /
//     StreamArtwork / StreamThumbnail) reached with a valid hash+index but no
//     active torrent — distinct from the existing bad-hash / bad-index tests.
//   - the ffmpeg-free HLS serving helpers (serveHLSPlaylist non-VOD branch,
//     readEventPlaylist token-rewrite + error, serveSegment, ensureVODSegment
//     no-op) driven against a hand-built non-VOD *transcode.HLSSession.
//   - the tmdb.ErrDisabled fall-through in TmdbMatch / TmdbTrending exercised
//     through a real (empty-key) *tmdb.Client rather than a nil one.
//
// Every identifier is hgH-prefixed to avoid collisions in the package.

// A syntactically valid 40-hex info hash that is never added to the streamer,
// so the streamer always reports "torrent not active".
const hgHHexHash = "cccccccccccccccccccccccccccccccccccccccc"

// hgHGET wires a single route on a throwaway engine and serves one GET.
func hgHGET(t *testing.T, path string, h gin.HandlerFunc, url string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET(path, h)
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// hgHCtx builds a bare gin.Context wired to a recorder for direct handler calls.
func hgHCtx(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	return c, w
}

// hgHDisabledTMDB returns a real client with no API key — every call returns
// tmdb.ErrDisabled, which the handlers must translate into 503.
func hgHDisabledTMDB(t *testing.T) *tmdb.Client {
	t.Helper()
	c, err := tmdb.New("", "", filepath.Join(t.TempDir(), "tmdb.db"))
	if err != nil {
		t.Fatalf("tmdb.New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// ─── stream.go: streamer-error branches with a valid hash+index ──────────────

func Test_hgH_StreamProbe_TorrentNotActive(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgHGET(t, "/p/:hash/:file", StreamProbe(s), "/p/"+hgHHexHash+"/0")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status=%d want 502; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgH_StreamSidecars_TorrentNotActive(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgHGET(t, "/sc/:hash/:file", StreamSidecars(s), "/sc/"+hgHHexHash+"/0")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status=%d want 502; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgH_StreamSidecarRead_TorrentNotActive(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgHGET(t, "/sr/:hash/:file", StreamSidecarRead(s), "/sr/"+hgHHexHash+"/0")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status=%d want 502; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgH_StreamSidecarRead_BadFileIndex(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgHGET(t, "/sr/:hash/:file", StreamSidecarRead(s), "/sr/"+hgHHexHash+"/notanint")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), errInvalidFileIndex) {
		t.Errorf("body should mention invalid file index; got %s", w.Body.String())
	}
}

func Test_hgH_StreamSidecars_BadFileIndex(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgHGET(t, "/sc/:hash/:file", StreamSidecars(s), "/sc/"+hgHHexHash+"/xx")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgH_StreamSubtitleExtract_TorrentNotActive(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgHGET(t, "/se/:hash/:file/:track", StreamSubtitleExtract(s),
		"/se/"+hgHHexHash+"/0/0")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status=%d want 502; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgH_StreamSubtitleExtract_BadFileIndex(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgHGET(t, "/se/:hash/:file/:track", StreamSubtitleExtract(s),
		"/se/"+hgHHexHash+"/zz/0")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), errInvalidFileIndex) {
		t.Errorf("body should mention invalid file index; got %s", w.Body.String())
	}
}

func Test_hgH_StreamArtwork_TorrentNotActive(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgHGET(t, "/aw/:hash/:file", StreamArtwork(s), "/aw/"+hgHHexHash+"/0")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgH_StreamThumbnail_TorrentNotActive(t *testing.T) {
	s := streamer.NewForTesting()
	w := hgHGET(t, "/th/:hash/:file", StreamThumbnail(s), "/th/"+hgHHexHash+"/0?at=30")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404; body=%s", w.Code, w.Body.String())
	}
}

// ─── hls.go: ffmpeg-free serving helpers on a hand-built non-VOD session ─────

// hgHSession returns a non-VOD HLSSession (spec nil → IsVOD()==false) whose Dir
// is a temp dir we can seed with playlist / segment files.
func hgHSession(t *testing.T) *transcode.HLSSession {
	t.Helper()
	return &transcode.HLSSession{Dir: t.TempDir()}
}

// serveHLSPlaylist non-VOD branch + readEventPlaylist with NO token: serves the
// playlist on disk verbatim.
func Test_hgH_ServeHLSPlaylist_NonVOD_NoToken(t *testing.T) {
	sess := hgHSession(t)
	body := "#EXTM3U\n#EXT-X-VERSION:3\nseg_00000.ts\n"
	if err := os.WriteFile(filepath.Join(sess.Dir, "index.m3u8"), []byte(body), 0o644); err != nil {
		t.Fatalf("write playlist: %v", err)
	}
	c, w := hgHCtx(t)
	serveHLSPlaylist(c, sess)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "seg_00000.ts") {
		t.Errorf("playlist body missing segment line: %s", w.Body.String())
	}
	if got := w.Header().Get(ContentType); !strings.Contains(got, "mpegurl") {
		t.Errorf("content-type=%q want mpegurl", got)
	}
}

// readEventPlaylist must append ?token= to each media line (and leave # lines
// untouched) when a token query param is present.
func Test_hgH_ReadEventPlaylist_TokenRewrite(t *testing.T) {
	sess := hgHSession(t)
	body := "#EXTM3U\nseg_00000.ts\nseg_00001.ts\n"
	if err := os.WriteFile(filepath.Join(sess.Dir, "index.m3u8"), []byte(body), 0o644); err != nil {
		t.Fatalf("write playlist: %v", err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/?token=abc123", nil)

	out := readEventPlaylist(c, sess)
	if out == nil {
		t.Fatal("expected playlist bytes, got nil")
	}
	rewritten := string(out)
	if strings.Count(rewritten, "?token=abc123") != 2 {
		t.Errorf("expected token appended to both media lines; got:\n%s", rewritten)
	}
	if !strings.Contains(rewritten, "#EXTM3U\n") {
		t.Errorf("# header line must be left untouched; got:\n%s", rewritten)
	}
}

// readEventPlaylist returns nil + 500 when the playlist file is missing.
func Test_hgH_ReadEventPlaylist_Missing(t *testing.T) {
	sess := hgHSession(t) // empty dir, no index.m3u8
	c, w := hgHCtx(t)
	if out := readEventPlaylist(c, sess); out != nil {
		t.Fatalf("expected nil for missing playlist, got %q", string(out))
	}
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status=%d want 500; body=%s", w.Code, w.Body.String())
	}
}

// serveHLSPlaylist non-VOD branch must surface the missing-playlist 500 too.
func Test_hgH_ServeHLSPlaylist_NonVOD_Missing(t *testing.T) {
	sess := hgHSession(t)
	c, w := hgHCtx(t)
	serveHLSPlaylist(c, sess)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500; body=%s", w.Code, w.Body.String())
	}
}

// serveSegment with an existing non-empty segment returns it immediately (no
// 30s wait), exercising the success path including the video/mp2t headers.
func Test_hgH_ServeSegment_Existing(t *testing.T) {
	sess := hgHSession(t)
	seg := "seg_00000.ts"
	if err := os.WriteFile(filepath.Join(sess.Dir, seg), []byte("ts-bytes"), 0o644); err != nil {
		t.Fatalf("write segment: %v", err)
	}
	c, w := hgHCtx(t)
	serveSegment(c, sess, seg)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get(ContentType); got != "video/mp2t" {
		t.Errorf("content-type=%q want video/mp2t", got)
	}
	if w.Body.String() != "ts-bytes" {
		t.Errorf("body=%q want ts-bytes", w.Body.String())
	}
}

// serveSegment rejects a traversal segment name with 404 (immediate, no wait).
func Test_hgH_ServeSegment_TraversalName(t *testing.T) {
	sess := hgHSession(t)
	c, w := hgHCtx(t)
	serveSegment(c, sess, "../escape.ts")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404; body=%s", w.Code, w.Body.String())
	}
}

// ensureVODSegment must no-op on a non-VOD session even for a parseable name
// (the IsVOD guard short-circuits before any stat/EnsureSegment work).
func Test_hgH_EnsureVODSegment_NonVODNoop(t *testing.T) {
	sess := hgHSession(t) // IsVOD()==false
	// Must not panic and must not create the file.
	ensureVODSegment(sess, "seg_00003.ts")
	if _, err := os.Stat(filepath.Join(sess.Dir, "seg_00003.ts")); !os.IsNotExist(err) {
		t.Errorf("non-VOD ensureVODSegment should not touch the dir; stat err=%v", err)
	}
}

// ─── tmdb.go: ErrDisabled fall-through via a real empty-key client ───────────

func Test_hgH_TmdbMatch_DisabledClient503(t *testing.T) {
	c := hgHDisabledTMDB(t)
	w := hgHGET(t, "/match", TmdbMatch(c), "/match?title=Inception+2010")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), ErrTMDBDisabled) {
		t.Errorf("body should mention tmdb disabled; got %s", w.Body.String())
	}
}

func Test_hgH_TmdbTrending_DisabledClient503(t *testing.T) {
	c := hgHDisabledTMDB(t)
	w := hgHGET(t, "/trending", TmdbTrending(c), "/trending")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), ErrTMDBDisabled) {
		t.Errorf("body should mention tmdb disabled; got %s", w.Body.String())
	}
}
