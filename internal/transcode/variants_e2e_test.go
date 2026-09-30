package transcode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// make1080pMP4 renders a short 1920x1080 fixture (skips if ffmpeg is absent).
func make1080pMP4(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "src_1080p.mp4")
	cmd := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=1920x1080:rate=10",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		out,
	)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg 1080p fixture-gen failed: %v: %s", err, combined)
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
		t.Skipf("1080p fixture not generated: %v", err)
	}
	return out
}

func ffprobeSegHeight(t *testing.T, seg string) string {
	t.Helper()
	out, err := exec.Command("ffprobe",
		"-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=height", "-of", "csv=p=0", seg,
	).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", seg, err)
	}
	// An MPEG-TS segment may report the height on more than one line (PAT/PMT);
	// the first non-empty line is the video stream's height.
	for _, line := range strings.Split(string(out), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			return s
		}
	}
	return ""
}

// TestHLSVariantDownscaleE2E is the M2a E2E (CA-2.3, encode side): a session
// with the 720p rung, fed by a REAL 1080p source, must produce .ts segments at
// 720p — proving that per-variant videoScaleFilterH works end-to-end through
// ffmpeg (not just in the args). The master's structure (≥2 STREAM-INF) is
// covered by the buildMasterPlaylist unit tests.
func TestHLSVariantDownscaleE2E(t *testing.T) {
	installFastCapsForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	fixture := make1080pMP4(t)
	f, err := os.Open(fixture)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	fi, _ := f.Stat()

	mgr, err := NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}

	sess, err := mgr.GetOrStart(ctx, HLSStartOpts{
		Key:        "e2e-v720",
		Source:     f,
		SourceSize: fi.Size(),
		Variant:    mkVariant(1280, 720, 720), // 720p rung of the ladder
	})
	if err != nil {
		t.Fatalf("GetOrStart: %v", err)
	}
	defer mgr.Close(sess.Key)

	seg, err := sess.WaitForSegment("seg_00000.ts", 60*time.Second)
	if err != nil {
		t.Fatalf("720p segment never produced: %v", err)
	}
	if h := ffprobeSegHeight(t, seg); h != "720" {
		t.Errorf("variant segment height = %q, want 720 (1080→720 downscale failed)", h)
	}
}

// Note: the "default session (no Variant) keeps the 1080 cap" guard lives in the
// TestEncodeSpecVariantArgs unit test (args assertion, no ffmpeg cost) — this
// file keeps only the per-variant downscale E2E so the suite doesn't accumulate
// concurrent transcode load (the internal/handlers package already runs real
// ffmpeg and lived close to the timeout).
