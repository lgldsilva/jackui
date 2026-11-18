package streamer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/metrics/events"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func mediaProbeFailures(reason string) float64 {
	return testutil.ToFloat64(events.FFprobeFailures.WithLabelValues(events.ProbeMedia, reason))
}

// Each ffprobe failure class on the media probe path is counted: a ctx
// deadline (the starved-torrent hang), an exit/exec error and unparsable output.
func TestFFprobeMetrics_FailureClasses(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	before := mediaProbeFailures(events.ProbeReasonTimeout)
	if _, err := runFFprobe(ctx, "/dev/null", nil); err == nil {
		t.Fatal("expired ctx must fail the probe")
	}
	if got := mediaProbeFailures(events.ProbeReasonTimeout) - before; got != 1 {
		t.Errorf("timeout delta = %v, want 1", got)
	}

	before = mediaProbeFailures(events.ProbeReasonError)
	_, _ = runFFprobe(context.Background(), filepath.Join(t.TempDir(), "missing.mkv"), nil)
	if got := mediaProbeFailures(events.ProbeReasonError) - before; got != 1 {
		t.Errorf("error delta = %v, want 1", got)
	}

	before = mediaProbeFailures(events.ProbeReasonParse)
	if _, err := parseProbeOutput([]byte("not json")); err == nil {
		t.Fatal("garbage must not parse")
	}
	if got := mediaProbeFailures(events.ProbeReasonParse) - before; got != 1 {
		t.Errorf("parse delta = %v, want 1", got)
	}

	// A caller-cancelled probe (client went away) is not an ffprobe failure.
	cctx, ccancel := context.WithCancel(context.Background())
	ccancel()
	total := mediaProbeFailures(events.ProbeReasonTimeout) + mediaProbeFailures(events.ProbeReasonError)
	_, _ = runFFprobe(cctx, "/dev/null", nil)
	if after := mediaProbeFailures(events.ProbeReasonTimeout) + mediaProbeFailures(events.ProbeReasonError); after != total {
		t.Error("a cancelled probe must not be counted as a failure")
	}
}

// fakeFFprobe puts an `ffprobe` on PATH that prints stdout and exits non-zero
// (ffprobe does that for damaged files while still emitting usable JSON).
func fakeFFprobe(t *testing.T, stdout string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\ncat <<'EOF'\n" + stdout + "\nEOF\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "ffprobe"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func totalMediaProbeFailures() float64 {
	return mediaProbeFailures(events.ProbeReasonTimeout) + mediaProbeFailures(events.ProbeReasonError) +
		mediaProbeFailures(events.ProbeReasonParse) + mediaProbeFailures(events.ProbeReasonNoDuration)
}

// A probe that exits non-zero AND returns unparsable output is ONE failure
// (parse), not an `error` plus a `parse`.
func TestFFprobeMetrics_NonZeroExitWithGarbageCountsOnce(t *testing.T) {
	fakeFFprobe(t, "not json")
	before := totalMediaProbeFailures()
	if _, err := ProbeLocal(context.Background(), "/dev/null"); err == nil {
		t.Fatal("garbage output must fail the probe")
	}
	if got := totalMediaProbeFailures() - before; got != 1 {
		t.Errorf("failure delta = %v, want exactly 1", got)
	}
}

// A non-zero exit whose output is still valid JSON keeps being returned and
// parsed (ffprobe does this for damaged files), and stays ONE `error` event.
func TestFFprobeMetrics_NonZeroExitWithValidJSONStaysOneEvent(t *testing.T) {
	fakeFFprobe(t, `{"streams":[],"format":{"duration":"1.0"}}`)
	before := totalMediaProbeFailures()
	if _, err := ProbeLocal(context.Background(), "/dev/null"); err != nil {
		t.Fatalf("valid JSON output must still parse: %v", err)
	}
	if got := totalMediaProbeFailures() - before; got != 1 {
		t.Errorf("failure delta = %v, want 1", got)
	}
}
