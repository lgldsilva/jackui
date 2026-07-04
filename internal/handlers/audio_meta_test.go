package handlers

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/audiometa"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// minimalID3 builds a tiny valid ID3v2.3 tag with a single TIT2 (title) frame —
// enough for dhowden/tag to parse without real audio frames after it.
func minimalID3(title string) []byte {
	text := append([]byte{0x00}, []byte(title)...) // 0x00 = ISO-8859-1
	frame := []byte("TIT2")
	sz := len(text)
	frame = append(frame, byte(sz>>24), byte(sz>>16), byte(sz>>8), byte(sz), 0x00, 0x00)
	frame = append(frame, text...)
	total := len(frame)
	ss := []byte{byte((total >> 21) & 0x7f), byte((total >> 14) & 0x7f), byte((total >> 7) & 0x7f), byte(total & 0x7f)}
	hdr := append([]byte("ID3"), 0x03, 0x00, 0x00)
	hdr = append(hdr, ss...)
	return append(hdr, frame...)
}

type bytesRSC struct{ *bytes.Reader }

func (bytesRSC) Close() error { return nil }

func TestReadTorrentTags_Success(t *testing.T) {
	rc := bytesRSC{bytes.NewReader(minimalID3("Wintersaga"))}
	tags, ok := readTorrentTags(rc, time.Second)
	if !ok || tags.Title != "Wintersaga" {
		t.Fatalf("got ok=%v title=%q, want ok=true title=Wintersaga", ok, tags.Title)
	}
}

// blockingRSC's Read blocks until done is closed — drives the timeout branch.
type blockingRSC struct{ done chan struct{} }

func (b blockingRSC) Read([]byte) (int, error)     { <-b.done; return 0, io.EOF }
func (blockingRSC) Seek(int64, int) (int64, error) { return 0, nil }
func (blockingRSC) Close() error                   { return nil }

func TestReadTorrentTags_Timeout(t *testing.T) {
	b := blockingRSC{done: make(chan struct{})}
	_, ok := readTorrentTags(b, 30*time.Millisecond)
	close(b.done) // let the spawned goroutine finish + close, no leak
	if ok {
		t.Fatal("want ok=false on timeout")
	}
}

type stubSource struct {
	data []byte
	ct   string
}

func (stubSource) Name() string { return "stub" }
func (s stubSource) Find(context.Context, string) ([]byte, string, error) {
	return s.data, s.ct, nil
}

func newTestCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	return c, w
}

func ctxWithParams(hash, file string) (*gin.Context, *httptest.ResponseRecorder) {
	c, w := newTestCtx()
	c.Params = gin.Params{{Key: "hash", Value: hash}, {Key: "file", Value: file}}
	return c, w
}

func TestStreamAudioMeta_BadParamsAndNoTorrent(t *testing.T) {
	s := streamer.NewForTesting()
	validHash := strings.Repeat("a", 40)

	c1, _ := ctxWithParams("not-a-hash", "0")
	StreamAudioMeta(s)(c1)
	if c1.Writer.Status() != 400 {
		t.Errorf("bad hash: status=%d want 400", c1.Writer.Status())
	}

	c2, _ := ctxWithParams(validHash, "abc")
	StreamAudioMeta(s)(c2)
	if c2.Writer.Status() != 400 {
		t.Errorf("bad idx: status=%d want 400", c2.Writer.Status())
	}

	// Valid params but no active torrent → FileReader errors → empty tags (200).
	c3, w3 := ctxWithParams(validHash, "0")
	StreamAudioMeta(s)(c3)
	if w3.Code != 200 {
		t.Errorf("inactive torrent: code=%d want 200", w3.Code)
	}
}

func TestStreamAudioMeta_CacheHit(t *testing.T) {
	hash := strings.Repeat("b", 40)
	torrentTagCache.Store(hash+":0", audiometa.Tags{Title: "Cached Title"})
	c, w := ctxWithParams(hash, "0")
	StreamAudioMeta(nil)(c) // cache hit returns before touching the streamer
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Cached Title") {
		t.Fatalf("cache hit: code=%d body=%q", w.Code, w.Body.String())
	}
}
