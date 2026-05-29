package transcode

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// installFastCapsForTest bypasses the slow per-encoder smoke probe (each
// candidate gets 15s; on Mac without NVENC/QSV/VAAPI most fail by timeout
// totalling ~90s). We hand-build a minimal Capabilities with libx264 as the
// preferred encoder so the HLS session can start. Restores the previous
// cached value at the end of the test.
func installFastCapsForTest(t *testing.T) {
	t.Helper()
	ffPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skipf("ffmpeg not in PATH: %v", err)
	}
	stub := &Capabilities{
		FFmpegPath: ffPath,
		Encoders: []Encoder{
			{ID: "libx264", Codec: "h264", Backend: "cpu", Available: true, Functional: true},
		},
		Preferred: "libx264",
	}
	cacheMu.Lock()
	prev := cached
	cached = stub
	cacheMu.Unlock()
	t.Cleanup(func() {
		cacheMu.Lock()
		cached = prev
		cacheMu.Unlock()
	})
}

// makeSmallMoovAtEndMP4 produces a fast, small MP4 with moov-at-end (libx264
// default, no -movflags +faststart). Small enough that test runs in ~5s but
// big enough that mdat is non-trivial. Skips test if ffmpeg unavailable.
func makeSmallMoovAtEndMP4(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	out := filepath.Join(tmpDir, "moov_end.mp4")
	cmd := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=3:size=320x240:rate=10",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		out,
	)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg fixture-gen failed: %v: %s", err, combined)
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
		t.Skipf("fixture not generated: %v", err)
	}
	return out
}

// TestHLSPipelineUsesHTTPInputNotPipe is the architectural guard. The HLS
// transcoder previously consumed the source via stdin pipe (non-seekable),
// which broke MP4 sources with moov-at-end because ffmpeg can't walk past a
// multi-GB mdat box without seeking. The fix exposes the source over a
// loopback HTTP server so ffmpeg can issue Range requests. This test asserts
// ffmpeg is launched with an `http://` input URL — if anyone regresses to
// `pipe:0` (e.g. by reverting the source server), this fails immediately.
func TestHLSPipelineUsesHTTPInputNotPipe(t *testing.T) {
	installFastCapsForTest(t)

	mgr, err := NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}

	// Tiny in-memory source — fixture content irrelevant, we're checking the
	// args ffmpeg got. Session starts ffmpeg synchronously; we inspect args
	// before it has a chance to fail on bad input.
	sess, err := mgr.GetOrStart(context.Background(), HLSStartOpts{
		Key:        "args-only",
		Source:     bytes.NewReader([]byte("not a real video")),
		SourceSize: 16,
	})
	if err != nil {
		t.Fatalf("GetOrStart: %v", err)
	}
	defer mgr.Close(sess.Key)

	args := sess.Cmd.Args
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-i http://127.0.0.1:") {
		t.Errorf("expected ffmpeg input to be a loopback HTTP URL, got args:\n%s", joined)
	}
	if strings.Contains(joined, "pipe:0") {
		t.Errorf("regression — ffmpeg input fell back to pipe:0, args:\n%s", joined)
	}
}

// TestHLSPipelineProducesPlaylistForMP4 is the end-to-end smoke test: feed
// a real MP4 fixture through the pipeline and verify the master playlist
// and the first segment land on disk. Validates that the HTTP+Range source
// server, ffmpeg's HLS muxer, and our WaitForMaster/WaitForSegment polling
// all work together.
func TestHLSPipelineProducesPlaylistForMP4(t *testing.T) {
	installFastCapsForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fixture := makeSmallMoovAtEndMP4(t)
	f, err := os.Open(fixture)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		t.Fatalf("stat fixture: %v", err)
	}

	mgr, err := NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}

	sess, err := mgr.GetOrStart(ctx, HLSStartOpts{
		Key:        "smoke-mp4",
		Source:     f,
		SourceSize: fi.Size(),
	})
	if err != nil {
		t.Fatalf("GetOrStart: %v", err)
	}
	defer mgr.Close(sess.Key)

	if err := sess.WaitForMaster(30 * time.Second); err != nil {
		t.Fatalf("master playlist never produced: %v", err)
	}
	if _, err := sess.WaitForSegment("seg_00000.ts", 10*time.Second); err != nil {
		t.Fatalf("first segment never produced: %v", err)
	}
}

