package metrics

import (
	"context"
	"testing"

	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// collect must populate every gauge from a single ActiveList sampling pass —
// the old GlobalStats() follow-up call re-sampled within <250ms of buildInfo's
// sample and permanently reported 0 b/s for the network gauges.
func TestCollectUpdatesGauges(t *testing.T) {
	s := streamer.NewForTesting()

	collect(s, nil)

	if got := testutil.ToFloat64(ActiveTorrents); got != 0 {
		t.Errorf("ActiveTorrents = %v, want 0 for an empty streamer", got)
	}
	if got := testutil.ToFloat64(TotalPeers); got != 0 {
		t.Errorf("TotalPeers = %v, want 0", got)
	}
	if got := testutil.ToFloat64(GlobalDownloadRate); got != 0 {
		t.Errorf("GlobalDownloadRate = %v, want 0", got)
	}
	if got := testutil.ToFloat64(GlobalUploadRate); got != 0 {
		t.Errorf("GlobalUploadRate = %v, want 0", got)
	}
	if got := testutil.ToFloat64(ActiveTranscodeSessions); got != 0 {
		t.Errorf("ActiveTranscodeSessions = %v, want 0 with a nil HLS manager", got)
	}
}

// StartWorker must be a no-op on a nil streamer and stoppable via context.
func TestStartWorkerNilAndCancel(t *testing.T) {
	StartWorker(context.Background(), nil, nil) // must not panic nor spawn

	ctx, cancel := context.WithCancel(context.Background())
	done := StartWorker(ctx, streamer.NewForTesting(), nil)
	cancel()
	<-done // goroutine observes ctx.Done and exits
}
