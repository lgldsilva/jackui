package stats

import (
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/library"
)

var (
	now = time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	utc = time.UTC
)

func entry(kind string, resume, duration float64, played, added time.Time) library.Entry {
	return library.Entry{Kind: kind, ResumeSeconds: resume, DurationSeconds: duration, LastPlayedAt: played, AddedAt: added}
}

func TestAggregate_Empty(t *testing.T) {
	agg := Aggregate(nil, now, utc)
	if agg.Titles != 0 || agg.Completed != 0 || agg.WatchSeconds != 0 {
		t.Fatalf("empty aggregate not zeroed: %+v", agg)
	}
	if len(agg.AddedByMonth) != monthsBack {
		t.Fatalf("expected %d months, got %d", monthsBack, len(agg.AddedByMonth))
	}
	if agg.AddedByMonth[monthsBack-1].Month != "2026-06" || agg.AddedByMonth[0].Month != "2026-01" {
		t.Fatalf("month window wrong: %+v", agg.AddedByMonth)
	}
}

func TestAggregate_CompletedInProgressAndWatchSeconds(t *testing.T) {
	played := now.Add(-time.Hour)
	entries := []library.Entry{
		entry("video", 95, 100, played, now),  // completed (≥90%)
		entry("video", 30, 100, played, now),  // in progress
		entry("video", 0, 100, played, now),   // untouched
		entry("video", 120, 100, played, now), // resume past the end → capped at duration, completed
		entry("audio", 50, 0, played, now),    // no duration known → counts resume, not completed
	}
	agg := Aggregate(entries, now, utc)
	if agg.Titles != 5 {
		t.Fatalf("titles = %d", agg.Titles)
	}
	if agg.Completed != 2 {
		t.Fatalf("completed = %d, want 2", agg.Completed)
	}
	if agg.InProgress != 2 {
		t.Fatalf("inProgress = %d, want 2 (30s video + 50s audio)", agg.InProgress)
	}
	want := 95.0 + 30 + 0 + 100 + 50
	if agg.WatchSeconds != want {
		t.Fatalf("watchSeconds = %v, want %v", agg.WatchSeconds, want)
	}
	if agg.ByKind["video"] != 4 || agg.ByKind["audio"] != 1 {
		t.Fatalf("byKind = %+v", agg.ByKind)
	}
}

func TestAggregate_KindFallbackToOther(t *testing.T) {
	agg := Aggregate([]library.Entry{entry("", 0, 0, now, now)}, now, utc)
	if agg.ByKind["other"] != 1 {
		t.Fatalf("byKind = %+v, want other:1", agg.ByKind)
	}
}

func TestAggregate_WeekdayAndHourBuckets(t *testing.T) {
	// 2026-06-10 is a Wednesday (weekday 3). 22h UTC.
	played := time.Date(2026, 6, 10, 22, 30, 0, 0, time.UTC)
	agg := Aggregate([]library.Entry{entry("video", 1, 10, played, now)}, now, utc)
	if agg.PlaysByWeekday[3] != 1 {
		t.Fatalf("weekday buckets = %v, want index 3 (Wed)", agg.PlaysByWeekday)
	}
	if agg.PlaysByHour[22] != 1 {
		t.Fatalf("hour buckets = %v, want index 22", agg.PlaysByHour)
	}
	// The same instant in UTC-3 lands on 19h — bucketing must follow loc.
	bsb := time.FixedZone("BRT", -3*3600)
	agg = Aggregate([]library.Entry{entry("video", 1, 10, played, now)}, now, bsb)
	if agg.PlaysByHour[19] != 1 {
		t.Fatalf("hour buckets in BRT = %v, want index 19", agg.PlaysByHour)
	}
}

func TestAggregate_AddedByMonthWindow(t *testing.T) {
	entries := []library.Entry{
		entry("video", 0, 0, now, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),   // current month
		entry("video", 0, 0, now, time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)),  // oldest in window
		entry("video", 0, 0, now, time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)), // outside window → ignored
	}
	agg := Aggregate(entries, now, utc)
	if agg.AddedByMonth[monthsBack-1].Count != 1 {
		t.Fatalf("current month count = %d", agg.AddedByMonth[monthsBack-1].Count)
	}
	if agg.AddedByMonth[0].Count != 1 {
		t.Fatalf("oldest month count = %d", agg.AddedByMonth[0].Count)
	}
	var total int
	for _, m := range agg.AddedByMonth {
		total += m.Count
	}
	if total != 2 {
		t.Fatalf("total in window = %d, want 2", total)
	}
}

func TestAggregate_NilLocationDefaults(t *testing.T) {
	// Must not panic; uses time.Local.
	_ = Aggregate([]library.Entry{entry("video", 1, 10, now, now)}, now, nil)
}
