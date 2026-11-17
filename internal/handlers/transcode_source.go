package handlers

import (
	"context"
	"io"
	"net/http"
	"os"
	"sync"

	"github.com/lgldsilva/jackui/internal/transcode"
)

// guardedReader serializes Close against an in-flight Read. transcode.Run
// feeds ffmpeg from a goroutine it does not join, so Run can return while that
// pump is still inside the source's Read — and anacrolix's reader is not safe
// for a concurrent Close (both sides touch its storage reader unlocked: double
// close, a handle re-created after Close, or a nil call that no recover
// covers). Close therefore waits for the Read to return, and later Reads fail
// instead of reaching the closed source.
type guardedReader struct {
	mu     sync.Mutex
	r      io.ReadCloser
	closed bool
}

func (g *guardedReader) Read(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return 0, os.ErrClosed
	}
	return g.r.Read(p)
}

func (g *guardedReader) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil
	}
	g.closed = true
	return g.r.Close()
}

// runTorrentTranscode runs the progressive transcode over a torrent reader and
// closes it. The derived context is canceled BEFORE the close: that wakes a
// Read parked on a stalled swarm, so the guarded Close cannot hang, and it
// also covers ffmpeg exiting on its own while the request is still live.
func runTorrentTranscode(ctx context.Context, src io.ReadCloser, w http.ResponseWriter, opts transcode.Options) error {
	ctx, cancel := context.WithCancel(ctx)
	bindReaderContext(ctx, src)
	g := &guardedReader{r: src}
	defer func() {
		cancel()
		_ = g.Close()
	}()
	return transcode.Run(ctx, g, w, opts)
}
