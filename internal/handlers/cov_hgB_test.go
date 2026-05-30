package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/config"
	"github.com/luizg/jackui/internal/downloads"
	"github.com/luizg/jackui/internal/local"
	"github.com/luizg/jackui/internal/streamer"
	"github.com/luizg/jackui/internal/subtitles"
	"github.com/luizg/jackui/internal/transcode"
)

// ─── shared helpers (hgB-prefixed to avoid collisions) ──────────────────────

const hgBHexHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// hgBBrowser builds a local.Browser exposing a single mount "Test" rooted at a
// fresh temp dir (no AllowedUsers → anon access permitted, so no auth wiring
// needed for these handler paths).
func hgBBrowser(t *testing.T) (*local.Browser, string) {
	t.Helper()
	dir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{{Name: "Test", Path: dir}})
	return b, dir
}

// hgBManager spins up a real HLSSessionManager backed by a temp dir. We never
// drive ffmpeg through it — only the Peek / not-found bookkeeping paths.
func hgBManager(t *testing.T) *transcode.HLSSessionManager {
	t.Helper()
	mgr, err := transcode.NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	return mgr
}

func hgBGET(t *testing.T, r *gin.Engine, url string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ─── hls.go ─────────────────────────────────────────────────────────────────

func Test_hgB_StreamHLSMaster_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	mgr := hgBManager(t)
	r := gin.New()
	r.GET("/m/:hash/:file", StreamHLSMaster(s, mgr, nil))

	w := hgBGET(t, r, "/m/nothex/0")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_StreamHLSMaster_BadFileIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	mgr := hgBManager(t)
	r := gin.New()
	r.GET("/m/:hash/:file", StreamHLSMaster(s, mgr, nil))

	w := hgBGET(t, r, "/m/"+hgBHexHash+"/notanint")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), errInvalidFileIndex) {
		t.Errorf("body should mention invalid file index; got %s", w.Body.String())
	}
}

func Test_hgB_StreamHLSSegment_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr := hgBManager(t)
	r := gin.New()
	r.GET("/s/:hash/:file/:seg", StreamHLSSegment(mgr))

	w := hgBGET(t, r, "/s/nothex/0/seg_00000.ts")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_StreamHLSSegment_BadFileIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr := hgBManager(t)
	r := gin.New()
	r.GET("/s/:hash/:file/:seg", StreamHLSSegment(mgr))

	w := hgBGET(t, r, "/s/"+hgBHexHash+"/xx/seg_00000.ts")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

// No active session for this key → resolveHLSSession / getSession (Peek) 404s.
func Test_hgB_StreamHLSSegment_SessionNotActive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr := hgBManager(t)
	r := gin.New()
	r.GET("/s/:hash/:file/:seg", StreamHLSSegment(mgr))

	w := hgBGET(t, r, "/s/"+hgBHexHash+"/0/seg_00000.ts")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "session not active") {
		t.Errorf("expected 'session not active' hint; got %s", w.Body.String())
	}
}

// resolveHLSSession returns nil (404) for an unknown key — exercised directly.
func Test_hgB_ResolveHLSSession_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr := hgBManager(t)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	h, err := parseHash(hgBHexHash)
	if err != nil {
		t.Fatalf("parseHash: %v", err)
	}
	if sess := resolveHLSSession(c, mgr, h, 0); sess != nil {
		t.Fatal("expected nil session for unknown key")
	}
	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d want 404", w.Code)
	}
}

