package logging

import (
	"context"
	"log/slog"
)

type requestIDKey struct{}

// WithRequestID returns ctx carrying the request correlation id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the correlation id stored by WithRequestID, or "".
func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// ContextHandler decorates a handler so records logged with a request
// context (slog.InfoContext(c.Request.Context(), ...)) carry request_id.
type ContextHandler struct {
	slog.Handler
}

// Handle adds request_id when the context has one.
func (h ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestID(ctx); id != "" && !hasAttr(r, "request_id") {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func hasAttr(r slog.Record, key string) bool {
	found := false
	r.Attrs(func(a slog.Attr) bool {
		found = a.Key == key
		return !found
	})
	return found
}

// WithAttrs keeps the decoration on derived handlers.
func (h ContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return ContextHandler{h.Handler.WithAttrs(attrs)}
}

// WithGroup keeps the decoration on derived handlers.
func (h ContextHandler) WithGroup(name string) slog.Handler {
	return ContextHandler{h.Handler.WithGroup(name)}
}
