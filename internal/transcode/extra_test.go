package transcode

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLastLine(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"hello\nworld\n", "world"},
		{"single line", "single line"},
		{"", ""},
		{"line1\nline2\nline3", "line3"},
	}
	for _, tc := range cases {
		got := lastLine(tc.input)
		if got != tc.want {
			t.Errorf("lastLine(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestLastLineTruncatesLong(t *testing.T) {
	long := strings.Repeat("x", 300)
	got := lastLine(long)
	if len(got) > 200 {
		t.Errorf("expected truncated to <=200, got %d", len(got))
	}
}

func TestLastLines(t *testing.T) {
	cases := []struct {
		input string
		n     int
		want  string
	}{
		{"a\nb\nc\nd\ne", 3, "c\nd\ne"},
		{"a\nb", 5, "a\nb"},
		{"", 3, ""},
		{"only", 1, "only"},
	}
	for _, tc := range cases {
		got := lastLines(tc.input, tc.n)
		if got != tc.want {
			t.Errorf("lastLines(%q, %d) = %q, want %q", tc.input, tc.n, got, tc.want)
		}
	}
}

func TestResolveContainer(t *testing.T) {
	if got := resolveContainer(""); got != "mp4" {
		t.Errorf("empty: got %q", got)
	}
	if got := resolveContainer("matroska"); got != "matroska" {
		t.Errorf("matroska: got %q", got)
	}
}

func TestResolvePreferredEncoder(t *testing.T) {
	caps := &Capabilities{Preferred: "libx264", PreferredHE: "libx265"}
	if got := resolvePreferredEncoder(caps, "hevc"); got != "libx265" {
		t.Errorf("hevc: got %q", got)
	}
	if got := resolvePreferredEncoder(caps, "h264"); got != "libx264" {
		t.Errorf("h264: got %q", got)
	}
	if got := resolvePreferredEncoder(caps, ""); got != "libx264" {
		t.Errorf("empty: got %q", got)
	}
}

func TestAppendVideoCodecArgs(t *testing.T) {
	caps := &Capabilities{Preferred: "libx264", PreferredHE: "libx265"}

	t.Run("copy", func(t *testing.T) {
		args := appendVideoCodecArgs(nil, caps, "libx264", "")
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-c:v copy") {
			t.Errorf("expected copy, got %s", joined)
		}
	})

	t.Run("h264", func(t *testing.T) {
		args := appendVideoCodecArgs(nil, caps, "libx264", "h264")
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-c:v libx264") {
			t.Errorf("expected libx264, got %s", joined)
		}
		if !strings.Contains(joined, "-pix_fmt yuv420p") {
			t.Errorf("expected pix_fmt, got %s", joined)
		}
	})

	t.Run("hevc", func(t *testing.T) {
		args := appendVideoCodecArgs(nil, caps, "libx265", "hevc")
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-c:v libx265") {
			t.Errorf("expected libx265, got %s", joined)
		}
	})

	t.Run("custom", func(t *testing.T) {
		args := appendVideoCodecArgs(nil, caps, "", "h264_videotoolbox")
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-c:v h264_videotoolbox") {
			t.Errorf("expected custom codec, got %s", joined)
		}
	})
}

func TestAppendAudioCodecArgs(t *testing.T) {
	t.Run("empty_mp4_defaults_to_aac", func(t *testing.T) {
		args := appendAudioCodecArgs(nil, "", "mp4")
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-c:a aac") {
			t.Errorf("expected aac for mp4, got %s", joined)
		}
	})

	t.Run("empty_non_mp4_passthrough", func(t *testing.T) {
		args := appendAudioCodecArgs(nil, "", "matroska")
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-c:a copy") {
			t.Errorf("expected copy for matroska, got %s", joined)
		}
	})

	t.Run("aac_explicit", func(t *testing.T) {
		args := appendAudioCodecArgs(nil, "aac", "mp4")
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-b:a 192k") {
			t.Errorf("expected bitrate, got %s", joined)
		}
	})

	t.Run("custom_audio_codec", func(t *testing.T) {
		args := appendAudioCodecArgs(nil, "libopus", "webm")
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-c:a libopus") {
			t.Errorf("expected libopus, got %s", joined)
		}
	})
}

func TestAppendContainerArgs(t *testing.T) {
	t.Run("mp4", func(t *testing.T) {
		args := appendContainerArgs(nil, "mp4")
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-f mp4") {
			t.Errorf("expected mp4, got %s", joined)
		}
		if !strings.Contains(joined, "+frag_keyframe") {
			t.Errorf("expected frag_keyframe, got %s", joined)
		}
	})

	t.Run("matroska", func(t *testing.T) {
		args := appendContainerArgs(nil, "matroska")
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-f matroska") {
			t.Errorf("expected matroska, got %s", joined)
		}
	})
}

func TestSessionSegmentsReadyWithSegments(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "seg_00000.ts"), []byte("data"), 0644)
	os.WriteFile(filepath.Join(dir, "seg_00001.ts"), []byte("data"), 0644)
	os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte("#EXTM3U"), 0644)
	s := &HLSSession{Dir: dir}
	if n := sessionSegmentsReady(s); n != 2 {
		t.Errorf("expected 2, got %d", n)
	}
}