// resolveTranscodeSource prefers a completed file on disk over the streamer.
func Test_hgB_ResolveTranscodeSource_CompletedFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	// Write a real completed file and register it.
	dir := t.TempDir()
	file := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(file, []byte("payload-bytes"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	d, err := store.Create(downloads.Download{UserID: 1, InfoHash: hgBHexHash, FileIndex: 0, Magnet: "m", Name: "x", FilePath: file})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	store.SetStatus(1, d.ID, downloads.StatusCompleted)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	h, _ := parseHash(hgBHexHash)
	hc := &hlsCtx{c: c, s: streamer.NewForTesting(), store: store, h: h, fileIdx: 0}

	src, size := resolveTranscodeSource(hc)
	if src == nil {
		t.Fatal("expected a source reader for the completed file")
	}
	defer func() { _ = src.Close() }()
	if size != int64(len("payload-bytes")) {
		t.Errorf("size=%d want %d", size, len("payload-bytes"))
	}
}

// serveHLSPlaylist's VOD branch synthesises a finite playlist via buildVODPlaylist.
func Test_hgB_BuildVODPlaylist_ZeroDuration(t *testing.T) {
	pl := string(buildVODPlaylist(0, ""))
	if !strings.Contains(pl, "#EXT-X-ENDLIST") {
		t.Error("zero-duration playlist must still be finite")
	}
	// ceil(0/4) clamps to at least 1 segment.
	if got := strings.Count(pl, "seg_"); got != 1 {
		t.Errorf("expected 1 segment for 0s, got %d\n%s", got, pl)
	}
}

// ensureVODSegment must no-op silently for an unparseable segment name.
func Test_hgB_EnsureVODSegment_BadName(t *testing.T) {
	// A nil-ish session would panic on IsVOD; instead validate the parse guard
	// indirectly: ParseSegIndex rejects junk, which is the gate ensureVODSegment
	// relies on before touching the session.
	if _, ok := transcode.ParseSegIndex("not-a-segment"); ok {
		t.Error("expected ParseSegIndex to reject junk name")
	}
}

// ─── local_play.go ──────────────────────────────────────────────────────────

func Test_hgB_LocalHLSMaster_MissingParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := hgBBrowser(t)
	mgr := hgBManager(t)
	r := gin.New()
	r.GET("/hls", LocalHLSMaster(b, mgr))

	w := hgBGET(t, r, "/hls?mount=Test")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), errMissingMountOrPathParam) {
		t.Errorf("body=%s", w.Body.String())
	}
}

func Test_hgB_LocalHLSMaster_ForbiddenMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := hgBBrowser(t)
	mgr := hgBManager(t)
	r := gin.New()
	r.GET("/hls", LocalHLSMaster(b, mgr))

	// Unknown mount → UserCanAccess false → 403.
	w := hgBGET(t, r, "/hls?mount=Nope&path=x.mkv")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_LocalHLSMaster_FileNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := hgBBrowser(t)
	mgr := hgBManager(t)
	r := gin.New()
	r.GET("/hls", LocalHLSMaster(b, mgr))

	w := hgBGET(t, r, "/hls?mount=Test&path=missing.mkv")
	// resolveLocalFileStat returns an os.Stat error; handler returns silently
	// without writing a body, so the recorder keeps the default 200. Either way
	// we exercised the early-return error branch.
	if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
		t.Fatalf("unexpected status=%d; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_LocalHLSMaster_PathIsDir(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, dir := hgBBrowser(t)
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mgr := hgBManager(t)
	r := gin.New()
	r.GET("/hls", LocalHLSMaster(b, mgr))

	w := hgBGET(t, r, "/hls?mount=Test&path=sub")
	// resolveLocalFileStat errors (ErrPathIsDir) → silent return, default 200.
	if w.Code != http.StatusOK && w.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status=%d; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_LocalHLSSegment_MissingParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := hgBBrowser(t)
	mgr := hgBManager(t)
	r := gin.New()
	r.GET("/seg", LocalHLSSegment(b, mgr))

	w := hgBGET(t, r, "/seg?mount=Test&path=x.mkv")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_LocalHLSSegment_ForbiddenMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := hgBBrowser(t)
	mgr := hgBManager(t)
	r := gin.New()
	r.GET("/seg", LocalHLSSegment(b, mgr))

	w := hgBGET(t, r, "/seg?mount=Nope&path=x.mkv&seg=seg_00000.ts")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_LocalHLSSegment_InvalidSegName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := hgBBrowser(t)
	mgr := hgBManager(t)
	r := gin.New()
	r.GET("/seg", LocalHLSSegment(b, mgr))

	w := hgBGET(t, r, "/seg?mount=Test&path=x.mkv&seg=../escape.ts")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid segment name") {
		t.Errorf("body=%s", w.Body.String())
	}
}

