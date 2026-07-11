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
	// Um segmento MPEG-TS pode reportar a altura em mais de uma linha (PAT/PMT);
	// a primeira linha não-vazia é a altura do stream de vídeo.
	for _, line := range strings.Split(string(out), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			return s
		}
	}
	return ""
}

// TestHLSVariantDownscaleE2E é o E2E do M2a (CA-2.3, lado do encode): uma sessão
// com a rung de 720p, alimentada por uma fonte 1080p REAL, deve produzir
// segmentos .ts a 720p — provando que videoScaleFilterH por variante funciona
// ponta-a-ponta pelo ffmpeg (não só nos args). A estrutura do master (≥2
// STREAM-INF) é coberta pelos unit tests de buildMasterPlaylist.
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
		Variant:    mkVariant(720), // rung 720p do ladder
	})
	if err != nil {
		t.Fatalf("GetOrStart: %v", err)
	}
	defer mgr.Close(sess.Key)

	seg, err := sess.WaitForSegment("seg_00000.ts", 60*time.Second)
	if err != nil {
		t.Fatalf("segmento 720p nunca produzido: %v", err)
	}
	if h := ffprobeSegHeight(t, seg); h != "720" {
		t.Errorf("altura do segmento da variante = %q, want 720 (downscale 1080→720 falhou)", h)
	}
}

// Nota: o guard "sessão default (sem Variant) mantém cap 1080" fica no unit
// TestEncodeSpecVariantArgs (assertiva de args, sem custo de ffmpeg) — este
// arquivo mantém só o E2E do downscale por variante pra não somar carga de
// transcode concorrente ao suite (o pacote internal/handlers já roda ffmpeg real
// e vivia perto do timeout).