// make4KMoovAtEndMP4 produces a 3840×2160 H.264 MP4 source. Used to
// reproduce the production "Invalid Level" failure where the pipeline
// hardcoded -level:v 4.0, which only permits up to 1920×1080@30. Anything
// larger makes nvenc (and libx264 in strict mode) refuse to initialise.
// Keep duration short — testsrc renders ~1s in <3s wall-clock with ultrafast.
func make4KMoovAtEndMP4(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	out := filepath.Join(tmpDir, "src_4k.mp4")
	cmd := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=3840x2160:rate=10",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		out,
	)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg 4K fixture-gen failed: %v: %s", err, combined)
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
		t.Skipf("4K fixture not generated: %v", err)
	}
	return out
}

// TestHLSPipelineHandles4KSource is the regression guard for the production
// failure with 2160p HEVC torrents. Before the fix, hls.go appended
// `-level:v 4.0` unconditionally; ffmpeg refused to initialise the encoder
// with "Invalid Level" for any source taller than 1080p, and the session
// died ~26s later with exit status 1. After the fix, the pipeline must
// either omit `-level:v` (letting the encoder choose) or pick a level
// matching the source dimensions, and produce a playlist normally.
func TestHLSPipelineHandles4KSource(t *testing.T) {
	installFastCapsForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	fixture := make4KMoovAtEndMP4(t)
	f, err := os.Open(fixture)
	if err != nil {
		t.Fatalf("open 4K fixture: %v", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		t.Fatalf("stat 4K fixture: %v", err)
	}

	mgr, err := NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}

	sess, err := mgr.GetOrStart(ctx, HLSStartOpts{
		Key:        "smoke-4k",
		Source:     f,
		SourceSize: fi.Size(),
	})
	if err != nil {
		t.Fatalf("GetOrStart: %v", err)
	}
	defer mgr.Close(sess.Key)

	// 60s timeout — 4K encode is slow on CPU (libx264 ultrafast). If we don't
	// see a playlist in 60s the pipeline likely died from "Invalid Level"
	// (the bug we're guarding against).
	if err := sess.WaitForMaster(60 * time.Second); err != nil {
		t.Fatalf("master playlist never produced for 4K source — likely Invalid Level: %v", err)
	}
}

// TestHLSPipelineDoesNotHardcodeRestrictiveLevel asserts at the argv level
// that the pipeline does NOT pass `-level:v 4.0` (or any explicit level
// below 5.1). This is a fast architectural guard — it catches regressions
// in milliseconds without needing a 4K fixture. If someone re-adds a
// hardcoded low level, this test fails immediately.
func TestHLSPipelineDoesNotHardcodeRestrictiveLevel(t *testing.T) {
	installFastCapsForTest(t)

	mgr, err := NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}

	sess, err := mgr.GetOrStart(context.Background(), HLSStartOpts{
		Key:        "level-guard",
		Source:     bytes.NewReader([]byte("not a real video")),
		SourceSize: 16,
	})
	if err != nil {
		t.Fatalf("GetOrStart: %v", err)
	}
	defer mgr.Close(sess.Key)

	args := sess.Cmd.Args
	for i, a := range args {
		if a == "-level:v" && i+1 < len(args) {
			lvl := args[i+1]
			// Forbid anything below 5.1 — 4K@30 needs L5.1, 4K@60 needs L5.2.
			// Allowing "auto" or omitting entirely is fine.
			switch lvl {
			case "4.0", "4.1", "4.2", "5.0":
				t.Errorf("ffmpeg invoked with -level:v %s — too restrictive for ≥1440p sources, will fail with 'Invalid Level' on 4K. Use -level:v 5.2 or omit.", lvl)
			}
		}
	}
}

