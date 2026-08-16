package httpshared

import (
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestRespondError(t *testing.T) {
	c, w := testCtx("")
	RespondError(c, 422, errors.New("bad input"))
	if w.Code != 422 || w.Body.String() != `{"error":"bad input"}` {
		t.Fatalf("status/body = %d/%s, want 422/{\"error\":\"bad input\"}", w.Code, w.Body.String())
	}
}

func TestRespondErrorMessage(t *testing.T) {
	c, w := testCtx("")
	RespondErrorMessage(c, 400, "invalid request")
	if w.Code != 400 || w.Body.String() != `{"error":"invalid request"}` {
		t.Fatalf("status/body = %d/%s, want 400/{\"error\":\"invalid request\"}", w.Code, w.Body.String())
	}
}

func TestRespondErrorFields(t *testing.T) {
	c, w := testCtx("")
	RespondErrorFields(c, 409, errors.New("busy"), gin.H{"retryAfter": 30})
	if w.Code != 409 {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	want := `{"error":"busy","retryAfter":30}`
	if w.Body.String() != want {
		t.Fatalf("body = %s, want %s", w.Body.String(), want)
	}
}

func TestRespondErrorMessageFields(t *testing.T) {
	c, w := testCtx("")
	RespondErrorMessageFields(c, 400, "invalid", gin.H{"field": "email"})
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	want := `{"error":"invalid","field":"email"}`
	if w.Body.String() != want {
		t.Fatalf("body = %s, want %s", w.Body.String(), want)
	}
}

func TestSanitizeForLog_RemovesControlChars(t *testing.T) {
	in := "hello\r\nworld\x00\tone\ntwo"
	want := "hello␊world␉one␊two"
	if got := SanitizeForLog(in); got != want {
		t.Fatalf("SanitizeForLog = %q, want %q", got, want)
	}
}

func TestSanitizeForLog_Truncates(t *testing.T) {
	in := strings.Repeat("a", 1024)
	got := SanitizeForLog(in)
	if len(got) != 512 {
		t.Fatalf("len = %d, want 512", len(got))
	}
}

func TestSanitizeInt(t *testing.T) {
	if got := SanitizeInt(-42); got != "-42" {
		t.Fatalf("SanitizeInt(-42) = %q, want -42", got)
	}
}

func TestSanitizeIntSlice(t *testing.T) {
	got := SanitizeIntSlice([]int{1, 2, 3})
	want := "[1 2 3]"
	if got != want {
		t.Fatalf("SanitizeIntSlice = %q, want %q", got, want)
	}
}

func TestSanitizeIntSlice_RemovesControlChars(t *testing.T) {
	// The function is a no-op for numeric slices, but it must not panic and
	// should still pass through SanitizeForLog so the API is consistent.
	got := SanitizeIntSlice([]int{1, 2})
	want := "[1 2]"
	if got != want {
		t.Fatalf("SanitizeIntSlice = %q, want %q", got, want)
	}
}

func TestSanitizeIntPtr(t *testing.T) {
	if got := SanitizeIntPtr(nil); got != "<nil>" {
		t.Fatalf("SanitizeIntPtr(nil) = %q, want <nil>", got)
	}
	v := 7
	if got := SanitizeIntPtr(&v); got != "7" {
		t.Fatalf("SanitizeIntPtr(&7) = %q, want 7", got)
	}
}

// SanitizeForLog é a barreira de log-injection dos handlers: nenhum controle
// de linha/coluna sobrevive, e o tamanho é limitado.
func TestSanitizeForLog(t *testing.T) {
	in := "line1\r\nline2\tcol\x00nul"
	want := "line1␊line2␉colnul"
	if got := SanitizeForLog(in); got != want {
		t.Fatalf("SanitizeForLog(%q) = %q, want %q", in, got, want)
	}
	long := strings.Repeat("a", 600)
	if got := SanitizeForLog(long); len(got) != 512 {
		t.Fatalf("SanitizeForLog clamps to 512, got %d", len(got))
	}
	if got := SanitizeForLog("plain"); got != "plain" {
		t.Fatalf("plain passthrough broken: %q", got)
	}
}
