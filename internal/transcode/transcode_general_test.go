package transcode

import (
	"context"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseSegIndex(t *testing.T) {
	tests := []struct {
		input string
		index int
		ok    bool
	}{
		{"seg_00000.ts", 0, true},
		{"seg_00042.ts", 42, true},
		{"seg_0.ts", 0, true},
		{"", 0, false},
		{"not-a-seg", 0, false},
		{"index.m3u8", 0, false},
		{"seg_00007.ts.tmp", 0, false},
		{"foo.ts", 0, false},
	}
	for _, tc := range tests {
		idx, ok := ParseSegIndex(tc.input)
		if ok != tc.ok {
			t.Errorf("ParseSegIndex(%q) ok=%v, want %v", tc.input, ok, tc.ok)
			continue
		}
		if ok && idx != tc.index {
			t.Errorf("ParseSegIndex(%q) = %d, want %d", tc.input, idx, tc.index)
		}
	}
}

func TestIsVOD_NilSpec(t *testing.T) {
	s := &HLSSession{}
	if s.IsVOD() {
		t.Error("expected IsVOD=false for nil spec")
	}
}

func TestIsVOD_True(t *testing.T) {
	s := &HLSSession{spec: &encodeSpec{vod: true}}
	if !s.IsVOD() {
		t.Error("expected IsVOD=true")
	}
}

func TestIsVOD_False(t *testing.T) {
	s := &HLSSession{spec: &encodeSpec{vod: false}}
	if s.IsVOD() {
		t.Error("expected IsVOD=false")
	}
}

func TestFFprobePathFrom(t *testing.T) {
	tests := []struct {
		ffmpeg string
		want   string
	}{
		{"/usr/local/bin/ffmpeg", "/usr/local/bin/ffprobe"},
		{"/usr/bin/ffmpeg", "/usr/bin/ffprobe"},
		{"ffmpeg", "ffprobe"},
		{"/custom/path/ffmpeg", "/custom/path/ffprobe"},
	}
	for _, tc := range tests {
		got := ffprobePathFrom(tc.ffmpeg)
		if got != tc.want {
			t.Errorf("ffprobePathFrom(%q) = %q, want %q", tc.ffmpeg, got, tc.want)
		}
	}
}

func TestParseRange(t *testing.T) {
	tests := []struct {
		header    string
		totalSize int64
		start     int64
		end       int64
		ok        bool
	}{
		{"bytes=0-99", 1000, 0, 99, true},
		{"bytes=100-199", 1000, 100, 199, true},
		{"bytes=0-", 1000, 0, 999, true},
		{"bytes=500-", 1000, 500, 999, true},
		{"", 1000, 0, 0, false},
		{"invalid", 1000, 0, 0, false},
		{"bytes=abc-", 1000, 0, 0, false},
		{"bytes=-100", 1000, 0, 0, false},
		{"bytes=0-999999", 1000, 0, 999, true}, // clamp to totalSize-1
		{"bytes=1000-", 1000, 0, 0, false},      // start >= totalSize
		{"bytes=0,100-200", 1000, 0, 0, false}, // multipart not supported
		{"bytes=100-50", 1000, 0, 0, false},    // end < start
		{"bytes=100-100", 1000, 100, 100, true},
		{"bytes=999-", 1000, 999, 999, true},
	}
	for _, tc := range tests {
		start, end, ok := parseRange(tc.header, tc.totalSize)
		if ok != tc.ok {
			t.Errorf("parseRange(%q, %d) ok=%v, want %v", tc.header, tc.totalSize, ok, tc.ok)
			continue
		}
		if ok && (start != tc.start || end != tc.end) {
			t.Errorf("parseRange(%q, %d) = (%d,%d), want (%d,%d)", tc.header, tc.totalSize, start, end, tc.start, tc.end)
		}
	}
}

func TestSessionPid(t *testing.T) {
	s := &HLSSession{}
	if pid := sessionPid(s); pid != 0 {
		t.Errorf("expected 0 for nil Cmd/Process, got %d", pid)
	}
}

func TestSessionEncoder_NilSpec(t *testing.T) {
	s := &HLSSession{}
	if enc := sessionEncoder(s); enc != "cpu" {
		t.Errorf("expected 'cpu' fallback, got %q", enc)
	}
}

func TestSessionEncoder_WithSpec(t *testing.T) {
	s := &HLSSession{spec: &encodeSpec{encoder: "h264_nvenc"}}
	if enc := sessionEncoder(s); enc != "h264_nvenc" {
		t.Errorf("expected 'h264_nvenc', got %q", enc)
	}
}

func TestSessionSegmentsReady_EmptyDir(t *testing.T) {
	s := &HLSSession{Dir: t.TempDir()}
	if n := sessionSegmentsReady(s); n != 0 {
		t.Errorf("expected 0 segments in empty dir, got %d", n)
	}
}

func TestSessionSegmentsReady_NonEmpty(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "seg_00000.ts"), []byte("data"), 0644)
	os.WriteFile(filepath.Join(dir, "seg_00001.ts"), []byte("data"), 0644)
	os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte("#EXTM3U"), 0644)
	s := &HLSSession{Dir: dir}
	if n := sessionSegmentsReady(s); n != 2 {
		t.Errorf("expected 2 segments, got %d", n)
	}
}