// TestHLSPipelineUsesEventNotVodPlaylist guards the killer bug: with
// `-hls_playlist_type vod` ffmpeg DEFERS writing index.m3u8 until the whole
// transcode finishes. For a movie streamed over a torrent that never happens
// in time, so the playlist never appears and WaitForMaster times out despite
// hundreds of .ts segments on disk. `event` writes the playlist incrementally
// (and is still seekable over the transcoded buffer). This test asserts we use
// `event` and NOT `vod`.
func TestHLSPipelineUsesEventNotVodPlaylist(t *testing.T) {
	installFastCapsForTest(t)

	mgr, err := NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}

	sess, err := mgr.GetOrStart(context.Background(), HLSStartOpts{
		Key:        "playlist-type-guard",
		Source:     bytes.NewReader([]byte("not a real video")),
		SourceSize: 16,
	})
	if err != nil {
		t.Fatalf("GetOrStart: %v", err)
	}
	defer mgr.Close(sess.Key)

	joined := strings.Join(sess.Cmd.Args, " ")
	if !strings.Contains(joined, "-hls_playlist_type event") {
		t.Errorf("ffmpeg must use -hls_playlist_type event (incremental m3u8).\nargs: %s", joined)
	}
	if strings.Contains(joined, "-hls_playlist_type vod") {
		t.Errorf("regression — vod defers m3u8 to end-of-transcode, playlist never appears for streamed movies.\nargs: %s", joined)
	}
}

// TestHLSStartRejectsNilSource ensures we don't regress to the previous
// API that accepted nil/plain io.Reader (which would let stdin-piping creep
// back in by accident).
func TestHLSStartRejectsNilSource(t *testing.T) {
	mgr, err := NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	if _, err := mgr.GetOrStart(context.Background(), HLSStartOpts{Key: "no-source"}); err == nil {
		t.Fatal("expected error for nil Source, got nil")
	}
}

// TestProbeDurationSeekableReadsMoovAtEnd is the decisive guard for #61: the
// finite VOD playlist (full seekbar) hinges on knowing the total duration, and
// for MP4 with moov-at-end that duration is ONLY readable when ffprobe can seek
// to the END of the file. This test feeds a real moov-at-end MP4 through the
// production serveSource (Range-capable) and asserts probeDurationSeekable
// recovers ~3s. Without the seekable path (e.g. a truncated pipe), ffprobe
// returns no duration — the regression this test exists to catch.
func TestProbeDurationSeekableReadsMoovAtEnd(t *testing.T) {
	fixture := makeSmallMoovAtEndMP4(t) // testsrc duration=3, no faststart
	f, err := os.Open(fixture)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		t.Fatalf("stat fixture: %v", err)
	}

	url, shutdown := startSourceServerForTest(t, f, fi.Size())
	defer shutdown()

	dur := probeDurationSeekable(context.Background(), "ffmpeg", url)
	if dur < 2.5 || dur > 3.5 {
		t.Fatalf("expected duration ~3s for moov-at-end MP4 via seekable source, got %.3f", dur)
	}
}

// TestEncodeSpecArgsVODForcesKeyframes guards the #61 invariant: in VOD mode
// the encoder must force a keyframe every hlsSegDur seconds so segment N maps
// to media time [N*hlsSegDur, …). Without this, segments land on arbitrary
// keyframes and seek-restart can't align to a boundary.
func TestEncodeSpecArgsVODForcesKeyframes(t *testing.T) {
	spec := &encodeSpec{dir: "/tmp/x", inputURL: "http://127.0.0.1:1/source", encoder: "libx264", ffmpegPath: "ffmpeg", vod: true}
	joined := strings.Join(spec.args(0), " ")
	if !strings.Contains(joined, "-force_key_frames expr:gte(t,n_forced*4)") {
		t.Errorf("VOD args must force 4s keyframes; got:\n%s", joined)
	}
	if !strings.Contains(joined, "-start_number 0") {
		t.Errorf("expected -start_number 0 for initial launch; got:\n%s", joined)
	}
	// Initial launch (startSeg 0) must NOT input-seek nor offset, but DOES
	// zero the source start (setpts/asetpts) so a non-zero source PTS doesn't
	// leave a [0, offset] hole that stalls Safari.
	if strings.Contains(joined, "-ss ") || strings.Contains(joined, "-output_ts_offset") {
		t.Errorf("startSeg 0 must not seek/offset; got:\n%s", joined)
	}
	if !strings.Contains(joined, "setpts=PTS-STARTPTS") || !strings.Contains(joined, "asetpts=PTS-STARTPTS") {
		t.Errorf("VOD must zero timestamps (setpts/asetpts); got:\n%s", joined)
	}
}