// Valid name, accessible mount, but no live session → resolveLocalSession 404s.
func Test_hgB_LocalHLSSegment_SessionNotActive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, dir := hgBBrowser(t)
	if err := os.WriteFile(filepath.Join(dir, "x.mkv"), []byte("data"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mgr := hgBManager(t)
	r := gin.New()
	r.GET("/seg", LocalHLSSegment(b, mgr))

	w := hgBGET(t, r, "/seg?mount=Test&path=x.mkv&seg=seg_00000.ts")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_ResolveLocalSession_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr := hgBManager(t)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	if sess := resolveLocalSession(c, mgr, "Test", "x.mkv"); sess != nil {
		t.Fatal("expected nil session")
	}
	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d want 404", w.Code)
	}
}

// startLocalHLSSession returns an open error when the file can't be opened.
func Test_hgB_StartLocalHLSSession_OpenError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr := hgBManager(t)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	f, sess, err := startLocalHLSSession(c, mgr, "Test", "x.mkv", "/no/such/file.mkv", nil)
	if err == nil {
		t.Fatal("expected open error")
	}
	if f != nil || sess != nil {
		t.Error("expected nil file/session on error")
	}
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status=%d want 500", w.Code)
	}
}

// resolveLocalFileStat surfaces a directory as ErrPathIsDir.
func Test_hgB_ResolveLocalFileStat_Dir(t *testing.T) {
	b, dir := hgBBrowser(t)
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, _, err := resolveLocalFileStat(b, "Test", "d")
	if err == nil || !strings.Contains(err.Error(), ErrPathIsDir) {
		t.Errorf("err=%v want %q", err, ErrPathIsDir)
	}
}

func Test_hgB_ResolveLocalFileStat_Missing(t *testing.T) {
	b, _ := hgBBrowser(t)
	if _, _, err := resolveLocalFileStat(b, "Test", "nope.mkv"); err == nil {
		t.Error("expected stat error for missing file")
	}
}

// serveLocalPlaylist's index.m3u8-missing branch on a non-VOD session is hard
// to drive without ffmpeg; instead verify the VOD synth builder used by it.
func Test_hgB_BuildLocalVODPlaylist_Rewrites(t *testing.T) {
	seen := []string{}
	build := segURLBuilder("Test", "movie.mkv", "TOK", "")
	pl := string(buildLocalVODPlaylist(10, func(name string) string {
		u := build(name)
		seen = append(seen, u)
		return u
	}))
	if !strings.Contains(pl, "#EXT-X-ENDLIST") {
		t.Error("playlist must be finite")
	}
	if len(seen) == 0 {
		t.Fatal("expected segURL builder to be invoked")
	}
	if !strings.Contains(seen[0], "token=TOK") || !strings.Contains(seen[0], "mount=Test") {
		t.Errorf("seg URL missing params: %s", seen[0])
	}
}

// ─── local_subtitles.go ─────────────────────────────────────────────────────

func Test_hgB_LocalProbe_MissingParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := hgBBrowser(t)
	r := gin.New()
	r.GET("/probe", LocalProbe(b))
	w := hgBGET(t, r, "/probe?mount=Test")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_LocalProbe_ForbiddenMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := hgBBrowser(t)
	r := gin.New()
	r.GET("/probe", LocalProbe(b))
	w := hgBGET(t, r, "/probe?mount=Nope&path=x.mkv")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_LocalProbe_FileNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := hgBBrowser(t)
	r := gin.New()
	r.GET("/probe", LocalProbe(b))
	w := hgBGET(t, r, "/probe?mount=Test&path=missing.mkv")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_LocalSidecars_MissingParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := hgBBrowser(t)
	r := gin.New()
	r.GET("/sc", LocalSidecars(b))
	w := hgBGET(t, r, "/sc?mount=Test")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_LocalSidecars_ForbiddenMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := hgBBrowser(t)
	r := gin.New()
	r.GET("/sc", LocalSidecars(b))
	w := hgBGET(t, r, "/sc?mount=Nope&path=x.mkv")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403; body=%s", w.Code, w.Body.String())
	}
}

