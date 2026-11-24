package streamer

import (
	"context"
	"errors"

	"golang.org/x/sync/singleflight"
)

// ErrThumbnailBusy means every extraction slot is taken; the hover preview just
// skips this position (the handler answers 204, the player shows no preview).
var ErrThumbnailBusy = errors.New("thumbnail extraction busy")

// maxThumbExtractions bounds concurrent preview ffmpeg runs per process. Each
// one may block on torrent pieces at an arbitrary offset, competing with the
// video being watched for bandwidth, and costs ~50 MB of RSS.
const maxThumbExtractions = 2

// thumbGate bounds concurrent thumbnail extractions and collapses concurrent
// requests for the same bucket into one run.
type thumbGate struct {
	sem    chan struct{}
	flight singleflight.Group
}

func newThumbGate(n int) *thumbGate {
	return &thumbGate{sem: make(chan struct{}, n)}
}

// try runs fn for key, or fails fast with ErrThumbnailBusy when all slots are
// in use. Callers asking for a key already in flight share its result.
func (g *thumbGate) try(key string, fn func() ([]byte, error)) ([]byte, error) {
	return g.do(key, func() ([]byte, error) {
		select {
		case g.sem <- struct{}{}:
		default:
			return nil, ErrThumbnailBusy
		}
		defer func() { <-g.sem }()
		return fn()
	})
}

// wait is try for background callers: it waits for a slot until ctx ends.
func (g *thumbGate) wait(ctx context.Context, key string, fn func() ([]byte, error)) ([]byte, error) {
	return g.do(key, func() ([]byte, error) {
		select {
		case g.sem <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		defer func() { <-g.sem }()
		return fn()
	})
}

func (g *thumbGate) do(key string, fn func() ([]byte, error)) ([]byte, error) {
	v, err, _ := g.flight.Do(key, func() (any, error) { return fn() })
	out, _ := v.([]byte)
	return out, err
}