// TestEncodeSpecArgsRestartSeeksWithOffset guards the seek-restart timestamp
// alignment: a non-zero start segment must input-seek (-ss), zero the PTS at
// the seek point (setpts/asetpts) and shift it to the segment's slot
// (-output_ts_offset) so seg N's PTS starts at N*hlsSegDur, matching the
// synthesised VOD playlist. start_number must equal the seek segment so
// filenames line up.
func TestEncodeSpecArgsRestartSeeksWithOffset(t *testing.T) {
	spec := &encodeSpec{dir: "/tmp/x", inputURL: "http://127.0.0.1:1/source", encoder: "libx264", ffmpegPath: "ffmpeg", vod: true}
	joined := strings.Join(spec.args(100), " ")
	// Restart: input-seek + zero the PTS + shift to the segment's slot.
	for _, want := range []string{"-ss 400", "setpts=PTS-STARTPTS", "-output_ts_offset 400", "-start_number 100"} {
		if !strings.Contains(joined, want) {
			t.Errorf("restart args missing %q; got:\n%s", want, joined)
		}
	}
}

// TestEncodeSpecArgsNvencForcesIDR guards the GTX 1070 fix: h264_nvenc ignores
// -force_key_frames unless -forced-idr 1 is set, which made segments come out
// ~10s instead of 4s. libx264 honours it natively so must NOT get the flag.
func TestEncodeSpecArgsNvencForcesIDR(t *testing.T) {
	nv := &encodeSpec{dir: "/tmp/x", inputURL: "http://127.0.0.1:1/source", encoder: "h264_nvenc", ffmpegPath: "ffmpeg", vod: true}
	if !strings.Contains(strings.Join(nv.args(0), " "), "-forced-idr 1") {
		t.Error("h264_nvenc VOD args must set -forced-idr 1")
	}
	x264 := &encodeSpec{dir: "/tmp/x", inputURL: "http://127.0.0.1:1/source", encoder: "libx264", ffmpegPath: "ffmpeg", vod: true}
	if strings.Contains(strings.Join(x264.args(0), " "), "-forced-idr") {
		t.Error("libx264 honours -force_key_frames natively; must not set -forced-idr")
	}
}

// TestEncodeSpecArgsEventUnchanged ensures the non-VOD path keeps the proven
// EVENT flags (-g 60, no forced keyframes, no setpts) so a duration-unknown
// source behaves EXACTLY as before #61 — none of the VOD timestamp surgery
// must leak into the stable live path.
func TestEncodeSpecArgsEventUnchanged(t *testing.T) {
	spec := &encodeSpec{dir: "/tmp/x", inputURL: "http://127.0.0.1:1/source", encoder: "libx264", ffmpegPath: "ffmpeg", vod: false}
	joined := strings.Join(spec.args(0), " ")
	if !strings.Contains(joined, "-g 60") {
		t.Errorf("EVENT mode must keep -g 60; got:\n%s", joined)
	}
	for _, forbidden := range []string{"-force_key_frames", "setpts", "-output_ts_offset", "-forced-idr"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("EVENT mode must not contain %q (VOD-only); got:\n%s", forbidden, joined)
		}
	}
}

func TestParseSegName(t *testing.T) {
	cases := map[string]struct {
		n  int
		ok bool
	}{
		"seg_00000.ts":     {0, true},
		"seg_00042.ts":     {42, true},
		"index.m3u8":       {0, false},
		"seg_00007.ts.tmp": {0, false},
		"foo.ts":           {0, false},
	}
	for name, want := range cases {
		n, ok := parseSegName(name)
		if ok != want.ok || (ok && n != want.n) {
			t.Errorf("parseSegName(%q) = (%d,%v), want (%d,%v)", name, n, ok, want.n, want.ok)
		}
	}
}

