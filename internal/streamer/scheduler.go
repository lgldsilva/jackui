package streamer

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/lgldsilva/jackui/internal/config"
)

// InTimeRange checks whether the current time falls inside a time_range like "08:00-18:00".
// Supports ranges that wrap around midnight, e.g. "22:00-06:00".
func InTimeRange(now time.Time, rangeStr string) bool {
	parts := strings.Split(rangeStr, "-")
	if len(parts) != 2 {
		return false
	}
	startStr, endStr := parts[0], parts[1]

	sh, sm, err1 := parseHourMin(startStr)
	eh, em, err2 := parseHourMin(endStr)
	if err1 != nil || err2 != nil {
		return false
	}

	// Time of day in minutes
	currentMinutes := now.Hour()*60 + now.Minute()
	startMinutes := sh*60 + sm
	endMinutes := eh*60 + em

	if startMinutes <= endMinutes {
		return currentMinutes >= startMinutes && currentMinutes <= endMinutes
	} else {
		// Range crosses midnight (e.g. 22:00-06:00)
		return currentMinutes >= startMinutes || currentMinutes <= endMinutes
	}
}

func parseHourMin(s string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid time format")
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("invalid time digits")
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("invalid time values")
	}
	return h, m, nil
}

// StartBandwidthScheduler starts the background periodic loop that applies the time-based bandwidth limit rules.
func StartBandwidthScheduler(ctx context.Context, s *Streamer, cfg *config.Config) {
	if s == nil || len(cfg.Stream.BandwidthSchedules) == 0 {
		return
	}
	go runBandwidthScheduler(ctx, s, cfg)
}

func runBandwidthScheduler(ctx context.Context, s *Streamer, cfg *config.Config) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	lastDown, lastUp := int64(-2), int64(-2) // Initial dummy values
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			lastDown, lastUp = applyBandwidthSchedule(s, cfg, time.Now(), lastDown, lastUp)
		}
	}
}

// applyBandwidthSchedule resolves the active schedule (or defaults) and pushes
// rate limits only when they change. Returns the last-applied pair.
func applyBandwidthSchedule(s *Streamer, cfg *config.Config, now time.Time, lastDown, lastUp int64) (int64, int64) {
	targetDown, targetUp := resolveBandwidthTargets(cfg, now)
	if targetDown == lastDown && targetUp == lastUp {
		return lastDown, lastUp
	}
	s.SetRateLimits(targetDown, targetUp)
	log.Printf("[BandwidthScheduler] Bandwidth limits updated to download=%d B/s, upload=%d B/s (current time: %02d:%02d)", targetDown, targetUp, now.Hour(), now.Minute())
	return targetDown, targetUp
}

func resolveBandwidthTargets(cfg *config.Config, now time.Time) (down, up int64) {
	for _, sched := range cfg.Stream.BandwidthSchedules {
		if InTimeRange(now, sched.TimeRange) {
			return sched.MaxDownloadRate, sched.MaxUploadRate
		}
	}
	return cfg.Stream.MaxDownloadRate, cfg.Stream.MaxUploadRate
}
