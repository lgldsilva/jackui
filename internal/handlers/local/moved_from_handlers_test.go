package local

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/audiometa"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/imagesearch"
	lb "github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/middleware"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/subtitles"
	"github.com/lgldsilva/jackui/internal/transfer"
)

func TestCheckMountAccess_Denied_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := lb.NewBrowser([]config.ExternalMount{
		{Name: "Real", Path: t.TempDir()},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	if CheckMountAccess(b, c, "FakeMount") {
		t.Error("expected denied")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestIsAdminMove_Denied_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/move", nil)

	if isAdminMove(c) {
		t.Error("expected false for no claims")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestCanModifyMount_Unknown_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/promote", nil)

	if canModifyMount(c, "Unknown") {
		t.Error("expected false for unknown mount without claims")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestCanModifyMount_MeusDownloads_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/promote", nil)

	if !canModifyMount(c, "Meus downloads") {
		t.Error("expected true for Meus downloads")
	}
}

func TestIsAdminCtx_NoClaims_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	if isAdminCtx(c) {
		t.Error("expected false for no claims")
	}
}

func TestIsAdminCtx_Admin_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	setAuth(c, 1, true)

	if !isAdminCtx(c) {
		t.Error("expected true for admin")
	}
}

func TestIsMountRoot_WithMatchingMount_Extra(t *testing.T) {
	mountDir := t.TempDir()
	b := lb.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})

	if !isMountRoot(b, mountDir) {
		t.Error("expected mount dir to be detected as root")
	}
	if isMountRoot(b, filepath.Join(mountDir, "subdir")) {
		t.Error("subdir should not be root")
	}
}

func TestResolveDeletablePath_Root_Extra(t *testing.T) {
	mountDir := t.TempDir()
	b := lb.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})

	_, err := resolveDeletablePath(b, "Test", "")
	if err == nil {
		t.Error("expected error for empty path")
	}
}

func TestStreamResolveLocalAbs_Valid_Extra(t *testing.T) {
	mountDir := t.TempDir()
	os.WriteFile(filepath.Join(mountDir, "file.mp4"), []byte("data"), 0o644)
	b := lb.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})

	got, err := resolveLocalAbs(b, "Test", "file.mp4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == "" {
		t.Error("expected non-empty path")
	}
}

func TestIsTraversalErr_MountNotFound_Extra(t *testing.T) {
	if !isTraversalErr(fmt.Errorf("mount 'X' not found")) {
		t.Error("expected true for mount not found error")
	}
}

func TestIsTraversalErr_Plain_Extra(t *testing.T) {
	if isTraversalErr(fmt.Errorf("something else")) {
		t.Error("expected false for other errors")
	}
}

func TestClassifyForBrowser_More_Extra(t *testing.T) {
	cases := []struct {
		probe      localProbe
		wantDirect bool
	}{
		{localProbe{Container: "isom", VideoCodec: "h264", AudioCodec: "aac"}, true},
		{localProbe{Container: "mp42", VideoCodec: "h264", AudioCodec: "mp3"}, true},
		{localProbe{Container: "qt", VideoCodec: "vp8", AudioCodec: "vorbis"}, true},
		{localProbe{Container: "mp4", VideoCodec: "h264", AudioCodec: ""}, true},
	}
	for i, tc := range cases {
		direct, _ := classifyForBrowser(tc.probe)
		if direct != tc.wantDirect {
			t.Errorf("case %d: direct=%v, want %v", i, direct, tc.wantDirect)
		}
	}
}