func TestSessionSegmentsReady_EmptyDirField(t *testing.T) {
	s := &HLSSession{}
	if n := sessionSegmentsReady(s); n != 0 {
		t.Errorf("expected 0 for empty Dir, got %d", n)
	}
}

func TestReadSeekerContentReadAt(t *testing.T) {
	data := []byte("hello world")
	r := &readSeekerContent{ReadSeeker: strings.NewReader(string(data))}
	buf := make([]byte, 5)
	n, err := r.readAt(buf, 6)
	if err != nil {
		t.Fatalf("readAt: %v", err)
	}
	if n != 5 || string(buf) != "world" {
		t.Errorf("readAt(6,5) = %q (n=%d), want 'world'", string(buf[:n]), n)
	}
}

func TestReadSeekerContentSize(t *testing.T) {
	r := &readSeekerContent{ReadSeeker: strings.NewReader("hello world")}
	sz, err := r.size()
	if err != nil {
		t.Fatalf("size: %v", err)
	}
	if sz != 11 {
		t.Errorf("expected size 11, got %d", sz)
	}
}

func TestAppendSnapshotIfActive_Closed(t *testing.T) {
	s := &HLSSession{closed: true}
	result := appendSnapshotIfActive(nil, "test", s)
	if len(result) != 0 {
		t.Errorf("expected empty for closed session, got %d", len(result))
	}
}

func TestAppendSnapshotIfActive_Open(t *testing.T) {
	s := &HLSSession{
		Key: "test", Dir: t.TempDir(),
		StartedAt: time.Now(), LastAccess: time.Now(),
		spec: &encodeSpec{encoder: "libx264"},
	}
	result := appendSnapshotIfActive(nil, "test", s)
	if len(result) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(result))
	}
	if result[0].Key != "test" {
		t.Errorf("snapshot Key: want 'test', got %q", result[0].Key)
	}
}

func TestSessions_NilManager(t *testing.T) {
	var m *HLSSessionManager
	if s := m.Sessions(); s != nil {
		t.Errorf("expected nil from nil manager")
	}
}

func TestHLSPeek_NotFound(t *testing.T) {
	m, err := NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	if _, err := m.Peek("nonexistent"); err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestHLSStartRejectsMissingCaps(t *testing.T) {
	m, err := NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}

	// Without caps cached, GetOrStart should fail
	src := strings.NewReader("test")
	_, err = m.GetOrStart(context.Background(), HLSStartOpts{
		Key:        "no-caps",
		Source:     src,
		SourceSize: 4,
	})
	if err == nil {
		t.Fatal("expected error when caps not probed")
	}
}

func TestEnsureSegment_NonVOD(t *testing.T) {
	s := &HLSSession{}
	s.EnsureSegment(42) // should not panic on nil spec
}

func TestEnsureSegment_VODNoRestart(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "seg_00050.ts"), []byte("data"), 0644)
	s := &HLSSession{
		spec: &encodeSpec{vod: true},
		Dir:  dir,
	}
	// startSeg=0, requested idx=5 which is >0 and within forward seek threshold
	// EnsureSegment check: idx < start (5 < 0=false) AND idx > highestSeg()+30
	// highestSeg()=50, 50+30=80, 5<80=true, so neither branch fires => no restart
	s.EnsureSegment(5)
}

func TestEnsureSegment_BackwardSeek(t *testing.T) {
	s := &HLSSession{
		startSeg: 50,
		spec:     &encodeSpec{vod: true},
		Dir:      t.TempDir(),
		restartMu: sync.Mutex{},
	}
	// We need to set the internal startSeg to test backward seek.
	// Since startSeg is a private field, we test the logic indirectly.
	// idx=10 < start=50 => should trigger RestartAt
	s.EnsureSegment(10)
	// restartMu doesn't have RestartAt lock contention because no goroutine,
	// but the public test is more of a no-panic assertion.
}

