package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/local"
)

// TestClassifyForBrowser pins the direct-play vs HLS decision so a future tweak
// to the codec/container whitelist doesn't accidentally route MKV/HEVC through
// the browser (which would resurface the "Hobbit em mkv não toca" failure mode
// the local-HLS path was added to fix).
func TestClassifyForBrowser(t *testing.T) {
	cases := []struct {
		name       string
		probe      localProbe
		wantDirect bool
		// matchReason is a substring expected in the rejection reason; "" means
		// no reason expected (direct-play).
		matchReason string
	}{
		{
			name:       "mp4_h264_aac_direct",
			probe:      localProbe{Container: "mov", VideoCodec: "h264", AudioCodec: "aac"},
			wantDirect: true,
		},
		{
			name:        "matroska_hevc_ac3_hls",
			probe:       localProbe{Container: "matroska", VideoCodec: "hevc", AudioCodec: "ac3"},
			wantDirect:  false,
			matchReason: "container=matroska",
		},
		{
			name:        "mp4_hevc_aac_hls",
			probe:       localProbe{Container: "mov", VideoCodec: "hevc", AudioCodec: "aac"},
			wantDirect:  false,
			matchReason: "vcodec=hevc",
		},
		{
			name:        "mp4_h264_ac3_hls",
			probe:       localProbe{Container: "mp4", VideoCodec: "h264", AudioCodec: "ac3"},
			wantDirect:  false,
			matchReason: "acodec=ac3",
		},
		{
			name:       "webm_vp9_opus_direct",
			probe:      localProbe{Container: "webm", VideoCodec: "vp9", AudioCodec: "opus"},
			wantDirect: true,
		},
		{
			name:        "av1_in_mp4_hls",
			probe:       localProbe{Container: "mp4", VideoCodec: "av1", AudioCodec: "aac"},
			wantDirect:  false,
			matchReason: "vcodec=av1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotDirect, gotReason := classifyForBrowser(tc.probe)
			if gotDirect != tc.wantDirect {
				t.Errorf("direct=%v, want %v (reason=%q)", gotDirect, tc.wantDirect, gotReason)
			}
			if tc.matchReason != "" && !strings.Contains(gotReason, tc.matchReason) {
				t.Errorf("reason=%q, want substring %q", gotReason, tc.matchReason)
			}
		})
	}
}

// TestLocalSessionKeyStable ensures the (mount, path) → key derivation is a
// stable function — same input always yields the same session key, different
// inputs differ. Without this, two viewers of the same file would spawn
// duplicate ffmpeg sessions (manager dedupes by exact key).
func TestLocalSessionKeyStable(t *testing.T) {
	a := localSessionKey("Downloads", "movies/The.Hobbit.mkv")
	b := localSessionKey("Downloads", "movies/The.Hobbit.mkv")
	if a != b {
		t.Errorf("session key not stable: %s vs %s", a, b)
	}
	c := localSessionKey("Downloads", "movies/Other.mkv")
	if a == c {
		t.Errorf("different paths produced same key %s", a)
	}
	if !strings.HasPrefix(a, "local-") {
		t.Errorf("expected local- prefix, got %s", a)
	}
}

// TestBuildLocalVODPlaylistShape mirrors the torrent-side guard:
// ceil(duration/segDur) segment lines, EXT-X-ENDLIST present, and each segment
// line is the URL the segURL builder produced (so the token reaches the segment
// endpoint).
func TestBuildLocalVODPlaylistShape(t *testing.T) {
	segURL := func(name string) string {
		return "/api/local/hls/seg?mount=M&path=p.mkv&seg=" + name + "&token=TOK"
	}
	pl := string(buildLocalVODPlaylist(30, segURL))
	if !strings.Contains(pl, "#EXT-X-PLAYLIST-TYPE:VOD") {
		t.Error("playlist must be VOD")
	}
	if !strings.Contains(pl, "#EXT-X-ENDLIST") {
		t.Error("missing ENDLIST")
	}
	// 30/4 = 7.5 → 8 segments
	if got := strings.Count(pl, "seg_"); got != 8 {
		t.Errorf("expected 8 segments, got %d\n%s", got, pl)
	}
	if !strings.Contains(pl, "seg_00007.ts&token=TOK") {
		t.Errorf("missing tokenised last segment; got:\n%s", pl)
	}
}

func TestAppendTokenToURL_EmptyToken(t *testing.T) {
	got := appendTokenToURL("", "/api/local/file")
	if got != "/api/local/file" {
		t.Errorf("got %q, want %q", got, "/api/local/file")
	}
}

func TestAppendTokenToURL_WithToken(t *testing.T) {
	got := appendTokenToURL("mytoken", "/api/local/file")
	if got != "/api/local/file?token=mytoken" {
		t.Errorf("got %q, want %q", got, "/api/local/file?token=mytoken")
	}
}

func TestAppendTokenToURL_ExistingQuery(t *testing.T) {
	got := appendTokenToURL("mytoken", "/api/local/file?mount=Test")
	if got != "/api/local/file?mount=Test&token=mytoken" {
		t.Errorf("got %q, want %q", got, "/api/local/file?mount=Test&token=mytoken")
	}
}