func TestServeWholeFile(t *testing.T) {
	data := []byte("hello world this is a test file")
	src := &readSeekerContent{ReadSeeker: bytes.NewReader(data)}
	r := httptest.NewRequest("GET", "/source", nil)
	w := httptest.NewRecorder()

	serveWholeFile(w, r, src, int64(len(data)))

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != string(data) {
		t.Errorf("body mismatch: got %q", w.Body.String())
	}
}

func TestServeWholeFileHead(t *testing.T) {
	src := &readSeekerContent{ReadSeeker: bytes.NewReader([]byte("test"))}
	r := httptest.NewRequest("HEAD", "/source", nil)
	w := httptest.NewRecorder()

	serveWholeFile(w, r, src, 4)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("expected empty body for HEAD, got %d bytes", w.Body.Len())
	}
}

func TestServeRangeFile(t *testing.T) {
	data := []byte("0123456789")
	src := &readSeekerContent{ReadSeeker: bytes.NewReader(data)}
	r := httptest.NewRequest("GET", "/source", nil)
	w := httptest.NewRecorder()

	serveRangeFile(w, r, src, 10, 2, 5)

	if w.Code != http.StatusPartialContent {
		t.Errorf("expected 206, got %d", w.Code)
	}
	if w.Body.String() != "2345" {
		t.Errorf("body: got %q, want '2345'", w.Body.String())
	}
}

func TestServeRangeFileHead(t *testing.T) {
	src := &readSeekerContent{ReadSeeker: bytes.NewReader([]byte("test"))}
	r := httptest.NewRequest("HEAD", "/source", nil)
	w := httptest.NewRecorder()

	serveRangeFile(w, r, src, 4, 0, 3)

	if w.Code != http.StatusPartialContent {
		t.Errorf("expected 206, got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("expected empty body for HEAD, got %d bytes", w.Body.Len())
	}
}

func TestServeSourceNoRange(t *testing.T) {
	data := []byte("full content")
	src := &readSeekerContent{ReadSeeker: bytes.NewReader(data)}
	r := httptest.NewRequest("GET", "/source", nil)
	w := httptest.NewRecorder()

	serveSource(w, r, src, int64(len(data)))

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != string(data) {
		t.Errorf("body: want %q, got %q", data, w.Body.String())
	}
}

func TestServeSourceWithRange(t *testing.T) {
	data := []byte("0123456789")
	src := &readSeekerContent{ReadSeeker: bytes.NewReader(data)}
	r := httptest.NewRequest("GET", "/source", nil)
	r.Header.Set("Range", "bytes=2-5")
	w := httptest.NewRecorder()

	serveSource(w, r, src, 10)

	if w.Code != http.StatusPartialContent {
		t.Errorf("expected 206, got %d", w.Code)
	}
	if w.Body.String() != "2345" {
		t.Errorf("body: want '2345', got %q", w.Body.String())
	}
}

func TestServeSourceInvalidRange(t *testing.T) {
	src := &readSeekerContent{ReadSeeker: bytes.NewReader([]byte("test"))}
	r := httptest.NewRequest("GET", "/source", nil)
	r.Header.Set("Range", "bytes=abc-")
	w := httptest.NewRecorder()

	serveSource(w, r, src, 4)

	if w.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Errorf("expected 416, got %d", w.Code)
	}
}

func TestEnsureSegmentNonVOD(t *testing.T) {
	s := &HLSSession{spec: nil}
	s.EnsureSegment(5)
}

func TestEnsureSegmentNoRestartIfWithinRange(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "seg_00003.ts"), []byte("data"), 0644)
	s := &HLSSession{
		spec:     &encodeSpec{vod: true},
		Dir:      dir,
		startSeg: 0,
	}
	s.EnsureSegment(3)
}

func TestHighestSeg(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "seg_00003.ts"), []byte("data"), 0644)
	os.WriteFile(filepath.Join(dir, "seg_00007.ts"), []byte("data"), 0644)
	os.WriteFile(filepath.Join(dir, "seg_00001.ts"), []byte("data"), 0644)
	s := &HLSSession{Dir: dir}
	if hi := s.highestSeg(); hi != 7 {
		t.Errorf("expected 7, got %d", hi)
	}
}

func TestHighestSegEmpty(t *testing.T) {
	dir := t.TempDir()
	s := &HLSSession{Dir: dir}
	if hi := s.highestSeg(); hi != -1 {
		t.Errorf("expected -1, got %d", hi)
	}
}

func TestEncodeSpecArgs(t *testing.T) {
	dir := t.TempDir()
	e := &encodeSpec{
		dir:        dir,
		inputURL:   "http://127.0.0.1:9999/source",
		encoder:    "libx264",
		ffmpegPath: "/usr/bin/ffmpeg",
		vod:        true,
	}
	args := e.args(0)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-i http://") {
		t.Errorf("expected http input, got %s", joined)
	}
	if !strings.Contains(joined, "-force_key_frames") {
		t.Errorf("expected force_key_frames for VOD, got %s", joined)
	}
}

