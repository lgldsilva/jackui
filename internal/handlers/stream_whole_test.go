package handlers

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
)

const wholeHexHash = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

// wholeTestEnv wires the trio the whole-torrent play path needs: a completed
// -2 row whose file_path is the moved tree's directory, plus a metadata-cache
// snapshot so FileRelPath can map the file index without activating the
// torrent.
func wholeTestEnv(t *testing.T) (*downloads.Store, *streamer.Streamer, string) {
	t.Helper()
	store := newDownloadsStore(t)
	destDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(destDir, "Sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destDir, "Sub", "a.bin"), []byte("AAAA"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := store.Create(downloads.Download{
		UserID: 0, InfoHash: wholeHexHash, FileIndex: downloads.FileIndexWholeTorrent,
		Magnet: "m", Name: "Pack",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.UpdateMetadata(0, d.ID, "Pack", destDir, 4); err != nil {
		t.Fatalf("UpdateMetadata: %v", err)
	}
	if err := store.SetStatus(0, d.ID, downloads.StatusCompleted); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	s := streamer.NewForTesting()
	mc, err := streamer.NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	t.Cleanup(func() { _ = mc.Close() })
	s.SetMetadataCache(mc)
	if err := mc.Set(&streamer.TorrentInfo{
		InfoHash: wholeHexHash, Name: "Pack",
		Files: []streamer.FileInfo{{Index: 0, Path: "Pack/Sub/a.bin", Size: 4}},
	}); err != nil {
		t.Fatalf("cache Set: %v", err)
	}
	return store, s, destDir
}

// The MAJOR from the review: playing a file of a torrent downloaded as ONE
// whole item must serve the moved bytes from disk — never fall back to the
// swarm (a dead swarm meant "doesn't play" despite the bytes being local).
func TestStreamFile_WholeTorrentRowServesFromDisk(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, s, _ := wholeTestEnv(t)
	r := gin.New()
	r.GET("/api/stream/:hash/:file", StreamFile(s, store))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/stream/"+wholeHexHash+"/0", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != "AAAA" {
		t.Fatalf("body = %q, want the moved file's bytes", w.Body.String())
	}
}

func TestStreamFile_WholeRowDirectoryIndexNotServed(t *testing.T) {
	// Requesting the sentinel index resolves the whole row's file_path — a
	// DIRECTORY. It must not be served from disk (falls to the streamer → 404
	// here, torrent inactive).
	gin.SetMode(gin.TestMode)
	store, s, _ := wholeTestEnv(t)
	r := gin.New()
	r.GET("/api/stream/:hash/:file", StreamFile(s, store))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/stream/"+wholeHexHash+"/-2", nil))
	if w.Code == http.StatusOK {
		t.Fatalf("a directory must never be served; got 200, body=%s", w.Body.String())
	}
}

func TestTryServeFromCompleted_WholeRowClaimsTheRequest(t *testing.T) {
	// The progressive-transcode path must claim the request once the whole-row
	// file resolves on disk (true = no swarm fallback), even when the encode
	// itself fails — the bytes ARE local.
	gin.SetMode(gin.TestMode)
	store, s, _ := wholeTestEnv(t)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	h, _ := parseHash(wholeHexHash)

	if !tryServeFromCompleted(c, store, wholeHexHash, 0, transcode.Options{}, s.FileRelPath(h, 0), 0) {
		t.Fatal("expected the whole-torrent completed file to be served from disk")
	}
}

func TestTryServeFromCompleted_MissAndDirectory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, _, _ := wholeTestEnv(t)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	// Unknown hash → no completed row → fall through to the streamer.
	if tryServeFromCompleted(c, store, "0123456789abcdef0123456789abcdef01234567", 0, transcode.Options{}, "", 0) {
		t.Error("expected false when no completed row exists")
	}
	// The sentinel index resolves the whole row's file_path — a DIRECTORY.
	if tryServeFromCompleted(c, store, wholeHexHash, downloads.FileIndexWholeTorrent, transcode.Options{}, "", 0) {
		t.Error("expected false for a directory path")
	}
}

