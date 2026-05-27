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