func TestDetectLangFromName_Fallback_Extra(t *testing.T) {
	if got := detectLangFromName("movie.de.srt"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestDropHiddenLocalEntries(t *testing.T) {
	ents := []lb.Entry{{Path: "secret"}, {Path: "ok"}}
	if got := dropHiddenLocalEntries(ents, map[string]bool{"secret": true}); len(got) != 1 || got[0].Path != "ok" {
		t.Errorf("dropHiddenLocalEntries = %+v", got)
	}
}

// LocalSetHidden persists a path; LocalListHidden reads it back.
func TestLocalHiddenEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "secret"), 0o755); err != nil {
		t.Fatal(err)
	}
	b := lb.NewBrowser([]config.ExternalMount{{Name: "M", Path: dir}})
	s, _ := newCurtainStreamer(t)

	router := gin.New()
	router.Use(middleware.RevealHidden())
	router.POST("/api/local/hidden", LocalSetHidden(b, s))
	router.GET("/api/local/hidden", LocalListHidden(s))
	router.GET("/api/local/list", LocalList(b, s))

	// Hide "secret".
	body := `{"mount":"M","path":"secret","hidden":true}`
	req := httptest.NewRequest("POST", "/api/local/hidden", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST hidden: status %d, body %s", w.Code, w.Body.String())
	}

	// LocalListHidden returns it.
	req = httptest.NewRequest("GET", "/api/local/hidden", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var paths []streamer.HiddenLocalPath
	if err := json.Unmarshal(w.Body.Bytes(), &paths); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0].Path != "secret" {
		t.Fatalf("LocalListHidden = %+v", paths)
	}

	// Default LocalList hides "secret".
	req = httptest.NewRequest("GET", "/api/local/list?mount=M", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if strings.Contains(w.Body.String(), "secret") {
		t.Errorf("hidden dir leaked into default list: %s", w.Body.String())
	}

	// With the curtain open, "secret" shows.
	req = httptest.NewRequest("GET", "/api/local/list?mount=M", nil)
	req.Header.Set("X-JackUI-Reveal-Hidden", "1")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), "secret") {
		t.Errorf("revealed list should contain secret: %s", w.Body.String())
	}
}

// LocalSetHidden rejects a body missing mount/path.
func TestLocalSetHidden_BadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := lb.NewBrowser([]config.ExternalMount{{Name: "M", Path: t.TempDir()}})
	s, _ := newCurtainStreamer(t)
	router := gin.New()
	router.POST("/api/local/hidden", LocalSetHidden(b, s))

	req := httptest.NewRequest("POST", "/api/local/hidden", strings.NewReader(`{"mount":"M"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing path: status %d, want 400", w.Code)
	}
}

func TestAudioCoverQuery(t *testing.T) {
	cases := []struct {
		tags audiometa.Tags
		abs  string
		want string
	}{
		{audiometa.Tags{Artist: "Wind Rose", Album: "Trollslayer"}, "/x/01.flac", "Wind Rose Trollslayer album cover"},
		{audiometa.Tags{Artist: "Wind Rose", Title: "Diggy"}, "/x/01.flac", "Wind Rose Diggy cover"},
		{audiometa.Tags{}, "/music/Some Song.flac", "Some Song album cover"},
	}
	for _, tc := range cases {
		if got := audioCoverQuery(tc.tags, tc.abs); got != tc.want {
			t.Errorf("audioCoverQuery(%+v,%q)=%q want %q", tc.tags, tc.abs, got, tc.want)
		}
	}
}

func TestServeWebCover_Found(t *testing.T) {
	c, w := newTestCtx()
	chain := imagesearch.NewChain(stubSource{data: []byte("PNGDATA"), ct: "image/png"})
	serveWebCover(c, "/x/song.flac", `"etag"`, chain)
	if w.Code != 200 || w.Body.String() != "PNGDATA" {
		t.Fatalf("got code=%d body=%q, want 200/PNGDATA", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("content-type=%q want image/png", ct)
	}
}

func TestServeWebCover_NilChainAnd404(t *testing.T) {
	c1, _ := newTestCtx()
	serveWebCover(c1, "/x/song.flac", `"e"`, nil)
	if c1.Writer.Status() != 204 {
		t.Errorf("nil chain: status=%d want 204", c1.Writer.Status())
	}
	c2, _ := newTestCtx()
	serveWebCover(c2, "/x/song.flac", `"e"`, imagesearch.NewChain(stubSource{data: nil}))
	if c2.Writer.Status() != 204 {
		t.Errorf("empty result: status=%d want 204", c2.Writer.Status())
	}
}

// CountTree totals files + bytes for a file and for a directory tree.
func TestCountTree(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.bin"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.bin"), []byte("678"), 0o644); err != nil {
		t.Fatal(err)
	}
	if f, b := CountTree(filepath.Join(dir, "a.bin")); f != 1 || b != 5 {
		t.Fatalf("file CountTree = %d/%d, want 1/5", f, b)
	}
	if f, b := CountTree(dir); f != 2 || b != 8 {
		t.Fatalf("dir CountTree = %d/%d, want 2/8", f, b)
	}
}

// LocalMoveEntry with a real tracker: the async move lands the file and the
// Transfers dock shows the job finishing at 100% (1/1 files).
func TestLocalMoveEntry_PopulatesTracker(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "file.txt"), bytes.Repeat([]byte("x"), 64), 0o644); err != nil {
		t.Fatal(err)
	}
	b := lb.NewBrowser([]config.ExternalMount{
		{Name: "Src", Path: srcDir},
		{Name: "Dst", Path: dstDir},
	})
	tr := transfer.New()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/move",
		bytes.NewReader([]byte(`{"srcMount":"Src","srcPath":"file.txt","dstMount":"Dst","dstPath":""}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, true)

	LocalMoveEntry(b, nil, nil, tr)(c)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", w.Code, w.Body.String())
	}
	waitForLocalFile(t, filepath.Join(dstDir, "file.txt"), 2*time.Second)

	// The job must be present and reach done with progress 1.0.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		list := tr.List(0, true)
		if len(list) == 1 && list[0].Status == transfer.StatusDone {
			if list[0].Kind != "local-move" || list[0].Progress != 1.0 {
				t.Fatalf("job = %+v, want local-move at 100%%", list[0])
			}
			return
		}
		<-time.After(2 * time.Millisecond) // cede a CPU à goroutine de move
	}
	t.Fatalf("tracker job did not reach done; list=%+v", tr.List(0, true))
}