func TestResolveTranscodeSource_WholeTorrentRow(t *testing.T) {
	// The HLS/transcode path must also reach the moved file (stream.go,
	// transcode.go and hls.go share GetCompletedPathRel).
	gin.SetMode(gin.TestMode)
	store, s, _ := wholeTestEnv(t)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	h, _ := parseHash(wholeHexHash)
	hc := &hlsCtx{c: c, s: s, store: store, h: h, fileIdx: 0}

	src, size, complete := resolveTranscodeSource(hc)
	if src == nil {
		t.Fatal("expected a reader for the whole-torrent completed file")
	}
	defer func() { _ = src.Close() }()
	if size != 4 || !complete {
		t.Fatalf("(size, complete) = (%d, %v), want (4, true)", size, complete)
	}
}

// realStreamerEnv spins a REAL anacrolix-backed streamer (ephemeral peer port,
// tiny metadata wait) so the swarm-fallback half of resolveTranscodeSource
// runs. Returns the streamer and its DataDir (`.metainfo/` lives under it).
// Port probe-then-bind is TOCTOU under parallel `go test ./...`, hence retries.
func realStreamerEnv(t *testing.T) (*streamer.Streamer, string) {
	t.Helper()
	dir := t.TempDir()
	var lastErr error
	for attempt := 0; attempt < 8; attempt++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		_ = ln.Close()
		s, err := streamer.New(streamer.Config{
			DataDir: dir, MetadataWait: 100 * time.Millisecond, ListenPort: port,
		})
		if err == nil {
			return s, dir
		}
		lastErr = err
		if !strings.Contains(err.Error(), "address already in use") {
			break
		}
	}
	t.Fatalf("streamer.New: %v", lastErr)
	return nil, ""
}

func TestResolveTranscodeSource_UnknownMagnetTimesOut(t *testing.T) {
	// No completed row and no cached metainfo: the bare-magnet Add can't get
	// metadata (no peers) → 404, no source.
	gin.SetMode(gin.TestMode)
	s, _ := realStreamerEnv(t)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	h, _ := parseHash("0123456789abcdef0123456789abcdef01234567")

	src, _, complete := resolveTranscodeSource(&hlsCtx{c: c, s: s, h: h, fileIdx: 0})
	if src != nil || complete {
		t.Fatal("expected no source for an unknown dead magnet")
	}
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestResolveTranscodeSource_StreamerFallback(t *testing.T) {
	// Seeding the .metainfo cache lets the bare-magnet Add resolve instantly
	// (no swarm), exercising the in-progress streaming half of the resolver.
	gin.SetMode(gin.TestMode)
	s, dataDir := realStreamerEnv(t)
	piece := metainfo.HashBytes([]byte("zzzz"))
	infoBytes, err := bencode.Marshal(metainfo.Info{
		Name: "Solo.mkv", PieceLength: 1 << 14, Pieces: piece[:], Length: 4,
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

	// Out-of-range index: torrent activates but FileReader refuses → 404.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	if src, _, _ := resolveTranscodeSource(&hlsCtx{c: c, s: s, h: h, fileIdx: 7}); src != nil {
		t.Fatal("expected no source for an out-of-range file index")
	}
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}

	// Valid index: a live streaming reader, NOT complete (the #61 VOD guard).
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest("GET", "/", nil)
	src, size, complete := resolveTranscodeSource(&hlsCtx{c: c2, s: s, h: h, fileIdx: 0})
	if src == nil {
		t.Fatal("expected a streaming reader for the activated torrent")
	}
	defer func() { _ = src.Close() }()
	if size != 4 || complete {
		t.Fatalf("(size, complete) = (%d, %v), want (4, false)", size, complete)
	}
}
