// Package logging owns the process-wide log pipeline. In JSON mode
// (JACKUI_LOG_FORMAT=json) every producer — slog, the stdlib log package,
// gin's writers and anacrolix/log — ends up as exactly one JSON object per
// line on the same handler, with a level.
package logging

import (
	"log/slog"
	"regexp"
)

var (
	// Upper-case markers are deliberate shouting in the codebase
	// ("ffmpeg FAILED", "WARNING: auth disabled ...") — trust them.
	errorMarkRe = regexp.MustCompile(`\b(ERROR|FATAL|PANIC|FAILED)\b`)
	warnMarkRe  = regexp.MustCompile(`\bWARN(ING)?\b|^Warning:`)
	// Lower-case failure words cover the "pkg: X failed: %v" convention used by
	// ~all error paths. Kept at WARN (not ERROR) because many of them are
	// retried or fail-open; a trailing '=' excludes counters like "failed=3".
	failWordRe = regexp.MustCompile(`(?i)\b(failed|failure|error|err)\b([^=]|$)`)
)

// InferLevel derives a slog level for a legacy log.Printf line. It is a
// conservative heuristic over the message text so the ~250 log.Printf call
// sites gain a usable level without being rewritten: explicit upper-case
// markers map to ERROR/WARN, lower-case failure words to WARN, the rest INFO.
func InferLevel(msg string) slog.Level {
	switch {
	case errorMarkRe.MatchString(msg):
		return slog.LevelError
	case warnMarkRe.MatchString(msg), failWordRe.MatchString(msg):
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}
