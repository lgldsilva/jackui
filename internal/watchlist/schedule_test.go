package watchlist

import (
	"testing"
	"time"
)

// fixed reference: Wednesday 2026-06-10 10:30:00 local
var schedNow = time.Date(2026, 6, 10, 10, 30, 0, 0, time.Local)

func TestNextCheckTime_Interval(t *testing.T) {
	got := nextCheckTime(Schedule{Kind: SchedInterval, Minutes: 45}, schedNow, 0)
	if want := schedNow.Add(45 * time.Minute); !got.Equal(want) {
		t.Fatalf("interval 45m: got %v, want %v", got, want)
	}
}

func TestNextCheckTime_IntervalServerDefault(t *testing.T) {
	// Minutes <= 0 means "server default"
	got := nextCheckTime(Schedule{Kind: SchedInterval, Minutes: 0}, schedNow, 30*time.Minute)
	if want := schedNow.Add(30 * time.Minute); !got.Equal(want) {
		t.Fatalf("server default 30m: got %v, want %v", got, want)
	}
	// no server default either → DefaultInterval
	got = nextCheckTime(Schedule{Kind: SchedInterval, Minutes: -1}, schedNow, 0)
	if want := schedNow.Add(DefaultInterval); !got.Equal(want) {
		t.Fatalf("hard default: got %v, want %v", got, want)
	}
}

func TestNextCheckTime_UnknownKindFallsBackToInterval(t *testing.T) {
	got := nextCheckTime(Schedule{Kind: "lunar", Minutes: 10}, schedNow, 0)
	if want := schedNow.Add(10 * time.Minute); !got.Equal(want) {
		t.Fatalf("unknown kind: got %v, want %v", got, want)
	}
}

func TestNextCheckTime_DailyLaterToday(t *testing.T) {
	got := nextCheckTime(Schedule{Kind: SchedDaily, Hour: 22, Minute: 15}, schedNow, 0)
	want := time.Date(2026, 6, 10, 22, 15, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("daily later today: got %v, want %v", got, want)
	}
}

func TestNextCheckTime_DailyAlreadyPassedRollsToTomorrow(t *testing.T) {
	got := nextCheckTime(Schedule{Kind: SchedDaily, Hour: 8, Minute: 0}, schedNow, 0)
	want := time.Date(2026, 6, 11, 8, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("daily passed: got %v, want %v", got, want)
	}
}

func TestNextCheckTime_DailyExactlyNowRollsToTomorrow(t *testing.T) {
	// strictly after now: 10:30 at 10:30 → tomorrow
	got := nextCheckTime(Schedule{Kind: SchedDaily, Hour: 10, Minute: 30}, schedNow, 0)
	want := time.Date(2026, 6, 11, 10, 30, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("daily at-now: got %v, want %v", got, want)
	}
}

func TestNextCheckTime_WeeklySameDayLater(t *testing.T) {
	// schedNow is a Wednesday (weekday 3)
	got := nextCheckTime(Schedule{Kind: SchedWeekly, Weekday: 3, Hour: 23, Minute: 0}, schedNow, 0)
	want := time.Date(2026, 6, 10, 23, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("weekly same day later: got %v, want %v", got, want)
	}
}

func TestNextCheckTime_WeeklySameDayPassedRollsAWeek(t *testing.T) {
	got := nextCheckTime(Schedule{Kind: SchedWeekly, Weekday: 3, Hour: 9, Minute: 0}, schedNow, 0)
	want := time.Date(2026, 6, 17, 9, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("weekly rolled: got %v, want %v", got, want)
	}
}

func TestNextCheckTime_WeeklyOtherDay(t *testing.T) {
	// Saturday (6) from Wednesday → +3 days
	got := nextCheckTime(Schedule{Kind: SchedWeekly, Weekday: 6, Hour: 7, Minute: 45}, schedNow, 0)
	want := time.Date(2026, 6, 13, 7, 45, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("weekly saturday: got %v, want %v", got, want)
	}
}

func TestNextCheckTime_WeeklyWrapsWeekday(t *testing.T) {
	// Sunday (0) from Wednesday → +4 days
	got := nextCheckTime(Schedule{Kind: SchedWeekly, Weekday: 0, Hour: 12, Minute: 0}, schedNow, 0)
	want := time.Date(2026, 6, 14, 12, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("weekly sunday: got %v, want %v", got, want)
	}
}

func TestNextCheckTime_AlwaysAfterNow_Property(t *testing.T) {
	// light property check: for a spread of schedules and instants, the result
	// is always strictly in the future.
	kinds := []string{SchedInterval, SchedDaily, SchedWeekly, "garbage"}
	for dayOff := 0; dayOff < 8; dayOff++ {
		now := schedNow.AddDate(0, 0, dayOff)
		for _, kind := range kinds {
			for h := 0; h < 24; h += 5 {
				s := Schedule{Kind: kind, Minutes: h, Weekday: dayOff % 7, Hour: h, Minute: (h * 7) % 60}
				if got := nextCheckTime(s, now, 0); !got.After(now) {
					t.Fatalf("nextCheckTime(%+v, %v) = %v — not after now", s, now, got)
				}
			}
		}
	}
}

func TestScheduleNormalized_ClampsRanges(t *testing.T) {
	s := Schedule{Kind: "bogus", Minutes: -3, Weekday: 9, Hour: 31, Minute: 99}.normalized()
	if s.Kind != SchedInterval || s.Weekday != 0 || s.Hour != 0 || s.Minute != 0 {
		t.Fatalf("normalized = %+v", s)
	}
	if s.Minutes != -3 {
		t.Fatalf("Minutes must be preserved (server-default marker), got %d", s.Minutes)
	}
	ok := Schedule{Kind: SchedWeekly, Minutes: 10, Weekday: 6, Hour: 23, Minute: 59}.normalized()
	if ok.Kind != SchedWeekly || ok.Weekday != 6 || ok.Hour != 23 || ok.Minute != 59 {
		t.Fatalf("valid schedule mutated: %+v", ok)
	}
}