func TestFfprobePathFrom(t *testing.T) {
	cases := map[string]string{
		"/usr/bin/ffmpeg":      "/usr/bin/ffprobe",
		"ffmpeg":               "ffprobe",
		"/usr/local/bin/ffmpeg": "/usr/local/bin/ffprobe",
		"/custom/path/ff":      "ffprobe",
		"":                     "ffprobe",
	}
	for input, want := range cases {
		got := ffprobePathFrom(input)
		if got != want {
			t.Errorf("ffprobePathFrom(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseRange(t *testing.T) {
	cases := []struct {
		header    string
		total     int64
		wStart    int64
		wEnd      int64
		wantOk    bool
	}{
		{"bytes=0-99", 1000, 0, 99, true},
		{"bytes=100-", 1000, 100, 999, true},
		{"bytes=0-", 500, 0, 499, true},
		{"bytes=200-299", 1000, 200, 299, true},
		{"", 1000, 0, 0, false},
		{"invalid", 1000, 0, 0, false},
		{"bytes=abc-def", 1000, 0, 0, false},
		{"bytes=-100", 1000, 0, 0, false},
		{"bytes=0-99,200-299", 1000, 0, 0, false},
		{"bytes=2000-", 1000, 0, 0, false},
		{"bytes=0-2000", 1000, 0, 999, true},
		{"bytes=500-400", 1000, 0, 0, false},
		{"BYTES=0-99", 1000, 0, 0, false},
	}
	for _, tc := range cases {
		start, end, ok := parseRange(tc.header, tc.total)
		if ok != tc.wantOk || (ok && (start != tc.wStart || end != tc.wEnd)) {
			t.Errorf("parseRange(%q, %d) = (%d,%d,%v), want (%d,%d,%v)",
				tc.header, tc.total, start, end, ok, tc.wStart, tc.wEnd, tc.wantOk)
		}
	}
}

func TestSessionPid(t *testing.T) {
	s := &HLSSession{}
	if pid := sessionPid(s); pid != 0 {
		t.Errorf("expected 0 pid for unstarted session, got %d", pid)
	}
}

func TestSessionEncoder(t *testing.T) {
	s := &HLSSession{}
	if enc := sessionEncoder(s); enc != "cpu" {
		t.Errorf("expected 'cpu' for nil spec, got %q", enc)
	}
	s.spec = &encodeSpec{encoder: "h264_nvenc"}
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

func TestSessionSegmentsReady_NoDir(t *testing.T) {
	s := &HLSSession{}
	if n := sessionSegmentsReady(s); n != 0 {
		t.Errorf("expected 0 segments for empty session, got %d", n)
	}
}

func TestAppendSnapshotIfActive_Closed(t *testing.T) {
	s := &HLSSession{closed: true}
	snaps := appendSnapshotIfActive(nil, "test-key", s)
	if len(snaps) != 0 {
		t.Errorf("expected no snapshots for closed session, got %d", len(snaps))
	}
}

func TestAppendSnapshotIfActive_Open(t *testing.T) {
	s := &HLSSession{
		Key:    "test",
		Dir:    t.TempDir(),
		closed: false,
	}
	snaps := appendSnapshotIfActive(nil, "test-key", s)
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}
	if snaps[0].Key != "test-key" {
		t.Errorf("key mismatch: %q", snaps[0].Key)
	}
}

func TestSessions_NilManager(t *testing.T) {
	var m *HLSSessionManager
	if s := m.Sessions(); s != nil {
		t.Errorf("expected nil sessions from nil manager, got %v", s)
	}
}

func TestPeek_NotFound(t *testing.T) {
	mgr, err := NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	_, err = mgr.Peek("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestClose_NotFound(t *testing.T) {
	mgr, err := NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	mgr.Close("nonexistent")
	// Should not panic
}

func TestNewLogWriter(t *testing.T) {
	lw := newLogWriter("test: ")
	if lw == nil {
		t.Fatal("expected non-nil log writer")
	}
	lw.Write([]byte("hello\nworld\n"))
	if len(lw.buf) != 0 {
		t.Errorf("expected buffer to be empty after newline flush, got %q", lw.buf)
	}
}

func TestLogWriter_PartialLine(t *testing.T) {
	lw := newLogWriter("test: ")
	lw.Write([]byte("hello "))
	if string(lw.buf) != "hello " {
		t.Errorf("expected buffered 'hello ', got %q", lw.buf)
	}
}

func TestLogWriter_EmptyLine(t *testing.T) {
	lw := newLogWriter("test: ")
	n, err := lw.Write([]byte("\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 byte written, got %d", n)
	}
}

func TestReadSeekerContentSize(t *testing.T) {
	data := []byte("hello world")
	r := &readSeekerContent{ReadSeeker: bytes.NewReader(data)}
	sz, err := r.size()
	if err != nil {
		t.Fatalf("size: %v", err)
	}
	if sz != int64(len(data)) {
		t.Errorf("expected size %d, got %d", len(data), sz)
	}
}

func TestHLSSession_IsVOD(t *testing.T) {
	s := &HLSSession{}
	if s.IsVOD() {
		t.Error("expected IsVOD to be false for nil spec")
	}
	s.spec = &encodeSpec{}
	if s.IsVOD() {
		t.Error("expected IsVOD to be false for non-VOD spec")
	}
	s.spec = &encodeSpec{vod: true}
	if !s.IsVOD() {
		t.Error("expected IsVOD to be true for VOD spec")
	}
}

func TestHLSSession_HighestSeg_NoDir(t *testing.T) {
	s := &HLSSession{}
	if n := s.highestSeg(); n != -1 {
		t.Errorf("expected -1 for no dir, got %d", n)
	}
}

func TestHLSSession_HighestSeg_EmptyDir(t *testing.T) {
	s := &HLSSession{Dir: t.TempDir()}
	if n := s.highestSeg(); n != -1 {
		t.Errorf("expected -1 for empty dir, got %d", n)
	}
}

func TestParseSegIndex(t *testing.T) {
	n, ok := ParseSegIndex("seg_00042.ts")
	if !ok || n != 42 {
		t.Errorf("ParseSegIndex = (%d, %v), want (42, true)", n, ok)
	}
	_, ok = ParseSegIndex("index.m3u8")
	if ok {
		t.Error("ParseSegIndex should be false for m3u8")
	}
}

func TestProbeDurationSeekable_NoFFprobe(t *testing.T) {
	dur := probeDurationSeekable(context.Background(), "/nonexistent/ffmpeg", "http://127.0.0.1:1/source")
	if dur != 0 {
		t.Errorf("expected 0 for nonexistent ffmpeg, got %f", dur)
	}
}

// Compile-time check that *bytes.Reader satisfies io.ReadSeeker — guards the
// HLSStartOpts.Source field type.
var _ io.ReadSeeker = (*bytes.Reader)(nil)

// slowReadSeeker wraps an io.ReadSeeker and sleeps between Seek and Read to
// widen the race window that exists when concurrent callers interleave on a
// stateful single-cursor reader. Used to deterministically reproduce the
// "STSC and STCO contradictionary" bug we hit in production: anacrolix's
// torrent.Reader is single-cursor, ServeContent does Seek+Read sequentially,
// and concurrent Range handlers (ffmpeg with -multiple_requests 1) read from
// each other's offsets.
type slowReadSeeker struct {
	io.ReadSeeker
	seekDelay time.Duration
}

func (s *slowReadSeeker) Seek(offset int64, whence int) (int64, error) {
	pos, err := s.ReadSeeker.Seek(offset, whence)
	if s.seekDelay > 0 {
		time.Sleep(s.seekDelay)
	}
	return pos, err
}

// patternBytes generates a deterministic byte sequence keyed by absolute
// offset: byte at position N == byte(N % 251). 251 is prime so the pattern
// doesn't align with common power-of-2 buffer sizes; if a handler reads from
// the wrong offset, the returned bytes won't match the requested offset's
// expected pattern, even if the wrong-offset bytes themselves were valid.
func patternBytes(start, length int64) []byte {
	out := make([]byte, length)
	for i := range out {
		out[i] = byte((start + int64(i)) % 251)
	}
	return out
}

// fetchRange does a single HTTP Range GET against the loopback source server
// and returns the body bytes plus the response status. Used by the concurrency
// test to assert each handler receives bytes from the requested offset.
func fetchRange(url string, start, end int64) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return body, resp.StatusCode, err
}

// startSourceServerForTest spins up the same loopback HTTP source server the
// HLS pipeline uses, exercising exactly the production handler (serveSource)
// so the concurrency test stays meaningful as the code evolves.
func startSourceServerForTest(t *testing.T, source io.ReadSeeker, size int64) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	wrapped := &readSeekerContent{ReadSeeker: source}
	mux := http.NewServeMux()
	mux.HandleFunc("/source", func(w http.ResponseWriter, r *http.Request) {
		serveSource(w, r, wrapped, size)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(listener) }()
	return "http://" + listener.Addr().String() + "/source", func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

// TestSourceServerHonoursOffsetUnderConcurrentRanges is the regression guard
// for the "STSC and STCO contradictionary" production failure. We don't need
// ffmpeg to reproduce it — the failure mode is purely at the HTTP source
// layer. We fire two concurrent Range GETs against very different offsets
// while the underlying ReadSeeker has a forced delay between Seek() and Read()
// to widen the race. If the source server fails to serialise Seek+Read as one
// atomic operation per request, one of the responses will contain bytes from
// the OTHER request's offset — STSC/STCO would parse plausibly but wrong.
//
// Before the fix this fails ~100% with seekDelay=10ms.
// After the fix it passes deterministically.
func TestSourceServerHonoursOffsetUnderConcurrentRanges(t *testing.T) {
	const total = 8 << 20 // 8 MiB — large enough that the two ranges can't overlap
	full := patternBytes(0, total)
	src := &slowReadSeeker{ReadSeeker: bytes.NewReader(full), seekDelay: 10 * time.Millisecond}

	url, shutdown := startSourceServerForTest(t, src, total)
	defer shutdown()

	// Range A: first 64 KiB. Range B: 64 KiB starting at 4 MiB. Disjoint and
	// from very different regions so wrong-offset bytes are unmistakable.
	const chunk int64 = 64 << 10
	type req struct {
		start, end int64
		name       string
	}
	reqs := []req{
		{start: 0, end: chunk - 1, name: "A-head"},
		{start: 4 << 20, end: (4 << 20) + chunk - 1, name: "B-mid"},
	}

	var wg sync.WaitGroup
	results := make(map[string][]byte, len(reqs))
	statuses := make(map[string]int, len(reqs))
	errs := make(map[string]error, len(reqs))
	var mu sync.Mutex

	for _, r := range reqs {
		wg.Add(1)
		go func(r req) {
			defer wg.Done()
			body, status, err := fetchRange(url, r.start, r.end)
			mu.Lock()
			results[r.name] = body
			statuses[r.name] = status
			errs[r.name] = err
			mu.Unlock()
		}(r)
	}
	wg.Wait()

	for _, r := range reqs {
		if err := errs[r.name]; err != nil {
			t.Fatalf("%s fetch: %v", r.name, err)
		}
		if statuses[r.name] != http.StatusPartialContent {
			t.Fatalf("%s status: want 206, got %d", r.name, statuses[r.name])
		}
		expected := patternBytes(r.start, r.end-r.start+1)
		got := results[r.name]
		if len(got) != len(expected) {
			t.Fatalf("%s length: want %d, got %d", r.name, len(expected), len(got))
		}
		// Find the FIRST mismatching byte so the failure message points at
		// exactly where the wrong-offset data starts.
		for i := range expected {
			if got[i] != expected[i] {
				t.Fatalf("%s wrong bytes at idx %d (abs offset %d): want=%d got=%d — handler read from a different offset (race)",
					r.name, i, r.start+int64(i), expected[i], got[i])
			}
		}
	}
}

func TestPeek_NilManager(t *testing.T) {
	var m *HLSSessionManager
	_, err := m.Peek("key")
	if err == nil {
		t.Error("expected error for nil manager")
	}
}
