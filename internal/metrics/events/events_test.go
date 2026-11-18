package events

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// Out-of-set label values fold into "other" so a caller bug can never create
// unbounded series.
func TestBoundedLabelsFoldUnknownValues(t *testing.T) {
	before := testutil.ToFloat64(HLSSessionFailures.WithLabelValues(other))
	HLSSessionFailed("ffmpeg exited: /data/streams/Some.Movie.mkv")
	if got := testutil.ToFloat64(HLSSessionFailures.WithLabelValues(other)) - before; got != 1 {
		t.Errorf("unknown reason → other delta = %v, want 1", got)
	}
	HLSAdmissionEvent("nope")
	DownloadCompleted("nope")
	DownloadMoveFailed("nope")
	FFprobeFailed("nope", "nope")
	for name, v := range map[string]float64{
		"admission": testutil.ToFloat64(HLSAdmission.WithLabelValues(other)),
		"completed": testutil.ToFloat64(DownloadCompletions.WithLabelValues(other)),
		"move":      testutil.ToFloat64(DownloadMoveFailures.WithLabelValues(other)),
		"ffprobe":   testutil.ToFloat64(FFprobeFailures.WithLabelValues(other, other)),
	} {
		if v < 1 {
			t.Errorf("%s: unknown label not folded into other", name)
		}
	}
}

func TestSeriesPreInitialized(t *testing.T) {
	// 2 modes x 2 decodes, 4 failure reasons, 2 admission events, 2x4 probe.
	for name, want := range map[string]int{
		"started":   4,
		"failures":  4,
		"admission": 2,
		"ffprobe":   8,
	} {
		var got int
		switch name {
		case "started":
			got = testutil.CollectAndCount(HLSSessionsStarted)
		case "failures":
			got = testutil.CollectAndCount(HLSSessionFailures)
		case "admission":
			got = testutil.CollectAndCount(HLSAdmission)
		case "ffprobe":
			got = testutil.CollectAndCount(FFprobeFailures)
		}
		if got < want {
			t.Errorf("%s: %d series, want >= %d pre-initialized", name, got, want)
		}
	}
}

func TestHLSSessionStartedLabels(t *testing.T) {
	before := testutil.ToFloat64(HLSSessionsStarted.WithLabelValues("event", "sw"))
	HLSSessionStarted(false, true)
	if got := testutil.ToFloat64(HLSSessionsStarted.WithLabelValues("event", "sw")) - before; got != 1 {
		t.Errorf("event/sw delta = %v, want 1", got)
	}
}

func TestFFprobeExecFailedClassifiesContext(t *testing.T) {
	read := func(r string) float64 { return testutil.ToFloat64(FFprobeFailures.WithLabelValues(ProbeMedia, r)) }
	timeout, errs := read(ProbeReasonTimeout), read(ProbeReasonError)

	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	FFprobeExecFailed(expired, ProbeMedia)
	FFprobeExecFailed(context.Background(), ProbeMedia)
	cancelled, cancel2 := context.WithCancel(context.Background())
	cancel2()
	FFprobeExecFailed(cancelled, ProbeMedia)

	if got := read(ProbeReasonTimeout) - timeout; got != 1 {
		t.Errorf("timeout delta = %v, want 1", got)
	}
	if got := read(ProbeReasonError) - errs; got != 1 {
		t.Errorf("error delta = %v, want 1 (cancelled must not count)", got)
	}
}

func TestDownloadRecorders(t *testing.T) {
	stalls, pauses := testutil.ToFloat64(DownloadStalls), testutil.ToFloat64(DownloadStallPauses)
	retry := testutil.ToFloat64(DownloadInitFailures.WithLabelValues("retry"))
	DownloadStalled(false)
	DownloadInitFailed(false)
	if testutil.ToFloat64(DownloadStalls)-stalls != 1 || testutil.ToFloat64(DownloadStallPauses) != pauses {
		t.Error("a non-pausing stall must bump stalls only")
	}
	if testutil.ToFloat64(DownloadInitFailures.WithLabelValues("retry"))-retry != 1 {
		t.Error("transient init failure must count as retry")
	}
}
