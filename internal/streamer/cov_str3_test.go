package streamer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// ---------------------------------------------------------------------------
// computeOSHash / ComputeFileOSHash — exercise the non-zero arithmetic and the
// distinct seek/read error paths not already covered by oshash_test.go.
// ---------------------------------------------------------------------------

// str3SeekErrReader fails on the Nth Seek call (1-indexed); reads succeed.
type str3SeekErrReader struct {
	data   []byte
	pos    int64
	seekN  int
	failOn int
}

func (r *str3SeekErrReader) Read(p []byte) (int, error) {
	if r.pos >= int64(len(r.data)) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += int64(n)
	return n, nil
}

func (r *str3SeekErrReader) Seek(offset int64, whence int) (int64, error) {
	r.seekN++
	if r.seekN == r.failOn {
		return 0, errors.New("str3 seek boom")
	}
	switch whence {
	case io.SeekStart:
		r.pos = offset
	case io.SeekCurrent:
		r.pos += offset
	case io.SeekEnd:
		r.pos = int64(len(r.data)) + offset
	}
	return r.pos, nil
}

func Test_str3_ComputeOSHashNonZeroContent(t *testing.T) {
	const size = 200 * 1024
	buf := make([]byte, size)
	for i := range buf {
		buf[i] = byte(i*7 + 3)
	}
	got, err := computeOSHash(bytes.NewReader(buf), size)
	if err != nil {
		t.Fatalf("computeOSHash: %v", err)
	}
	if len(got) != 16 {
		t.Fatalf("hash length = %d, want 16", len(got))
	}
	// All-zero file of the same size must produce a different hash (the chunk
	// sums must actually contribute).
	zero, _ := computeOSHash(bytes.NewReader(make([]byte, size)), size)
	if got == zero {
		t.Errorf("non-zero content produced same hash as zero file: %q", got)
	}
}

func Test_str3_ComputeOSHashSeekStartError(t *testing.T) {
	r := &str3SeekErrReader{data: make([]byte, 200*1024), failOn: 1}
	if _, err := computeOSHash(r, 200*1024); err == nil {
		t.Error("expected error when seek-to-start fails")
	}
}

func Test_str3_ComputeOSHashSeekEndError(t *testing.T) {
	// Second seek (to end) fails; the first read must have succeeded.
	r := &str3SeekErrReader{data: make([]byte, 200*1024), failOn: 2}
	if _, err := computeOSHash(r, 200*1024); err == nil {
		t.Error("expected error when seek-to-end fails")
	}
}

func Test_str3_ComputeFileOSHashRoundTrip(t *testing.T) {
	buf := make([]byte, 128*1024)
	for i := range buf {
		buf[i] = byte(i)
	}
	hr, err := ComputeFileOSHash(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		t.Fatalf("ComputeFileOSHash: %v", err)
	}
	if hr.Size != int64(len(buf)) {
		t.Errorf("Size = %d, want %d", hr.Size, len(buf))
	}
	if len(hr.Hash) != 16 {
		t.Errorf("Hash length = %d, want 16", len(hr.Hash))
	}
}

// ---------------------------------------------------------------------------
// SSRF guard — checkRedirect, ssrfDialContext, injectJackettAPIKey, and the
// constructor wiring (newSSRFGuardedClient / newSSRFTransport).
// ---------------------------------------------------------------------------

func Test_str3_CheckRedirectCapturesMagnet(t *testing.T) {
	var captured string
	req, _ := http.NewRequest("GET", "magnet:?xt=urn:btih:abc", nil)
	err := checkRedirect(req, nil, &captured)
	if !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("expected ErrUseLastResponse, got %v", err)
	}
	if captured != "magnet:?xt=urn:btih:abc" {
		t.Errorf("captured = %q", captured)
	}
}

func Test_str3_CheckRedirectTooManyRedirects(t *testing.T) {
	var captured string
	req, _ := http.NewRequest("GET", "https://example.com/next", nil)
	via := make([]*http.Request, 10)
	if err := checkRedirect(req, via, &captured); err == nil {
		t.Error("expected error after 10 redirects")
	}
	if captured != "" {
		t.Errorf("captured should be empty, got %q", captured)
	}
}

func Test_str3_CheckRedirectAllowsNormal(t *testing.T) {
	var captured string
	req, _ := http.NewRequest("GET", "https://example.com/torrent", nil)
	if err := checkRedirect(req, []*http.Request{{}}, &captured); err != nil {
		t.Errorf("expected nil for a normal redirect, got %v", err)
	}
}

func Test_str3_SSRFDialContextBlocksLoopback(t *testing.T) {
	// 127.0.0.1 resolves to loopback and is not the trusted Jackett host, so the
	// guard must refuse to dial it.
	_, err := ssrfDialContext(context.Background(), "tcp", "127.0.0.1:80", "")
	if err == nil {
		t.Fatal("expected the SSRF guard to block a loopback address")
	}
}