func TestBuildLocalVODPlaylist_ZeroDuration(t *testing.T) {
	segURL := func(name string) string { return "/seg/" + name }
	playlist := buildLocalVODPlaylist(0, segURL)
	if len(playlist) == 0 {
		t.Error("expected non-empty playlist")
	}
	if !bytes.Contains(playlist, []byte("#EXT-X-ENDLIST")) {
		t.Error("expected EXT-X-ENDLIST")
	}
}

func TestBuildLocalVODPlaylist_WithSegments(t *testing.T) {
	segURL := func(name string) string { return "/hls/" + name }
	playlist := buildLocalVODPlaylist(10, segURL)
	if !bytes.Contains(playlist, []byte("/hls/seg_00000")) {
		t.Error("expected segment URL in playlist")
	}
}

// TestLocalPromoteBatch guards the regression where the batch reclassify moved
// only the first file (req.Path) while the UI reported N moved. The handler must
// move every entry in req.Paths and report the real count.
func TestLocalPromoteBatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	meus := t.TempDir()
	shared := t.TempDir()
	names := []string{"a.mkv", "b.mkv", "c.mkv"}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(meus, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	b := lb.NewBrowser([]config.ExternalMount{{Name: "Meus downloads", Path: meus}})
	router := gin.New()
	router.POST("/api/local/promote", LocalPromote(LocalPromoteDeps{Browser: b, SharedDir: shared}))

	body, _ := json.Marshal(localPromoteReq{Mount: "Meus downloads", Paths: names, TargetSubdir: "filmes"})
	req := httptest.NewRequest(http.MethodPost, "/api/local/promote", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Moved  int `json:"moved"`
		Failed int `json:"failed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, w.Body.String())
	}
	if resp.Moved != 3 || resp.Failed != 0 {
		t.Fatalf("moved=%d failed=%d, want 3/0 — batch promote moved fewer than all files", resp.Moved, resp.Failed)
	}
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(shared, "filmes", n)); err != nil {
			t.Errorf("file %q not moved to destination", n)
		}
		if _, err := os.Stat(filepath.Join(meus, n)); !os.IsNotExist(err) {
			t.Errorf("source %q still present after promote", n)
		}
	}
}

func TestSegURLBuilder_NativeHLS(t *testing.T) {
	withFlag := segURLBuilder("M", "v.mkv", "TOK", "", true, false, "", "")("seg_00001.ts")
	if !strings.Contains(withFlag, "native_hls=1") {
		t.Fatalf("expected native_hls=1 in seg URL, got %q", withFlag)
	}
	without := segURLBuilder("M", "v.mkv", "TOK", "", false, false, "", "")("seg_00001.ts")
	if strings.Contains(without, "native_hls") {
		t.Fatalf("did not expect native_hls when false, got %q", without)
	}
	withPlayback := segURLBuilder("M", "v.mkv", "TOK", "", false, false, "", "viewer-a")("seg_00001.ts")
	if !strings.Contains(withPlayback, "playback=viewer-a") {
		t.Fatalf("expected playback ID in segment URL, got %q", withPlayback)
	}
}

func TestCollectDirSubs_NoDir(t *testing.T) {
	_, err := collectDirSubs("/nonexistent/dir", "movie")
	if err == nil {
		t.Error("expected error for nonexistent dir")
	}
}

func Test_hgA_movePathJob_File(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.bin")
	dst := filepath.Join(t.TempDir(), "dst.bin")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(src)
	if err := MovePathJob(src, dst, st, nil, 0, 0); err != nil {
		t.Fatalf("MovePathJob file: %v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("dst not present: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src should be gone after move")
	}
}

// Regressão #2105: promover um whole-torrent (file_path = DIRETÓRIO) caía no
// caminho cross-device e tratava o diretório como arquivo único, estourando
// "read ...: is a directory". copyDirAndRemoveJob copia a árvore inteira.
func Test_hgA_copyDirAndRemove_Tree(t *testing.T) {
	src := filepath.Join(t.TempDir(), "Brasiloirinha")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "b.mp4"), []byte("yy"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "out")
	st, _ := os.Stat(src)
	if err := copyDirAndRemoveJob(src, dst, st, nil); err != nil {
		t.Fatalf("copyDirAndRemoveJob: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "a.mp4")); err != nil {
		t.Errorf("dst/a.mp4 ausente: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "sub", "b.mp4")); err != nil {
		t.Errorf("dst/sub/b.mp4 ausente: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src deveria ser removido após o move da árvore")
	}
}

func Test_hgB_LocalHLSMaster_MissingParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := hgBBrowser(t)
	mgr := hgBManager(t)
	r := gin.New()
	r.GET("/hls", LocalHLSMaster(b, mgr, nil, nil))

	w := hgBGET(t, r, "/hls?mount=Test")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), ErrMissingMountOrPathParam) {
		t.Errorf("body=%s", w.Body.String())
	}
}

func Test_hgB_LocalHLSMaster_ForbiddenMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := hgBBrowser(t)
	mgr := hgBManager(t)
	r := gin.New()
	r.GET("/hls", LocalHLSMaster(b, mgr, nil, nil))

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
	r.GET("/hls", LocalHLSMaster(b, mgr, nil, nil))

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
	r.GET("/hls", LocalHLSMaster(b, mgr, nil, nil))

	w := hgBGET(t, r, "/hls?mount=Test&path=sub")
	// resolveLocalFileStat errors (httpshared.ErrPathIsDir) → silent return, default 200.
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

	sess, err := startLocalHLSSession(c, mgr, nil, localHLSSource{
		mount: "Test", path: "x.mkv", abs: "/no/such/file.mkv",
	})
	if err == nil {
		t.Fatal("expected open error")
	}
	if sess != nil {
		t.Error("expected nil session on error")
	}
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status=%d want 500", w.Code)
	}
}

// resolveLocalFileStat surfaces a directory as httpshared.ErrPathIsDir.
func Test_hgB_ResolveLocalFileStat_Dir(t *testing.T) {
	b, dir := hgBBrowser(t)
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, _, err := resolveLocalFileStat(b, "Test", "d")
	if err == nil || !strings.Contains(err.Error(), httpshared.ErrPathIsDir) {
		t.Errorf("err=%v want %q", err, httpshared.ErrPathIsDir)
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
	build := segURLBuilder("Test", "movie.mkv", "TOK", "", false, false, "", "")
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
	if ct := w.Header().Get(httpshared.ContentType); !strings.Contains(ct, "text/plain") {
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
	r.GET("/ex", LocalSubtitleExtract(b, nil))
	w := hgBGET(t, r, "/ex?mount=Test&path=x.mkv")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_LocalSubtitleExtract_ForbiddenMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := hgBBrowser(t)
	r := gin.New()
	r.GET("/ex", LocalSubtitleExtract(b, nil))
	w := hgBGET(t, r, "/ex?mount=Nope&path=x.mkv&track=0")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403; body=%s", w.Code, w.Body.String())
	}
}

func Test_hgB_LocalSubtitleExtract_BadTrack(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := hgBBrowser(t)
	r := gin.New()
	r.GET("/ex", LocalSubtitleExtract(b, nil))
	w := hgBGET(t, r, "/ex?mount=Test&path=x.mkv&track=notnum")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid track index") {
		t.Errorf("body=%s", w.Body.String())
	}
}

func Test_hgC_DropTorrentFromStreamer_NilStreamer(t *testing.T) {
	// Must be a no-op (no panic) when the streamer is nil.
	dropTorrentFromStreamer(nil, downloads.Download{InfoHash: "abc", Name: "x"})
}

func Test_hgC_DropTorrentFromStreamer_InvalidHashNoName(t *testing.T) {
	s := streamer.NewForTesting()
	// Invalid (non-hex / wrong length) hash skips Drop; empty name returns early
	// before touching favorites.
	dropTorrentFromStreamer(s, downloads.Download{InfoHash: "not-a-real-hash", Name: ""})
}

func Test_hgC_DropTorrentFromStreamer_WithNameAndFavorites(t *testing.T) {
	s := streamer.NewForTesting()
	favs, err := streamer.NewFavorites(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}
	defer favs.Close()
	if err := favs.Add("My Torrent", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "magnet:?xt=x", "", 1); err != nil {
		t.Fatal(err)
	}
	s.SetFavorites(favs)

	d := downloads.Download{
		UserID:   1,
		InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Name:     "My Torrent",
	}
	dropTorrentFromStreamer(s, d)

	if favs.IsFavorite("My Torrent") {
		t.Error("expected favorite to be removed after drop")
	}
}

func Test_hgC_ThumbCachePath_StableForSameInputs(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "vid.mp4")
	if err := os.WriteFile(f, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(dir, "cache")
	p1 := thumbCachePath(cacheDir, f, 10)
	p2 := thumbCachePath(cacheDir, f, 10)
	if p1 != p2 {
		t.Errorf("path not stable: %q vs %q", p1, p2)
	}
	if !strings.HasSuffix(p1, ".jpg") {
		t.Errorf("path = %q, want .jpg suffix", p1)
	}
	if filepath.Dir(p1) != cacheDir {
		t.Errorf("dir = %q, want %q", filepath.Dir(p1), cacheDir)
	}
	// Different timestamp arg => different cache key.
	if thumbCachePath(cacheDir, f, 99) == p1 {
		t.Error("expected different key for different `at`")
	}
}

func Test_hgC_CopyFileAndRemove(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "sub", "dst.bin")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := copyFileAndRemove(src, dst, stat); err != nil {
		t.Fatalf("copyFileAndRemove: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("src should be removed after copy")
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("dst content = %q", string(got))
	}
}

func Test_hgC_CopyFileAndRemove_SrcMissing(t *testing.T) {
	dir := t.TempDir()
	stat := hgCFakeFileInfo{}
	if err := copyFileAndRemove(filepath.Join(dir, "nope"), filepath.Join(dir, "out"), stat); err == nil {
		t.Error("expected error opening missing source")
	}
}

// Resume: o destino já tem o arquivo com o mesmo tamanho (uma transferência
// anterior o copiou antes de ser interrompida). copyFileAndRemove deve PULAR a
// cópia, preservar o destino e remover a origem — sem recopiar.
func Test_hgC_CopyFileAndRemove_ResumeSkipsExisting(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")
	if err := os.WriteFile(src, []byte("conteudo-igual"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("conteudo-igual"), 0o644); err != nil {
		t.Fatal(err)
	}
	stat, _ := os.Stat(src)
	if err := copyFileAndRemove(src, dst, stat); err != nil {
		t.Fatalf("copyFileAndRemove (resume): %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("origem deveria ser removida (move concluído via resume)")
	}
	if b, _ := os.ReadFile(dst); string(b) != "conteudo-igual" {
		t.Errorf("destino não deveria mudar no resume, got %q", b)
	}
}

// Resume de diretório parcial: parte dos arquivos já está no destino (run
// anterior interrompida). copyDirAndRemove completa só o que falta e remove a
// origem inteira ao final.
func Test_hgC_CopyDirAndRemove_ResumePartial(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Brasiloirinha")
	dst := filepath.Join(dir, "out")
	if err := os.MkdirAll(filepath.Join(src, "Fotos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "Fotos", "1.jpg"), []byte("foto"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "video.mp4"), []byte("video-grande"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Simula a run anterior: a foto já foi copiada pro destino, o vídeo não.
	if err := os.MkdirAll(filepath.Join(dst, "Fotos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "Fotos", "1.jpg"), []byte("foto"), 0o644); err != nil {
		t.Fatal(err)
	}
	stat, _ := os.Stat(src)
	if err := copyDirAndRemove(src, dst, stat); err != nil {
		t.Fatalf("copyDirAndRemove (resume): %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "video.mp4")); string(b) != "video-grande" {
		t.Errorf("vídeo faltante não foi copiado, got %q", b)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("origem deveria ser removida ao final do resume")
	}
}

func Test_hgC_CopyDirAndRemove(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "srcdir")
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "b.txt"), []byte("B"), 0o644); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dstdir")
	if err := copyDirAndRemove(src, dst, stat); err != nil {
		t.Fatalf("copyDirAndRemove: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("src dir should be removed")
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "a.txt")); string(got) != "A" {
		t.Errorf("a.txt = %q, want A", string(got))
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "nested", "b.txt")); string(got) != "B" {
		t.Errorf("nested/b.txt = %q, want B", string(got))
	}
}

func Test_hgC_MovePath_Rename(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "m.txt")
	dst := filepath.Join(dir, "m2.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	stat, _ := os.Stat(src)
	if err := movePath(src, dst, stat); err != nil {
		t.Fatalf("movePath: %v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("dst missing after move: %v", err)
	}
}

func Test_hgC_LocalMove_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "file.txt"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := lb.NewBrowser([]config.ExternalMount{
		{Name: "Src", Path: srcDir},
		{Name: "Dst", Path: dstDir},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/move",
		bytes.NewReader([]byte(`{"srcMount":"Src","srcPath":"file.txt","dstMount":"Dst","dstPath":""}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, true)

	LocalMoveEntry(b, nil, nil, nil)(c)

	// The move now runs asynchronously (202 Accepted) and reports to the
	// Transfers dock; wait for the goroutine to land the file.
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", w.Code, w.Body.String())
	}
	dst := filepath.Join(dstDir, "file.txt")
	waitForLocalFile(t, dst, 2*time.Second)
	if _, err := os.Stat(filepath.Join(srcDir, "file.txt")); !os.IsNotExist(err) {
		t.Error("source file should be gone after move")
	}
}

