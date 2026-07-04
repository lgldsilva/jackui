package httpshared

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/transcode"
)

func testCtx(query string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	url := "/"
	if query != "" {
		url += "?" + query
	}
	c.Request = httptest.NewRequest("GET", url, nil)
	return c, w
}

func TestParseIntOr(t *testing.T) {
	cases := []struct {
		s         string
		def, want int
	}{
		{"", 5, 5},
		{"42", 0, 42},
		{"abc", 7, 7},
		{"-3", 0, -3},
	}
	for _, c := range cases {
		if got := ParseIntOr(c.s, c.def); got != c.want {
			t.Errorf("ParseIntOr(%q, %d) = %d, want %d", c.s, c.def, got, c.want)
		}
	}
}

func TestNativeHLSParam(t *testing.T) {
	if c, _ := testCtx("native_hls=1"); !NativeHLSParam(c) {
		t.Error("native_hls=1 should be true")
	}
	if c, _ := testCtx("native_hls=0"); NativeHLSParam(c) {
		t.Error("native_hls=0 should be false")
	}
	if c, _ := testCtx(""); NativeHLSParam(c) {
		t.Error("absent should be false")
	}
}

func TestResolveTargetBase(t *testing.T) {
	dests := []PromoteDest{{Name: "G", Path: "/g"}}
	if got, err := ResolveTargetBase("", "/shared", dests); err != nil || got != "/shared" {
		t.Errorf("empty → sharedDir: got %q, err %v", got, err)
	}
	if got, err := ResolveTargetBase("/g", "/shared", dests); err != nil || got != "/g" {
		t.Errorf("match: got %q, err %v", got, err)
	}
	if _, err := ResolveTargetBase("/nope", "/shared", dests); err == nil {
		t.Error("unknown target should error")
	}
}

func TestSanitizeSubdir(t *testing.T) {
	if got, err := SanitizeSubdir(""); err != nil || got != "" {
		t.Errorf("empty: got %q, err %v", got, err)
	}
	if got, err := SanitizeSubdir("."); err != nil || got != "" {
		t.Errorf("dot: got %q, err %v", got, err)
	}
	if got, err := SanitizeSubdir("movies/2026"); err != nil || got != filepath.Clean("movies/2026") {
		t.Errorf("clean: got %q, err %v", got, err)
	}
	if _, err := SanitizeSubdir("/abs/path"); err == nil {
		t.Error("absolute should error")
	}
	if _, err := SanitizeSubdir("../escape"); err == nil {
		t.Error("traversal should error")
	}
}

func TestListDirs(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"beta", "alpha", ".hidden"} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := ListDirs(entries)
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Errorf("ListDirs = %v, want [alpha beta] (sorted, no hidden, no files)", got)
	}
}

func TestServeSegment_Existing(t *testing.T) {
	sess := &transcode.HLSSession{Dir: t.TempDir()}
	seg := "seg_00000.ts"
	if err := os.WriteFile(filepath.Join(sess.Dir, seg), []byte("ts-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, w := testCtx("")
	ServeSegment(c, sess, seg)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get(ContentType); got != "video/mp2t" {
		t.Errorf("content-type = %q, want video/mp2t", got)
	}
	if w.Body.String() != "ts-bytes" {
		t.Errorf("body = %q, want ts-bytes", w.Body.String())
	}
}

func TestServeSegment_Traversal(t *testing.T) {
	sess := &transcode.HLSSession{Dir: t.TempDir()}
	c, w := testCtx("")
	ServeSegment(c, sess, "../escape.ts")
	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestEnsureVODSegment_NonVODNoop(t *testing.T) {
	sess := &transcode.HLSSession{Dir: t.TempDir()} // IsVOD()==false
	EnsureVODSegment(sess, "seg_00003.ts")
	if _, err := os.Stat(filepath.Join(sess.Dir, "seg_00003.ts")); !os.IsNotExist(err) {
		t.Errorf("non-VOD EnsureVODSegment must not create the segment; stat err=%v", err)
	}
}
