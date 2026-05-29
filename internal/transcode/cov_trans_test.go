package transcode

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// transSwapCache replaces the package-level capability cache for the duration of
// a test and restores the previous value on cleanup. It mirrors the locking
// discipline used by TestCached_Nil in capabilities_test.go.
func transSwapCache(t *testing.T, c *Capabilities) {
	t.Helper()
	cacheMu.Lock()
	prev := cached
	cached = c
	cacheMu.Unlock()
	t.Cleanup(func() {
		cacheMu.Lock()
		cached = prev
		cacheMu.Unlock()
	})
}

// ─── prewarmReader ────────────────────────────────────────────────────────────

func Test_trans_prewarmReader_EmptyReturnsNil(t *testing.T) {
	if got := prewarmReader(bytes.NewReader(nil)); got != nil {
		t.Errorf("empty reader should yield nil, got %v", got)
	}
}

func Test_trans_prewarmReader_SmallInputPreservesBytes(t *testing.T) {
	data := []byte("hello world payload")
	r := prewarmReader(bytes.NewReader(data))
	if r == nil {
		t.Fatal("non-empty reader should not yield nil")
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("roundtrip mismatch: got %q want %q", got, data)
	}
}

func Test_trans_prewarmReader_LargeInputPreservesAllBytes(t *testing.T) {
	// Larger than the 256 KiB prewarm window to exercise the io.MultiReader tail.
	data := bytes.Repeat([]byte("A"), 300*1024)
	r := prewarmReader(bytes.NewReader(data))
	if r == nil {
		t.Fatal("non-empty reader should not yield nil")
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != len(data) {
		t.Errorf("expected %d bytes back, got %d", len(data), len(got))
	}
}

// ─── Run ─────────────────────────────────────────────────────────────────────

func Test_trans_Run_NoCapabilities(t *testing.T) {
	transSwapCache(t, nil)
	w := httptest.NewRecorder()
	err := Run(context.Background(), bytes.NewReader([]byte("data")), w, Options{})
	if err == nil {
		t.Fatal("expected error when capabilities not probed")
	}
	if !strings.Contains(err.Error(), "not probed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func Test_trans_Run_EmptySource(t *testing.T) {
	transSwapCache(t, &Capabilities{
		FFmpegPath: "/usr/bin/ffmpeg",
		Preferred:  "libx264",
	})
	w := httptest.NewRecorder()
	// Empty reader → prewarm yields nil → Run reports "no data" before invoking ffmpeg.
	err := Run(context.Background(), bytes.NewReader(nil), w, Options{})
	if err == nil {
		t.Fatal("expected error for empty source")
	}
	if !strings.Contains(err.Error(), "no data") {
		t.Errorf("unexpected error: %v", err)
	}
}

func Test_trans_Run_FFmpegInvocationFails(t *testing.T) {
	// A bogus ffmpeg path makes cmd.Run() fail deterministically, exercising the
	// stderr-tail failure branch without depending on a real ffmpeg binary.
	transSwapCache(t, &Capabilities{
		FFmpegPath: "/nonexistent/ffmpeg-binary-that-does-not-exist",
		Preferred:  "libx264",
	})
	w := httptest.NewRecorder()
	err := Run(context.Background(), bytes.NewReader([]byte("some payload bytes")), w, Options{VideoCodec: "h264", AudioTrack: -1})
	if err == nil {
		t.Fatal("expected error when ffmpeg binary is missing")
	}
	if !strings.Contains(err.Error(), "ffmpeg failed") {
		t.Errorf("expected 'ffmpeg failed' error, got: %v", err)
	}
	// The Content-Type header is set before the (failing) invocation.
	if ct := w.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("expected video/mp4 content-type, got %q", ct)
	}
}

// ─── encoderPresetArgs (uncovered backend branches) ────────────────────────────

func Test_trans_encoderPresetArgs_AllBackends(t *testing.T) {
	cases := []struct {
		encoder string
		want    string // a token that must appear; "" means expect nil
	}{
		{"h264_nvenc", "p4"},
		{"hevc_nvenc", "p4"},
		{"h264_vaapi", "-compression_level"},
		{"hevc_vaapi", "-compression_level"},
		{"h264_qsv", "medium"},
		{"hevc_qsv", "medium"},
		{"libx264", "veryfast"},
		{"libx265", "veryfast"},
		{"h264_videotoolbox", ""},
		{"weird_unknown", ""},
	}
	for _, tc := range cases {
		args := encoderPresetArgs(tc.encoder)
		if tc.want == "" {
			if args != nil {
				t.Errorf("%s: expected nil, got %v", tc.encoder, args)
			}
			continue
		}
		if !strings.Contains(strings.Join(args, " "), tc.want) {
			t.Errorf("%s: expected token %q in %v", tc.encoder, tc.want, args)
		}
	}
}

// ─── pickPreferred (unknown-backend fallback branch) ───────────────────────────

func Test_trans_pickPreferred_UnknownBackendUsesMidPriority(t *testing.T) {
	// An encoder with an unmapped backend gets priority 50; CPU is 99. So the
	// unknown backend should win over a functional CPU encoder.
	encs := []Encoder{
		{ID: "libx264", Codec: "h264", Backend: backendCPU, Functional: true},
		{ID: "h264_mystery", Codec: "h264", Backend: "some-future-gpu", Functional: true},
	}
	if got := pickPreferred(encs, "h264"); got != "h264_mystery" {
		t.Errorf("expected unknown-backend encoder to win over cpu, got %q", got)
	}
}

// ─── ffmpeg-dependent probe helpers ───────────────────────────────────────────
// These exercise the real exec paths when ffmpeg is installed; otherwise they
// skip cleanly so the suite stays green on hosts without ffmpeg.

func Test_trans_findFFmpegPath(t *testing.T) {
	p := findFFmpegPath()
	if p == "" {
		t.Skip("ffmpeg not in PATH; skipping exec-dependent assertion")
	}
	if !strings.Contains(p, "ffmpeg") {
		t.Errorf("path should reference ffmpeg, got %q", p)
	}
}

func Test_trans_readFFmpegVersion(t *testing.T) {
	ff := findFFmpegPath()
	if ff == "" {
		t.Skip("ffmpeg not in PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ver := readFFmpegVersion(ctx, ff)
	if ver == "" {
		t.Error("expected a non-empty ffmpeg version line")
	}
}

func Test_trans_listEncoders_And_Decoders(t *testing.T) {
	ff := findFFmpegPath()
	if ff == "" {
		t.Skip("ffmpeg not in PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	encs := listEncoders(ctx, ff)
	decs := listDecoders(ctx, ff)
	if len(encs) == 0 {
		t.Error("expected at least one compiled-in encoder")
	}
	if len(decs) == 0 {
		t.Error("expected at least one compiled-in decoder")
	}
	// libx264 / h264 are near-universal in ffmpeg builds.
	if !encs["libx264"] {
		t.Log("note: libx264 not listed (unusual build) — not asserting")
	}
}

func Test_trans_smokeTestEncoder_Libx264(t *testing.T) {
	ff := findFFmpegPath()
	if ff == "" {
		t.Skip("ffmpeg not in PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ok, fps, err := smokeTestEncoder(ctx, ff, "libx264")
	if err != nil {
		t.Skipf("libx264 smoke test failed in this environment: %v", err)
	}
	if !ok {
		t.Error("libx264 should be functional")
	}
	if fps <= 0 {
		t.Errorf("expected positive fps, got %v", fps)
	}
}

func Test_trans_smokeTestEncoder_BogusEncoderErrors(t *testing.T) {
	ff := findFFmpegPath()
	if ff == "" {
		t.Skip("ffmpeg not in PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ok, _, err := smokeTestEncoder(ctx, ff, "definitely_not_a_real_encoder")
	if ok || err == nil {
		t.Errorf("expected failure for bogus encoder, got ok=%v err=%v", ok, err)
	}
}

func Test_trans_Probe_PopulatesCapsWhenFFmpegPresent(t *testing.T) {
	if findFFmpegPath() == "" {
		t.Skip("ffmpeg not in PATH")
	}
	// Isolate the package cache so we always force a fresh probe and restore after.
	transSwapCache(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	caps, err := Probe(ctx, true)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if caps.FFmpegPath == "" {
		t.Error("expected ffmpeg path to be set")
	}
	if len(caps.Encoders) == 0 {
		t.Error("expected encoder candidates to be evaluated")
	}
	// A second non-forced call must hit the cache and return a value.
	caps2, err := Probe(ctx, false)
	if err != nil || caps2 == nil {
		t.Fatalf("cached Probe: caps2=%v err=%v", caps2, err)
	}
	if Cached() == nil {
		t.Error("Cached() should be populated after a probe")
	}
}