func TestLogWriter(t *testing.T) {
	w := newLogWriter("test: ")
	n, err := w.Write([]byte("hello\nworld\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 12 {
		t.Errorf("expected 12 bytes written, got %d", n)
	}
}

func TestWebServerResponse(t *testing.T) {
	mgr, err := NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
}

func TestWaitForMaster_SessionEnded(t *testing.T) {
	dir := t.TempDir()
	s := &HLSSession{Dir: dir, closed: true}
	err := s.WaitForMaster(100 * time.Millisecond)
	if err == nil {
		t.Fatal("expected error for closed session without playlist")
	}
}

func TestWaitForSegment_RejectsPathTraversal(t *testing.T) {
	s := &HLSSession{Dir: t.TempDir()}
	_, err := s.WaitForSegment("../../etc/passwd", time.Millisecond)
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestWaitForSegment_SessionEnded(t *testing.T) {
	dir := t.TempDir()
	s := &HLSSession{Dir: dir, closed: true}
	_, err := s.WaitForSegment("seg_00000.ts", 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected error for closed session without segment")
	}
}

func TestServeWholeFile_HeadRequest(t *testing.T) {
	src := &readSeekerContent{ReadSeeker: strings.NewReader("data")}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("HEAD", "/source", nil)
	serveWholeFile(w, r, src, 4)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestServeRangeFile_HeadRequest(t *testing.T) {
	src := &readSeekerContent{ReadSeeker: strings.NewReader("data")}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("HEAD", "/source", nil)
	serveRangeFile(w, r, src, 4, 1, 3)
	if w.Code != 206 {
		t.Errorf("expected 206, got %d", w.Code)
	}
	if w.Header().Get("Content-Length") != "3" {
		t.Errorf("Content-Length: want 3, got %s", w.Header().Get("Content-Length"))
	}
}

func TestHighestSeg_Empty(t *testing.T) {
	s := &HLSSession{Dir: t.TempDir()}
	if n := s.highestSeg(); n != -1 {
		t.Errorf("expected -1 for empty dir, got %d", n)
	}
}

func TestHighestSeg_WithFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "seg_00005.ts"), []byte("d"), 0644)
	os.WriteFile(filepath.Join(dir, "seg_00042.ts"), []byte("d"), 0644)
	s := &HLSSession{Dir: dir}
	if n := s.highestSeg(); n != 42 {
		t.Errorf("expected 42, got %d", n)
	}
}

func TestNewLogWriter(t *testing.T) {
	w := newLogWriter("test: ")
	if w == nil {
		t.Fatal("expected non-nil writer")
	}
}

func TestResolveContainer(t *testing.T) {
	if got := resolveContainer(""); got != "mp4" {
		t.Errorf("empty container: want mp4, got %q", got)
	}
	if got := resolveContainer("webm"); got != "webm" {
		t.Errorf("webm container: want webm, got %q", got)
	}
}

func TestResolvePreferredEncoder(t *testing.T) {
	caps := &Capabilities{Preferred: "libx264", PreferredHE: "libx265"}
	if got := resolvePreferredEncoder(caps, ""); got != "libx264" {
		t.Errorf("empty codec: want libx264, got %q", got)
	}
	if got := resolvePreferredEncoder(caps, "h264"); got != "libx264" {
		t.Errorf("h264: want libx264, got %q", got)
	}
	if got := resolvePreferredEncoder(caps, "hevc"); got != "libx265" {
		t.Errorf("hevc: want libx265, got %q", got)
	}
}

func TestLastLine(t *testing.T) {
	if got := lastLine("foo\nbar\nbaz"); got != "baz" {
		t.Errorf("expected 'baz', got %q", got)
	}
	if got := lastLine("single"); got != "single" {
		t.Errorf("expected 'single', got %q", got)
	}
	if got := lastLine(""); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestLastLines(t *testing.T) {
	if got := lastLines("a\nb\nc\nd\ne\n", 3); got != "c\nd\ne" {
		t.Errorf("expected 'c\\nd\\ne', got %q", got)
	}
	if got := lastLines("only", 3); got != "only" {
		t.Errorf("expected 'only', got %q", got)
	}
}

// Compile-time assertions for io.ReadSeeker interface.
var _ io.ReadSeeker = (*strings.Reader)(nil)
