package logging

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// LineWriter adapts an io.Writer producer (the stdlib log package, gin's
// DefaultWriter/DefaultErrorWriter) to a slog.Handler: each Write becomes one
// record whose level is inferred from the text. The stdlib logger issues one
// Write per entry, so a multi-line message (e.g. ffmpeg stderr) stays a single
// record instead of being split into orphan lines.
type LineWriter struct {
	h      slog.Handler
	source string
}

// NewLineWriter returns a writer that emits records on h. source, when
// non-empty, is attached as a "logger" attribute (e.g. "gin").
func NewLineWriter(h slog.Handler, source string) *LineWriter {
	return &LineWriter{h: h, source: source}
}

// Write implements io.Writer. It never fails: logging must not break callers.
func (w *LineWriter) Write(p []byte) (int, error) {
	msg := strings.TrimRight(string(p), "\r\n")
	if strings.TrimSpace(msg) == "" {
		return len(p), nil
	}
	level := InferLevel(msg)
	ctx := context.Background()
	if !w.h.Enabled(ctx, level) {
		return len(p), nil
	}
	r := slog.NewRecord(time.Now(), level, msg, 0)
	if w.source != "" {
		r.AddAttrs(slog.String("logger", w.source))
	}
	_ = w.h.Handle(ctx, r)
	return len(p), nil
}
