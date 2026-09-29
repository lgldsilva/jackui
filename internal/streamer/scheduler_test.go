package streamer

import (
	"context"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/config"
)

func TestInTimeRange(t *testing.T) {
	tests := []struct {
		name     string
		now      time.Time
		rangeStr string
		expected bool
	}{
		{
			name:     "happy path inside the daytime range",
			now:      time.Date(2026, 6, 9, 10, 30, 0, 0, time.Local),
			rangeStr: "08:00-18:00",
			expected: true,
		},
		{
			name:     "happy path outside the daytime range",
			now:      time.Date(2026, 6, 9, 19, 0, 0, 0, time.Local),
			rangeStr: "08:00-18:00",
			expected: false,
		},
		{
			name:     "inside the range that crosses midnight",
			now:      time.Date(2026, 6, 9, 23, 30, 0, 0, time.Local),
			rangeStr: "22:00-06:00",
			expected: true,
		},
		{
			name:     "inside the range that crosses midnight (early morning of the next day)",
			now:      time.Date(2026, 6, 9, 3, 15, 0, 0, time.Local),
			rangeStr: "22:00-06:00",
			expected: true,
		},
		{
			name:     "outside the range that crosses midnight",
			now:      time.Date(2026, 6, 9, 12, 0, 0, 0, time.Local),
			rangeStr: "22:00-06:00",
			expected: false,
		},
		{
			name:     "invalid range format",
			now:      time.Date(2026, 6, 9, 10, 30, 0, 0, time.Local),
			rangeStr: "08:00_18:00",
			expected: false,
		},
		{
			name:     "invalid hours at the start",
			now:      time.Date(2026, 6, 9, 10, 30, 0, 0, time.Local),
			rangeStr: "25:00-18:00",
			expected: false,
		},
		{
			name:     "invalid minutes at the end",
			now:      time.Date(2026, 6, 9, 10, 30, 0, 0, time.Local),
			rangeStr: "08:00-18:65",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InTimeRange(tt.now, tt.rangeStr)
			if got != tt.expected {
				t.Errorf("InTimeRange(%v, %q) = %v; want %v", tt.now, tt.rangeStr, got, tt.expected)
			}
		})
	}
}

// applyBandwidthSchedule pushes the resolved limits only when they CHANGE: the
// first call (dummy -2/-2) applies, an identical target is a no-op, and a
// matching schedule window overrides the defaults.
func TestApplyBandwidthSchedule_AppliesOnlyOnChange(t *testing.T) {
	s := NewForTesting()
	cfg := &config.Config{}
	cfg.Stream.MaxDownloadRate = 1000
	cfg.Stream.MaxUploadRate = 200
	cfg.Stream.BandwidthSchedules = []config.BandwidthSchedule{{TimeRange: "08:00-18:00", MaxDownloadRate: 50, MaxUploadRate: 10}}

	night := time.Date(2026, 6, 9, 23, 0, 0, 0, time.Local)
	down, up := applyBandwidthSchedule(s, cfg, night, -2, -2)
	if down != 1000 || up != 200 {
		t.Fatalf("defaults applied = %d/%d, want 1000/200", down, up)
	}
	if s.cfg.MaxDownloadRate != 1000 || s.cfg.MaxUploadRate != 200 {
		t.Errorf("streamer limits = %d/%d, want 1000/200", s.cfg.MaxDownloadRate, s.cfg.MaxUploadRate)
	}

	// Same target → unchanged, SetRateLimits not re-applied.
	s.cfg.MaxDownloadRate = 0
	if d, u := applyBandwidthSchedule(s, cfg, night, 1000, 200); d != 1000 || u != 200 || s.cfg.MaxDownloadRate != 0 {
		t.Errorf("unchanged target must be a no-op: got %d/%d, streamer=%d", d, u, s.cfg.MaxDownloadRate)
	}

	day := time.Date(2026, 6, 9, 12, 0, 0, 0, time.Local)
	if d, u := applyBandwidthSchedule(s, cfg, day, 1000, 200); d != 50 || u != 10 {
		t.Errorf("daytime schedule = %d/%d, want 50/10", d, u)
	}
}

// The scheduler loop exits on ctx cancellation (StartBandwidthScheduler with
// schedules configured spawns it; a nil streamer or no schedules is a no-op).
func TestRunBandwidthScheduler_StopsOnCancel(t *testing.T) {
	s := NewForTesting()
	cfg := &config.Config{}
	cfg.Stream.BandwidthSchedules = []config.BandwidthSchedule{{TimeRange: "00:00-23:59"}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		runBandwidthScheduler(ctx, s, cfg)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runBandwidthScheduler did not stop on canceled ctx")
	}

	StartBandwidthScheduler(ctx, nil, cfg) // nil streamer → no goroutine, no panic
	StartBandwidthScheduler(ctx, s, cfg)   // canceled ctx → goroutine exits at once
}
