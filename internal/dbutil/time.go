// Package dbutil holds tiny helpers shared by SQLite-backed stores.
package dbutil

import "time"

// ParseTime decodes a timestamp string from SQLite, tolerating both formats the
// driver produces:
//
//   - SQLite native `YYYY-MM-DD HH:MM:SS` (when stored via CURRENT_TIMESTAMP)
//   - RFC3339 `YYYY-MM-DDTHH:MM:SSZ` (which modernc.org/sqlite emits in some
//     versions when reading DATETIME columns back out)
//
// Returns Go zero time if neither parse works. Stores were previously parsing
// only the first format and silently dropping every timestamp, producing
// `0001-01-01T00:00:00Z` in API responses. Centralising the logic here means
// any new store gets correct timestamps for free.
func ParseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		TimeFormat,
		"2006-01-02 15:04:05.999999999",
		time.RFC3339Nano,
		time.RFC3339,
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