func Test_str3_SSRFDialContextBadAddr(t *testing.T) {
	// Missing port → SplitHostPort fails before any lookup.
	if _, err := ssrfDialContext(context.Background(), "tcp", "no-port", ""); err == nil {
		t.Error("expected error for address without a port")
	}
}

func Test_str3_SSRFDialContextUnresolvable(t *testing.T) {
	// A syntactically valid but unresolvable host should surface a lookup error.
	host := "str3.invalid.nonexistent.example."
	if _, err := ssrfDialContext(context.Background(), "tcp", host+":80", ""); err == nil {
		t.Error("expected lookup error for an unresolvable host")
	}
}

func Test_str3_SSRFDialContextTrustedHostBypass(t *testing.T) {
	// When the host matches the trusted Jackett host, the loopback check is
	// skipped — the dial then proceeds and fails only because nothing listens.
	// We point it at a real listener to confirm the bypass actually dials.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	conn, err := ssrfDialContext(context.Background(), "tcp", "127.0.0.1:"+port, "127.0.0.1")
	if err != nil {
		t.Fatalf("trusted-host dial should succeed: %v", err)
	}
	conn.Close()
}

func Test_str3_InjectJackettAPIKey(t *testing.T) {
	tests := []struct {
		name       string
		host       string
		apiKey     string
		url        string
		wantHasKey bool
	}{
		{"no jackett host configured", "", "k", "https://idx.example/t.torrent", false},
		{"different host", "jackett.lan", "k", "https://other.example/t.torrent", false},
		{"empty api key", "jackett.lan", "", "https://jackett.lan/t.torrent", false},
		{"already has apikey", "jackett.lan", "k", "https://jackett.lan/t.torrent?apikey=old", false},
		{"injects key", "jackett.lan", "secret", "https://jackett.lan/dl?file=x", true},
		{"unparseable url", "jackett.lan", "secret", "://bad-url", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &Streamer{cfg: Config{JackettHost: tc.host, JackettAPIKey: tc.apiKey}}
			got := s.injectJackettAPIKey(tc.url)
			has := bytes.Contains([]byte(got), []byte("apikey="+tc.apiKey)) && tc.apiKey != ""
			if tc.wantHasKey {
				if !has {
					t.Errorf("expected apikey injected, got %q", got)
				}
			} else if tc.apiKey == "secret" && has {
				t.Errorf("did not expect apikey injected, got %q", got)
			}
		})
	}
}

func Test_str3_NewSSRFGuardedClientWiring(t *testing.T) {
	var captured string
	c := newSSRFGuardedClient("jackett.lan", &captured)
	if c == nil {
		t.Fatal("nil client")
	}
	if c.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", c.Timeout)
	}
	if c.Transport == nil {
		t.Fatal("nil transport")
	}
	if c.CheckRedirect == nil {
		t.Fatal("nil CheckRedirect")
	}
	// The wired CheckRedirect must funnel a magnet redirect into capturedMagnet.
	req, _ := http.NewRequest("GET", "magnet:?xt=urn:btih:wired", nil)
	if err := c.CheckRedirect(req, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Errorf("wired CheckRedirect returned %v", err)
	}
	if captured != "magnet:?xt=urn:btih:wired" {
		t.Errorf("captured = %q", captured)
	}
}

func Test_str3_NewSSRFTransport(t *testing.T) {
	tr := newSSRFTransport("jackett.lan")
	if tr == nil {
		t.Fatal("nil transport")
	}
	if tr.DialContext == nil {
		t.Fatal("nil DialContext")
	}
	// The wired DialContext must enforce the same SSRF block as ssrfDialContext.
	if _, err := tr.DialContext(context.Background(), "tcp", "127.0.0.1:80"); err == nil {
		t.Error("expected transport DialContext to block loopback")
	}
}

// ---------------------------------------------------------------------------
// addFromTorrentResponse / addFromCapturedMagnet — error validation only.
// ---------------------------------------------------------------------------

func Test_str3_AddFromTorrentResponseNon200(t *testing.T) {
	s := NewForTesting()
	resp := &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(bytes.NewReader(nil))}
	if _, err := s.addFromTorrentResponse(resp); err == nil {
		t.Error("expected error for non-200 .torrent response")
	}
}

func Test_str3_AddFromTorrentResponseBadBody(t *testing.T) {
	// 200 OK but the body is not valid bencode metainfo → metainfo.Load fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("this is not a torrent file"))
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	s := NewForTesting()
	if _, err := s.addFromTorrentResponse(resp); err == nil {
		t.Error("expected parse error for non-metainfo body")
	}
}

