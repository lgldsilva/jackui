package logging

import (
	"io"
	"log"
	"log/slog"
	"sync/atomic"
)

var jsonMode atomic.Bool

// JSONEnabled reports whether Setup installed the JSON pipeline.
func JSONEnabled() bool { return jsonMode.Load() }

// Setup installs the process-wide logger on w and returns its handler.
//
// format "json": slog JSON handler (request_id aware); the stdlib log package
// is routed through a LineWriter so each log.Printf becomes one JSON record
// with an inferred level, and anacrolix/log's default sink is bridged too.
// Callers should also point gin.DefaultWriter/DefaultErrorWriter at
// NewLineWriter(h, "gin").
//
// Any other format keeps the historical text pipeline unchanged.
func Setup(format string, w io.Writer) slog.Handler {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if format != "json" {
		jsonMode.Store(false)
		h := slog.NewTextHandler(w, opts)
		slog.SetDefault(slog.New(h))
		log.SetOutput(slog.NewLogLogger(h, slog.LevelInfo).Writer())
		log.SetFlags(0)
		return h
	}
	jsonMode.Store(true)
	h := ContextHandler{slog.NewJSONHandler(w, opts)}
	slog.SetDefault(slog.New(h))
	log.SetOutput(NewLineWriter(h, ""))
	log.SetFlags(0)
	BridgeAnacrolix(h)
	return h
}
