// Package stats aggregates per-user usage statistics from data the app
// already records (library resume positions, downloads, search history,
// watchlists). Pure functions over store rows — no DB access of its own, so
// every aggregation is unit-testable without SQLite.
package stats

import (
	"time"

	"github.com/lgldsilva/jackui/internal/library"
)

// completedRatio is how much of the duration must have been watched for a
// title to count as "finished" (players rarely reach the exact last second).
const completedRatio = 0.9

// monthsBack is the window of the "added per month" series.
const monthsBack = 6

// MonthCount is one bar of the additions-per-month series.
type MonthCount struct {
	Month string `json:"month"` // "2026-06"
	Count int    `json:"count"`
}

// LibraryAgg summarizes the user's playback library.
type LibraryAgg struct {
	Titles         int            `json:"titles"`
	Completed      int            `json:"completed"`
	InProgress     int            `json:"inProgress"`
	WatchSeconds   float64        `json:"watchSeconds"`   // accumulated playback position (approximation: rewatches don't add)
	ByKind         map[string]int `json:"byKind"`         // "video" | "audio" | "other"
	PlaysByWeekday [7]int         `json:"playsByWeekday"` // 0 = Sunday, local time
	PlaysByHour    [24]int        `json:"playsByHour"`    // local time
	AddedByMonth   []MonthCount   `json:"addedByMonth"`   // last 6 months, chronological
}

// Aggregate folds library entries into the display aggregates. now/loc define
// the month window and the timezone of the weekday/hour buckets (the container
// TZ — same convention as the bandwidth windows).
func Aggregate(entries []library.Entry, now time.Time, loc *time.Location) LibraryAgg {
	if loc == nil {
		loc = time.Local
	}
	agg := LibraryAgg{ByKind: map[string]int{}, AddedByMonth: monthWindow(now.In(loc))}
	monthIdx := map[string]int{}
	for i, m := range agg.AddedByMonth {
		monthIdx[m.Month] = i
	}
	for _, e := range entries {
		agg.Titles++
		agg.ByKind[kindLabel(e.Kind)]++
		agg.WatchSeconds += watchedSeconds(e)
		switch {
		case isCompleted(e):
			agg.Completed++
		case e.ResumeSeconds > 0:
			agg.InProgress++
		}
		if !e.LastPlayedAt.IsZero() {
			lp := e.LastPlayedAt.In(loc)
			agg.PlaysByWeekday[int(lp.Weekday())]++
			agg.PlaysByHour[lp.Hour()]++
		}
		if !e.AddedAt.IsZero() {
			if i, ok := monthIdx[e.AddedAt.In(loc).Format("2006-01")]; ok {
				agg.AddedByMonth[i].Count++
			}
		}
	}
	return agg
}

func kindLabel(kind string) string {
	if kind == "" {
		return "other"
	}
	return kind
}

// watchedSeconds approximates time spent on an entry: the resume position,
// capped at the duration when known (a finished play can report a position a
// hair past the end).
func watchedSeconds(e library.Entry) float64 {
	if e.DurationSeconds > 0 && e.ResumeSeconds > e.DurationSeconds {
		return e.DurationSeconds
	}
	return e.ResumeSeconds
}

func isCompleted(e library.Entry) bool {
	return e.DurationSeconds > 0 && e.ResumeSeconds >= e.DurationSeconds*completedRatio
}

// monthWindow returns the last monthsBack months ending at now, oldest first,
// all counts zero.
func monthWindow(now time.Time) []MonthCount {
	out := make([]MonthCount, 0, monthsBack)
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	for i := monthsBack - 1; i >= 0; i-- {
		out = append(out, MonthCount{Month: first.AddDate(0, -i, 0).Format("2006-01")})
	}
	return out
}