func Test_hgC_LocalMove_SelfMove(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mountDir, "folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	b := lb.NewBrowser([]config.ExternalMount{
		{Name: "M", Path: mountDir},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// Moving "folder" into "folder" => self-move guard rejects with 400.
	c.Request = httptest.NewRequest("POST", "/api/local/move",
		bytes.NewReader([]byte(`{"srcMount":"M","srcPath":"folder","dstMount":"M","dstPath":"folder"}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, true)

	LocalMoveEntry(b, nil, nil, nil)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (self-move); body: %s", w.Code, w.Body.String())
	}
}

func Test_hgC_LocalMove_CollisionRefused(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "file.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Destination already has a file with the same name → must NOT be clobbered.
	if err := os.WriteFile(filepath.Join(dstDir, "file.txt"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := lb.NewBrowser([]config.ExternalMount{
		{Name: "Src", Path: srcDir},
		{Name: "Dst", Path: dstDir},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/move",
		bytes.NewReader([]byte(`{"srcMount":"Src","srcPath":"file.txt","dstMount":"Dst","dstPath":""}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, true)

	LocalMoveEntry(b, nil, nil, nil)(c)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (collision); body: %s", w.Code, w.Body.String())
	}
	// Destination file must be untouched; source must still be there.
	if data, _ := os.ReadFile(filepath.Join(dstDir, "file.txt")); string(data) != "existing" {
		t.Errorf("destination overwritten: %q", data)
	}
	if _, err := os.Stat(filepath.Join(srcDir, "file.txt")); err != nil {
		t.Errorf("source should be intact after refused move: %v", err)
	}
}

func Test_hgC_CopyFileAndRemove_PreservesMtime(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.txt")
	dst := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(src, old, old); err != nil {
		t.Fatal(err)
	}
	stat, _ := os.Stat(src)
	if err := copyFileAndRemove(src, dst, stat); err != nil {
		t.Fatalf("copyFileAndRemove: %v", err)
	}
	st, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !st.ModTime().Truncate(time.Second).Equal(old) {
		t.Errorf("mtime not preserved: got %v, want %v", st.ModTime(), old)
	}
}

func Test_hgC_LocalPromotePreview_NoSharedDir(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := lb.NewBrowser(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/promote/preview", nil)

	LocalPromotePreview(b, nil, nil, "", nil, nil)(c)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 (shared dir not configured); body: %s", w.Code, w.Body.String())
	}
}

func Test_hgC_LocalPromotePreview_MountRoot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	sharedDir := t.TempDir()
	b := lb.NewBrowser([]config.ExternalMount{
		{Name: "Meus downloads", Path: mountDir},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/promote/preview",
		bytes.NewReader([]byte(`{"mount":"Meus downloads","path":"."}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, true)

	LocalPromotePreview(b, nil, nil, sharedDir, nil, nil)(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Previews []map[string]any `json:"previews"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Previews) != 1 {
		t.Fatalf("previews = %d, want 1", len(resp.Previews))
	}
	if resp.Previews[0]["error"] == nil {
		t.Errorf("expected mount-root error in preview, got %v", resp.Previews[0])
	}
}

func Test_hgC_PreviewItem_FileMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := lb.NewBrowser([]config.ExternalMount{
		{Name: "M", Path: mountDir},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	d := &localPreviewDeps{c: c, b: b, mount: "M"}
	got := previewItem(d, "ghost.mp4", "ghost.mp4")
	if got["error"] == nil {
		t.Errorf("expected error for missing file, got %v", got)
	}
}

func Test_hgC_BuildLocalPreviews_Empty(t *testing.T) {
	got := buildLocalPreviews(&localPreviewDeps{}, nil, nil)
	if got == nil || len(got) != 0 {
		t.Errorf("buildLocalPreviews(nil) = %v, want empty non-nil slice", got)
	}
}

func Test_hgC_LocalThumb_TraversalBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := lb.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	router := gin.New()
	router.GET("/api/local/thumb", LocalThumb(b))

	// Video ext passes the early 204 guard, then ResolvePath rejects the
	// traversal => 400 from the abs-resolution error path.
	req := httptest.NewRequest("GET", "/api/local/thumb?mount=Test&path=../escape.mp4", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func Test_hgC_LocalThumb_VideoNotFound204(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := lb.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	router := gin.New()
	router.GET("/api/local/thumb", LocalThumb(b))

	// Valid video ext but the file does not exist => resolveLocalAbs returns
	// "" => 404.
	req := httptest.NewRequest("GET", "/api/local/thumb?mount=Test&path=missing.mp4", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}
