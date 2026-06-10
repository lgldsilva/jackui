package watchlist

import "time"

// Schedule kinds. "interval" re-checks every N minutes; "daily" checks once a
// day at HH:MM; "weekly" checks once a week on Weekday at HH:MM.
const (
	SchedInterval = "interval"
	SchedDaily    = "daily"
	SchedWeekly   = "weekly"
)

// DefaultInterval is the fallback cadence used when an interval schedule has
// Minutes <= 0 ("use the server default") and no global default was configured.
const DefaultInterval = 15 * time.Minute

// Schedule describes when a watchlist should be re-checked. The fields are
// flattened into the watchlists table (sched_* columns) and into the JSON API.
type Schedule struct {
	Kind    string `json:"schedKind"`
	Minutes int    `json:"schedMinutes"` // interval: every N minutes; <= 0 means "server default"
	Weekday int    `json:"schedWeekday"` // weekly: 0=Sunday … 6=Saturday (time.Weekday)
	Hour    int    `json:"schedHour"`    // daily/weekly: 0–23
	Minute  int    `json:"schedMinute"`  // daily/weekly: 0–59
}

// Normalized clamps out-of-range values so the DB only ever holds a schedule
// nextCheckTime can act on. Minutes <= 0 is preserved on purpose: it means
// "use the server-wide default interval". Exported because the AI schedule
// parser (handlers) clamps the model's output through the same single path.
func (s Schedule) Normalized() Schedule {
	switch s.Kind {
	case SchedDaily, SchedWeekly, SchedInterval:
	default:
		s.Kind = SchedInterval
	}
	if s.Weekday < 0 || s.Weekday > 6 {
		s.Weekday = 0
	}
	if s.Hour < 0 || s.Hour > 23 {
		s.Hour = 0
	}
	if s.Minute < 0 || s.Minute > 59 {
		s.Minute = 0
	}
	return s
}

// nextCheckTime computes when the next check is due, strictly after now.
// defaultEvery is the server-wide interval applied when an interval schedule
// has Minutes <= 0; pass 0 to fall back to DefaultInterval.
func nextCheckTime(s Schedule, now time.Time, defaultEvery time.Duration) time.Time {
	s = s.Normalized()
	switch s.Kind {
	case SchedDaily:
		next := time.Date(now.Year(), now.Month(), now.Day(), s.Hour, s.Minute, 0, 0, now.Location())
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		return next
	case SchedWeekly:
		next := time.Date(now.Year(), now.Month(), now.Day(), s.Hour, s.Minute, 0, 0, now.Location())
		next = next.AddDate(0, 0, (s.Weekday-int(now.Weekday())+7)%7)
		if !next.After(now) {
			next = next.AddDate(0, 0, 7)
		}
		return next
	default: // SchedInterval
		every := time.Duration(s.Minutes) * time.Minute
		if every <= 0 {
			every = defaultEvery
		}
		if every <= 0 {
			every = DefaultInterval
		}
		return now.Add(every)
	}
}