// Success path: a video plus a matching .srt sidecar in the same dir.
func Test_hgB_LocalSidecars_Found(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, dir := hgBBrowser(t)
	if err := os.WriteFile(filepath.Join(dir, "movie.mkv"), []byte("v"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "movie.pt-BR.srt"), []byte("1\n00:00:01,000 --> 00:00:02,000\nhi\n"), 0o644); err != nil {
		t.Fatalf("write srt: %v", err)
	}
	r := gin.New()
	r.GET("/sc", LocalSidecars(b))
	w := hgBGET(t, r, "/sc?mount=Test&path=movie.mkv")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "movie.pt-BR.srt") {
		t.Errorf("expected sidecar listed; body=%s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "pt-BR") {
		t.Errorf("expected detected language pt-BR; body=%s", w.Body.String())
	}
}

func Test_hgB_LocalSidecarRead_MissingParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := hgBBrowser(t)
	r := gin.New()
	r.GET("/sr", LocalSidecarRead(b))
	w := hgBGET(t, r, "/sr?mount=Test&path=x.mkv")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_LocalSidecarRead_NameTraversal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := hgBBrowser(t)
	r := gin.New()
	r.GET("/sr", LocalSidecarRead(b))
	w := hgBGET(t, r, "/sr?mount=Test&path=x.mkv&name=../etc/passwd")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid name") {
		t.Errorf("body=%s", w.Body.String())
	}
}

func Test_hgB_LocalSidecarRead_UnsupportedFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, dir := hgBBrowser(t)
	if err := os.WriteFile(filepath.Join(dir, "x.mkv"), []byte("v"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	r := gin.New()
	r.GET("/sr", LocalSidecarRead(b))
	w := hgBGET(t, r, "/sr?mount=Test&path=x.mkv&name=sub.txt")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "unsupported subtitle format") {
		t.Errorf("body=%s", w.Body.String())
	}
}

func Test_hgB_LocalSidecarRead_FileNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, dir := hgBBrowser(t)
	if err := os.WriteFile(filepath.Join(dir, "x.mkv"), []byte("v"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	r := gin.New()
	r.GET("/sr", LocalSidecarRead(b))
	w := hgBGET(t, r, "/sr?mount=Test&path=x.mkv&name=missing.srt")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404; body=%s", w.Code, w.Body.String())
	}
}

