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

// makeMultiAudioMP4 renders a short clip with video + 2 audio streams (skips if
// ffmpeg is absent). Streams: 0=video, 1=audio(440Hz), 2=audio(880Hz).
func makeMultiAudioMP4(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "multi_audio.mp4")
	cmd := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=640x360:rate=10",
		"-f", "lavfi", "-i", "sine=f=440:duration=1",
		"-f", "lavfi", "-i", "sine=f=880:duration=1",
		"-map", "0:v", "-map", "1:a", "-map", "2:a",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac",
		out,
	)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg multi-audio fixture-gen failed: %v: %s", err, combined)
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
		t.Skipf("multi-audio fixture not generated: %v", err)
	}
	return out
}

func ffprobeStreamCount(t *testing.T, seg, codecType string) int {
	t.Helper()
	out, err := exec.Command("ffprobe",
		"-v", "error", "-select_streams", codecType[:1],
		"-show_entries", "stream=codec_type", "-of", "csv=p=0", seg,
	).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", seg, err)
	}
	n := 0
	for _, l := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(l) == codecType {
			n++
		}
	}
	return n
}

// TestHLSAudioOnlyRenditionE2E is the M2b gate (Phase 6b): an AudioOnly session
// mapping the 2nd track (stream 2) of a multi-audio source must produce .ts
// segments with ONLY audio — zero video — proving the EXT-X-MEDIA TYPE=AUDIO
// rendition end-to-end through ffmpeg.
func TestHLSAudioOnlyRenditionE2E(t *testing.T) {
	installFastCapsForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	fixture := makeMultiAudioMP4(t)
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
		Key:        "e2e-ao2",
		Source:     f,
		SourceSize: fi.Size(),
		AudioOnly:  true,
		AudioTrack: 2, // 2nd audio track (absolute stream 2 = 880Hz)
	})
	if err != nil {
		t.Fatalf("GetOrStart: %v", err)
	}
	defer mgr.Close(sess.Key)

	seg, err := sess.WaitForSegment("seg_00000.ts", 60*time.Second)
	if err != nil {
		t.Fatalf("audio-only segment never produced: %v", err)
	}
	// Video == 0 is the key assertion (audio-only). Audio is ≥1: an MPEG-TS
	// segment reports the same stream more than once (PAT/PMT), so requiring
	// == 1 would produce false negatives.
	if v := ffprobeStreamCount(t, seg, "video"); v != 0 {
		t.Errorf("audio rendition segment has %d video streams, want 0 (should be audio-only)", v)
	}
	if a := ffprobeStreamCount(t, seg, "audio"); a < 1 {
		t.Errorf("rendition segment has %d audio streams, want ≥1", a)
	}
}
