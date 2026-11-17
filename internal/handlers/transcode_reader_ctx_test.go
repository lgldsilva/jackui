package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
)

// peerlessTorrent activates a one-piece torrent that has metadata but no peers
// and nothing on disk, so every read of it blocks in anacrolix's waitAvailable.
func peerlessTorrent(t *testing.T) (*streamer.Streamer, metainfo.Hash) {
	t.Helper()
	s, dataDir := realStreamerEnv(t)
	t.Cleanup(s.Close)
	piece := metainfo.HashBytes([]byte("never-arrives"))
	infoBytes, err := bencode.Marshal(metainfo.Info{
		Name: "Stalled.mkv", PieceLength: 1 << 14, Pieces: piece[:], Length: 13,
	})
	if err != nil {
		t.Fatalf("bencode.Marshal: %v", err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: infoBytes}
	h := mi.HashInfoBytes()
	f, err := os.Create(filepath.Join(dataDir, ".metainfo", h.HexString()+".torrent"))
	if err != nil {
		t.Fatalf("create .torrent: %v", err)
	}
	if err := mi.Write(f); err != nil {
		t.Fatalf("write .torrent: %v", err)
	}
	_ = f.Close()
	if _, err := s.Add(context.Background(), "magnet:?xt=urn:btih:"+h.HexString()); err != nil {
		t.Fatalf("Add: %v", err)
	}
	return s, h
}

// TestTranscodeStream_ClientGoneUnblocksStalledRead is the regression test for
// the progressive transcode handler pinning its goroutine (and the reader, and
// ffmpeg's Wait) on a stalled swarm: the handler must return once the request
// context is canceled, even though no byte of the source ever arrives.
func TestTranscodeStream_ClientGoneUnblocksStalledRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, h := peerlessTorrent(t)
	transcode.SetCachedForTesting(&transcode.Capabilities{FFmpegPath: "/bin/true"})
	t.Cleanup(transcode.ResetCachedForTesting)

	ctx, cancel := context.WithCancel(context.Background())
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "hash", Value: h.HexString()}, {Key: "file", Value: "0"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/stream/transcode/"+h.HexString()+"/0", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		TranscodeStream(s, nil, "")(c)
	}()

	select {
	case <-done:
		t.Fatal("handler returned before the client left: the read was expected to stall")
	case <-time.After(300 * time.Millisecond):
	}
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler still blocked 5s after the client disconnected: torrent reader ignores the request context")
	}
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (source produced no data); body: %s", w.Code, w.Body.String())
	}
}

type plainReader struct{ io.ReadSeekCloser }

type ctxReader struct {
	io.ReadSeekCloser
	got context.Context
}

func (r *ctxReader) SetContext(ctx context.Context) { r.got = ctx }

func TestBindReaderContext(t *testing.T) {
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "req")

	r := &ctxReader{}
	bindReaderContext(ctx, r)
	if r.got != ctx {
		t.Error("reader with SetContext did not receive the request context")
	}

	// A reader without SetContext is left untouched (no panic).
	bindReaderContext(ctx, plainReader{})
}