// Success: SRT sidecar is converted to WebVTT.
func Test_hgB_LocalSidecarRead_SRTtoVTT(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, dir := hgBBrowser(t)
	if err := os.WriteFile(filepath.Join(dir, "x.mkv"), []byte("v"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	srt := "1\n00:00:01,000 --> 00:00:02,000\nhello\n"
	if err := os.WriteFile(filepath.Join(dir, "x.srt"), []byte(srt), 0o644); err != nil {
		t.Fatalf("write srt: %v", err)
	}
	r := gin.New()
	r.GET("/sr", LocalSidecarRead(b))
	w := hgBGET(t, r, "/sr?mount=Test&path=x.mkv&name=x.srt")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.HasPrefix(w.Body.String(), "WEBVTT") {
		t.Errorf("expected WEBVTT output; got %q", w.Body.String())
	}
}

// Success: VTT sidecar is served as-is.
func Test_hgB_LocalSidecarRead_VTTRaw(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, dir := hgBBrowser(t)
	if err := os.WriteFile(filepath.Join(dir, "x.mkv"), []byte("v"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	vtt := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nhi\n"
	if err := os.WriteFile(filepath.Join(dir, "x.vtt"), []byte(vtt), 0o644); err != nil {
		t.Fatalf("write vtt: %v", err)
	}
	r := gin.New()
	r.GET("/sr", LocalSidecarRead(b))
	w := hgBGET(t, r, "/sr?mount=Test&path=x.mkv&name=x.vtt")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", w.Code)
	}
	if w.Body.String() != vtt {
		t.Errorf("expected raw vtt passthrough; got %q", w.Body.String())
	}
}

// ASS/SSA fall through as text/plain.
func Test_hgB_LocalSidecarRead_ASSPlainText(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, dir := hgBBrowser(t)
	if err := os.WriteFile(filepath.Join(dir, "x.mkv"), []byte("v"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.ass"), []byte("[Script Info]\n"), 0o644); err != nil {
		t.Fatalf("write ass: %v", err)
	}
	r := gin.New()
	r.GET("/sr", LocalSidecarRead(b))
	w := hgBGET(t, r, "/sr?mount=Test&path=x.mkv&name=x.ass")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", w.Code)
	}
	if ct := w.Header().Get(ContentType); !strings.Contains(ct, "text/plain") {
		t.Errorf("Content-Type=%q want text/plain", ct)
	}
}

func Test_hgB_LocalSubtitlesAuto_MissingParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := hgBBrowser(t)
	client := subtitles.New("", "", "", "")
	r := gin.New()
	r.GET("/auto", LocalSubtitlesAuto(b, client))
	w := hgBGET(t, r, "/auto?mount=Test")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_LocalSubtitlesAuto_ForbiddenMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := hgBBrowser(t)
	client := subtitles.New("", "", "", "")
	r := gin.New()
	r.GET("/auto", LocalSubtitlesAuto(b, client))
	w := hgBGET(t, r, "/auto?mount=Nope&path=x.mkv")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403; body=%s", w.Code, w.Body.String())
	}
}

// Auto-subs with a disabled client: file resolves, hash computed, but
// SearchAuto returns an error → serveAutoSubtitles responds 502.
func Test_hgB_LocalSubtitlesAuto_ClientDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, dir := hgBBrowser(t)
	// >= 64KiB so computeOSHash succeeds (exercises the hash branch too).
	big := make([]byte, 128*1024)
	for i := range big {
		big[i] = byte(i)
	}
	if err := os.WriteFile(filepath.Join(dir, "movie.S01E02.mkv"), big, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	client := subtitles.New("", "", "", "") // disabled (no api key)
	r := gin.New()
	r.GET("/auto", LocalSubtitlesAuto(b, client))
	w := hgBGET(t, r, "/auto?mount=Test&path=movie.S01E02.mkv")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status=%d want 502; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_LocalSubtitleExtract_MissingParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := hgBBrowser(t)
	r := gin.New()
	r.GET("/ex", LocalSubtitleExtract(b))
	w := hgBGET(t, r, "/ex?mount=Test&path=x.mkv")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_LocalSubtitleExtract_ForbiddenMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := hgBBrowser(t)
	r := gin.New()
	r.GET("/ex", LocalSubtitleExtract(b))
	w := hgBGET(t, r, "/ex?mount=Nope&path=x.mkv&track=0")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_LocalSubtitleExtract_BadTrack(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := hgBBrowser(t)
	r := gin.New()
	r.GET("/ex", LocalSubtitleExtract(b))
	w := hgBGET(t, r, "/ex?mount=Test&path=x.mkv&track=notnum")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid track index") {
		t.Errorf("body=%s", w.Body.String())
	}
}

// ─── stream.go ──────────────────────────────────────────────────────────────

func Test_hgB_StreamAdd_MissingMagnet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	r := gin.New()
	r.POST("/add", StreamAdd(s, nil))
	req := httptest.NewRequest("POST", "/add", strings.NewReader(`{"magnet":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_StreamAdd_InvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	r := gin.New()
	r.POST("/add", StreamAdd(s, nil))
	req := httptest.NewRequest("POST", "/add", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_StreamPlaylistM3U_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	r := gin.New()
	r.GET("/pl/:hash/:file", StreamPlaylistM3U(s))
	w := hgBGET(t, r, "/pl/nothex/0")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_StreamPlaylistM3U_BadFileIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	r := gin.New()
	r.GET("/pl/:hash/:file", StreamPlaylistM3U(s))
	w := hgBGET(t, r, "/pl/"+hgBHexHash+"/zz")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_StreamSetFilePriority_BadHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	r := gin.New()
	r.POST("/p/:hash/files/:idx/priority", StreamSetFilePriority(s))
	req := httptest.NewRequest("POST", "/p/nothex/files/0/priority", strings.NewReader(`{"priority":"high"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_StreamSetFilePriority_BadIdx(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	r := gin.New()
	r.POST("/p/:hash/files/:idx/priority", StreamSetFilePriority(s))
	req := httptest.NewRequest("POST", "/p/"+hgBHexHash+"/files/zz/priority", strings.NewReader(`{"priority":"high"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_StreamSetFilePriority_BadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	r := gin.New()
	r.POST("/p/:hash/files/:idx/priority", StreamSetFilePriority(s))
	req := httptest.NewRequest("POST", "/p/"+hgBHexHash+"/files/0/priority", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

// Inactive torrent: SetFilePriority returns an error → handler maps to 400/404.
func Test_hgB_StreamSetFilePriority_NotActive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	r := gin.New()
	r.POST("/p/:hash/files/:idx/priority", StreamSetFilePriority(s))
	req := httptest.NewRequest("POST", "/p/"+hgBHexHash+"/files/0/priority", strings.NewReader(`{"priority":"high"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 400/404; body=%s", w.Code, w.Body.String())
	}
}

// StreamFile serves directly from the completed-download store when present.
func Test_hgB_StreamFile_FromCompletedStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "done.mp4")
	if err := os.WriteFile(file, []byte("completed-bytes"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	d, err := store.Create(downloads.Download{UserID: 1, InfoHash: hgBHexHash, FileIndex: 0, Magnet: "m", Name: "x", FilePath: file})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	store.SetStatus(1, d.ID, downloads.StatusCompleted)

	s := streamer.NewForTesting()
	r := gin.New()
	r.GET("/f/:hash/:file", StreamFile(s, store))
	w := hgBGET(t, r, "/f/"+hgBHexHash+"/0")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != "completed-bytes" {
		t.Errorf("body=%q want completed-bytes", w.Body.String())
	}
}

// StreamFile with no completed entry and inactive torrent → 404 from streamer.
func Test_hgB_StreamFile_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	s := streamer.NewForTesting()
	r := gin.New()
	r.GET("/f/:hash/:file", StreamFile(s, store))
	w := hgBGET(t, r, "/f/"+hgBHexHash+"/0")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_StreamFile_BadFileIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	r := gin.New()
	r.GET("/f/:hash/:file", StreamFile(s, nil))
	w := hgBGET(t, r, "/f/"+hgBHexHash+"/zz")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

// serveFromCompletedStore returns false when the recorded path no longer exists
// on disk (stat fails), forcing the streamer fallback.
func Test_hgB_ServeFromCompletedStore_MissingFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	d, err := store.Create(downloads.Download{UserID: 1, InfoHash: hgBHexHash, FileIndex: 0, Magnet: "m", Name: "x", FilePath: "/no/such/path.mkv"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	store.SetStatus(1, d.ID, downloads.StatusCompleted)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	h, _ := parseHash(hgBHexHash)
	if served := serveFromCompletedStore(c, store, h, 0); served {
		t.Fatal("expected false when completed path is gone")
	}
}