func TestEncodeSpecArgsNonVOD(t *testing.T) {
	e := &encodeSpec{
		dir:      t.TempDir(),
		inputURL: "http://127.0.0.1:9999/source",
		encoder:  "libx264",
		vod:      false,
	}
	args := e.args(0)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "-force_key_frames") {
		t.Error("non-VOD should not have force_key_frames")
	}
	if !strings.Contains(joined, "-g 60") {
		t.Errorf("expected -g 60 for non-VOD, got %s", joined)
	}
}

func TestEncodeSpecArgsVODWithStartSeg(t *testing.T) {
	e := &encodeSpec{
		dir:        t.TempDir(),
		inputURL:   "http://127.0.0.1:9999/source",
		encoder:    "libx264",
		ffmpegPath: "/usr/bin/ffmpeg",
		vod:        true,
	}
	args := e.args(5)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-ss 20") {
		t.Errorf("expected -ss 20 for startSeg=5, got %s", joined)
	}
	if !strings.Contains(joined, "-output_ts_offset 20") {
		t.Errorf("expected -output_ts_offset 20, got %s", joined)
	}
}

func TestBuildTranscodeArgs(t *testing.T) {
	caps := &Capabilities{Preferred: "libx264", PreferredHE: "libx265", FFmpegPath: "/usr/bin/ffmpeg"}
	args := buildTranscodeArgs(caps, "libx264", "mp4", Options{VideoCodec: "h264", AudioTrack: -1})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-c:v libx264") {
		t.Errorf("expected video codec, got %s", joined)
	}
	if !strings.Contains(joined, "-map 0:a:0?") {
		t.Errorf("expected audio conditional map, got %s", joined)
	}
}

func TestBuildTranscodeArgsWithSubBurn(t *testing.T) {
	caps := &Capabilities{Preferred: "libx264"}
	args := buildTranscodeArgs(caps, "libx264", "mp4", Options{SubBurnTrack: 2})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-filter_complex") {
		t.Errorf("expected filter_complex for sub burn, got %s", joined)
	}
}

func TestBuildTranscodeArgsWithHWDecode(t *testing.T) {
	caps := &Capabilities{Preferred: "h264_nvenc"}
	// AudioTrack/SubBurnTrack -1 = "none" (what the real handlers pass); SubBurnTrack
	// 0 would wrongly select the CPU subtitle-burn overlay path instead of HW decode.
	args := buildTranscodeArgs(caps, "h264_nvenc", "mp4", Options{VideoCodec: "h264", SourceVCodec: "hevc", AudioTrack: -1, SubBurnTrack: -1})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-hwaccel cuda") {
		t.Errorf("expected hwaccel, got %s", joined)
	}
	// NVENC downloads to sysmem + software scale/8-bit (scale_cuda's format= is
	// missing on the container's ffmpeg 4.4.2). So: -hwaccel cuda but NOT
	// -hwaccel_output_format cuda, and an sw scale+format filter.
	if strings.Contains(joined, "-hwaccel_output_format cuda") {
		t.Errorf("nvenc must let frames download to sysmem on old ffmpeg, got %s", joined)
	}
	if !strings.Contains(joined, "format=yuv420p") {
		t.Errorf("expected software scale+8-bit filter for nvenc, got %s", joined)
	}
}

func TestMime(t *testing.T) {
	if m := containerMime("mp4"); m != "video/mp4" {
		t.Fatalf("mp4: %s", m)
	}
	if m := containerMime("matroska"); m != "video/x-matroska" {
		t.Fatalf("mkv: %s", m)
	}
	if m := containerMime("webm"); m != "video/webm" {
		t.Fatalf("webm: %s", m)
	}
	if m := containerMime("unknown"); m != "application/octet-stream" {
		t.Fatalf("unknown: %s", m)
	}
}

func TestReadSeekerContentSeekRestores(t *testing.T) {
	r := &readSeekerContent{ReadSeeker: bytes.NewReader([]byte("abcdef"))}
	sz, err := r.size()
	if err != nil {
		t.Fatalf("size: %v", err)
	}
	if sz != 6 {
		t.Errorf("expected 6, got %d", sz)
	}
	buf := make([]byte, 3)
	n, err := r.Read(buf)
	if err != nil || n != 3 || string(buf) != "abc" {
		t.Errorf("after size(): Read = (%d, %v, %q), want (3, nil, 'abc')", n, err, buf)
	}
}

func TestReadSeekerContentReadAt(t *testing.T) {
	r := &readSeekerContent{ReadSeeker: bytes.NewReader([]byte("0123456789"))}
	buf := make([]byte, 4)
	n, err := r.readAt(buf, 3)
	if err != nil || n != 4 || string(buf) != "3456" {
		t.Errorf("readAt(3): got (%d, %v, %q)", n, err, buf)
	}
}

func TestNewHLSManagerInvalidDir(t *testing.T) {
	mgr, err := NewHLSManager("/nonexistent/deep/path/we/cannot/create")
	if err == nil && mgr != nil {
		mgr.Close("test")
	}
}
