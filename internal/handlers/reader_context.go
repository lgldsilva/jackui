package handlers

import (
	"context"
	"io"
)

// bindReaderContext wires ctx into a torrent reader's blocking reads.
// anacrolix's Reader.Close() does not wake a Read parked in waitAvailable, so
// a reader handed to a long-lived consumer (ffmpeg's stdin copier) without a
// context stays blocked on a stalled swarm long after the HTTP client left.
// Readers without SetContext (plain files) are left alone.
func bindReaderContext(ctx context.Context, r io.Reader) {
	if sc, ok := r.(interface{ SetContext(context.Context) }); ok {
		sc.SetContext(ctx)
	}
}