func TestAppendTokenToURL_TokenNeedsEscaping(t *testing.T) {
	got := appendTokenToURL("tok/?#", "/api/local/file")
	if got != "/api/local/file?token=tok%2F%3F%23" {
		t.Errorf("got %q, want %q", got, "/api/local/file?token=tok%2F%3F%23")
	}
}

func TestBuildLocalFileURL(t *testing.T) {
	got := buildLocalFileURL("Mount Name", "path/to/file.mp4")
	if !strings.Contains(got, "mount=Mount+Name") {
		t.Errorf("expected mount param, got %q", got)
	}
	if !strings.Contains(got, "path=path%2Fto%2Ffile.mp4") {
		t.Errorf("expected path param, got %q", got)
	}
	if !strings.HasPrefix(got, "/api/local/file?") {
		t.Errorf("expected /api/local/file? prefix, got %q", got)
	}
}

func TestBuildLocalHLSURL(t *testing.T) {
	got := buildLocalHLSURL("Mount", "video.mp4")
	if !strings.HasPrefix(got, "/api/local/hls/index.m3u8?") {
		t.Errorf("expected /api/local/hls/index.m3u8? prefix, got %q", got)
	}
}

func TestValidSegName_Valid(t *testing.T) {
	if !validSegName("seg_00000.ts") {
		t.Error("expected valid segment name")
	}
}

func TestValidSegName_WithSlash(t *testing.T) {
	if validSegName("../seg.ts") {
		t.Error("expected invalid segment name (traversal)")
	}
}

func TestValidSegName_WithDoubleDot(t *testing.T) {
	if validSegName("foo..bar") {
		t.Error("expected invalid segment name (double dot)")
	}
}

func TestValidLocalSegPath_Valid(t *testing.T) {
	// Use a Browser with a temp dir mount
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})
	if !validLocalSegPath(b, "Test", "video.mp4") {
		t.Error("expected valid path")
	}
}

func TestValidLocalSegPath_UnknownMount(t *testing.T) {
	b := local.NewBrowser(nil)
	if validLocalSegPath(b, "DoesNotExist", "video.mp4") {
		t.Error("expected invalid path (unknown mount)")
	}
}

func TestLocalPlayToken_QueryParam(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/play?token=abc123", nil)

	got := localPlayToken(c)
	if got != "abc123" {
		t.Errorf("got %q, want %q", got, "abc123")
	}
}

func TestLocalPlayToken_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/play", nil)

	got := localPlayToken(c)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestParseAt_Default(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	got := parseAt(c)
	if got != 10 {
		t.Errorf("got %d, want 10", got)
	}
}

func TestParseAt_Custom(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/?at=30", nil)

	got := parseAt(c)
	if got != 30 {
		t.Errorf("got %d, want 30", got)
	}
}

func TestParseAt_Negative(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/?at=-5", nil)

	got := parseAt(c)
	if got != 10 {
		t.Errorf("got %d, want 10 (negative should use default)", got)
	}
}

func TestIsSelfMove_NotDir(t *testing.T) {
	got := isSelfMove(fileInfoNotDir(), "/src/file.txt", "/dst/file.txt")
	if got {
		t.Error("expected false for non-directory")
	}
}

func TestIsSelfMove_DirIntoItself(t *testing.T) {
	got := isSelfMove(fileInfoIsDir(), "/movies", "/movies/subdir")
	if !got {
		t.Error("expected true: moving /movies INTO /movies/subdir is a self-move")
	}
}

func TestIsSelfMove_DifferentDirs(t *testing.T) {
	got := isSelfMove(fileInfoIsDir(), "/movies/a", "/movies/b")
	if got {
		t.Error("expected false for different directories")
	}
}

// Helper to create a mock FileInfo for testing isSelfMove
type mockFileInfo struct{ dir bool }

func (m mockFileInfo) Name() string       { return "test" }
func (m mockFileInfo) Size() int64        { return 0 }
func (m mockFileInfo) Mode() os.FileMode  { return 0 }
func (m mockFileInfo) ModTime() time.Time { return time.Time{} }
func (m mockFileInfo) IsDir() bool        { return m.dir }
func (m mockFileInfo) Sys() interface{}   { return nil }

func fileInfoNotDir() os.FileInfo { return mockFileInfo{dir: false} }
func fileInfoIsDir() os.FileInfo  { return mockFileInfo{dir: true} }

func TestIsTraversalErr_Traversal(t *testing.T) {
	if !isTraversalErr(fmt.Errorf("path traversal rejected")) {
		t.Error("expected true for traversal error")
	}
}

func TestIsTraversalErr_Relative(t *testing.T) {
	if !isTraversalErr(fmt.Errorf("must be relative to mount root")) {
		t.Error("expected true for relative error")
	}
}

func TestIsTraversalErr_Other(t *testing.T) {
	if isTraversalErr(fmt.Errorf("something else")) {
		t.Error("expected false for other error")
	}
}