func Test_str3_AddFromCapturedMagnetValid(t *testing.T) {
	// A well-formed magnet with a real btih is accepted by AddMagnet; the helper
	// must return the torrent and no error (covers the happy path).
	dir := t.TempDir()
	s, err := New(Config{DataDir: dir, ListenPort: str3FreePort(t)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	const magnet = "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&dn=str3"
	tor, err := s.addFromCapturedMagnet(magnet)
	if err != nil {
		t.Fatalf("addFromCapturedMagnet: %v", err)
	}
	if tor == nil {
		t.Error("expected a non-nil torrent")
	}
}

// ---------------------------------------------------------------------------
// sampleRateLocked — first-sample zero, too-soon zero, real delta, and the
// counter-reset clamp. Uses a real (info-complete) torrent so Stats() works.
// ---------------------------------------------------------------------------

func Test_str3_SampleRateLocked(t *testing.T) {
	dir := t.TempDir()
	s, err := New(Config{DataDir: dir, ListenPort: str3FreePort(t)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	spec := str3TorrentSpec(t)
	tor, _, err := s.client.AddTorrentSpec(spec)
	if err != nil {
		t.Fatalf("AddTorrentSpec: %v", err)
	}
	e := &entry{t: tor}

	// First sample: lastSampleAt is zero → must return (0,0) and seed state.
	now := time.Now()
	if d, u := sampleRateLocked(e, now); d != 0 || u != 0 {
		t.Errorf("first sample = (%d,%d), want (0,0)", d, u)
	}
	if e.lastSampleAt.IsZero() {
		t.Error("lastSampleAt should be seeded after first sample")
	}

	// Sampled too soon (< 250ms): still (0,0).
	if d, u := sampleRateLocked(e, now.Add(100*time.Millisecond)); d != 0 || u != 0 {
		t.Errorf("too-soon sample = (%d,%d), want (0,0)", d, u)
	}

	// A real window with simulated byte progress → non-negative rates.
	e.lastBytesRead = 0
	e.lastBytesWritten = 0
	e.lastSampleAt = now
	d, u := sampleRateLocked(e, now.Add(time.Second))
	if d < 0 || u < 0 {
		t.Errorf("rates must be non-negative, got (%d,%d)", d, u)
	}

	// Counter reset: previous sample claims MORE bytes than current → clamp to 0.
	e.lastBytesRead = 1 << 40
	e.lastBytesWritten = 1 << 40
	e.lastSampleAt = now
	d, u = sampleRateLocked(e, now.Add(time.Second))
	if d != 0 || u != 0 {
		t.Errorf("counter-reset clamp = (%d,%d), want (0,0)", d, u)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// str3FreePort grabs an ephemeral TCP port so parallel-ish torrent clients in
// the same package don't collide on the default ListenPort.
func str3FreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// Test_str3_DropActiveReadGuard covers the activeReadGuard branch in Drop():
// a torrent read within activeReadGuard is treated as still being watched, so an
// explicit Drop() (player close) must be a no-op — the entry stays in s.active.
func Test_str3_DropActiveReadGuard(t *testing.T) {
	dir := t.TempDir()
	s, err := New(Config{DataDir: dir, ListenPort: str3FreePort(t)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	spec := str3TorrentSpec(t)
	tor, _, err := s.client.AddTorrentSpec(spec)
	if err != nil {
		t.Fatalf("AddTorrentSpec: %v", err)
	}
	hash := tor.InfoHash()

	// Insert an active entry with a RECENT lastAccess (within activeReadGuard) and
	// no background-download protection, so Drop() reaches the lastAccess guard.
	s.mu.Lock()
	s.active[hash] = &entry{t: tor, lastAccess: time.Now()}
	s.mu.Unlock()

	s.Drop(hash)

	// Guard held: the recent read means someone is still watching, so the entry
	// must NOT have been removed from s.active.
	s.mu.Lock()
	_, stillActive := s.active[hash]
	s.mu.Unlock()
	if !stillActive {
		t.Error("Drop removed a recently-read torrent; activeReadGuard should have skipped it")
	}
}

// str3TorrentSpec builds an info-complete single-file torrent spec in memory so
// AddTorrentSpec yields an immediately-usable *torrent.Torrent (Stats() valid).
func str3TorrentSpec(t *testing.T) *torrent.TorrentSpec {
	t.Helper()
	const piece = 1 << 14
	data := bytes.Repeat([]byte("z"), piece)
	pieceHash := metainfo.HashBytes(data)
	info := metainfo.Info{
		Name:        "str3-sample.bin",
		PieceLength: piece,
		Length:      int64(len(data)),
		Pieces:      pieceHash[:],
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("bencode.Marshal info: %v", err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: infoBytes}
	spec, err := torrent.TorrentSpecFromMetaInfoErr(mi)
	if err != nil {
		t.Fatalf("TorrentSpecFromMetaInfoErr: %v", err)
	}
	return spec
}
