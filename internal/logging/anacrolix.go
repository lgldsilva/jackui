package logging

import (
	"context"
	"log/slog"
	"time"

	alog "github.com/anacrolix/log"
)

// AnacrolixHandler forwards anacrolix/log records (torrent client, trackers,
// uTP, DHT) to a slog.Handler. It replaces alog.SlogHandlerAsHandler, which
// panics on unset levels — a log line must never crash the server.
type AnacrolixHandler struct {
	H slog.Handler
}

// Handle implements alog.Handler.
func (a AnacrolixHandler) Handle(r alog.Record) {
	level := anacrolixLevel(r.Level)
	ctx := context.Background()
	if !a.H.Enabled(ctx, level) {
		return
	}
	rec := slog.NewRecord(time.Now(), level, r.String(), 0)
	rec.AddAttrs(slog.String("logger", "anacrolix"))
	if len(r.Names) > 0 {
		rec.AddAttrs(slog.Any("names", r.Names))
	}
	_ = a.H.Handle(ctx, rec)
}

func anacrolixLevel(l alog.Level) slog.Level {
	switch l {
	case alog.Debug:
		return slog.LevelDebug
	case alog.Warning:
		return slog.LevelWarn
	case alog.Error, alog.Critical:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// BridgeAnacrolix points alog.Default (which torrent.NewDefaultClientConfig
// and the tracker/uTP packages copy) at h. Must run before the torrent client
// is built; loggers already copied keep their old handler. Filtering levels
// are untouched — only the sink changes.
func BridgeAnacrolix(h slog.Handler) {
	alog.Default.SetHandlers(AnacrolixHandler{H: h})
}