func TestMovePath_Rename(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.txt")
	dst := filepath.Join(t.TempDir(), "dst.txt")
	os.WriteFile(src, []byte("content"), 0644)
	stat, _ := os.Stat(src)

	err := movePath(src, dst, stat)
	if err != nil {
		t.Fatalf("movePath: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source should be gone after rename")
	}
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		t.Error("destination should exist after rename")
	}
}

func TestErrStr_Nil(t *testing.T) {
	if got := errStr(nil); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestErrStr_WithError(t *testing.T) {
	if got := errStr(fmt.Errorf("some error")); got != "some error" {
		t.Errorf("got %q, want 'some error'", got)
	}
}

func TestErrStrIfAny_Nil(t *testing.T) {
	if got := errStrIfAny(nil); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestErrStrIfAny_WithError(t *testing.T) {
	if got := errStrIfAny(fmt.Errorf("test error")); got != "test error" {
		t.Errorf("got %q, want 'test error'", got)
	}
}

func TestSegURLBuilder(t *testing.T) {
	builder := segURLBuilder("Mount", "video.mp4", "mytoken", "")
	got := builder("seg_00000.ts")
	if !strings.Contains(got, "mount=Mount") {
		t.Errorf("expected mount param, got %q", got)
	}
	if !strings.Contains(got, "seg=seg_00000.ts") {
		t.Errorf("expected seg param, got %q", got)
	}
	if !strings.Contains(got, "token=mytoken") {
		t.Errorf("expected token param, got %q", got)
	}
}

func TestSegURLBuilder_NoToken(t *testing.T) {
	builder := segURLBuilder("Mount", "video.mp4", "", "")
	got := builder("seg_00000.ts")
	if strings.Contains(got, "token=") {
		t.Errorf("expected no token param, got %q", got)
	}
}

func TestLocalPlayVideoResp_ProbeFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	mountDir := t.TempDir()
	notAVideo := filepath.Join(mountDir, "video.mkv")
	os.WriteFile(notAVideo, []byte("garbage"), 0644)

	resp := localPlayVideoResp(c, notAVideo, "Test", "video.mkv", "tok")
	if resp.Kind != "hls" {
		t.Errorf("kind = %q, want 'hls' for MKV probe fail", resp.Kind)
	}
	if resp.URL == "" {
		t.Error("expected non-empty URL")
	}
}

func TestListHandleError_Traversal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/list?mount=Test&path=../escape", nil)

	listHandleError(local.NewBrowser(nil), c, fmt.Errorf("path traversal rejected"))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestListHandleError_Other(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/list", nil)

	listHandleError(local.NewBrowser(nil), c, fmt.Errorf("permission denied"))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500; body: %s", w.Code, w.Body.String())
	}
}

func TestListHandleError_UserSubpath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "My", Path: mountDir, UserSubpath: true},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/list?mount=My", nil)

	listHandleError(b, c, os.ErrNotExist)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestListHandleError_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/local/list?mount=Real", nil)

	listHandleError(local.NewBrowser([]config.ExternalMount{{Name: "Real", Path: t.TempDir()}}), c, os.ErrNotExist)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestResolveLocalPaths_Empty(t *testing.T) {
	b := local.NewBrowser(nil)
	got := resolveLocalPaths(b, &localPromoteReq{}, "testuser")
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestResolveLocalPaths_WithPath(t *testing.T) {
	b := local.NewBrowser(nil)
	got := resolveLocalPaths(b, &localPromoteReq{Path: "video.mp4"}, "testuser")
	if len(got) != 1 || !strings.HasSuffix(got[0], "video.mp4") {
		t.Errorf("expected [video.mp4], got %v", got)
	}
}

func TestResolveLocalPaths_WithPaths(t *testing.T) {
	b := local.NewBrowser(nil)
	got := resolveLocalPaths(b, &localPromoteReq{Paths: []string{"a.mp4", "b.mp4"}}, "testuser")
	if len(got) != 2 {
		t.Errorf("expected 2 paths, got %v", got)
	}
}

func TestBuildPromoteDests_Empty(t *testing.T) {
	dests := BuildPromoteDests("", nil)
	if len(dests) != 0 {
		t.Errorf("expected 0 dests, got %d", len(dests))
	}
}

func TestBuildPromoteDests_WithSharedDir(t *testing.T) {
	dests := BuildPromoteDests("/shared", nil)
	if len(dests) != 1 || dests[0].Name != "Biblioteca" {
		t.Errorf("expected [Biblioteca], got %v", dests)
	}
}

func TestBuildPromoteDests_WithExtra(t *testing.T) {
	dests := BuildPromoteDests("/shared", []PromoteDest{{Name: "Extra", Path: "/extra"}})
	if len(dests) != 2 {
		t.Errorf("expected 2 dests, got %d", len(dests))
	}
}

func TestSetSSEHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/search/stream?q=test", nil)

	setSSEHeaders(c)

	if w.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", w.Header().Get("Content-Type"))
	}
	if w.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", w.Header().Get("Cache-Control"))
	}
	if w.Header().Get("X-Accel-Buffering") != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", w.Header().Get("X-Accel-Buffering"))
	}
}

func TestNotify_NilMailer(t *testing.T) {
	notify(nil, "test@example.com", "Subject", "Intro text", "http://link")
}
